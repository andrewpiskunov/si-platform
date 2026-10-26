package server

import (
	"context"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"net/http"
	"siplatform/internal/game"
	"siplatform/internal/playpack"
	"time"
)

func (s *Server) startRound(w http.ResponseWriter, r *http.Request) {
	actor, err := s.roomIdentity(r)
	if err != nil {
		appError(w, err)
		return
	}
	var req struct {
		RoundID     string `json:"round_id"`
		CommandID   string `json:"command_id"`
		ExpectedSeq int64  `json:"expected_seq"`
	}
	if err = readJSON(w, r, &req); err != nil {
		appError(w, err)
		return
	}
	if !game.ValidID(req.RoundID) || !game.ValidID(req.CommandID) || req.ExpectedSeq < 1 {
		appError(w, game.Fault("invalid_input"))
		return
	}
	rm, err := s.getRoom(r.Context(), chi.URLParam(r, "room"))
	if err != nil {
		appError(w, err)
		return
	}
	_, err = rm.do(r.Context(), func(ctx context.Context, rm *room) (any, error) {
		state, err := s.store.Load(ctx, rm.id)
		if err != nil {
			return nil, err
		}
		p, ok := state.ActiveParticipant(actor)
		if !ok || p.Role != game.Host {
			return nil, game.Fault("forbidden")
		}
		if !rm.connected(actor) {
			return nil, game.Fault("host_offline")
		}
		if rm.limits[actor] == nil {
			rm.limits[actor] = newBucket()
		}
		if !rm.limits[actor].allow(time.Now()) {
			return nil, game.Fault("rate_limited")
		}
		_, doc, err := s.roomDocument(ctx, rm.id)
		if err != nil {
			return nil, err
		}
		round, err := playpack.Round(doc, req.RoundID)
		if err != nil {
			return nil, game.Fault("unsupported_round")
		}
		data, err := json.Marshal(game.BeginRoundPayload{Round: round})
		if err != nil {
			return nil, err
		}
		cmd := game.Command{Version: 2, ID: req.CommandID, Type: "begin_round", ExpectedSeq: &req.ExpectedSeq, Data: data}
		out, err := s.store.Execute(ctx, rm.id, actor, cmd)
		if err != nil {
			return nil, err
		}
		if out.Code != "ok" {
			return nil, game.Fault(out.Code)
		}
		rm.broadcast(out.State, nil)
		return nil, nil
	})
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (r *room) connected(id string) bool {
	for c := range r.clients {
		if c.id == id && !c.appearanceOnly && !c.displayOnly && c.ctx.Err() == nil {
			return true
		}
	}
	return false
}
func (r *room) hostOnline(state game.State) bool {
	for _, p := range state.Participants {
		if !p.Removed && p.Role == game.Host && r.connected(p.ID) {
			return true
		}
	}
	return false
}
func (r *room) arm(state game.State) {
	deadline := game.NextDeadline(state)
	if r.timer != nil {
		r.timer.Stop()
	}
	r.timerC = nil
	if deadline == 0 {
		return
	}
	delay := max(time.Millisecond, time.Until(time.UnixMilli(deadline)))
	if r.timer == nil {
		r.timer = time.NewTimer(delay)
	} else {
		r.timer.Reset(delay)
	}
	r.timerC = r.timer.C
}
func (r *room) expire(ctx context.Context) error {
	if err := r.reconcileMembership(ctx); err != nil {
		return err
	}
	if err := r.resolvePause(ctx); err != nil {
		return err
	}
	state, err := r.server.store.Load(ctx, r.id)
	if err != nil {
		return err
	}
	if !r.hostOnline(state) {
		state, err = r.server.store.Pause(ctx, r.id, "host_disconnected")
	} else {
		state, err = r.server.store.Advance(ctx, r.id)
	}
	if err != nil {
		return err
	}
	r.broadcast(state, nil)
	return nil
}
