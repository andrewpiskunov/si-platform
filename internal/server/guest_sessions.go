package server

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"siplatform/internal/game"
)

func guestCookie(r *http.Request) string {
	if c, e := r.Cookie("si_guest"); e == nil {
		if b, e := hex.DecodeString(c.Value); e == nil && len(b) == 32 && hex.EncodeToString(b) == c.Value {
			return c.Value
		}
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
func (s *Server) setGuestCookie(w http.ResponseWriter, r *http.Request, roomID, guest string) {
	http.SetCookie(w, &http.Cookie{Name: "si_guest", Value: guest, Path: "/api/rooms/" + roomID, HttpOnly: true, Secure: r.TLS != nil || strings.HasPrefix(s.options.Origin, "https://"), SameSite: http.SameSiteStrictMode, MaxAge: 30 * 24 * 60 * 60})
}
func (s *Server) bindRoomSession(w http.ResponseWriter, r *http.Request) {
	if _, err := s.roomIdentity(r); err != nil {
		appError(w, err)
		return
	}
	var body struct{}
	if err := readJSON(w, r, &body); err != nil {
		appError(w, err)
		return
	}
	token := strings.Fields(r.Header.Get("Authorization"))[1]
	guest := guestCookie(r)
	roomID := chi.URLParam(r, "room")
	if err := s.store.BindGuest(r.Context(), roomID, token, guest); err != nil {
		appError(w, err)
		return
	}
	s.setGuestCookie(w, r, roomID, guest)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) roomRoster(w http.ResponseWriter, r *http.Request) {
	actor, err := s.roomIdentity(r)
	if err != nil {
		appError(w, err)
		return
	}
	limit, err := queryInt(r, "limit", 50)
	if err != nil {
		appError(w, game.Fault("invalid_input"))
		return
	}
	out, err := s.store.Roster(r.Context(), chi.URLParam(r, "room"), actor, r.URL.Query().Get("after"), limit)
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
