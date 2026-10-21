package server

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"siplatform/internal/game"
)

func (s *Server) publicRooms(w http.ResponseWriter, r *http.Request) {
	limit, err := queryInt(r, "limit", 50)
	if err != nil {
		appError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := s.store.ListPublicRooms(ctx, r.URL.Query().Get("after"), limit)
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) rotateInvite(w http.ResponseWriter, r *http.Request) {
	actor, err := s.roomIdentity(r)
	if err != nil {
		appError(w, err)
		return
	}
	var body struct{}
	if err = readJSON(w, r, &body); err != nil {
		appError(w, err)
		return
	}
	rm, err := s.getRoom(r.Context(), chi.URLParam(r, "room"))
	if err != nil {
		appError(w, err)
		return
	}
	value, err := rm.do(r.Context(), func(ctx context.Context, rm *room) (any, error) {
		token, state, err := s.store.RotateInvite(ctx, rm.id, actor)
		if err != nil {
			return nil, err
		}
		rm.broadcast(state, nil)
		return map[string]string{"invite_token": token}, nil
	})
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, 200, value)
}
func (s *Server) roomResults(w http.ResponseWriter, r *http.Request) {
	actor, err := s.roomIdentity(r)
	if err != nil {
		appError(w, err)
		return
	}
	var after int64
	if raw := r.URL.Query().Get("after_seq"); raw != "" {
		after, err = strconv.ParseInt(raw, 10, 64)
	}
	if err != nil || after < 0 {
		appError(w, game.Fault("invalid_input"))
		return
	}
	limit, err := queryInt(r, "limit", 100)
	if err != nil {
		appError(w, err)
		return
	}
	out, err := s.store.Results(r.Context(), chi.URLParam(r, "room"), actor, after, limit)
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
