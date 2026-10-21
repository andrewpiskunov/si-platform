package game

import (
	"encoding/json"
	"testing"
)

func TestLobbyReadiness(t *testing.T) {
	s := fixture()
	s.Seq = 1
	s.Gameplay = NewSession(DefaultRules())
	// Decode through JSON to exercise the wire contract.
	if err := json.Unmarshal([]byte(`{"visibility":"private","voice_url":"","require_ready":true}`), &s.Settings); err != nil {
		t.Fatal(err)
	}
	round := BeginRoundPayload{Round: RoundSpec{ID: "r", Name: "Round", Type: "standard", Themes: []ThemeSpec{{ID: "t", Name: "Theme", QuestionID: "q"}}, MaximumPrice: 500}}
	rejected(t, s, "host", "begin_round", round, 1000)
	for _, actor := range []string{"host", "s1"} {
		rejected(t, s, actor, "set_ready", map[string]any{"ready": true}, 1000)
	}
	for _, data := range []map[string]any{{}, {"ready": nil}, {"ready": "yes"}, {"ready": true, "target_id": "p2"}} {
		rejected(t, s, "p1", "set_ready", data, 1000)
	}
	s = do(t, s, "p1", "set_ready", map[string]any{"ready": true})
	rejected(t, s, "host", "begin_round", round, 1000)
	s = do(t, s, "p2", "set_ready", map[string]any{"ready": true})
	s = do(t, s, "p1", "set_ready", map[string]any{"ready": false})
	rejected(t, s, "host", "begin_round", round, 1000)
	s = do(t, s, "p1", "set_ready", map[string]any{"ready": true})
	raw, _ := json.Marshal(s)
	var restored State
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	s = do(t, restored, "host", "begin_round", round)
	rejected(t, s, "p1", "set_ready", map[string]any{"ready": false}, 1000)
}

func TestLobbyIndependentReadyAndMembership(t *testing.T) {
	s := fixture()
	s.Seq = 3
	s.Gameplay = NewSession(DefaultRules())
	s.Settings.RequireReady = true
	stale := int64(1)
	c := v2cmd("set_ready", map[string]any{"ready": true})
	c.ExpectedSeq = &stale
	var err error
	for _, id := range []string{"p1", "p2"} {
		s, err = ApplyAt(s, id, c, 1000, 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !lobbyCanStart(s) {
		t.Fatal("independent ready commands must not conflict")
	}
	s.Participants = append(s.Participants, Participant{ID: "p3", Role: Player, Name: "New"})
	if lobbyCanStart(s) {
		t.Fatal("new player must block start")
	}
	s.Participants[len(s.Participants)-1].Removed = true
	if !lobbyCanStart(s) {
		t.Fatal("removed player blocks start")
	}
	s.Participants[2].Removed = true
	if lobbyCanStart(s) {
		t.Fatal("only one player")
	}
	s.Phase = Running
	if !lobbyCanStart(s) {
		t.Fatal("readiness must not gate later rounds")
	}
}

func TestLobbySettingsStrictAndCompatible(t *testing.T) {
	for _, raw := range []string{`{"require_ready":null}`, `{"require_ready":"true"}`} {
		var settings RoomSettings
		if json.Unmarshal([]byte(raw), &settings) == nil {
			t.Fatal("invalid ready setting accepted")
		}
	}
	var settings RoomSettings
	if err := json.Unmarshal([]byte(`{"visibility":"private","voice_url":""}`), &settings); err != nil || settings.RequireReady {
		t.Fatal("legacy settings changed", err)
	}
}

func TestLobbyReadyActionUnique(t *testing.T) {
	s := fixture()
	s.Gameplay = NewSession(DefaultRules())
	count := 0
	for _, a := range allowedActions(s, "p1", 1000) {
		if a.Type == "set_ready" {
			count++
			if a.RequiresSeq {
				t.Fatal("global CAS for independent intent")
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected one ready action, got %d", count)
	}
}
