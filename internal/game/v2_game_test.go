package game

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func v2Fixture() State {
	s := fixture()
	s.Seq = 1
	s.Gameplay = NewSession(DefaultRules())
	n, err := ApplyAt(s, "host", v2cmd("begin_round", BeginRoundPayload{Round: RoundSpec{ID: "r", Name: "Round", Type: "standard", Themes: []ThemeSpec{{ID: "t", Name: "Theme", QuestionID: "q"}}, MinimumPrice: 100, MaximumPrice: 500}}), 1000, 7)
	if err != nil {
		panic(err)
	}
	return n
}
func qspec(kind string) QuestionSpec {
	return QuestionSpec{ID: "q", RoundID: "r", ThemeID: "t", Theme: "Theme", Type: kind, Price: 100, Question: []TextStep{{Text: "Question"}}, Right: []string{"SECRET RIGHT"}}
}
func v2cmd(kind string, data any) Command {
	b, _ := json.Marshal(data)
	return Command{Version: 2, ID: "cmd", Type: kind, Data: b}
}
func doAt(t *testing.T, s State, actor, kind string, data any, now int64) State {
	t.Helper()
	before, _ := json.Marshal(s)
	n, e := ApplyAt(s, actor, v2cmd(kind, data), now, 7)
	after, _ := json.Marshal(s)
	if string(before) != string(after) {
		t.Fatal("input mutated")
	}
	if e != nil {
		t.Fatalf("%s/%s: %v (stage %+v)", actor, kind, e, s.Gameplay.Stage)
	}
	return n
}
func do(t *testing.T, s State, actor, kind string, data any) State {
	t.Helper()
	return doAt(t, s, actor, kind, data, 1000)
}
func dep(s State) map[string]any {
	return map[string]any{"stage_id": s.Gameplay.Stage.ID, "instance_id": s.Gameplay.Stage.InstanceID}
}
func with(s State, key string, value any) map[string]any { d := dep(s); d[key] = value; return d }
func begin(t *testing.T, s State, kind string) State {
	t.Helper()
	return do(t, s, "host", "begin_question", BeginQuestionPayload{Question: qspec(kind)})
}
func judge(t *testing.T, s State, id string, correct bool) State {
	t.Helper()
	d := with(s, "target_id", id)
	d["correct"] = correct
	return do(t, s, "host", "judge", d)
}
func rejected(t *testing.T, s State, actor, kind string, data any, now int64) {
	t.Helper()
	before, _ := json.Marshal(s)
	n, e := ApplyAt(s, actor, v2cmd(kind, data), now, 7)
	after, _ := json.Marshal(s)
	out, _ := json.Marshal(n)
	if e == nil || string(before) != string(after) || string(before) != string(out) {
		t.Fatalf("expected immutable rejection %s: %v", kind, e)
	}
}
func hasAction(v View, kind string) bool {
	if v.Gameplay == nil {
		return false
	}
	for _, a := range v.Gameplay.AllowedActions {
		if a.Type == kind {
			return true
		}
	}
	return false
}

