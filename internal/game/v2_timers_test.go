package game

import (
	"encoding/json"
	"testing"
)

func TestV2EveryActiveStageFreezesAndResumesDeadline(t *testing.T) {
	states := map[string]State{}
	remember := func(s State) State { states[s.Gameplay.Stage.Kind] = s; return s }
	round := RoundSpec{ID: "r", Type: "standard", Themes: []ThemeSpec{{ID: "t", QuestionID: "q"}}, MinimumPrice: 100, MaximumPrice: 500}
	s := remember(do(t, v2Fixture(), "host", "begin_round", BeginRoundPayload{Round: round}))
	s = remember(begin(t, s, "withButton"))
	s = remember(do(t, s, "host", "open_buzzer", dep(s)))
	s = remember(do(t, s, "p1", "buzz", dep(s)))
	s = remember(do(t, s, "host", "advance", dep(s)))
	s = remember(judge(t, s, "p1", true))
	s = v2Fixture()
	s.Gameplay.Rules.BuzzerMode = "random"
	s = begin(t, s, "withButton")
	s = do(t, s, "host", "open_buzzer", dep(s))
	remember(do(t, s, "p1", "buzz", dep(s)))
	s = v2Fixture()
	q := qspec("secret")
	q.PriceRange = &PriceRange{Minimum: 100, Maximum: 500, Step: 100}
	s = remember(do(t, s, "host", "begin_question", BeginQuestionPayload{Question: q}))
	remember(do(t, s, "p1", "select_recipient", with(s, "target_id", "p2")))
	s = v2Fixture()
	s.Participants[1].Score = 500
	s.Participants[2].Score = 600
	remember(begin(t, s, "stake"))
	s = remember(begin(t, s, "stakeAll"))
	s = do(t, s, "p1", "wager", with(s, "amount", 50))
	s = do(t, s, "p2", "wager", with(s, "amount", 60))
	s = remember(do(t, s, "host", "advance", dep(s)))
	remember(do(t, s, "host", "advance", dep(s)))
	round.Type = "final"
	round.Themes = append(round.Themes, ThemeSpec{ID: "other", QuestionID: "other-q"})
	s = v2Fixture()
	s.Participants[1].Score = 100
	s.Participants[2].Score = 200
	s = remember(do(t, s, "host", "begin_round", BeginRoundPayload{Round: round}))
	remember(do(t, s, "host", "eliminate_theme", map[string]any{"stage_id": s.Gameplay.Stage.ID, "theme_id": "other"}))
	q = qspec("noRisk")
	q.Answer = []TextStep{{Text: "Explanation", DurationMS: 5000}}
	s = do(t, v2Fixture(), "host", "begin_question", BeginQuestionPayload{Question: q})
	s = do(t, s, "host", "advance", dep(s))
	remember(judge(t, s, "p1", true))
	kinds := []string{"select_question", "present_question", "buzz_open", "buzz_window", "collect_answer", "collect_answers", "judge", "resolved", "select_recipient", "select_price", "auction", "collect_wagers", "reveal", "eliminate_theme", "select_final", "present_answer"}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			s, ok := states[kind]
			if !ok {
				t.Fatalf("missing fixture %s", kind)
			}
			before, _ := json.Marshal(s)
			deadline := NextDeadline(s)
			paused, changed := PauseAt(s, 1100)
			if !changed || NextDeadline(paused) != 0 {
				t.Fatal("pause did not freeze stage")
			}
			if _, again := PauseAt(paused, 1200); again {
				t.Fatal("pause applied twice")
			}
			after, _ := json.Marshal(s)
			if string(before) != string(after) {
				t.Fatal("pause mutated original")
			}
			resumed := doAt(t, paused, "host", "resume", EmptyPayload{}, 100000)
			want := int64(0)
			if deadline != 0 {
				want = 100000 + deadline - 1100
			}
			if NextDeadline(resumed) != want {
				t.Fatalf("deadline %d, want %d", NextDeadline(resumed), want)
			}
			if resumed.Gameplay.Stage.ID != s.Gameplay.Stage.ID {
				t.Fatal("pause should preserve stage identity")
			}
		})
	}
}
func TestV2RulesSnapshotDurationOverridesAndTimeoutActions(t *testing.T) {
	s := v2Fixture()
	q := qspec("withButton")
	q.AnswerSeconds = 17
	s = do(t, s, "host", "begin_question", BeginQuestionPayload{Question: q})
	rejected(t, s, "host", "set_rules", RulesPayload{Rules: DefaultRules()}, 1000)
	s = do(t, s, "host", "open_buzzer", dep(s))
	if NextDeadline(s) != 18000 {
		t.Fatal("package duration did not override buzzer wait")
	}
	s = do(t, s, "p1", "buzz", dep(s))
	if NextDeadline(s) != 11000 {
		t.Fatal("package duration overrode ordinary post-buzz answer")
	}
	s = judge(t, s, "p1", true)
	rules := DefaultRules()
	rules.Preset = "friends"
	rules.Penalties = false
	rules.FalseStart = false
	s = do(t, s, "host", "set_rules", RulesPayload{Rules: rules})
	if !s.Gameplay.Question.Rules.Penalties || s.Gameplay.RulesVersion != 2 {
		t.Fatal("changing rules mutated completed snapshot")
	}
	q = qspec("withButton")
	q.ID = "q2"
	s = do(t, s, "host", "begin_question", BeginQuestionPayload{Question: q})
	rejected(t, s, "p1", "buzz", dep(s), 1000)
	s = do(t, s, "host", "open_buzzer", dep(s))
	deadline := NextDeadline(s)
	s, changed := AdvanceAt(s, deadline)
	if !changed || !s.Gameplay.Stage.Expired || s.Gameplay.Stage.Kind != "buzz_open" {
		t.Fatal("buzzer timeout must await host")
	}
	rejected(t, s, "p2", "buzz", dep(s), deadline)
	s = doAt(t, s, "host", "extend", ExtendPayload{StageID: s.Gameplay.Stage.ID, Seconds: 5}, deadline)
	s = doAt(t, s, "p2", "buzz", dep(s), deadline)
	s = judge(t, s, "p2", false)
	if s.Participants[2].Score != 0 {
		t.Fatal("friends penalty applied")
	}
	for _, kind := range []string{"noRisk", "forAll"} {
		q = qspec(kind)
		q.AnswerSeconds = 19
		s = do(t, v2Fixture(), "host", "begin_question", BeginQuestionPayload{Question: q})
		s = do(t, s, "host", "advance", dep(s))
		if NextDeadline(s) != 20000 {
			t.Fatalf("package duration not used for %s", kind)
		}
	}
}
func TestV2RuleBoundsReplayAndZeroPrice(t *testing.T) {
	for _, change := range []func(*Rules){func(r *Rules) { r.SelectionSeconds = 4 }, func(r *Rules) { r.AnswerSeconds = 121 }, func(r *Rules) { r.SpecialSeconds = 0 }, func(r *Rules) { r.WagerSeconds = 121 }, func(r *Rules) { r.FinalSeconds = 4 }, func(r *Rules) { r.WindowMS = 99 }, func(r *Rules) { r.WindowMS = 301 }, func(r *Rules) { r.FalseStartSeconds = 0 }, func(r *Rules) { r.FalseStartSeconds = 6 }, func(r *Rules) { r.NoRiskMultiplier = 3 }, func(r *Rules) { r.BuzzerMode = "unknown" }} {
		r := DefaultRules()
		change(&r)
		if r.Validate() == nil {
			t.Fatalf("invalid rules accepted %+v", r)
		}
	}
	s := v2Fixture()
	q := qspec("noRisk")
	q.Price = 0
	s = do(t, s, "host", "begin_question", BeginQuestionPayload{Question: q})
	old := s.Gameplay.Question.InstanceID
	s = do(t, s, "host", "advance", dep(s))
	s = judge(t, s, "p1", true)
	if s.Participants[1].Score != 0 {
		t.Fatal("zero price scored nonzero")
	}
	rejected(t, s, "host", "begin_question", BeginQuestionPayload{Question: q}, 1000)
	s = do(t, s, "host", "begin_question", BeginQuestionPayload{Question: q, Replay: true})
	if s.Gameplay.Question.InstanceID == old {
		t.Fatal("replay reused instance identity")
	}
}

