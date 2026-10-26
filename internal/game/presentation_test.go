package game

import (
	"encoding/json"
	"testing"
)

func TestPresentationRoundPauseRecoveryAndSkip(t *testing.T) {
	s := v2Fixture()
	s.Gameplay.Rules.Presentation = true
	r := *s.Gameplay.Round
	s = doAt(t, s, "host", "begin_round", BeginRoundPayload{Round: r}, 2000)
	if s.Gameplay.Stage.Kind != "round_intro" {
		t.Fatalf("expected round intro, got %s", s.Gameplay.Stage.Kind)
	}
	if hasAction(ProjectAt(s, "host", 2100), "begin_question") {
		t.Fatal("questions enabled under intro")
	}
	s = doAt(t, s, "host", "pause", EmptyPayload{}, 2500)
	if NextDeadline(s) != 0 {
		t.Fatal("paused intro has deadline")
	}
	encoded, _ := json.Marshal(s)
	var restored State
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	restored = doAt(t, restored, "host", "resume", EmptyPayload{}, 10000)
	if restored.Gameplay.Stage.DeadlineMS != 12500 {
		t.Fatalf("lost paused time: %+v", restored.Gameplay.Stage)
	}
	restored, changed := AdvanceAt(restored, 12500)
	if !changed || restored.Gameplay.Stage.Kind != "round_themes" {
		t.Fatal("no themes phase")
	}
	restored = doAt(t, restored, "host", "skip_presentation", StagePayload{StageID: restored.Gameplay.Stage.ID}, 12600)
	if restored.Gameplay.Stage.Kind != "select_question" || restored.Gameplay.Stage.DeadlineMS != 42600 {
		t.Fatalf("selection timer consumed by intro: %+v", restored.Gameplay.Stage)
	}
}

func TestPresentationQuestionPrivacyAndNoEarlyTimer(t *testing.T) {
	for _, kind := range []string{"withButton", "stake", "secret", "forAll", "noRisk", "stakeAll"} {
		t.Run(kind, func(t *testing.T) {
			s := v2Fixture()
			s.Gameplay.Rules.Presentation = true
			for i := range s.Participants {
				s.Participants[i].Score = 1000
			}
			s = doAt(t, s, "host", "begin_question", BeginQuestionPayload{Question: qspec(kind)}, 2000)
			if s.Gameplay.Stage.Kind != "question_price" {
				t.Fatalf("missing price scene: %+v", s.Gameplay.Stage)
			}
			public := ProjectAt(s, "p1", 2100).Gameplay
			if public.Question.Text != "" || public.Question.Answer != nil {
				t.Fatal("question or answer leaked before intro completed")
			}
			if hasAction(ProjectAt(s, "p1", 2100), "buzz") {
				t.Fatal("early buzzer")
			}
			if ProjectAt(s, "host", 2100).Gameplay.Question.Answer == nil {
				t.Fatal("host must see answer immediately")
			}
			first := s.Gameplay.Stage.ID
			s, changed := AdvanceAt(s, 3000)
			if !changed {
				t.Fatal("price phase did not complete")
			}
			if kind != "withButton" {
				if s.Gameplay.Stage.Kind != "question_type" {
					t.Fatal("missing special type announcement")
				}
				s, changed = AdvanceAt(s, 5000)
				if !changed {
					t.Fatal("type phase did not complete")
				}
			}
			if s.Gameplay.Stage.ID == first {
				t.Fatal("stage identity reused")
			}
			if s.Gameplay.Stage.Kind == "present_question" && s.Gameplay.Stage.DeadlineMS <= 5000 {
				t.Fatal("question timer consumed by intro")
			}
		})
	}
}

func TestPresentationSkipAuthorityAndStaleStage(t *testing.T) {
	s := v2Fixture()
	s.Gameplay.Rules.Presentation = true
	s = doAt(t, s, "host", "begin_question", BeginQuestionPayload{Question: qspec("secret")}, 2000)
	payload := StagePayload{StageID: s.Gameplay.Stage.ID}
	if _, err := ApplyAt(s, "p1", v2cmd("skip_presentation", payload), 2100, 0); err == nil {
		t.Fatal("player skipped shared scene")
	}
	if ProjectDisplayAt(s, 2100).Gameplay.Question.Answer != nil {
		t.Fatal("answer leaked to second screen")
	}
	old := s.Gameplay.Stage.ID
	s = doAt(t, s, "host", "skip_presentation", payload, 2200)
	if s.Gameplay.Stage.Kind != "select_recipient" || s.Gameplay.Stage.DeadlineMS != 32200 {
		t.Fatal("skip did not start recipient timer")
	}
	if _, err := ApplyAt(s, "host", v2cmd("skip_presentation", StagePayload{StageID: old}), 2300, 0); err == nil {
		t.Fatal("stale skip accepted")
	}
}

func TestPresentationFinalAndAutomaticRoundCompletion(t *testing.T) {
	for _, final := range []bool{false, true} {
		s := v2Fixture()
		s.Gameplay.Rules.Presentation = true
		for i := range s.Participants {
			s.Participants[i].Score = 1000
		}
		r := *s.Gameplay.Round
		if final {
			r.Type = "final"
			r.Themes = append(r.Themes, ThemeSpec{ID: "t2", Name: "Other", QuestionID: "q2"})
		}
		s = doAt(t, s, "host", "begin_round", BeginRoundPayload{Round: r}, 2000)
		s, _ = AdvanceAt(s, 5000)
		s, _ = AdvanceAt(s, 9000)
		want := "select_question"
		if final {
			want = "eliminate_theme"
		}
		if s.Gameplay.Stage.Kind != want || s.Gameplay.Stage.DeadlineMS != 39000 || s.Gameplay.IntroReturn != nil {
			t.Fatalf("bad return: %+v", s.Gameplay.Stage)
		}
	}
}

func TestPresentationDefersMediaPreparation(t *testing.T) {
	s := v2Fixture()
	s.Gameplay.Rules.Presentation = true
	q := mediaQuestion("image")
	s = doAt(t, s, "host", "begin_question", BeginQuestionPayload{Question: q}, 2000)
	if s.Gameplay.Stage.Kind != "question_price" || s.Gameplay.Content != nil {
		t.Fatal("media started behind price scene")
	}
	s = doAt(t, s, "host", "skip_presentation", StagePayload{StageID: s.Gameplay.Stage.ID}, 2500)
	if s.Gameplay.Stage.Kind != "prepare_content" || s.Gameplay.Content == nil {
		t.Fatalf("missing media readiness after skip: %+v", s.Gameplay.Stage)
	}
}