func TestV2OrdinaryWrongContinuesAndStaleActionsFail(t *testing.T) {
	s := begin(t, v2Fixture(), "withButton")
	s = do(t, s, "host", "open_buzzer", dep(s))
	stale := dep(s)
	s = do(t, s, "p1", "buzz", dep(s))
	s = do(t, s, "host", "advance", dep(s))
	s = judge(t, s, "p1", false)
	if s.Gameplay.Stage.Kind != "buzz_open" || s.Participants[1].Score != -100 {
		t.Fatalf("wrong answer: %+v", s)
	}
	rejected(t, s, "p1", "buzz", dep(s), 1000)
	rejected(t, s, "p2", "buzz", stale, 1000)
	s = do(t, s, "p2", "buzz", dep(s))
	s = judge(t, s, "p2", true)
	if s.Gameplay.Stage.Kind != "resolved" || s.Participants[2].Score != 100 || s.Gameplay.ChooserID != "p2" {
		t.Fatalf("winner: %+v", s)
	}
	if ProjectAt(s, "s1", 1000).Gameplay.Question.Answer == nil {
		t.Fatal("resolved ordinary answer hidden")
	}
	rejected(t, s, "host", "judge", with(s, "target_id", "p2"), 1000)
}
func TestV2NoRiskAndForAllScoring(t *testing.T) {
	for _, correct := range []bool{false, true} {
		s := begin(t, v2Fixture(), "noRisk")
		s = do(t, s, "host", "advance", dep(s))
		s = judge(t, s, "p1", correct)
		want := int64(0)
		if correct {
			want = 200
		}
		if s.Participants[1].Score != want || s.Gameplay.Stage.Kind != "resolved" {
			t.Fatalf("noRisk %+v", s)
		}
	}
	s := begin(t, v2Fixture(), "forAll")
	s = do(t, s, "host", "advance", dep(s))
	stage := dep(s)
	s = do(t, s, "p1", "answer", with(s, "text", "one"))
	d := map[string]any{"stage_id": stage["stage_id"], "instance_id": stage["instance_id"], "text": "two"}
	s = do(t, s, "p2", "answer", d)
	s = judge(t, s, "p1", true)
	s = judge(t, s, "p2", false)
	if s.Participants[1].Score != 100 || s.Participants[2].Score != -100 {
		t.Fatal("independent scoring failed")
	}
}
func TestV2WagersAndAnswersArePrivateUntilReveal(t *testing.T) {
	s := v2Fixture()
	s.Participants[1].Score = 500
	s.Participants[2].Score = 700
	s = begin(t, s, "stakeAll")
	s = do(t, s, "p1", "wager", with(s, "amount", 300))
	s = do(t, s, "p2", "wager", with(s, "amount", 400))
	for _, id := range []string{"host", "p2", "s1"} {
		b, _ := json.Marshal(ProjectAt(s, id, 1000))
		if strings.Contains(string(b), `"wager":300`) {
			t.Fatalf("wager leak to %s", id)
		}
	}
	s = do(t, s, "host", "advance", dep(s))
	s = do(t, s, "p1", "answer", with(s, "text", "PRIVATE ONE"))
	b, _ := json.Marshal(ProjectAt(s, "host", 1000))
	if strings.Contains(string(b), "PRIVATE ONE") {
		t.Fatal("host saw answer during collection")
	}
	s = do(t, s, "p2", "answer", with(s, "text", "PRIVATE TWO"))
	b, _ = json.Marshal(ProjectAt(s, "host", 1000))
	if !strings.Contains(string(b), "PRIVATE ONE") {
		t.Fatal("closed answer not visible to host")
	}
	rejected(t, s, "host", "judge", map[string]any{"stage_id": s.Gameplay.Stage.ID, "instance_id": s.Gameplay.Stage.InstanceID, "target_id": "p1", "correct": true}, 1000)
	s = do(t, s, "host", "reveal", with(s, "target_id", "p1"))
	s = judge(t, s, "p1", true)
	if s.Participants[1].Score != 800 {
		t.Fatal("wrong wager score")
	}
	b, _ = json.Marshal(ProjectAt(s, "s1", 1000))
	if !strings.Contains(string(b), "PRIVATE ONE") || strings.Contains(string(b), "PRIVATE TWO") {
		t.Fatal("selective reveal leak")
	}
	s = do(t, s, "host", "finish", EmptyPayload{})
	b, _ = json.Marshal(ProjectAt(s, "s1", 1000))
	if strings.Contains(string(b), "PRIVATE TWO") {
		t.Fatal("finish reveals hidden answers")
	}
}
func TestV2SecretRecipientAndPrice(t *testing.T) {
	s := v2Fixture()
	q := qspec("secret")
	q.SelectionMode = "exceptCurrent"
	q.PriceRange = &PriceRange{Minimum: 100, Maximum: 550, Step: 200}
	s = do(t, s, "host", "begin_question", BeginQuestionPayload{Question: q})
	v := ProjectAt(s, "s1", 1000)
	if v.Gameplay.Question.Theme != "" || v.Gameplay.Question.Text != "" || v.Gameplay.Question.Price != 0 {
		t.Fatal("secret leaked before recipient")
	}
	rejected(t, s, "p1", "select_recipient", with(s, "target_id", "p1"), 1000)
	s = do(t, s, "p1", "select_recipient", with(s, "target_id", "p2"))
	rejected(t, s, "p2", "select_price", with(s, "amount", 200), 1000)
	s = do(t, s, "p2", "select_price", with(s, "amount", 550))
	s = do(t, s, "host", "advance", dep(s))
	s = judge(t, s, "p2", true)
	if s.Participants[2].Score != 550 {
		t.Fatal("secret wrong price")
	}
	s = v2Fixture()
	s = begin(t, s, "secret")
	s = do(t, s, "p1", "select_recipient", with(s, "target_id", "p2"))
	if s.Gameplay.Stage.Kind != "present_question" {
		t.Fatal("absent price must skip price selection")
	}
}
func TestV2AuctionOrderingAllInAndForcedNominal(t *testing.T) {
	s := v2Fixture()
	s.Participants[1].Score = 50
	s.Participants[2].Score = 700
	s = begin(t, s, "stake")
	if s.Gameplay.Question.Auction.Amount != 100 || s.Gameplay.Stage.TurnID != "p2" {
		t.Fatalf("low chooser must bid nominal: %+v", s.Gameplay)
	}
	s = do(t, s, "p2", "bid", with(s, "mode", "pass"))
	if s.Gameplay.Question.RecipientID != "p1" {
		t.Fatal("chooser lost nominal")
	}
	s = v2Fixture()
	s.Participants[1].Score = 555
	s.Participants[2].Score = 777
	s = begin(t, s, "stake")
	d := with(s, "mode", "bid")
	d["amount"] = 555
	s = do(t, s, "p1", "bid", d)
	if s.Gameplay.Question.Auction.AllIn {
		t.Fatal("numeric maximum must not imply all-in")
	}
	s = do(t, s, "p2", "bid", with(s, "mode", "all_in"))
	if s.Gameplay.Question.RecipientID != "p2" || s.Gameplay.Question.Price != 777 {
		t.Fatal("all-in not accepted")
	}
	s = v2Fixture()
	s.Participants = append(s.Participants, Participant{ID: "p3", Name: "Third", Role: Player, Score: 900})
	s.Participants[1].Score = 500
	s.Participants[2].Score = 800
	s = begin(t, s, "stake")
	s = do(t, s, "p1", "bid", with(s, "mode", "all_in"))
	d = with(s, "mode", "bid")
	d["amount"] = 600
	rejected(t, s, "p2", "bid", d, 1000)
}
func TestV2FinalEliminationAndPositiveEligibility(t *testing.T) {
	s := v2Fixture()
	s.Participants[1].Score = 200
	s.Participants[2].Score = 100
	s.Participants = append(s.Participants, Participant{ID: "p3", Name: "Zero", Role: Player})
	round := RoundSpec{ID: "r", Name: "Final", Type: "final", Themes: []ThemeSpec{{ID: "a", Name: "A", QuestionID: "qa"}, {ID: "b", Name: "B", QuestionID: "qb"}, {ID: "t", Name: "T", QuestionID: "q"}}}
	s = do(t, s, "host", "begin_round", BeginRoundPayload{Round: round})
	if s.Gameplay.Stage.TurnID != "p2" || len(s.Gameplay.Final.EligibleIDs) != 2 {
		t.Fatal("wrong final order")
	}
	s = do(t, s, "p2", "eliminate_theme", map[string]any{"stage_id": s.Gameplay.Stage.ID, "theme_id": "a"})
	if s.Gameplay.Stage.TurnID != "p1" {
		t.Fatal("leader must eliminate last")
	}
	s = do(t, s, "p1", "eliminate_theme", map[string]any{"stage_id": s.Gameplay.Stage.ID, "theme_id": "b"})
	q := qspec("stakeAll")
	bad := q
	bad.ID = "other"
	rejected(t, s, "host", "begin_question", BeginQuestionPayload{Question: bad}, 1000)
	s = do(t, s, "host", "begin_question", BeginQuestionPayload{Question: q})
	if s.Gameplay.Stage.Kind != "collect_wagers" || len(s.Gameplay.Question.Players) != 2 {
		t.Fatal("final wager stage invalid")
	}
}
func TestV2TimerPauseFalseStartRandomWindow(t *testing.T) {
	s := begin(t, v2Fixture(), "withButton")
	s = do(t, s, "p1", "buzz", dep(s))
	if s.Gameplay.Question.Players[0].BlockedUntilMS != 4000 {
		t.Fatal("false start not recorded")
	}
	s = do(t, s, "host", "open_buzzer", dep(s))
	rejected(t, s, "p1", "buzz", dep(s), 3999)
	deadline := s.Gameplay.Stage.DeadlineMS
	s, changed := PauseAt(s, deadline+100)
	if !changed || s.Gameplay.Stage.RemainingMS != 0 || NextDeadline(s) != 0 {
		t.Fatal("overdue recovery must pause at zero")
	}
	s = doAt(t, s, "host", "resume", EmptyPayload{}, 50000)
	s, changed = AdvanceAt(s, 50000)
	if !changed || !s.Gameplay.Stage.Expired {
		t.Fatal("zero remaining must expire on resume")
	}
	s = v2Fixture()
	s.Gameplay.Rules.BuzzerMode = "random"
	s = begin(t, s, "withButton")
	s = do(t, s, "host", "open_buzzer", dep(s))
	s = do(t, s, "p1", "buzz", dep(s))
	s = doAt(t, s, "p2", "buzz", dep(s), 1100)
	rejected(t, s, "p1", "buzz", dep(s), 1101)
	paused, _ := PauseAt(s, 1150)
	paused = doAt(t, paused, "host", "resume", EmptyPayload{}, 10000)
	deadline = NextDeadline(paused)
	a, ok := AdvanceAt(paused, deadline)
	b, ok2 := AdvanceAt(paused, deadline)
	if !ok || !ok2 || a.Winner != b.Winner || a.Winner == "" {
		t.Fatal("seeded winner must be stable")
	}
	if _, changed = AdvanceAt(a, deadline); changed {
		t.Fatal("window resolved twice")
	}
}
func TestV2CorrectionsAppealTransferAndRulesSnapshot(t *testing.T) {
	s := begin(t, v2Fixture(), "withButton")
	s = do(t, s, "host", "open_buzzer", dep(s))
	s = do(t, s, "p1", "buzz", dep(s))
	s = judge(t, s, "p1", true)
	id := s.Gameplay.Decisions[0].ID
	s = do(t, s, "p2", "appeal", AppealPayload{JudgementID: id, Reason: "Review"})
	b := false
	s = do(t, s, "host", "correct_judgement", CorrectionPayload{JudgementID: id, Correct: &b, Reason: "Incorrect"})
	if s.Participants[1].Score != -100 || len(s.Gameplay.Decisions) != 2 {
		t.Fatal("correction must compensate +100 with -200")
	}
	rejected(t, s, "host", "correct_judgement", CorrectionPayload{JudgementID: id, Correct: &b, Reason: "Again"}, 1000)
	s = do(t, s, "host", "resolve_appeal", ResolveAppealPayload{AppealID: s.Gameplay.Appeals[0].ID, Reason: "Done"})
	s = do(t, s, "host", "pause", EmptyPayload{})
	s = do(t, s, "host", "transfer_host", TransferPayload{TargetID: "s1"})
	p, _ := s.Participant("host")
	if p.Role != Spectator {
		t.Fatal("old host retains authority")
	}
	rejected(t, s, "host", "resume", EmptyPayload{}, 1000)
	s = do(t, s, "s1", "resume", EmptyPayload{})
	q := qspec("forAll")
	q.ID = "q2"
	s = do(t, s, "s1", "begin_question", BeginQuestionPayload{Question: q})
	rejected(t, s, "s1", "correct_judgement", CorrectionPayload{JudgementID: id, Correct: &b, Reason: "Too late"}, 1000)
}
func TestV2LateAnswerWagerAndScoreLock(t *testing.T) {
	s := v2Fixture()
	s.Participants[1].Score = 100
	s.Participants[2].Score = 200
	s = begin(t, s, "stakeAll")
	rejected(t, s, "p1", "wager", with(s, "amount", 50), NextDeadline(s))
	s = do(t, s, "p1", "wager", with(s, "amount", 50))
	delta := int64(100)
	rejected(t, s, "host", "adjust_score", AdjustPayload{TargetID: "p1", Delta: &delta, Reason: "Fix"}, 1000)
	s = do(t, s, "host", "restart_stage", RestartPayload{StageID: s.Gameplay.Stage.ID, Reason: "Reset wager"})
	if s.Gameplay.Question.Players[0].Wager != nil {
		t.Fatal("restart retains wager")
	}
	s, _ = AdvanceAt(s, NextDeadline(s))
	if s.Gameplay.Stage.Kind != "resolved" {
		t.Fatal("all missing wagers should resolve")
	}
	s = begin(t, v2Fixture(), "noRisk")
	s = do(t, s, "host", "advance", dep(s))
	rejected(t, s, "p1", "answer", with(s, "text", "late"), NextDeadline(s))
	s, _ = AdvanceAt(s, NextDeadline(s))
	if s.Gameplay.Stage.Kind != "judge" || s.Participants[1].Score != 0 {
		t.Fatal("timeout must not score automatically")
	}
}
func TestV2RandomWindowAcceptsOriginalStageAndPreservesSeedOnRecovery(t *testing.T) {
	s := v2Fixture()
	s.Gameplay.Rules.BuzzerMode = "random"
	s = begin(t, s, "withButton")
	s = do(t, s, "host", "open_buzzer", dep(s))
	original := dep(s)
	s = do(t, s, "p1", "buzz", original)
	s = doAt(t, s, "p2", "buzz", original, 1050)
	if len(s.Gameplay.Question.Candidates) != 2 || s.Gameplay.Question.Seed != 7 {
		t.Fatal("random candidates lost")
	}
	s, _ = PauseAt(s, 1100)
	s = doAt(t, s, "host", "resume", EmptyPayload{}, 2000)
	if len(s.Gameplay.Question.Candidates) != 2 || s.Gameplay.Question.Seed != 7 {
		t.Fatal("recovery lost window")
	}
}
func TestV2AnswerExplanationProgressionKeepsPrimaryAnswerAndHidesFutureSteps(t *testing.T) {
	s := v2Fixture()
	q := qspec("withButton")
	q.Answer = []TextStep{{Text: "First explanation", DurationMS: 2000}, {Text: "Second explanation"}, {Text: "HOST ONLY", Placement: "replic"}}
	s = do(t, s, "host", "begin_question", BeginQuestionPayload{Question: q})
	v := ProjectAt(s, "host", 1000)
	if v.Gameplay.Question.Answer == nil || !strings.Contains(*v.Gameplay.Question.Answer, "SECRET RIGHT") {
		t.Fatal("host lost canonical correct answer")
	}
	s = do(t, s, "host", "open_buzzer", dep(s))
	s = do(t, s, "p1", "buzz", dep(s))
	s = judge(t, s, "p1", true)
	if s.Gameplay.Stage.Kind != "present_answer" {
		t.Fatalf("answer presentation missing: %s", s.Gameplay.Stage.Kind)
	}
	v = ProjectAt(s, "s1", 1000)
	if v.Gameplay.Question.Answer == nil || !strings.Contains(*v.Gameplay.Question.Answer, "First explanation") || strings.Contains(*v.Gameplay.Question.Answer, "Second explanation") {
		t.Fatal("future explanation leaked")
	}
	s, changed := AdvanceAt(s, 3000)
	if !changed {
		t.Fatal("answer fragment timer did not advance")
	}
	v = ProjectAt(s, "s1", 3000)
	if !strings.Contains(*v.Gameplay.Question.Answer, "Second explanation") {
		t.Fatal("second explanation missing")
	}
	s = doAt(t, s, "host", "advance", dep(s), 3000)
	v = ProjectAt(s, "s1", 3000)
	if strings.Contains(*v.Gameplay.Question.Answer, "HOST ONLY") {
		t.Fatal("replic answer leaked")
	}
	s = doAt(t, s, "host", "advance", dep(s), 3000)
	if s.Gameplay.Stage.Kind != "resolved" {
		t.Fatal("answer steps did not finish")
	}
}
func TestV2FinalExplanationResumesCorrectParticipantJudgement(t *testing.T) {
	s := v2Fixture()
	s.Participants[1].Score = 100
	s.Participants[2].Score = 100
	q := qspec("stakeAll")
	q.Answer = []TextStep{{Text: "Explanation"}}
	s = do(t, s, "host", "begin_question", BeginQuestionPayload{Question: q})
	s = do(t, s, "p1", "wager", with(s, "amount", 50))
	s = do(t, s, "p2", "wager", with(s, "amount", 60))
	s = do(t, s, "host", "advance", dep(s))
	s = do(t, s, "host", "advance", dep(s))
	s = do(t, s, "host", "reveal", with(s, "target_id", "p1"))
	if s.Gameplay.Stage.Kind != "present_answer" {
		t.Fatal("explanation skipped")
	}
	s = do(t, s, "host", "advance", dep(s))
	if s.Gameplay.Stage.Kind != "judge" || s.Gameplay.Stage.TurnID != "p1" {
		t.Fatal("lost reveal target")
	}
	s = judge(t, s, "p1", true)
	s = do(t, s, "host", "reveal", with(s, "target_id", "p2"))
	if s.Gameplay.Stage.Kind != "judge" {
		t.Fatal("answer replayed for every participant")
	}
}
func TestV2AuctionSourceAlignedMinimumAndNumericMaximum(t *testing.T) {
	for _, maximum := range []int64{600, 777, 900} {
		s := v2Fixture()
		s.Participants[1].Score = 555
		s.Participants[2].Score = maximum
		s = begin(t, s, "stake")
		bid := with(s, "mode", "bid")
		bid["amount"] = 555
		s = do(t, s, "p1", "bid", bid)
		bid = with(s, "mode", "bid")
		bid["amount"] = 600
		rejected(t, s, "p2", "bid", bid, 1000)
		if maximum == 600 {
			s = do(t, s, "p2", "bid", with(s, "mode", "all_in"))
		} else {
			bid["amount"] = maximum
			if maximum == 900 {
				bid["amount"] = 700
			}
			s = do(t, s, "p2", "bid", bid)
			if s.Gameplay.Question.Auction.AllIn {
				t.Fatal("numeric bid became all-in")
			}
		}
	}
}
func TestV2RandomWrongAnswerAllowsPreviousWindowLoser(t *testing.T) {
	s := v2Fixture()
	s.Gameplay.Rules.BuzzerMode = "random"
	s = begin(t, s, "withButton")
	s = do(t, s, "host", "open_buzzer", dep(s))
	s = do(t, s, "p1", "buzz", dep(s))
	s = do(t, s, "p2", "buzz", dep(s))
	s, _ = AdvanceAt(s, 1200)
	winner := s.Winner
	other := "p1"
	if winner == other {
		other = "p2"
	}
	s = judge(t, s, winner, false)
	s = doAt(t, s, other, "buzz", dep(s), 1300)
	if s.Gameplay.Stage.Kind != "buzz_window" {
		t.Fatal("loser cannot buzz again")
	}
}
func TestV2CannotAppealOrCorrectScoreAdjustmentAsJudgement(t *testing.T) {
	s := begin(t, v2Fixture(), "noRisk")
	s = do(t, s, "host", "advance", dep(s))
	s = judge(t, s, "p1", true)
	delta := int64(20)
	s = do(t, s, "host", "adjust_score", AdjustPayload{TargetID: "p1", Delta: &delta, Reason: "Manual"})
	id := s.Gameplay.Decisions[len(s.Gameplay.Decisions)-1].ID
	rejected(t, s, "p1", "appeal", AppealPayload{JudgementID: id, Reason: "No judgement"}, 1000)
	correct := true
	rejected(t, s, "host", "correct_judgement", CorrectionPayload{JudgementID: id, Correct: &correct, Reason: "No judgement"}, 1000)
}
func TestV2ResolvedPauseResumePreservesSelectionTimer(t *testing.T) {
	s := begin(t, v2Fixture(), "noRisk")
	s = do(t, s, "host", "advance", dep(s))
	s = judge(t, s, "p1", true)
	s, _ = PauseAt(s, 1500)
	s = doAt(t, s, "host", "resume", EmptyPayload{}, 5000)
	if NextDeadline(s) != 34500 {
		t.Fatal("resolved selection timer did not freeze and resume")
	}
}
func TestV2SelectionExpiryLeavesOnlyHostChoice(t *testing.T) {
	s := do(t, v2Fixture(), "host", "begin_round", BeginRoundPayload{Round: RoundSpec{ID: "r", Type: "standard", Themes: []ThemeSpec{{ID: "t", QuestionID: "q"}}, MinimumPrice: 100, MaximumPrice: 100}})
	rejected(t, s, "p1", "begin_question", BeginQuestionPayload{Question: qspec("noRisk")}, NextDeadline(s))
	s = doAt(t, s, "host", "begin_question", BeginQuestionPayload{Question: qspec("noRisk")}, NextDeadline(s))
	if s.Gameplay.Question == nil {
		t.Fatal("host could not replace expired chooser")
	}
}
func TestV2BoundedAnswerAndDisplayHistory(t *testing.T) {
	s := v2Fixture()
	for i := 3; i <= 12; i++ {
		s.Participants = append(s.Participants, Participant{ID: fmt.Sprintf("p%d", i), Name: strings.Repeat("Я", 32), Role: Player, Score: 100})
	}
	for i := range s.Participants {
		if s.Participants[i].Role == Player {
			s.Participants[i].Score = 100
		}
	}
	q := qspec("stakeAll")
	q.Question = []TextStep{{Text: strings.Repeat("Q", 4096)}}
	q.Right = []string{strings.Repeat("A", 4096)}
	q.Answer = []TextStep{{Text: strings.Repeat("E", 4096)}}
	s = do(t, s, "host", "begin_question", BeginQuestionPayload{Question: q})
	for _, id := range playerIDs(s) {
		s = do(t, s, id, "wager", with(s, "amount", 50))
	}
	s = do(t, s, "host", "advance", dep(s))
	rejected(t, s, "p1", "answer", with(s, "text", strings.Repeat("я", 513)), 1000)
	rejected(t, s, "p1", "answer", with(s, "text", strings.Repeat("<", 1024)), 1000)
	for _, id := range playerIDs(s) {
		s = do(t, s, id, "answer", with(s, "text", strings.Repeat("x", 1024)))
	}
	reason := strings.Repeat("<&", 512)
	for i := 0; i < 64; i++ {
		s.Gameplay.Decisions = append(s.Gameplay.Decisions, Decision{ID: fmt.Sprintf("judgement-%d", i), ParticipantID: "p1", Reason: reason, QuestionID: "old"})
		s.Gameplay.Appeals = append(s.Gameplay.Appeals, Appeal{ID: fmt.Sprintf("appeal-%d", i), JudgementID: fmt.Sprintf("judgement-%d", i), ParticipantID: "p1", Reason: reason, Resolution: reason})
	}
	for _, id := range []string{"host", "p1", "s1"} {
		v := ProjectAt(s, id, 1000)
		b, _ := json.Marshal(v)
		if len(b) > 60*1024 {
			t.Fatalf("%s snapshot exceeds budget: %d", id, len(b))
		}
		if len(v.Gameplay.Decisions) != 20 || len(v.Gameplay.Appeals) != 20 {
			t.Fatal("history count changed")
		}
		if len(v.Gameplay.Decisions[0].Reason) > 160 || len(v.Gameplay.Appeals[0].Resolution) > 160 {
			t.Fatal("display reasons not bounded")
		}
	}
	if s.Gameplay.Decisions[0].Reason != reason {
		t.Fatal("private ledger text truncated")
	}
}
func TestV2RestartPreservesCompletedAnswerCollectionAndCurrentFragment(t *testing.T) {
	s := v2Fixture()
	q := qspec("forAll")
	q.Question = append(q.Question, TextStep{Text: "SECOND"})
	s = do(t, s, "host", "begin_question", BeginQuestionPayload{Question: q})
	s = do(t, s, "host", "advance", dep(s))
	old := s.Gameplay.Stage.ID
	s = do(t, s, "host", "restart_stage", RestartPayload{StageID: old, Reason: "Reset current fragment"})
	if s.Gameplay.Question.ContentIndex != 1 || s.Gameplay.Stage.ID == old {
		t.Fatal("restarting fragment reset completed content")
	}
	s = do(t, s, "host", "advance", dep(s))
	s = do(t, s, "p1", "answer", with(s, "text", "first"))
	s = do(t, s, "p2", "answer", with(s, "text", "second"))
	old = s.Gameplay.Stage.ID
	s = do(t, s, "host", "restart_stage", RestartPayload{StageID: old, Reason: "Renew judgement deadline"})
	if s.Gameplay.Stage.Kind != "judge" || s.Gameplay.Question.Players[0].Answer == nil || *s.Gameplay.Question.Players[0].Answer != "first" {
		t.Fatal("restart erased completed answer collection")
	}
}
func TestV2HistoryTrimmingRetainsCurrentJudgementAndCorrectionGuard(t *testing.T) {
	s := begin(t, v2Fixture(), "noRisk")
	s = do(t, s, "host", "advance", dep(s))
	s = judge(t, s, "p1", true)
	id := s.Gameplay.Decisions[0].ID
	incorrect := false
	s = do(t, s, "host", "correct_judgement", CorrectionPayload{JudgementID: id, Correct: &incorrect, Reason: "Correction"})
	delta := int64(1)
	for i := 0; i < 70; i++ {
		s = do(t, s, "host", "adjust_score", AdjustPayload{TargetID: "p1", Delta: &delta, Reason: "Manual"})
	}
	found := false
	for _, d := range s.Gameplay.Decisions {
		found = found || d.ID == id
	}
	if !found {
		t.Fatal("current original judgement trimmed out")
	}
	rejected(t, s, "host", "correct_judgement", CorrectionPayload{JudgementID: id, Correct: &incorrect, Reason: "Duplicate correction"}, 1000)
	if len(s.Gameplay.Decisions) > 76 {
		t.Fatal("history unbounded")
	}
}

