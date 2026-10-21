package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"siplatform/internal/testutil"
	"siplatform/migrations"
	"strings"
	"testing"
	"time"

	"siplatform/internal/game"
	"siplatform/internal/storage/postgres"
)

func removeMember(t *testing.T, s *postgres.Store, h postgres.Credentials, target, mode, commandID string) postgres.Outcome {
	t.Helper()
	state, err := s.Load(context.Background(), h.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"target_id": target, "mode": mode})
	out, err := s.Execute(context.Background(), h.RoomID, h.ParticipantID, game.Command{Version: 2, ID: commandID, Type: "remove_participant", ExpectedSeq: &state.Seq, Data: raw})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestMembershipKickBanAndBoundedHistory(t *testing.T) {
	ctx := context.Background()
	s, h, _ := v2Room(t, game.DefaultRules())
	guest := strings.Repeat("a", 64)
	first, err := s.JoinWithGuest(ctx, h.RoomID, h.InviteToken, "First", game.Spectator, guest)
	if err != nil {
		t.Fatal(err)
	}
	if out := removeMember(t, s, h, first.ParticipantID, "kick", "kick-first"); out.Code != "ok" {
		t.Fatal(out.Code)
	}
	if _, err = s.Authenticate(ctx, h.RoomID, first.Token); game.Code(err) != "unauthorized" {
		t.Fatal("removed credential", err)
	}
	second, err := s.JoinWithGuest(ctx, h.RoomID, h.InviteToken, "Second", game.Spectator, guest)
	if err != nil {
		t.Fatal(err)
	}
	third, err := s.JoinWithGuest(ctx, h.RoomID, h.InviteToken, "Third", game.Spectator, guest)
	if err != nil {
		t.Fatal(err)
	}
	if out := removeMember(t, s, h, second.ParticipantID, "ban_guest_session", "ban-second"); out.Code != "ok" {
		t.Fatal(out.Code)
	}
	if _, err = s.Authenticate(ctx, h.RoomID, third.Token); game.Code(err) != "unauthorized" {
		t.Fatal("group credential", err)
	}
	if _, err = s.JoinWithGuest(ctx, h.RoomID, h.InviteToken, "Again", game.Spectator, guest); game.Code(err) != "guest_banned" {
		t.Fatal("banned admission", err)
	}
	for i := range 70 {
		p, e := s.JoinWithGuest(ctx, h.RoomID, h.InviteToken, fmt.Sprintf("Person%d", i), game.Spectator, strings.Repeat("b", 64))
		if e != nil {
			t.Fatal(e)
		}
		if out := removeMember(t, s, h, p.ParticipantID, "kick", fmt.Sprintf("kick-%d", i)); out.Code != "ok" {
			t.Fatal(out.Code)
		}
	}
	state, _ := s.Load(ctx, h.RoomID)
	if len(state.Participants) > 13 {
		t.Fatal("unbounded live roster", len(state.Participants))
	}
	roster, err := s.Roster(ctx, h.RoomID, h.ParticipantID, "", 50)
	if err != nil || len(roster.Items) != 50 || roster.NextAfter == "" {
		t.Fatal("first page", roster, err)
	}
	next, err := s.Roster(ctx, h.RoomID, h.ParticipantID, roster.NextAfter, 50)
	if err != nil || len(next.Items) < 24 || next.NextAfter != "" {
		t.Fatal("second page", next, err)
	}
	if _, err = s.Roster(ctx, h.RoomID, first.ParticipantID, "", 50); game.Code(err) != "unauthorized" {
		t.Fatal("removed history access", err)
	}
	results, err := s.Results(ctx, h.RoomID, h.ParticipantID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range results.Participants {
		if p.ID == first.ParticipantID && p.Name == "First" && p.Removed {
			found = true
		}
	}
	if !found {
		t.Fatal("archived event name missing")
	}
	encoded, _ := json.Marshal(results)
	if strings.Contains(string(encoded), guest) || strings.Contains(string(encoded), "guest_hash") {
		t.Fatal("private guest identity leaked")
	}
}
func TestMembershipLegacyBindOnceAndHostGroup(t *testing.T) {
	ctx := context.Background()
	s, h, _ := v2Room(t, game.DefaultRules())
	guest := strings.Repeat("a", 64)
	if err := s.BindGuest(ctx, h.RoomID, h.Token, guest); err != nil {
		t.Fatal(err)
	}
	if err := s.BindGuest(ctx, h.RoomID, h.Token, strings.Repeat("b", 64)); game.Code(err) != "guest_session_mismatch" {
		t.Fatal("rebound session", err)
	}
	if err := s.BindGuest(ctx, h.RoomID, strings.Repeat("c", 64), guest); game.Code(err) != "unauthorized" {
		t.Fatal("cookie granted identity", err)
	}
	p, err := s.JoinWithGuest(ctx, h.RoomID, h.InviteToken, "Same browser", game.Spectator, guest)
	if err != nil {
		t.Fatal(err)
	}
	if out := removeMember(t, s, h, p.ParticipantID, "ban_guest_session", "ban-host-group"); out.Code != "forbidden" {
		t.Fatal("host group", out.Code)
	}
	if _, err = s.Authenticate(ctx, h.RoomID, p.Token); err != nil {
		t.Fatal("partial group revoke", err)
	}
	if _, err = s.Authenticate(ctx, h.RoomID, h.Token); err != nil {
		t.Fatal("host revoked", err)
	}
}

func TestMembershipReplayAndQueuedAdmission(t *testing.T) {
	ctx := context.Background()
	s, h, players := v2Room(t, game.DefaultRules())
	guest := strings.Repeat("e", 64)
	p, err := s.JoinWithGuest(ctx, h.RoomID, h.InviteToken, "Target", game.Spectator, guest)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := s.Load(ctx, h.RoomID)
	raw, _ := json.Marshal(map[string]string{"target_id": p.ParticipantID, "mode": "ban_guest_session"})
	cmd := game.Command{Version: 2, ID: "ban-replay", Type: "remove_participant", ExpectedSeq: &state.Seq, Data: raw}
	out, err := s.Execute(ctx, h.RoomID, h.ParticipantID, cmd)
	if err != nil || out.Code != "ok" {
		t.Fatal(out, err)
	}
	duplicate, err := s.Execute(ctx, h.RoomID, h.ParticipantID, cmd)
	if err != nil || !duplicate.Duplicate || duplicate.Seq != out.Seq {
		t.Fatal("replay", duplicate, err)
	}
	if _, err = s.Execute(ctx, h.RoomID, p.ParticipantID, cmd); game.Code(err) != "unauthorized" {
		t.Fatal("revoked replay", err)
	}
	// A join already waiting on the room must see a committed ban, not its earlier invitation.
	tx, err := s.Pool().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT id FROM rooms WHERE id=$1 FOR UPDATE", h.RoomID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, e := s.JoinWithGuest(ctx, h.RoomID, h.InviteToken, "Queued", game.Spectator, guest)
		done <- e
	}()
	deadline := time.Now().Add(3 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		if err = s.Pool().QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))", tx.Conn().PgConn().PID()).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("join did not wait on room")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; game.Code(err) != "guest_banned" {
		t.Fatal("queued banned join", err)
	}
	// Legacy unbound targets can be kicked but cannot promise a browser-session ban.
	if out := removeMember(t, s, h, players[0].ParticipantID, "ban_guest_session", "legacy-ban"); out.Code != "guest_session_unavailable" {
		t.Fatal(out.Code)
	}
	if out := removeMember(t, s, h, players[0].ParticipantID, "kick", "legacy-kick"); out.Code != "ok" {
		t.Fatal(out.Code)
	}
	for _, cursor := range []string{"invalid", strings.Repeat("A", 32)} {
		if _, err = s.Roster(ctx, h.RoomID, h.ParticipantID, cursor, 50); game.Code(err) != "invalid_input" {
			t.Fatal("cursor", err)
		}
	}
}
func TestMembershipMigrationBackfill(t *testing.T) {
	ctx := context.Background()
	pool := testutil.Pool(t)
	sqlDB := stdlib.OpenDB(*pool.Config().ConnConfig)
	defer sqlDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 11); err != nil {
		t.Fatal(err)
	}
	id, person := strings.Repeat("a", 32), strings.Repeat("b", 32)
	state := game.State{Settings: game.RoomSettings{Visibility: "private"}, ID: id, Seq: 1, Phase: game.Lobby, Participants: []game.Participant{{ID: person, Name: "Old host", Role: game.Host, Score: 42}}}
	raw, _ := json.Marshal(state)
	if _, err = pool.Exec(ctx, "INSERT INTO rooms(id,snapshot,seq,invite_hash) VALUES($1,$2,1,'old')", id, raw); err != nil {
		t.Fatal(err)
	}
	if err = postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	result, err := postgres.New(pool).Roster(ctx, id, person, "", 50)
	if err != nil || len(result.Items) != 1 || result.Items[0].Score != 42 || result.Items[0].Removed {
		t.Fatal("backfill", result, err)
	}
	if _, err = provider.DownTo(ctx, 11); err != nil {
		t.Fatal(err)
	}
	if err = postgres.Migrate(ctx, pool); err != nil {
		t.Fatal("reapply", err)
	}
	result, err = postgres.New(pool).Roster(ctx, id, person, "", 50)
	if err != nil || len(result.Items) != 1 {
		t.Fatal(result, err)
	}
}