func TestV2FalseStartExpiryRestoresPermissionWithoutAdvancingStage(t *testing.T) {
	s := begin(t, v2Fixture(), "withButton")
	s = doAt(t, s, "p1", "buzz", dep(s), 1000)
	s = doAt(t, s, "host", "open_buzzer", dep(s), 1100)
	original := s.Gameplay.Stage
	if hasAction(ProjectAt(s, "p1", 1100), "buzz") {
		t.Fatal("false starter can buzz early")
	}
	if deadline := NextDeadline(s); deadline != 4000 {
		t.Fatalf("permission expires at 4000, next runtime wakeup %d", deadline)
	}
	if _, changed := AdvanceAt(s, 3999); changed {
		t.Fatal("early tick changed state")
	}
	n, changed := AdvanceAt(s, 4000)
	if !changed || n.Gameplay.Stage != original || !hasAction(ProjectAt(n, "p1", 4000), "buzz") {
		t.Fatal("unblock must publish permission without advancing stage")
	}
	if submission(n.Gameplay.Question, "p1").BlockedUntilMS != 0 || NextDeadline(n) != original.DeadlineMS {
		t.Fatal("permission wakeup was not consumed")
	}
	if _, changed = AdvanceAt(n, 4000); changed {
		t.Fatal("duplicate wakeup changed state")
	}
	if submission(s.Gameplay.Question, "p1").BlockedUntilMS != 4000 {
		t.Fatal("tick mutated input")
	}
}

func TestV2FalseStartExpiryFreezesOnPause(t *testing.T) {
	s := begin(t, v2Fixture(), "withButton")
	s = doAt(t, s, "p1", "buzz", dep(s), 1000)
	s = doAt(t, s, "host", "open_buzzer", dep(s), 1100)
	s, _ = PauseAt(s, 2000)
	if NextDeadline(s) != 0 {
		t.Fatal("paused room still scheduled")
	}
	s = doAt(t, s, "host", "resume", EmptyPayload{}, 10000)
	if NextDeadline(s) != 12000 || hasAction(ProjectAt(s, "p1", 10000), "buzz") {
		t.Fatal("pause lost remaining false-start block")
	}
	s, _ = AdvanceAt(s, 12000)
	if !hasAction(ProjectAt(s, "p1", 12000), "buzz") {
		t.Fatal("resumed false-start did not expire")
	}
}
