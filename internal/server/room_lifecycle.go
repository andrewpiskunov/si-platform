package server

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"siplatform/internal/game"
)

func (s *Server) roomRetention(w http.ResponseWriter, r *http.Request) {
	if _, err := s.roomIdentity(r); err != nil {
		appError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out, err := s.store.Retention(ctx, chi.URLParam(r, "room"))
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) watchRoomExpiry() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case now := <-ticker.C:
			ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
			err := s.sweepRooms(ctx, now)
			cancel()
			if game.Code(err) == "ownership_lost" {
				s.cancel()
				return
			}
		}
	}
}

func (s *Server) sweepRooms(ctx context.Context, now time.Time) error {
	ids, err := s.store.ExpiredRooms(ctx, now, 50)
	if err != nil {
		return err
	}
	for _, id := range ids {
		unlock, err := s.lockRoomAdmission(ctx, id)
		if err != nil {
			return err
		}
		s.mu.Lock()
		rm := s.rooms[id]
		s.mu.Unlock()
		if rm == nil {
			_, err = s.store.RetireRoom(ctx, id, now)
			unlock()
			if err != nil {
				return err
			}
			continue
		}
		unlock()
		// Retirement is itself an actor job; accepted joins/commands ahead of it
		// commit first and eligibility is checked again under the room row lock.
		j := job{ctx: ctx, result: make(chan reply, 1), run: func(ctx context.Context, rm *room) (any, error) {
			retired, err := s.store.RetireRoom(ctx, id, now)
			if retired || err != nil {
				rm.stopForExpiry(retired, err)
			}
			return nil, err
		}}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-rm.done:
			continue
		case rm.requests <- j:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-j.result:
			if result.err != nil {
				return result.err
			}
		case <-rm.done:
			select {
			case <-rm.retirementDone:
			case <-ctx.Done():
				return ctx.Err()
			}
			if rm.retirementErr != nil {
				return rm.retirementErr
			}

		}
	}
	return nil
}

func (r *room) stopForExpiry(retired bool, err error) {
	if r.stopped.Swap(true) {
		return
	}
	if r.timer != nil {
		r.timer.Stop()
	}
	r.timerC = nil
	for c := range r.clients {
		if retired {
			c.expireRoom()
		} else {
			c.cancel()
		}
	}
	r.retirementErr = err
	if game.Code(err) == "ownership_lost" {
		r.server.cancel()
	}
	close(r.done)
	r.server.mu.Lock()
	if r.server.rooms[r.id] == r {
		delete(r.server.rooms, r.id)
	}
	r.server.mu.Unlock()
	r.server.appearanceMu.Lock()
	delete(r.server.appearanceKnown, r.id)
	r.server.appearanceMu.Unlock()
	close(r.retirementDone)
}

func (r *room) checkRetention(ctx context.Context) error {
	info, err := r.server.store.Retention(ctx, r.id)
	if err != nil {
		if game.Code(err) == "not_found" {
			r.stopForExpiry(true, nil)
			return game.Fault("room_expired")
		}
		return err
	}
	if info.ExpiresAt.After(time.Now()) {
		return nil
	}
	retired, err := r.server.store.RetireRoom(ctx, r.id, time.Now())
	if retired || err != nil {
		r.stopForExpiry(retired, err)
	}
	if err != nil {
		return err
	}
	if retired {
		return game.Fault("room_expired")
	}
	return nil
}

// roomGate exists only while an admission/retirement operation holds or waits
// for it. Its reference count prevents ABA replacement while a loader waits.
type roomGate struct {
	token chan struct{}
	refs  int
}

func (s *Server) lockRoomAdmission(ctx context.Context, id string) (func(), error) {
	s.mu.Lock()
	g := s.roomGates[id]
	if g == nil {
		g = &roomGate{token: make(chan struct{}, 1)}
		s.roomGates[id] = g
	}
	g.refs++
	s.mu.Unlock()
	drop := func() {
		s.mu.Lock()
		g.refs--
		if g.refs == 0 {
			delete(s.roomGates, id)
		}
		s.mu.Unlock()
	}
	select {
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case g.token <- struct{}{}:
		return func() { <-g.token; drop() }, nil
	}
}
