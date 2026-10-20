package postgres_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"siplatform/internal/game"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
)

func room(t *testing.T) (*postgres.Store, postgres.Credentials, []postgres.Credentials) {
	t.Helper()
	pool := testutil.Pool(t)
	if err := postgres.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	s := postgres.New(pool)
	h, err := s.Create(context.Background(), "Ведущий")
	if err != nil {
		t.Fatal(err)
	}
	var players []postgres.Credentials
	for i := 0; i < 12; i++ {
		p, err := s.Join(context.Background(), h.RoomID, h.InviteToken, fmt.Sprintf("Игрок %d", i), game.Player)
		if err != nil {
			t.Fatal(err)
		}
		players = append(players, p)
	}
	return s, h, players
}

func execute(t *testing.T, s *postgres.Store, h postgres.Credentials, c game.Command) postgres.Outcome {
	t.Helper()
	c.Version = 1
	out, err := s.Execute(context.Background(), h.RoomID, h.ParticipantID, c)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDurableDuplicateAndConflict(t *testing.T) {
	s, h, p := room(t)
	execute(t, s, h, game.Command{ID: "open", Type: "open_question", QuestionID: "q1", Text: "Q", Answer: "secret", Value: 100})
	execute(t, s, p[0], game.Command{ID: "buzz", Type: "buzz", QuestionID: "q1"})
	correct := true
	cmd := game.Command{Version: 1, ID: "judge", Type: "judge", QuestionID: "q1", Correct: &correct}
	first := execute(t, s, h, cmd)
	if first.Code != "ok" || first.State.Participants[1].Score != 100 {
		t.Fatalf("first: %+v", first)
	}
	restarted := postgres.New(s.Pool())
	duplicate := execute(t, restarted, h, cmd)
	if !duplicate.Duplicate || duplicate.Seq != first.Seq || duplicate.State.Seq != first.State.Seq || duplicate.State.Participants[1].Score != 100 {
		t.Fatalf("duplicate: %+v", duplicate)
	}
	wrong := false
	cmd.Correct = &wrong
	conflict := execute(t, restarted, h, cmd)
	if conflict.Code != "command_conflict" || conflict.State.Participants[1].Score != 100 {
		t.Fatalf("conflict: %+v", conflict)
	}
	var events int
	if err := s.Pool().QueryRow(context.Background(), "SELECT count(*) FROM game_events WHERE room_id=$1", h.RoomID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 16 {
		t.Fatalf("want 1 create + 12 joins + 3 commands, got %d", events)
	}
}

func TestConcurrentBuzz(t *testing.T) {
	s, h, p := room(t)
	execute(t, s, h, game.Command{ID: "open", Type: "open_question", QuestionID: "q1", Text: "Q", Answer: "A", Value: 100})
	start := make(chan struct{})
	results := make(chan postgres.Outcome, 12)
	errs := make(chan error, 12)
	var wg sync.WaitGroup
	for _, player := range p {
		wg.Add(1)
		go func(player postgres.Credentials) {
			defer wg.Done()
			<-start
			out, err := s.Execute(context.Background(), h.RoomID, player.ParticipantID, game.Command{Version: 1, ID: "buzz", Type: "buzz", QuestionID: "q1"})
			results <- out
			errs <- err
		}(player)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	winners := 0
	for out := range results {
		if out.Code == "ok" {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("winners=%d", winners)
	}
}

func TestAuthenticationAndCapacity(t *testing.T) {
	s, h, p := room(t)
	ctx := context.Background()
	id, err := s.Authenticate(ctx, h.RoomID, p[0].Token)
	if err != nil || id != p[0].ParticipantID {
		t.Fatalf("auth: %s %v", id, err)
	}
	if _, err = s.Authenticate(ctx, "different", p[0].Token); err == nil {
		t.Fatal("cross-room auth")
	}
	if _, err = s.Authenticate(ctx, h.RoomID, "invalid"); err == nil {
		t.Fatal("invalid auth")
	}
	if _, err = s.Join(ctx, h.RoomID, "invalid", "Intruder", game.Spectator); err == nil {
		t.Fatal("bad invitation")
	}
	if _, err = s.Join(ctx, h.RoomID, h.InviteToken, "Another host", game.Host); err == nil {
		t.Fatal("host injected")
	}
	if _, err = s.Join(ctx, h.RoomID, h.InviteToken, "Thirteenth", game.Player); game.Code(err) != "room_full" {
		t.Fatalf("capacity: %v", err)
	}
	for i := 0; i < 20; i++ {
		if _, err = s.Join(ctx, h.RoomID, h.InviteToken, "Observer", game.Spectator); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Join(ctx, h.RoomID, h.InviteToken, "Observer", game.Spectator); game.Code(err) != "room_full" {
		t.Fatalf("spectator capacity: %v", err)
	}
}

func TestConcurrentJoinCapacity(t *testing.T) {
	s, _, _ := room(t)
	ctx := context.Background()
	h, err := s.Create(ctx, "Host")
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 20)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Join(ctx, h.RoomID, h.InviteToken, "Player", game.Player)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	joined := 0
	for err := range results {
		if err == nil {
			joined++
		} else if game.Code(err) != "room_full" {
			t.Fatal(err)
		}
	}
	if joined != 12 {
		t.Fatalf("joined %d", joined)
	}
}

func TestTransactionRollbackAndRecovery(t *testing.T) {
	s, h, p := room(t)
	ctx := context.Background()
	execute(t, s, h, game.Command{ID: "open", Type: "open_question", QuestionID: "q1", Text: "Q", Answer: "A", Value: 100})
	before, err := s.Load(ctx, h.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Pool().Exec(ctx, `CREATE FUNCTION fail_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected failure'; END $$;
CREATE TRIGGER fail_event BEFORE INSERT ON game_events FOR EACH ROW EXECUTE FUNCTION fail_event();`)
	if err != nil {
		t.Fatal(err)
	}
	cmd := game.Command{Version: 1, ID: "buzz", Type: "buzz", QuestionID: "q1"}
	if _, err = s.Execute(ctx, h.RoomID, p[0].ParticipantID, cmd); err == nil {
		t.Fatal("commit acknowledged despite failure")
	}
	after, err := s.Load(ctx, h.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Seq != before.Seq || after.Winner != "" {
		t.Fatalf("partial transaction: %+v", after)
	}
	if _, err = s.Pool().Exec(ctx, "DROP TRIGGER fail_event ON game_events"); err != nil {
		t.Fatal(err)
	}
	if out := execute(t, s, p[0], cmd); out.Duplicate || out.Code != "ok" {
		t.Fatalf("retry: %+v", out)
	}
	if err = s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.Load(ctx, h.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Phase != game.Paused || recovered.Winner != p[0].ParticipantID || recovered.ResumePhase != game.Answering {
		t.Fatalf("recovered: %+v", recovered)
	}
}
