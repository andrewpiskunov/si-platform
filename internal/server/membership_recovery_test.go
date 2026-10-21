package server

import (
	"context"
	"encoding/json"
	"siplatform/internal/game"
	"testing"
)

func TestMembershipReconcilesLostDeliveryAndReadFailure(t *testing.T) {
	s, _, store := setup(t)
	ctx := context.Background()
	h, err := store.Create(ctx, "Host")
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.Join(ctx, h.RoomID, h.InviteToken, "Player", game.Player)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Load(ctx, h.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	state.Gameplay = game.NewSession(game.DefaultRules())
	raw, _ := json.Marshal(state)
	if _, err = store.Pool().Exec(ctx, "UPDATE rooms SET snapshot=$2 WHERE id=$1", h.RoomID, raw); err != nil {
		t.Fatal(err)
	}
	makeClient := func(id string) *client {
		c, cancel := context.WithCancel(ctx)
		t.Cleanup(cancel)
		return &client{id: id, ctx: c, cancel: cancel, out: newOutbox()}
	}
	hc, pc, appearance := makeClient(h.ParticipantID), makeClient(p.ParticipantID), makeClient(p.ParticipantID)
	appearance.appearanceOnly = true
	r := &room{id: h.RoomID, server: s, clients: map[*client]struct{}{hc: {}, pc: {}, appearance: {}}, limits: map[string]*bucket{h.ParticipantID: newBucket(), p.ParticipantID: newBucket()}}
	data, _ := json.Marshal(game.RemovalPayload{TargetID: p.ParticipantID, Mode: "kick"})
	cmd := game.Command{Version: 2, ID: "lost-delivery", Type: "remove_participant", ExpectedSeq: &state.Seq, Data: data}
	out, err := store.Execute(ctx, h.RoomID, h.ParticipantID, cmd)
	if err != nil || out.Code != "ok" {
		t.Fatal(out, err)
	}
	// Durable commit, lost acknowledgement: no successful local outcome is assumed.
	r.membershipPending = true
	failed, cancel := context.WithCancel(ctx)
	cancel()
	if err = r.reconcileMembership(failed); err == nil || !r.membershipPending {
		t.Fatal("failed read accepted", err)
	}
	if hc.ctx.Err() == nil || pc.ctx.Err() == nil || appearance.ctx.Err() == nil {
		t.Fatal("read failure did not close clients")
	}
	if err = r.reconcileMembership(ctx); err != nil || r.membershipPending {
		t.Fatal("retry reconcile", err)
	}
	if _, exists := r.clients[pc]; exists {
		t.Fatal("normal socket retained")
	}
	if _, exists := r.clients[appearance]; exists {
		t.Fatal("appearance socket retained")
	}
	if r.limits[p.ParticipantID] != nil {
		t.Fatal("rate bucket retained")
	}
	duplicate, err := store.Execute(ctx, h.RoomID, h.ParticipantID, cmd)
	if err != nil || !duplicate.Duplicate {
		t.Fatal(duplicate, err)
	}
	late := makeClient(p.ParticipantID)
	r.clients[late] = struct{}{}
	r.broadcast(duplicate.State, nil)
	if _, exists := r.clients[late]; exists || !late.revoked.Load() {
		t.Fatal("duplicate broadcast did not repair eviction")
	}
	for len(late.out.messages) > 0 {
		frame := <-late.out.messages
		if len(frame) != 0 {
			t.Fatal("removed participant received new frame")
		}
	}
}

func TestMembershipPreauthenticatedAttachIsRejected(t *testing.T) {
	s, _, store := setup(t)
	ctx := context.Background()
	h, err := store.Create(ctx, "Host")
	if err != nil {
		t.Fatal(err)
	}
	p, err := store.Join(ctx, h.RoomID, h.InviteToken, "Player", game.Player)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load(ctx, h.RoomID)
	state.Gameplay = game.NewSession(game.DefaultRules())
	raw, _ := json.Marshal(state)
	if _, err = store.Pool().Exec(ctx, "UPDATE rooms SET snapshot=$2 WHERE id=$1", h.RoomID, raw); err != nil {
		t.Fatal(err)
	}
	id, err := store.Authenticate(ctx, h.RoomID, p.Token)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(game.RemovalPayload{TargetID: id, Mode: "kick"})
	out, err := store.Execute(ctx, h.RoomID, h.ParticipantID, game.Command{Version: 2, ID: "before-attach", Type: "remove_participant", ExpectedSeq: &state.Seq, Data: data})
	if err != nil || out.Code != "ok" {
		t.Fatal(out, err)
	}
	rm, err := s.getRoom(ctx, h.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	for _, appearance := range []bool{false, true} {
		cctx, cancel := context.WithCancel(ctx)
		c := &client{id: id, appearanceOnly: appearance, ctx: cctx, cancel: cancel, out: newOutbox()}
		err = rm.attach(ctx, c)
		cancel()
		if game.Code(err) != "unauthorized" {
			t.Fatal("preauthenticated attach", appearance, err)
		}
	}
}
