package game

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func fixture() State {
	return State{ID: "room", Phase: Lobby, Participants: []Participant{
		{ID: "host", Name: "Ведущий", Role: Host},
		{ID: "p1", Name: "Аня", Role: Player},
		{ID: "p2", Name: "Борис", Role: Player},
		{ID: "s1", Name: "Зритель", Role: Spectator},
	}}
}

func open(t *testing.T, s State) State {
	t.Helper()
	n, err := Apply(s, "host", Command{Version: 1, ID: "open", Type: "open_question", QuestionID: "q1", Text: "2 + 2?", Answer: "4", Value: 100})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRound(t *testing.T) {
	for _, correct := range []bool{true, false} {
		t.Run(fmt.Sprint(correct), func(t *testing.T) {
			s := open(t, fixture())
			s, err := Apply(s, "p1", Command{Version: 1, ID: "buzz", Type: "buzz", QuestionID: "q1"})
			if err != nil || s.Winner != "p1" || s.Phase != Answering {
				t.Fatalf("winner: %+v %v", s, err)
			}
			if _, err = Apply(s, "p2", Command{Version: 1, ID: "late", Type: "buzz", QuestionID: "q1"}); err == nil {
				t.Fatal("second winner accepted")
			}
			n, err := Apply(s, "host", Command{Version: 1, ID: "judge", Type: "judge", QuestionID: "q1", Correct: &correct})
			want := int64(100)
			if !correct {
				want = -100
			}
			if err != nil || n.Participants[1].Score != want || n.Phase != Resolved {
				t.Fatalf("score: %+v %v", n, err)
			}
			if s.Participants[1].Score != 0 {
				t.Fatal("input was mutated")
			}
			if _, err = Apply(n, "host", Command{Version: 1, ID: "another", Type: "judge", QuestionID: "q1", Correct: &correct}); err == nil {
				t.Fatal("judged twice")
			}
		})
	}
}

func TestRolesAndStaleQuestion(t *testing.T) {
	s := open(t, fixture())
	for _, tc := range []struct {
		actor string
		c     Command
	}{
		{"s1", Command{Type: "buzz", QuestionID: "q1"}},
		{"host", Command{Type: "buzz", QuestionID: "q1"}},
		{"p1", Command{Type: "buzz", QuestionID: "old"}},
		{"unknown", Command{Type: "buzz", QuestionID: "q1"}},
		{"p1", Command{Type: "finish"}},
		{"p1", Command{Type: "pause"}},
	} {
		tc.c.Version, tc.c.ID = 1, "command"
		before, _ := json.Marshal(s)
		if _, err := Apply(s, tc.actor, tc.c); err == nil {
			t.Errorf("accepted %+v", tc)
		}
		after, _ := json.Marshal(s)
		if string(before) != string(after) {
			t.Fatal("error mutated input")
		}
	}
	s.Phase = Resolved
	if _, err := Apply(s, "host", Command{Version: 1, ID: "repeat", Type: "open_question", QuestionID: "q1", Text: "Again", Answer: "a", Value: 100}); err == nil {
		t.Fatal("reused question ID")
	}
}

func TestParticipantLimits(t *testing.T) {
	s := State{ID: "room", Phase: Lobby, Participants: []Participant{{ID: "host", Name: "Host", Role: Host}}}
	for i := 0; i < 12; i++ {
		n, err := AddParticipant(s, Participant{ID: fmt.Sprint(i), Name: "Player", Role: Player})
		if err != nil {
			t.Fatal(err)
		}
		s = n
	}
	if _, err := AddParticipant(s, Participant{ID: "extra", Name: "Extra", Role: Player}); err == nil {
		t.Fatal("13th player joined")
	}
	for i := 0; i < 20; i++ {
		n, err := AddParticipant(s, Participant{ID: fmt.Sprintf("s%d", i), Name: "Spectator", Role: Spectator})
		if err != nil {
			t.Fatal(err)
		}
		s = n
	}
	for _, p := range []Participant{{ID: "extra", Name: "Extra", Role: Spectator}, {ID: "host2", Name: "Host", Role: Host}, {ID: "0", Name: "Duplicate", Role: Player}, {ID: "x", Name: "", Role: Player}} {
		if _, err := AddParticipant(s, p); err == nil {
			t.Fatalf("invalid participant accepted: %+v", p)
		}
	}
}

func TestProjection(t *testing.T) {
	s := open(t, fixture())
	for _, id := range []string{"p1", "s1"} {
		v := Project(s, id)
		if v.Question.Answer != "" {
			t.Fatalf("answer leaked to %s", id)
		}
		b, _ := json.Marshal(v)
		if strings.Contains(string(b), `"answer"`) {
			t.Fatal("hidden answer field serialized")
		}
		v.Participants[0].Name = "changed"
		if s.Participants[0].Name != "Ведущий" {
			t.Fatal("view aliases state")
		}
	}
	if Project(s, "host").Question.Answer != "4" {
		t.Fatal("host missing answer")
	}
	s.Phase = Resolved
	if Project(s, "p1").Question.Answer != "4" {
		t.Fatal("answer not revealed")
	}
}

func TestPauseResume(t *testing.T) {
	s := open(t, fixture())
	n, err := Apply(s, "host", Command{Version: 1, ID: "pause", Type: "pause"})
	if err != nil || n.Phase != Paused || n.ResumePhase != Open {
		t.Fatalf("pause: %+v %v", n, err)
	}
	if _, err = Apply(n, "p1", Command{Version: 1, ID: "buzz", Type: "buzz", QuestionID: "q1"}); err == nil {
		t.Fatal("buzz while paused")
	}
	n, err = Apply(n, "host", Command{Version: 1, ID: "resume", Type: "resume"})
	if err != nil || n.Phase != Open {
		t.Fatalf("resume: %+v %v", n, err)
	}
	n, err = Apply(n, "host", Command{Version: 1, ID: "end", Type: "finish"})
	if err != nil || n.Phase != Finished {
		t.Fatalf("finish: %+v %v", n, err)
	}
}

func TestCommandValidation(t *testing.T) {
	for _, c := range []Command{
		{Version: 2, ID: "x", Type: "finish"},
		{Version: 1, Type: "finish"},
		{Version: 1, ID: "x", Type: "unknown"},
		{Version: 1, ID: "x", Type: "buzz", QuestionID: "q1", Answer: "injected"},
		{Version: 1, ID: "x", Type: "judge", QuestionID: "q1"},
		{Version: 1, ID: "x", Type: "open_question", QuestionID: "q", Text: "Q", Answer: "A", Value: -1},
		{Version: 1, ID: strings.Repeat("x", 65), Type: "finish"},
	} {
		s := fixture()
		_, err := Apply(s, "host", c)
		if err == nil {
			t.Fatalf("accepted invalid command: %+v", c)
		}
		if !reflect.DeepEqual(s, fixture()) {
			t.Fatal("mutated fixture")
		}
	}
}
