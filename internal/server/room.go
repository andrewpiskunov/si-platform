package server

import (
	"context"
	"time"

	"siplatform/internal/game"
)

type reply struct {
	value any
	err   error
}
type job struct {
	ctx    context.Context
	run    func(context.Context, *room) (any, error)
	result chan reply
}
type room struct {
	id           string
	server       *Server
	requests     chan job
	clients      map[*client]struct{}
	limits       map[string]*bucket
	pausePending bool
}

func (r *room) run() {
	defer func() {
		for c := range r.clients {
			c.cancel()
		}
	}()
	for {
		select {
		case <-r.server.ctx.Done():
			return
		case j := <-r.requests:
			if j.ctx.Err() != nil {
				j.result <- reply{err: j.ctx.Err()}
				continue
			}
			ctx, cancel := context.WithTimeout(j.ctx, 5*time.Second)
			stop := context.AfterFunc(r.server.ctx, cancel)
			value, err := j.run(ctx, r)
			stop()
			cancel()
			j.result <- reply{value: value, err: err}
		}
	}
}
func (r *room) do(ctx context.Context, run func(context.Context, *room) (any, error)) (any, error) {
	j := job{ctx: ctx, result: make(chan reply, 1), run: func(ctx context.Context, r *room) (any, error) {
		// Every new request, including reconnects and joins, must first make a
		// previously failed disconnect pause durable. Detach cleanup bypasses do.
		if err := r.resolvePause(ctx); err != nil {
			return nil, err
		}
		return run(ctx, r)
	}}
	select {
	case <-r.server.ctx.Done():
		return nil, game.Fault("unavailable")
	case <-ctx.Done():
		return nil, ctx.Err()
	case r.requests <- j:
	default:
		return nil, game.Fault("busy")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.server.ctx.Done():
		return nil, game.Fault("unavailable")
	case result := <-j.result:
		return result.value, result.err
	}
}
func (r *room) attach(ctx context.Context, c *client) error {
	_, err := r.do(ctx, func(ctx context.Context, r *room) (any, error) {
		count := 0
		for existing := range r.clients {
			if existing.id == c.id && existing.ctx.Err() == nil {
				count++
			}
		}
		if count >= 2 {
			return nil, game.Fault("connection_limit")
		}
		state, err := r.server.store.Load(ctx, r.id)
		if err != nil {
			return nil, err
		}
		participant, ok := state.Participant(c.id)
		if !ok {
			return nil, game.Fault("unauthorized")
		}
		c.role = participant.Role
		if c.ctx.Err() != nil {
			return nil, c.ctx.Err()
		}
		r.clients[c] = struct{}{}
		if r.limits[c.id] == nil {
			r.limits[c.id] = newBucket()
		}
		view := game.Project(state, c.id)
		if !c.send(Message{Type: "snapshot", State: &view}) {
			return nil, game.Fault("slow_client")
		}
		return nil, nil
	})
	return err
}
func (r *room) detach(c *client) {
	// Removal must not be dropped when the ordinary command queue is full.
	j := job{ctx: r.server.ctx, result: make(chan reply, 1), run: func(ctx context.Context, r *room) (any, error) {
		delete(r.clients, c)
		if c.role != game.Host {
			return nil, nil
		}
		for existing := range r.clients {
			if existing.role == game.Host && existing.ctx.Err() == nil {
				return nil, nil
			}
		}
		r.pausePending = true
		if err := r.resolvePause(ctx); err != nil {
			// Keep the pending pause after a failed/ambiguous commit and close
			// current connections. A reconnect must resolve it before attaching.
			for existing := range r.clients {
				existing.cancel()
			}
			return nil, err
		}
		return nil, nil
	}}
	select {
	case r.requests <- j:
	case <-r.server.ctx.Done():
	}
}
func (r *room) resolvePause(ctx context.Context) error {
	if !r.pausePending {
		return nil
	}
	state, err := r.server.store.Pause(ctx, r.id, "host_disconnected")
	if err != nil {
		return err
	}
	r.pausePending = false
	r.broadcast(state, nil)
	return nil
}
func (r *room) command(ctx context.Context, c *client, cmd game.Command) error {
	_, err := r.do(ctx, func(ctx context.Context, r *room) (any, error) {
		if _, ok := r.clients[c]; !ok {
			return nil, game.Fault("unauthorized")
		}
		if !r.limits[c.id].allow(time.Now()) {
			return nil, game.Fault("rate_limited")
		}
		// A connected host is required even when the persisted room is paused.
		hostOnline := false
		for existing := range r.clients {
			if existing.role == game.Host && existing.ctx.Err() == nil {
				hostOnline = true
			}
		}
		if !hostOnline {
			return nil, game.Fault("host_offline")
		}
		out, err := r.server.store.Execute(ctx, r.id, c.id, cmd)
		if err != nil {
			return nil, err
		}
		view := game.Project(out.State, c.id)
		c.send(Message{Type: "result", CommandID: cmd.ID, Code: out.Code, ResultSeq: out.Seq, Duplicate: out.Duplicate, State: &view})
		if out.Code == "ok" {
			// A duplicate repairs distribution after an ambiguous commit without
			// reapplying the transition or advancing the sequence.
			r.broadcast(out.State, c)
		}
		return nil, nil
	})
	return err
}
func (r *room) broadcast(state game.State, except *client) {
	for c := range r.clients {
		if c == except {
			continue
		}
		view := game.Project(state, c.id)
		c.send(Message{Type: "snapshot", State: &view})
	}
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newBucket() *bucket                   { return &bucket{tokens: 20} }
func (b *bucket) allow(now time.Time) bool { return b.take(now, 10, 20) }
func (b *bucket) take(now time.Time, rate, burst float64) bool {
	if b.last.IsZero() {
		b.tokens = burst
	} else {
		b.tokens = min(burst, b.tokens+now.Sub(b.last).Seconds()*rate)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