func TestV2RoundRequiredAndSecretDefaults(t *testing.T) {
	s := fixture()
	s.Seq = 1
	s.Gameplay = NewSession(DefaultRules())
	rejected(t, s, "host", "begin_question", BeginQuestionPayload{Question: qspec("noRisk")}, 1000)
	s = begin(t, v2Fixture(), "secret")
	rejected(t, s, "p1", "select_recipient", with(s, "target_id", "p1"), 1000)
	q := qspec("secret")
	q.PriceRange = &PriceRange{Minimum: 300, Maximum: 300}
	s = do(t, v2Fixture(), "host", "begin_question", BeginQuestionPayload{Question: q})
	s = do(t, s, "p1", "select_recipient", with(s, "target_id", "p2"))
	if s.Gameplay.Stage.Kind != "present_question" || s.Gameplay.Question.Price != 300 {
		t.Fatal("singleton secret range did not autoapply")
	}
	s = v2Fixture()
	s.Participants[1].Score = 500
	s.Participants[2].Score = 600
	s = begin(t, s, "stake")
	rejected(t, s, "p1", "bid", with(s, "mode", "pass"), 1000)
	s, _ = AdvanceAt(s, NextDeadline(s))
	if s.Gameplay.Question.Auction.Amount != 100 || s.Gameplay.Stage.TurnID != "p2" {
		t.Fatal("first auction timeout must assign nominal")
	}
}
func TestV2RoundTransitionRejectsUnresolvedAndEndsPriorCorrectionWindow(t *testing.T) {
	s := begin(t, v2Fixture(), "noRisk")
	next := RoundSpec{ID: "next", Type: "standard", Themes: []ThemeSpec{{ID: "next-theme", QuestionID: "next-q"}}, MinimumPrice: 100, MaximumPrice: 500}
	rejected(t, s, "host", "begin_round", BeginRoundPayload{Round: next}, 1000)
	s = do(t, s, "host", "advance", dep(s))
	s = judge(t, s, "p1", true)
	id := s.Gameplay.Decisions[0].ID
	s = do(t, s, "host", "begin_round", BeginRoundPayload{Round: next})
	if hasAction(ProjectAt(s, "host", 1000), "correct_judgement") {
		t.Fatal("previous round correction action persisted")
	}
	incorrect := false
	rejected(t, s, "host", "correct_judgement", CorrectionPayload{JudgementID: id, Correct: &incorrect, Reason: "Dependent round"}, 1000)
	delta := int64(-200)
	s = do(t, s, "host", "adjust_score", AdjustPayload{TargetID: "p1", Delta: &delta, Reason: "Correction after round change"})
	if s.Participants[1].Score != 0 {
		t.Fatal("reasoned adjustment between questions unavailable")
	}
}
func TestV2RandomWindowCannotBeExtendedBeyondConfiguredMilliseconds(t *testing.T) {
	s := v2Fixture()
	s.Gameplay.Rules.BuzzerMode = "random"
	s = begin(t, s, "withButton")
	s = do(t, s, "host", "open_buzzer", dep(s))
	s = do(t, s, "p1", "buzz", dep(s))
	rejected(t, s, "host", "extend", ExtendPayload{StageID: s.Gameplay.Stage.ID, Seconds: 120}, 1000)
}

