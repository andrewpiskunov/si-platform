package server

import (
	"context"
	"github.com/go-chi/chi/v5"
	"net/http"
	"siplatform/internal/game"
	"siplatform/internal/storage/postgres"
)

func (s *Server) myRooms(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	if a.APIToken {
		appError(w, game.Fault("forbidden"))
		return
	}
	offset, err := queryInt(r, "offset", 0)
	if err != nil {
		appError(w, game.Fault("invalid_input"))
		return
	}
	out, err := s.store.MyRooms(r.Context(), a.User.ID, offset)
	if err != nil {
		if game.Code(err) == "room_expired" {
			err = game.Fault("not_found")
		}
		appError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
}
func (s *Server) recoverMyRoom(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	if a.APIToken {
		appError(w, game.Fault("forbidden"))
		return
	}
	var body struct{}
	if err := readJSON(w, r, &body); err != nil {
		appError(w, err)
		return
	}
	guest := guestCookie(r)
	rm, err := s.getRoom(r.Context(), chi.URLParam(r, "room"))
	if err != nil {
		if game.Code(err) == "room_expired" {
			err = game.Fault("not_found")
		}
		appError(w, err)
		return
	}
	value, err := rm.do(r.Context(), func(ctx context.Context, rm *room) (any, error) {
		c, err := s.store.RecoverMyRoom(ctx, postgres.AppearanceActor{UserID: a.User.ID, CredentialID: a.SessionID}, rm.id, guest)
		if err != nil {
			return nil, err
		}
		for client := range rm.clients {
			if client.id == c.ParticipantID {
				delete(rm.clients, client)
				client.revoke()
			}
		}
		return c, nil
	})
	if err != nil {
		if game.Code(err) == "room_expired" {
			err = game.Fault("not_found")
		}
		appError(w, err)
		return
	}
	c := value.(postgres.Credentials)
	s.setGuestCookie(w, r, c.RoomID, guest)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, c)
}
