package postgres_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"siplatform/internal/game"
)

func TestInviteRotationAndPrivateJournal(t *testing.T) {
	s, h, players := room(t)
	ctx := context.Background()
	if _, _, err := s.RotateInvite(ctx, h.RoomID, players[0].ParticipantID); game.Code(err) != "forbidden" {
		t.Fatalf("player rotated invite: %v", err)
	}
	token, state, err := s.RotateInvite(ctx, h.RoomID, h.ParticipantID)
	if err != nil {
		t.Fatal(err)
	}
	if token == h.InviteToken || len(token) != 64 {
		t.Fatal("invitation not rotated")
	}
	if state.Seq != 14 {
		t.Fatalf("rotation not evented: %d", state.Seq)
	}
	if _, err = s.Join(ctx, h.RoomID, h.InviteToken, "Old", game.Spectator); game.Code(err) != "unauthorized" {
		t.Fatalf("old invitation stillvalid: %v", err)
	}
	if _, err = s.Join(ctx, h.RoomID, token, "New", game.Spectator); err != nil {
		t.Fatal(err)
	}
	if id, err := s.Authenticate(ctx, h.RoomID, players[0].Token); err != nil || id != players[0].ParticipantID {
		t.Fatal("rotation revoked existing participant")
	}
	execute(t, s, h, game.Command{ID: "open", Type: "open_question", QuestionID: "q", Text: "VisibleQuestion", Answer: "SECRET-ANSWER", Value: 100})
	result, err := s.Results(ctx, h.RoomID, players[0].ParticipantID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 || result.NextAfterSeq != 2 {
		t.Fatalf("pagination %+v", result)
	}
	result, err = s.Results(ctx, h.RoomID, h.ParticipantID, result.NextAfterSeq, 100)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{h.Token, h.InviteToken, token, "SECRET-ANSWER", "VisibleQuestion", "token_hash", "payload"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("private journal leak", secret)
		}
	}
	if _, err = s.Results(ctx, h.RoomID, "unknown", 0, 100); game.Code(err) != "unauthorized" {
		t.Fatalf("anonymous journal: %v", err)
	}
}