func TestV2ResolvedSelectionExpiresForChooserAndFinalIsUntimed(t *testing.T) {
	s := begin(t, v2Fixture(), "noRisk")
	s = do(t, s, "host", "advance", dep(s))
	s = judge(t, s, "p1", true)
	if NextDeadline(s) != 31000 || s.Gameplay.Stage.TurnID != "p1" {
		t.Fatal("next selection did not start immediately")
	}
	q := qspec("noRisk")
	q.ID = "q2"
	rejected(t, s, "p1", "begin_question", BeginQuestionPayload{Question: q}, 31000)
	s = doAt(t, s, "host", "begin_question", BeginQuestionPayload{Question: q}, 31000)
	s = v2Fixture()
	s.Participants[1].Score = 100
	s.Participants[2].Score = 100
	round := RoundSpec{ID: "r", Type: "final", Themes: []ThemeSpec{{ID: "t", QuestionID: "q"}}, MinimumPrice: 100, MaximumPrice: 100}
	s = do(t, s, "host", "begin_round", BeginRoundPayload{Round: round})
	s = begin(t, s, "stakeAll")
	s = do(t, s, "p1", "wager", with(s, "amount", 50))
	s = do(t, s, "p2", "wager", with(s, "amount", 60))
	s = do(t, s, "host", "advance", dep(s))
	s = do(t, s, "host", "advance", dep(s))
	s = do(t, s, "host", "reveal", with(s, "target_id", "p1"))
	s = judge(t, s, "p1", true)
	s = do(t, s, "host", "reveal", with(s, "target_id", "p2"))
	s = judge(t, s, "p2", false)
	if NextDeadline(s) != 0 {
		t.Fatal("final results should not start next selection")
	}
	s = do(t, s, "host", "pause", EmptyPayload{})
	s = do(t, s, "host", "resume", EmptyPayload{})
	if NextDeadline(s) != 0 {
		t.Fatal("final pause created timer")
	}
}
func TestV2AuctionEqualNominalChooserCanExplicitlyGoAllIn(t *testing.T) {
	s := v2Fixture()
	s.Participants[1].Score = 100
	s.Participants[2].Score = 500
	s = begin(t, s, "stake")
	if s.Gameplay.Stage.TurnID != "p1" {
		t.Fatal("equal nominal chooser must retain bid mode choice")
	}
	s = do(t, s, "p1", "bid", with(s, "mode", "all_in"))
	bid := with(s, "mode", "bid")
	bid["amount"] = 200
	rejected(t, s, "p2", "bid", bid, 1000)
}
