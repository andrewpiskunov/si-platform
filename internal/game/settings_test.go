package game

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestRoomSettingsCommandEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name  string
		raw   string
		valid bool
	}{
		{"complete", `{"v":2,"id":"settings","type":"set_room_settings","expected_seq":1,"data":{"settings":{"visibility":"public","voice_url":"https://voice.example/room"}}}`, true},
		{"empty_voice", `{"v":2,"id":"settings","type":"set_room_settings","expected_seq":1,"data":{"settings":{"visibility":"private","voice_url":""}}}`, true},
		{"omitted_optional_voice", `{"v":2,"id":"settings","type":"set_room_settings","expected_seq":1,"data":{"settings":{"visibility":"private"}}}`, true},
		{"null_voice", `{"v":2,"id":"settings","type":"set_room_settings","expected_seq":1,"data":{"settings":{"visibility":"public","voice_url":null}}}`, false},
		{"missing_seq", `{"v":2,"id":"settings","type":"set_room_settings","data":{"settings":{"visibility":"public","voice_url":""}}}`, false},
		{"missing_settings", `{"v":2,"id":"settings","type":"set_room_settings","expected_seq":1,"data":{}}`, false},
		{"missing_visibility", `{"v":2,"id":"settings","type":"set_room_settings","expected_seq":1,"data":{"settings":{"voice_url":""}}}`, false},
		{"null_settings", `{"v":2,"id":"settings","type":"set_room_settings","expected_seq":1,"data":{"settings":null}}`, false},
		{"unknown_setting", `{"v":2,"id":"settings","type":"set_room_settings","expected_seq":1,"data":{"settings":{"visibility":"public","voice_url":"","secret":true}}}`, false},
		{"unknown_payload", `{"v":2,"id":"settings","type":"set_room_settings","expected_seq":1,"data":{"settings":{"visibility":"public","voice_url":""},"target_id":"host"}}`, false},
		{"bad_url", `{"v":2,"id":"settings","type":"set_room_settings","expected_seq":1,"data":{"settings":{"visibility":"public","voice_url":"javascript:alert(1)"}}}`, false},
		{"legacy", `{"v":1,"id":"settings","type":"set_room_settings","expected_seq":1,"data":{"settings":{"visibility":"public","voice_url":""}}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var c Command
			if err := json.Unmarshal([]byte(tc.raw), &c); err != nil {
				t.Fatal(err)
			}
			if err := c.Validate(); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestRoomSettingsValidateAndNormalize(t *testing.T) {
	if got := (RoomSettings{}).Normalized(); got != (RoomSettings{Visibility: "private"}) {
		t.Fatalf("legacy default: %+v", got)
	}
	if err := (RoomSettings{}).Validate(); Code(err) != "invalid_settings" {
		t.Fatalf("explicit visibility required: %v", err)
	}
	prefix := "https://voice.example/"
	for _, tc := range []struct {
		name, voice string
		valid       bool
	}{
		{"empty", "", true},
		{"https", "https://voice.example/room?token=abc#join", true},
		{"uppercase_scheme", "HTTPS://voice.example/room", true},
		{"unicode", "https://голос.пример/комната", true},
		{"encoded_path", "https://voice.example/room%20one", true},
		{"ipv6", "https://[2001:db8::1]:8443/room", true},
		{"byte_boundary", prefix + strings.Repeat("a", 2048-len(prefix)), true},
		{"too_many_bytes", prefix + strings.Repeat("a", 2049-len(prefix)), false},
		{"multibyte_limit", prefix + strings.Repeat("я", 1024), false},
		{"invalid_utf8", prefix + "\xff", false},
		{"http", "http://voice.example", false},
		{"javascript", "javascript:alert(1)", false},
		{"relative", "/room", false},
		{"protocol_relative", "//voice.example/room", false},
		{"opaque", "https:voice.example/room", false},
		{"empty_host", "https:///room", false},
		{"port_only", "https://:443/room", false},
		{"bad_port", "https://voice.example:invalid/room", false},
		{"userinfo", "https://user:secret@voice.example/room", false},
		{"empty_userinfo", "https://@voice.example/room", false},
		{"backslash", "https://voice.example\\@other.example/room", false},
		{"path_backslash", "https://voice.example/room\\name", false},
		{"space", "https://voice.example/room name", false},
		{"unicode_space", "https://voice.example/room\u00a0name", false},
		{"newline", "https://voice.example/room\nname", false},
		{"control", "https://voice.example/room\x7fname", false},
		{"bad_escape", "https://voice.example/%invalid", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, visibility := range []string{"private", "public"} {
				settings := RoomSettings{Visibility: visibility, VoiceURL: tc.voice}
				err := settings.Validate()
				if (err == nil) != tc.valid || err != nil && Code(err) != "invalid_settings" {
					t.Fatalf("valid=%v, error=%v", tc.valid, err)
				}
				if got := settings.Normalized(); got != settings {
					t.Fatalf("normalization rewrote explicit settings: %+v", got)
				}
			}
		})
	}
	for _, visibility := range []string{"", "friends", "PUBLIC", " public"} {
		if err := (RoomSettings{Visibility: visibility}).Validate(); Code(err) != "invalid_settings" {
			t.Fatalf("invalid visibility %q: %v", visibility, err)
		}
	}
}

func TestRoomSettingsOldSnapshotProjection(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			var s State
			if err := json.Unmarshal([]byte(`{"room_id":"old","seq":1,"phase":"lobby","participants":[{"id":"host","name":"Host","role":"host","score":0},{"id":"p1","name":"Player","role":"player","score":0},{"id":"s1","name":"Spectator","role":"spectator","score":0}]}`), &s); err != nil {
				t.Fatal(err)
			}
			if !legacy {
				s.Gameplay = NewSession(DefaultRules())
			}
			for _, actor := range []string{"host", "p1", "s1"} {
				view := ProjectAt(s, actor, 1000)
				if view.Settings != (RoomSettings{Visibility: "private"}) {
					t.Fatalf("old room projected as %+v", view.Settings)
				}
				wire, err := json.Marshal(view)
				if err != nil || !strings.Contains(string(wire), `"settings":{"visibility":"private","voice_url":""}`) {
					t.Fatalf("missing normalized settings: %s %v", wire, err)
				}
			}
			if s.Settings != (RoomSettings{}) {
				t.Fatal("projection mutated stored settings")
			}
			s.Settings = RoomSettings{Visibility: "public", VoiceURL: "https://voice.example/room"}
			for _, actor := range []string{"host", "p1", "s1"} {
				if got := ProjectAt(s, actor, 1000).Settings; got != s.Settings {
					t.Fatalf("explicit settings lost for %s: %+v", actor, got)
				}
			}
			wire, err := json.Marshal(s)
			var restored State
			if err != nil || json.Unmarshal(wire, &restored) != nil || restored.Settings != s.Settings {
				t.Fatalf("settings lost on state roundtrip: %s %v", wire, err)
			}
		})
	}
}

func settingsCommand(s State, settings RoomSettings) Command {
	c := v2cmd("set_room_settings", SettingsPayload{Settings: settings})
	c.ExpectedSeq = &s.Seq
	return c
}

func TestRoomSettingsPermissionsAndSeq(t *testing.T) {
	for _, phase := range []Phase{Lobby, Running, Paused, Resolved, Open, Answering, Finished} {
		for _, actor := range []string{"host", "p1", "s1", "missing"} {
			t.Run(string(phase)+"/"+actor, func(t *testing.T) {
				s := v2Fixture()
				s.Phase = phase
				before, _ := json.Marshal(s)
				settings := RoomSettings{Visibility: "public", VoiceURL: "https://voice.example/join"}
				permitted := actor == "host" && (phase == Lobby || phase == Paused)
				if got := hasAction(ProjectAt(s, actor, 1000), "set_room_settings"); got != permitted {
					t.Fatalf("projected permission=%v, want %v", got, permitted)
				}
				n, err := ApplyAt(s, actor, settingsCommand(s, settings), 1100, 9)
				if permitted {
					if err != nil || n.Settings != settings {
						t.Fatalf("accepted settings: %+v %v", n.Settings, err)
					}
					if !reflect.DeepEqual(n.Gameplay, s.Gameplay) || !reflect.DeepEqual(n.Participants, s.Participants) || n.Phase != phase {
						t.Fatal("settings changed gameplay, participants or phase")
					}
				} else if err == nil || !reflect.DeepEqual(n, s) {
					t.Fatalf("unauthorized settings: %v", err)
				}
				after, _ := json.Marshal(s)
				if string(before) != string(after) {
					t.Fatal("input mutated")
				}
			})
		}
	}
	s := v2Fixture()
	s.Phase = Lobby
	c := settingsCommand(s, RoomSettings{Visibility: "private"})
	stale := s.Seq + 1
	c.ExpectedSeq = &stale
	if n, err := ApplyAt(s, "host", c, 1000, 7); Code(err) != "stale_state" || !reflect.DeepEqual(n, s) {
		t.Fatalf("stale mutation: %v", err)
	}
	legacy := fixture()
	legacy.Seq = 1
	if _, err := ApplyAt(legacy, "host", settingsCommand(legacy, RoomSettings{Visibility: "public"}), 1000, 7); Code(err) != "invalid_version" {
		t.Fatalf("v2 command applied to legacy room: %v", err)
	}
}

func TestRoomSettingsTransferPreservesFrozenQuestion(t *testing.T) {
	s := v2Fixture()
	s.Participants[1].Score, s.Participants[2].Score = 400, 600
	s = begin(t, s, "stakeAll")
	s = do(t, s, "p1", "wager", with(s, "amount", 250))
	s = doAt(t, s, "host", "pause", EmptyPayload{}, 1200)
	s = doAt(t, s, "host", "transfer_host", TransferPayload{TargetID: "s1"}, 1300)
	before, _ := json.Marshal(s)
	settings := RoomSettings{Visibility: "public", VoiceURL: "https://voice.example/room"}
	if _, err := ApplyAt(s, "host", settingsCommand(s, settings), 1400, 7); Code(err) != "forbidden" {
		t.Fatalf("old host settings authority: %v", err)
	}
	n, err := ApplyAt(s, "s1", settingsCommand(s, settings), 1400, 7)
	if err != nil || n.Settings != settings {
		t.Fatalf("transferred host settings: %+v %v", n.Settings, err)
	}
	if !reflect.DeepEqual(n.Gameplay, s.Gameplay) || n.Gameplay.Question.Players[0].Wager == nil || *n.Gameplay.Question.Players[0].Wager != 250 || n.Phase != Paused {
		t.Fatal("settings changed frozen wager, rules, timers or paused stage")
	}
	if hasAction(ProjectAt(n, "s1", 1400), "set_rules") {
		t.Fatal("settings expanded paused rule editing permission")
	}
	after, _ := json.Marshal(s)
	if string(before) != string(after) {
		t.Fatal("settings mutation changed its input")
	}
}

func TestCanJoinRoleMatchesPhaseAdmission(t *testing.T) {
	for _, tc := range []struct {
		name                string
		phase               Phase
		stage               string
		players, spectators bool
	}{
		{"legacy_lobby", Lobby, "", true, true},
		{"legacy_resolved", Resolved, "", true, true},
		{"legacy_open", Open, "", false, true},
		{"legacy_answer", Answering, "", false, true},
		{"legacy_paused", Paused, "", false, true},
		{"legacy_finished", Finished, "", false, false},
		{"v2_lobby", Lobby, "select_question", true, true},
		{"v2_selection", Running, "select_question", true, true},
		{"v2_resolved", Running, "resolved", true, true},
		{"v2_auction", Running, "auction", false, true},
		{"v2_final", Running, "select_final", false, true},
		{"v2_paused_selection", Paused, "select_question", true, true},
		{"v2_paused_question", Paused, "collect_wagers", false, true},
		{"v2_finished", Finished, "resolved", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture()
			s.Phase = tc.phase
			if tc.stage != "" {
				s.Gameplay = NewSession(DefaultRules())
				s.Gameplay.Stage.Kind = tc.stage
			}
			for _, role := range []Role{Player, Spectator, Host, Role("other")} {
				want := role == Player && tc.players || role == Spectator && tc.spectators
				if got := CanJoinRole(s, role); got != want {
					t.Fatalf("%s admission=%v, want %v", role, got, want)
				}
				n, err := AddParticipant(s, Participant{ID: "new", Name: "New", Role: role, Score: 99})
				if (err == nil) != want {
					t.Fatalf("%s join: %v, want admission=%v", role, err, want)
				}
				if want && n.Participants[len(n.Participants)-1].Score != 0 {
					t.Fatal("join retained client score")
				}
			}
		})
	}
}

func TestCanJoinRoleSeatBoundaries(t *testing.T) {
	s := State{ID: "room", Phase: Lobby, Participants: []Participant{{ID: "host", Name: "Host", Role: Host}}}
	for _, tc := range []struct {
		role  Role
		limit int
	}{{Player, 12}, {Spectator, 20}} {
		for i := 0; i < tc.limit; i++ {
			if !CanJoinRole(s, tc.role) {
				t.Fatalf("%s seat %d rejected", tc.role, i+1)
			}
			var err error
			s, err = AddParticipant(s, Participant{ID: fmt.Sprintf("%s%d", tc.role, i), Name: "Guest", Role: tc.role})
			if err != nil {
				t.Fatal(err)
			}
		}
		if CanJoinRole(s, tc.role) {
			t.Fatalf("%s extra seat advertised", tc.role)
		}
		if _, err := AddParticipant(s, Participant{ID: "extra", Name: "Guest", Role: tc.role}); Code(err) != "room_full" {
			t.Fatalf("extra %s joined: %v", tc.role, err)
		}
	}
	if len(s.Participants) != 33 {
		t.Fatalf("full room has %d participants", len(s.Participants))
	}
}

func TestRoomSettingsJSONRejectsNullStringsAndUnknownFields(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"complete", `{"visibility":"public","voice_url":"https://voice.example/join"}`, true},
		{"omitted_optional_voice", `{"visibility":"private"}`, true},
		{"empty_legacy_object", `{}`, true},
		{"null_voice", `{"visibility":"private","voice_url":null}`, false},
		{"null_visibility", `{"visibility":null,"voice_url":""}`, false},
		{"wrong_voice_type", `{"visibility":"private","voice_url":42}`, false},
		{"wrong_visibility_type", `{"visibility":true,"voice_url":""}`, false},
		{"unknown_field", `{"visibility":"private","voice_url":"","css":"body{}"}`, false},
		{"null_object", `null`, false},
		{"array", `[]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var settings RoomSettings
			err := json.Unmarshal([]byte(tc.raw), &settings)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, decoded=%+v error=%v", tc.valid, settings, err)
			}
		})
	}
	for _, raw := range []string{`{}`, `{"settings":null}`} {
		var create struct {
			Settings *RoomSettings `json:"settings"`
		}
		if err := json.Unmarshal([]byte(raw), &create); err != nil || create.Settings != nil {
			t.Fatalf("optional creation settings: %+v %v", create.Settings, err)
		}
	}
	var create struct {
		Settings *RoomSettings `json:"settings"`
	}
	if err := json.Unmarshal([]byte(`{"settings":{"visibility":"private","voice_url":null}}`), &create); err == nil {
		t.Fatal("creation accepted a null voice URL")
	}
}
