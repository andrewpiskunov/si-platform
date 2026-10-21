package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"siplatform/internal/game"
	"siplatform/internal/library"
	"siplatform/internal/playpack"
)

const mediaTicketLifetime = 15 * time.Minute

type mediaClaims struct {
	Room        string `json:"r"`
	Participant string `json:"p"`
	Instance    string `json:"i"`
	Media       string `json:"m"`
	Quality     string `json:"q"`
	Expires     int64  `json:"e"`
}

func (s *Server) signMediaTicket(c mediaClaims) (string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, s.mediaSecret[:])
	_, _ = mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (s *Server) readMediaTicket(ticket string, now time.Time) (mediaClaims, error) {
	invalid := game.Fault("unauthorized")
	var c mediaClaims
	if len(ticket) > 2048 {
		return c, invalid
	}
	parts := strings.Split(ticket, ".")
	if len(parts) != 2 {
		return c, invalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(signature) != sha256.Size {
		return c, invalid
	}
	mac := hmac.New(sha256.New, s.mediaSecret[:])
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(mac.Sum(nil), signature) {
		return c, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || strictJSON(strings.NewReader(string(raw)), &c) != nil {
		return c, invalid
	}
	if !game.ValidID(c.Room) || !game.ValidID(c.Participant) || !game.ValidID(c.Instance) || !game.ValidID(c.Media) || !mediaQuality(c.Quality) || c.Expires <= now.Unix() || c.Expires > now.Add(mediaTicketLifetime).Unix() {
		return mediaClaims{}, invalid
	}
	return c, nil
}
func mediaQuality(value string) bool { return value == "standard" || value == "economy" }

// A ticket grants no additional visibility: the current participant projection
// and the live package ACL are checked again for every GET, HEAD and Range request.
func (s *Server) permittedMedia(ctx context.Context, roomID, actorID, mediaID, instance string) (game.MediaSpec, string, error) {
	unavailable := game.Fault("not_found")
	state, err := s.store.Load(ctx, roomID)
	if err != nil {
		return game.MediaSpec{}, "", err
	}
	if _, ok := state.ActiveParticipant(actorID); !ok || state.Phase == game.Finished {
		return game.MediaSpec{}, "", unavailable
	}
	view := game.ProjectAt(state, actorID, time.Now().UnixMilli())
	if view.Gameplay == nil || view.Gameplay.Question == nil {
		return game.MediaSpec{}, "", unavailable
	}
	q := view.Gameplay.Question
	if q.Media == nil || q.Media.ID != mediaID || instance != "" && q.InstanceID != instance {
		return game.MediaSpec{}, "", unavailable
	}
	_, draft, err := s.roomManifest(ctx, roomID)
	if err != nil {
		return game.MediaSpec{}, "", err
	}
	spec, err := playpack.CompileMedia(draft.Document, q.ID, library.MediaLookup(draft.Assets))
	if err != nil {
		return game.MediaSpec{}, "", unavailable
	}
	for _, group := range [][]game.TextStep{spec.Question, spec.Answer} {
		for _, step := range group {
			if step.Media != nil && step.Media.ID == mediaID {
				return *step.Media, q.InstanceID, nil
			}
		}
	}
	return game.MediaSpec{}, "", unavailable
}
func (s *Server) mediaTicket(w http.ResponseWriter, r *http.Request) {
	actor, err := s.roomIdentity(r)
	if err != nil {
		appError(w, err)
		return
	}
	var req struct {
		MediaID string `json:"media_id"`
		Quality string `json:"quality"`
	}
	if err = readJSON(w, r, &req); err != nil {
		appError(w, err)
		return
	}
	if !game.ValidID(req.MediaID) || !mediaQuality(req.Quality) {
		appError(w, library.ErrInvalid)
		return
	}
	roomID := chi.URLParam(r, "room")
	_, instance, err := s.permittedMedia(r.Context(), roomID, actor, req.MediaID, "")
	if err != nil {
		appError(w, err)
		return
	}
	expires := time.Now().Add(mediaTicketLifetime).Unix()
	token, err := s.signMediaTicket(mediaClaims{Room: roomID, Participant: actor, Instance: instance, Media: req.MediaID, Quality: req.Quality, Expires: expires})
	if err != nil {
		appError(w, err)
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	writeJSON(w, 200, map[string]any{"url": "/api/rooms/" + roomID + "/media/" + req.MediaID + "?ticket=" + url.QueryEscape(token), "expires_at": expires})
}
func (s *Server) roomMedia(w http.ResponseWriter, r *http.Request) {
	tokens := r.URL.Query()["ticket"]
	if len(tokens) != 1 {
		appError(w, game.Fault("unauthorized"))
		return
	}
	claims, err := s.readMediaTicket(tokens[0], time.Now())
	if err != nil || claims.Room != chi.URLParam(r, "room") || claims.Media != chi.URLParam(r, "media") {
		appError(w, game.Fault("unauthorized"))
		return
	}
	spec, _, err := s.permittedMedia(r.Context(), claims.Room, claims.Participant, claims.Media, claims.Instance)
	if err != nil {
		appError(w, err)
		return
	}
	variant := spec.Standard
	if claims.Quality == "economy" {
		variant = spec.Economy
	}
	s.serveBlobAs(w, r, variant.Key, safeMIME(variant.MIME), "media", "inline")
}
func (s *Server) preparedAsset(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.identity(w, r)
	if !ok {
		return
	}
	rev, err := queryInt(r, "revision", 0)
	if err != nil {
		appError(w, err)
		return
	}
	draft, err := s.options.Library.ForFile(r.Context(), actor, chi.URLParam(r, "id"), rev, false)
	if err != nil {
		appError(w, err)
		return
	}
	key := chi.URLParam(r, "key")
	for _, asset := range draft.Assets {
		if asset.Prepared == nil {
			continue
		}
		for _, v := range []struct{ key, mime string }{{asset.Prepared.Standard.Key, asset.Prepared.Standard.MIME}, {asset.Prepared.Economy.Key, asset.Prepared.Economy.MIME}} {
			if v.key == key {
				s.serveBlobAs(w, r, v.key, safeMIME(v.mime), "preview", "inline")
				return
			}
		}
	}
	appError(w, library.ErrNotFound)
}
