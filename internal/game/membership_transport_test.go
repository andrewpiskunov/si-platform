package game

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestMembershipCombinedTransportBound(t *testing.T) {
	for _, name := range []string{strings.Repeat("😀", 32), strings.Repeat("<", 32)} {
		for _, replacement := range []bool{false, true} {
			t.Run(fmt.Sprintf("escaped%d/replacement%v", len(name), replacement), func(t *testing.T) {
				s := v2Fixture()
				for i := 3; i <= 12; i++ {
					s.Participants = append(s.Participants, Participant{ID: fmt.Sprintf("p%d", i), Name: name, Role: Player})
				}
				q := qspec("forAll")
				q.Question = []TextStep{{Text: strings.Repeat("Q", 4094)}, {Media: mediaFixture("image", "question-media")}}
				q.Right = []string{strings.Repeat("A", 4096)}
				q.Answer = []TextStep{{Text: strings.Repeat("E", 4094)}, {Media: mediaFixture("video", "answer-media")}}
				s = startMedia(t, s, q)
				s = do(t, s, "host", "advance", dep(s))
				s = readyPlayers(t, s)
				s, _ = AdvanceAt(s, 1750)
				s = doAt(t, s, "host", "advance", dep(s), 2000)
				for _, id := range playerIDs(s) {
					s = doAt(t, s, id, "answer", with(s, "text", strings.Repeat("x", 1024)), 2000)
				}
				for _, id := range playerIDs(s) {
					d := with(s, "target_id", id)
					d["correct"] = true
					s = doAt(t, s, "host", "judge", d, 2000)
				}
				s = doAt(t, s, "host", "advance", dep(s), 2000)
				reason := strings.Repeat("<&", 512)
				for i := 0; i < 64; i++ {
					s.Gameplay.Decisions = append(s.Gameplay.Decisions, Decision{ID: fmt.Sprintf("judgement-%d", i), ParticipantID: "p1", Reason: reason, QuestionID: "old"})
					s.Gameplay.Appeals = append(s.Gameplay.Appeals, Appeal{ID: fmt.Sprintf("appeal-%d", i), JudgementID: fmt.Sprintf("judgement-%d", i), ParticipantID: "p1", Reason: reason, Resolution: reason})
				}

				for i := range s.Participants {
					s.Participants[i].Name = name
				}
				for i := 1; i < 20; i++ {
					s.Participants = append(s.Participants, Participant{ID: fmt.Sprintf("%032x", i+900), Name: name, Role: Spectator})
				}
				for i := 0; i < 64; i++ {
					id := fmt.Sprintf("%032x", i+1000)
					aid := fmt.Sprintf("%032x", i+2000)
					s.Participants = append(s.Participants, Participant{ID: id, Name: name, Role: Player, Removed: true}, Participant{ID: aid, Name: name, Role: Player, Removed: true})
					s.Gameplay.Decisions[len(s.Gameplay.Decisions)-64+i].ParticipantID = id
					s.Gameplay.Appeals[i].ParticipantID = aid
				}
				current := append([]Decision(nil), s.Gameplay.Decisions[:12]...)
				s.Gameplay.Decisions = append(s.Gameplay.Decisions[12:], current...)
				if replacement {
					for i := range s.Participants {
						if s.Participants[i].Role == Player && !s.Participants[i].Removed {
							s.Participants[i].Removed = true
						}
					}
					// Resolved questions retain their frozen twelve after twelve new seats join.
					s.Gameplay.Stage.Kind = "resolved"
					for i := 0; i < 12; i++ {
						s.Participants = append(s.Participants, Participant{ID: fmt.Sprintf("%032x", 3000+i), Name: name, Role: Player})
					}
				}
				voicePrefix := "https://voice.example/"
				s.Settings = RoomSettings{Visibility: "public", VoiceURL: voicePrefix + strings.Repeat("<", 2048-len(voicePrefix))}
				voiceJSON, _ := json.Marshal(s.Settings.VoiceURL)
				if len(voiceJSON) < 12000 {
					t.Fatal("fixture did not exercise HTML escaping inflation")
				}
				if err := s.Settings.Validate(); err != nil {
					t.Fatalf("maximum voice URL rejected: %v", err)
				}
				s.Appearance = &Appearance{ThemeID: strings.Repeat("a", 32), Revision: 100, Hash: strings.Repeat("b", 64)}
				if err := s.Appearance.Validate(); err != nil {
					t.Fatal(err)
				}
				for i := range s.Participants {
					if s.Participants[i].Role == Player {
						s.Participants[i].Score = -1000000000
					}
				}
				for i := range s.Gameplay.Question.Players {
					s.Gameplay.Question.Players[i].Maximum = 1000000000
				}
				s = PruneParticipants(s)
				// Real production participant IDs are 32 hex bytes, including every
				// frozen reference; short fixture IDs understate the wire size.
				raw, _ := json.Marshal(s)
				identities := map[string]string{}
				for i, p := range s.Participants {
					if len(p.ID) != 32 {
						identities[p.ID] = fmt.Sprintf("%032x", 5000+i)
					}
				}
				for old, id := range identities {
					raw = []byte(strings.ReplaceAll(string(raw), `"`+old+`"`, `"`+id+`"`))
				}
				if err := json.Unmarshal(raw, &s); err != nil {
					t.Fatal(err)
				}
				for i := range s.Participants {
					if s.Participants[i].ID == identities["host"] {
						s.Participants[i].Role = Host
					}
				}
				before, _ := json.Marshal(s)
				t.Logf("retained roster=%d decisions=%d appeals=%d", len(s.Participants), len(s.Gameplay.Decisions), len(s.Gameplay.Appeals))
				for _, id := range []string{identities["host"], identities["s1"]} {
					v := ProjectAt(s, id, 2000)
					b, _ := json.Marshal(struct {
						Version    int    `json:"v"`
						Type       string `json:"type"`
						CommandID  string `json:"command_id"`
						Code       string `json:"code"`
						ResultSeq  int64  `json:"result_seq"`
						ServerTime int64  `json:"server_time"`
						State      View   `json:"state"`
					}{1, "result", strings.Repeat("c", 128), "ok", 1000000, 1792565156083, v})
					t.Logf("%s envelope=%d projected roster=%d decisions=%d appeals=%d", id, len(b), len(v.Participants), len(v.Gameplay.Decisions), len(v.Gameplay.Appeals))
					visible := map[string]bool{}
					for _, p := range v.Participants {
						visible[p.ID] = true
					}
					for _, p := range s.Participants {
						if !p.Removed && !visible[p.ID] {
							t.Fatal("active participant omitted")
						}
					}
					for _, p := range s.Gameplay.Question.Players {
						if !visible[p.ID] {
							t.Fatal("frozen participant omitted")
						}
					}
					for _, d := range v.Gameplay.Decisions {
						if !visible[d.ParticipantID] {
							t.Fatal("visible decision lost name")
						}
					}
					for _, a := range v.Gameplay.Appeals {
						if !visible[a.ParticipantID] {
							t.Fatal("visible appeal lost name")
						}
					}
					after, _ := json.Marshal(s)
					if !bytes.Equal(before, after) {
						t.Fatal("projection mutated durable history")
					}
					if !reflect.DeepEqual(v.Gameplay.AllowedActions, allowedActions(s, id, 2000)) {
						t.Fatal("history budget changed authority actions")
					}
					base := s.Clone()
					base.Gameplay.Decisions = nil
					base.Gameplay.Appeals = nil
					baseView := ProjectAt(base, id, 2000)
					baseBytes, _ := json.Marshal(baseView)
					t.Logf("mandatory base=%d voice raw=%d JSON=%d", len(baseBytes), len(s.Settings.VoiceURL), len(voiceJSON))
					if v.Settings != s.Settings || !reflect.DeepEqual(v.Appearance, s.Appearance) {
						t.Fatal("transport budget truncated settings or appearance")
					}
					if len(baseBytes) > 59*1024 {
						t.Fatalf("mandatory gameplay alone exceeds projection budget: %d", len(baseBytes))
					}
					if len(v.Gameplay.Players) != 12 || len(v.Gameplay.Submissions) != 12 {
						t.Fatal("frozen submission omitted")
					}
					if id == identities["host"] {
						for _, sub := range v.Gameplay.Submissions {
							if sub.Answer == nil || len(*sub.Answer) != 1024 {
								t.Fatal("history budget truncated frozen answer")
							}
						}
					}
					if len(s.Gameplay.Decisions) != 64 || len(s.Gameplay.Appeals) != 64 {
						t.Fatal("private history changed")
					}
					if len(v.Gameplay.Readiness) != 12 || v.Gameplay.Question.Media == nil || len(b) > 60*1024 {
						t.Fatalf("%s maximum media snapshot invalid: %d bytes", id, len(b))
					}
				}
			})
		}
	}
}
