package server

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"net/http"
	"siplatform/internal/accounts"
	"siplatform/internal/game"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/themes"
	"time"
)

type appearanceAsset struct {
	Index        int    `json:"index"`
	Name         string `json:"name"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	StandardSize int64  `json:"standard_size"`
	EconomySize  int64  `json:"economy_size"`
}
type appearanceResponse struct {
	Appearance game.Appearance   `json:"appearance"`
	Status     string            `json:"status"`
	Manifest   *themes.Manifest  `json:"manifest,omitempty"`
	Assets     []appearanceAsset `json:"assets"`
}

func (s *Server) currentAppearance(r *http.Request) (game.Appearance, error) {
	actor, err := s.roomIdentity(r)
	if err != nil {
		return game.Appearance{}, err
	}
	state, err := s.store.Load(r.Context(), chi.URLParam(r, "room"))
	if err != nil {
		return game.Appearance{}, err
	}
	if _, ok := state.ActiveParticipant(actor); !ok {
		return game.Appearance{}, game.Fault("unauthorized")
	}
	if state.Appearance == nil {
		// Rooms created before appearance entered snapshots retain their builtin.
		if pin, e := s.store.GamePin(r.Context(), state.ID); e == nil && pin.Appearance != nil {
			return *pin.Appearance, nil
		}
	}
	return appearanceOf(state), nil
}
func (s *Server) roomAppearance(w http.ResponseWriter, r *http.Request) {
	a, err := s.currentAppearance(r)
	if err != nil {
		appError(w, err)
		return
	}
	out := appearanceResponse{Appearance: a, Status: "ready", Assets: []appearanceAsset{}}
	if a.ThemeID != "" {
		v, e := s.options.Themes.Pinned(r.Context(), a.ThemeID, a.Revision, a.Hash)
		if errors.Is(e, themes.ErrBlocked) || errors.Is(e, themes.ErrNotFound) {
			out.Status = "unavailable"
		} else if e != nil {
			appError(w, e)
			return
		} else {
			out.Manifest = &v.Prepared.Manifest
			for i, asset := range v.Prepared.Assets {
				p := asset.Prepared
				out.Assets = append(out.Assets, appearanceAsset{Index: i, Name: asset.Name, Width: p.Width, Height: p.Height, StandardSize: p.Standard.Size, EconomySize: p.Economy.Size})
			}
		}
	}
	writeJSON(w, 200, out)
}
func (s *Server) roomThemeAsset(w http.ResponseWriter, r *http.Request) {
	a, err := s.currentAppearance(r)
	if err != nil {
		appError(w, err)
		return
	}
	if a.ThemeID == "" || r.URL.Query().Get("hash") != a.Hash {
		appError(w, themes.ErrNotFound)
		return
	}
	v, err := s.options.Themes.Pinned(r.Context(), a.ThemeID, a.Revision, a.Hash)
	if err != nil {
		appError(w, err)
		return
	}
	variant, err := themeVariant(r, v)
	if err != nil {
		appError(w, err)
		return
	}
	s.serveBlobAs(w, r, variant.Key, variant.MIME, "theme.png", "inline")
}
func (s *Server) setAppearance(w http.ResponseWriter, r *http.Request) {
	participant, err := s.roomIdentity(r)
	if err != nil {
		appError(w, err)
		return
	}
	var req struct {
		Appearance  game.Appearance `json:"appearance"`
		CommandID   string          `json:"command_id"`
		ExpectedSeq int64           `json:"expected_seq"`
	}
	if err = readJSON(w, r, &req); err != nil {
		appError(w, err)
		return
	}
	if err = req.Appearance.Validate(); err != nil {
		appError(w, err)
		return
	}
	if !game.ValidID(req.CommandID) || req.ExpectedSeq < 1 {
		appError(w, themes.ErrInvalid)
		return
	}
	actor := postgres.AppearanceActor{}
	if req.Appearance.ThemeID != "" {
		cookies := r.CookiesNamed("si_session")
		if s.options.Accounts == nil || len(cookies) != 1 {
			appError(w, accounts.ErrUnauthorized)
			return
		}
		identity, e := s.options.Accounts.Authenticate(r.Context(), cookies[0].Value)
		if e != nil {
			appError(w, e)
			return
		}
		if identity.APIToken {
			appError(w, accounts.ErrForbidden)
			return
		}
		actor = postgres.AppearanceActor{UserID: identity.User.ID, CredentialID: identity.SessionID}
	}
	rm, err := s.getRoom(r.Context(), chi.URLParam(r, "room"))
	if err != nil {
		appError(w, err)
		return
	}
	_, err = rm.do(r.Context(), func(ctx context.Context, rm *room) (any, error) {
		state, e := s.store.Load(ctx, rm.id)
		if e != nil {
			return nil, e
		}
		p, ok := state.ActiveParticipant(participant)
		if !ok || p.Role != game.Host {
			return nil, game.Fault("forbidden")
		}
		if !rm.connected(participant) {
			return nil, game.Fault("host_offline")
		}
		if rm.limits[participant] == nil {
			rm.limits[participant] = newBucket()
		}
		if !rm.limits[participant].allow(time.Now()) {
			return nil, game.Fault("rate_limited")
		}
		data, _ := json.Marshal(game.AppearancePayload{Appearance: req.Appearance})
		command := game.Command{Version: 2, ID: req.CommandID, Type: "set_appearance", ExpectedSeq: &req.ExpectedSeq, Data: data}
		out, e := s.store.ExecuteAppearance(ctx, rm.id, participant, command, actor)
		if e != nil {
			return nil, e
		}
		if out.Code != "ok" {
			return nil, game.Fault(out.Code)
		}
		rm.broadcast(out.State, nil)
		for c := range rm.clients {
			if c.appearanceOnly {
				c.send(Message{Type: "appearance_refresh"})
			}
		}
		return nil, nil
	})
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
