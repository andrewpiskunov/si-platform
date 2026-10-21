package game

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func appearanceCommand(s State, a Appearance) Command {
	c := v2cmd("set_appearance", AppearancePayload{Appearance: a})
	c.ExpectedSeq = &s.Seq
	return c
}

func TestAppearanceReferenceAndCommandValidation(t *testing.T) {
	for _, a := range []Appearance{{Builtin: "classic"}, {Builtin: "pirate"}, {Builtin: "anime"}, {ThemeID: strings.Repeat("a", 32), Revision: 100, Hash: strings.Repeat("b", 64)}} {
		if err := a.Validate(); err != nil {
			t.Fatal(a, err)
		}
		c := appearanceCommand(State{Seq: 1}, a)
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
		c.ExpectedSeq = nil
		if c.Validate() == nil {
			t.Fatal("appearance mutation needs expected sequence")
		}
	}
	for _, a := range []Appearance{{}, {Builtin: "unknown"}, {Builtin: "classic", Revision: 1}, {Builtin: "pirate", ThemeID: strings.Repeat("a", 32)}, {ThemeID: "other", Revision: 1, Hash: strings.Repeat("b", 64)}, {ThemeID: strings.Repeat("a", 32), Revision: 101, Hash: strings.Repeat("b", 64)}, {ThemeID: strings.Repeat("a", 32), Revision: 1, Hash: "short"}} {
		if a.Validate() == nil {
			t.Fatal("invalid reference accepted", a)
		}
	}
	c := appearanceCommand(State{Seq: 1}, Appearance{Builtin: "classic"})
	c.Data = json.RawMessage(`{"appearance":{"builtin":"classic","css":"body{}"}}`)
	if c.Validate() == nil {
		t.Fatal("untrusted appearance field accepted")
	}
}

func TestAppearanceChangesOnlyForHostInLobbyOrPaused(t *testing.T) {
	for _, phase := range []Phase{Lobby, Running, Paused, Finished} {
		for _, actor := range []string{"host", "p1", "s1"} {
			t.Run(string(phase)+"/"+actor, func(t *testing.T) {
				s := v2Fixture()
				s.Phase = phase
				s.Participants[1].Score = 350
				before, _ := json.Marshal(s)
				a := Appearance{Builtin: "pirate"}
				n, err := ApplyAt(s, actor, appearanceCommand(s, a), 5000, 9)
				if actor == "host" && (phase == Lobby || phase == Paused) {
					if err != nil || n.Appearance == nil || *n.Appearance != a {
						t.Fatal(n.Appearance, err)
					}
					if !reflect.DeepEqual(n.Gameplay, s.Gameplay) || !reflect.DeepEqual(n.Participants, s.Participants) || n.Phase != phase {
						t.Fatal("appearance changed gameplay")
					}
					for _, viewer := range []string{"host", "p1", "s1"} {
						if v := ProjectAt(n, viewer, 6000); v.Appearance == nil || *v.Appearance != a {
							t.Fatal("appearance projection missing", viewer)
						}
					}
					wire, _ := json.Marshal(n)
					var restored State
					if json.Unmarshal(wire, &restored) != nil || !reflect.DeepEqual(restored.Appearance, n.Appearance) {
						t.Fatal("appearance lost on restore")
					}
				} else if err == nil {
					t.Fatal("unauthorized or live change accepted")
				}
				after, _ := json.Marshal(s)
				if string(before) != string(after) {
					t.Fatal("input state mutated")
				}
			})
		}
	}
}
