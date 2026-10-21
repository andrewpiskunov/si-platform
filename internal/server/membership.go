package server

import (
	"context"
	"siplatform/internal/game"
)

// evictInactive is actor-owned. It runs before any projection of a new snapshot.
func (r *room) evictInactive(state game.State) {
	for c := range r.clients {
		if _, ok := state.ActiveParticipant(c.id); !ok {
			delete(r.clients, c)
			delete(r.limits, c.id)
			c.revoke()
		}
	}
}
func (r *room) reconcileMembership(ctx context.Context) error {
	if !r.membershipPending {
		return nil
	}
	state, err := r.server.store.Load(ctx, r.id)
	if err != nil {
		for c := range r.clients {
			c.cancel()
		}
		return err
	}
	r.evictInactive(state)
	r.membershipPending = false
	return nil
}
