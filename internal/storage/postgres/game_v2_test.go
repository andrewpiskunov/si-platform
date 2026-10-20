package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"siplatform/internal/game"
	"siplatform/internal/storage/postgres"
)

func v2Room(t *testing.T, rules game.Rules) (*postgres.Store, postgres.Credentials, []postgres.Credentials) {
	t.Helper()
	s, h, p := room(t)
	state, err := s.Load(context.Background(), h.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	state.Gameplay = game.NewSession(rules)
	saveFixture(t, s, state)
	return s, h, p
}
func saveFixture(t *testing.T, s *postgres.Store, state game.State) {
	t.Helper()
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pool().Exec(context.Background(), "UPDATE rooms SET snapshot=$2 WHERE id=$1", state.ID, raw); err != nil {
		t.Fatal(err)
	}
}
func v2(t *testing.T, s *postgres.Store, h postgres.Credentials, id, kind string, data any) postgres.Outcome {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.Load(context.Background(), h.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	cmd := game.Command{Version: 2, ID: id, Type: kind, Data: raw}
	if p, _ := state.Participant(h.ParticipantID); p.Role == game.Host {
		cmd.ExpectedSeq = &state.Seq
	}
	out, err := s.Execute(context.Background(), h.RoomID, h.ParticipantID, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if out.Code != "ok" {
		t.Fatalf("%s: %s", kind, out.Code)
	}
	return out
}
func v2Question(t *testing.T, s *postgres.Store, h postgres.Credentials) game.Stage {
	t.Helper()
	v2(t, s, h, "round", "begin_round", game.BeginRoundPayload{Round: game.RoundSpec{ID: "r", Name: "Round", Type: "standard", Themes: []game.ThemeSpec{{ID: "theme", Name: "Theme", QuestionID: "q"}}, MinimumPrice: 100, MaximumPrice: 500}})
	out := v2(t, s, h, "question", "begin_question", game.BeginQuestionPayload{Question: game.QuestionSpec{ID: "q", RoundID: "r", ThemeID: "theme", Theme: "Theme", Type: "simple", Price: 100, Question: []game.TextStep{{Text: "Question"}}, Right: []string{"PRIVATE-ANSWER"}}})
	return out.State.Gameplay.Stage
}
func TestV2ServerClockAndDuplicatePenalty(t *testing.T) {
	s, h, p := v2Room(t, game.DefaultRules())
	stage := v2Question(t, s, h)
	raw, _ := json.Marshal(game.StagePayload{StageID: stage.ID, InstanceID: stage.InstanceID})
	cmd := game.Command{Version: 2, ID: "early", Type: "buzz", Data: raw}
	first, err := s.Execute(context.Background(), h.RoomID, p[0].ParticipantID, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if first.Code != "ok" {
		t.Fatalf("falsestart must persist: %s", first.Code)
	}
	var blocked int64
	for _, x := range game.ProjectAt(first.State, p[0].ParticipantID, time.Now().UnixMilli()).Gameplay.Players {
		if x.ID == p[0].ParticipantID {
			blocked = x.BlockedUntilMS
		}
	}
	if blocked <= time.Now().UnixMilli() {
		t.Fatal("server time was not injected")
	}
	repeated, err := s.Execute(context.Background(), h.RoomID, p[0].ParticipantID, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if !repeated.Duplicate || repeated.Seq != first.Seq {
		t.Fatal("time injection changed request identity")
	}
	var clock int64
	if err = s.Pool().QueryRow(context.Background(), "SELECT (payload->>'server_time_ms')::bigint FROM game_events WHERE room_id=$1 AND seq=$2", h.RoomID, first.Seq).Scan(&clock); err != nil || clock <= 0 {
		t.Fatalf("replay clock missing: %v %d", err, clock)
	}
}
func TestV2RandomWindowRecoveryAndExpiry(t *testing.T) {
	rules := game.DefaultRules()
	rules.BuzzerMode = "random"
	rules.WindowMS = 100
	s, h, p := v2Room(t, rules)
	stage := v2Question(t, s, h)
	out := v2(t, s, h, "open", "open_buzzer", game.StagePayload{StageID: stage.ID, InstanceID: stage.InstanceID})
	stage = out.State.Gameplay.Stage
	first := v2(t, s, p[0], "press", "buzz", game.StagePayload{StageID: stage.ID, InstanceID: stage.InstanceID})
	second := v2(t, s, p[1], "press", "buzz", game.StagePayload{StageID: stage.ID, InstanceID: stage.InstanceID})
	if first.State.Gameplay.Question.Seed != second.State.Gameplay.Question.Seed || len(second.State.Gameplay.Question.Candidates) != 2 {
		t.Fatal("randomwindow lost candidate or seed")
	}
	restarted := postgres.New(s.Pool())
	if err := restarted.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	paused, err := restarted.Load(context.Background(), h.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if paused.Phase != game.Paused || len(paused.Gameplay.Question.Candidates) != 2 {
		t.Fatal("recovery did not preserve collection")
	}
	v2(t, restarted, h, "resume", "resume", game.EmptyPayload{})
	time.Sleep(120 * time.Millisecond)
	finished, err := restarted.Advance(context.Background(), h.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Gameplay.Stage.Kind != "collect_answer" {
		t.Fatalf("window not settled: %+v", finished.Gameplay.Stage)
	}
	winner := finished.Gameplay.Stage.TurnID
	if winner != p[0].ParticipantID && winner != p[1].ParticipantID {
		t.Fatal("noncandidate selected")
	}
	again, err := restarted.Advance(context.Background(), h.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Seq != finished.Seq || again.Gameplay.Stage.TurnID != winner {
		t.Fatal("timer settled twice")
	}
}
