package game

import "testing"

func TestAutomaticTextOpening(t *testing.T) {
	s := v2Fixture()
	s.Gameplay.Rules.AutoOpen = true
	s.Gameplay.Rules.ReadingSeconds = 5
	q := qspec("withButton")
	q.Question = []TextStep{{Text: "One"}, {Text: "Two"}}
	s = do(t, s, "host", "begin_question", BeginQuestionPayload{Question: q})
	if s.Gameplay.Stage.DeadlineMS != 6000 {
		t.Fatal("reading deadline", s.Gameplay.Stage)
	}
	s, changed := AdvanceAt(s, 6000)
	if !changed || s.Gameplay.Question.ContentIndex != 1 || s.Gameplay.Stage.Kind != "present_question" {
		t.Fatal("next fragment", s.Gameplay.Stage)
	}
	// Changes to room rules cannot change this question's snapshot.
	s.Gameplay.Rules.AutoOpen = false
	s, _ = PauseAt(s, 7000)
	s = doAt(t, s, "host", "resume", EmptyPayload{}, 17000)
	s, changed = AdvanceAt(s, 21000)
	if !changed || s.Gameplay.Stage.Kind != "buzz_open" {
		t.Fatal("automatic opening after pause", s.Gameplay.Stage)
	}
}
func TestManualTextOpeningStillWaits(t *testing.T) {
	s := begin(t, v2Fixture(), "withButton")
	s, _ = AdvanceAt(s, s.Gameplay.Stage.DeadlineMS)
	if s.Gameplay.Stage.Kind != "present_question" || !s.Gameplay.Stage.Expired {
		t.Fatal("legacy manual mode changed")
	}
}

func TestAutomaticOpeningRespectsPackageTiming(t *testing.T) {
	for _, wait := range []bool{false, true} {
		s := v2Fixture()
		s.Gameplay.Rules.AutoOpen = true
		s.Gameplay.Rules.ReadingSeconds = 5
		q := qspec("withButton")
		q.Question[0].DurationMS = 2000
		q.Question[0].WaitForFinish = wait
		s = do(t, s, "host", "begin_question", BeginQuestionPayload{Question: q})
		if s.Gameplay.Stage.DeadlineMS != 3000 {
			t.Fatal("package duration overridden")
		}
		s, _ = AdvanceAt(s, 3000)
		if wait && (s.Gameplay.Stage.Kind != "present_question" || !s.Gameplay.Stage.Expired) {
			t.Fatal("wait-for-finish ignored")
		}
		if !wait && s.Gameplay.Stage.Kind != "buzz_open" {
			t.Fatal("timed step did not open")
		}
	}
}
func TestAutomaticOpeningRulesValidation(t *testing.T) {
	r := DefaultRules()
	r.AutoOpen = true
	for _, value := range []int{-1, 0, 121} {
		r.ReadingSeconds = value
		if r.Validate() == nil {
			t.Fatalf("accepted %d", value)
		}
	}
	for _, value := range []int{1, 10, 120} {
		r.ReadingSeconds = value
		if r.Validate() != nil {
			t.Fatalf("rejected %d", value)
		}
	}
}
