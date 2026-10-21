package game

import (
	"fmt"
	"slices"
	"strings"
)

func (g *Session) nextID(prefix string) string {
	g.Counter++
	return fmt.Sprintf("%s-%d", prefix, g.Counter)
}
func setStage(s *State, kind, turn string, now int64, seconds int) {
	g := s.Gameplay
	st := Stage{ID: g.nextID("stage"), Kind: kind, TurnID: turn}
	if g.Question != nil {
		st.InstanceID = g.Question.InstanceID
	}
	if seconds > 0 {
		st.DeadlineMS = now + int64(seconds)*1000
	}
	g.Stage = st
	if s.Phase != Paused {
		s.Phase = Running
	}
}
func playerIDs(s State) []string {
	ids := []string{}
	for _, p := range s.Participants {
		if p.Role == Player {
			ids = append(ids, p.ID)
		}
	}
	return ids
}
func submission(q *ActiveQuestion, id string) *Submission {
	if q != nil {
		for i := range q.Players {
			if q.Players[i].ID == id {
				return &q.Players[i]
			}
		}
	}
	return nil
}
func ordinary(q *ActiveQuestion) bool {
	return q != nil && (q.Spec.Type == "withButton" || q.Spec.Type == "simple")
}
func multi(q *ActiveQuestion) bool {
	return q != nil && (q.Spec.Type == "forAll" || q.Spec.Type == "stakeAll")
}
func wagered(q *ActiveQuestion) bool {
	if q == nil {
		return false
	}
	if q.Auction != nil && q.Auction.LeaderID != "" {
		return true
	}
	for _, p := range q.Players {
		if p.Wager != nil {
			return true
		}
	}
	return false
}
func validateQuestion(q QuestionSpec) error {
	if !serializedTextFits(q.Theme, 256) {
		return Fault("invalid_question")
	}
	if !ValidID(q.ID) || !ValidID(q.RoundID) || !ValidID(q.ThemeID) || q.Price < 0 || q.Price > 100000 || len(q.Question) == 0 || q.AnswerSeconds != 0 && (q.AnswerSeconds < 5 || q.AnswerSeconds > 120) {
		return Fault("invalid_question")
	}
	switch q.Type {
	case "simple", "withButton", "stake", "stakeAll", "secret", "forAll", "noRisk":
	default:
		return Fault("unsupported_question")
	}
	if q.SelectionMode != "" && q.SelectionMode != "any" && q.SelectionMode != "exceptCurrent" {
		return Fault("invalid_question")
	}
	if q.PriceRange != nil && (q.Type != "secret" || q.PriceRange.Minimum < 0 || q.PriceRange.Maximum < q.PriceRange.Minimum || q.PriceRange.Maximum > 100000 || q.PriceRange.Step < 0) {
		return Fault("invalid_question")
	}
	questionBytes, answerBytes := 0, 0
	questionMedia, answerMedia := 0, 0
	seenMedia := map[string]bool{}
	for i, steps := range [][]TextStep{q.Question, q.Answer} {
		for _, st := range steps {
			if st.DurationMS < 0 || st.DurationMS > 600000 || st.Placement != "" && st.Placement != "screen" && st.Placement != "replic" && !(st.Media != nil && st.Placement == "background") {
				return Fault("invalid_question")
			}
			if st.Media != nil {
				if st.Media.Validate() != nil || seenMedia[st.Media.ID] {
					return Fault("invalid_question")
				}
				seenMedia[st.Media.ID] = true
				if i == 0 {
					questionMedia++
				} else {
					answerMedia++
				}
			}
			if i == 0 {
				questionBytes += len(st.Text)
			} else {
				answerBytes += len(st.Text)
			}
		}
	}
	questionTexts, answerTexts := []string{}, []string{}
	for _, step := range q.Question {
		questionTexts = append(questionTexts, step.Text)
	}
	for _, step := range q.Answer {
		answerTexts = append(answerTexts, step.Text)
	}
	if !serializedTextFits(strings.Join(questionTexts, "\n"), 4096) || !serializedTextFits(strings.Join(answerTexts, "\n"), 4096) || !serializedTextFits(strings.Join(q.Right, " / "), 4096) {
		return Fault("invalid_question")
	}
	if questionBytes == 0 && questionMedia == 0 || questionBytes > 4096 || answerBytes > 4096 || len(strings.Join(q.Right, " / ")) > 4096 || strings.TrimSpace(strings.Join(q.Right, "")) == "" && answerBytes == 0 && answerMedia == 0 {
		return Fault("invalid_question")
	}
	return nil
}
func beginQuestion(s *State, q QuestionSpec, replay bool, now int64) error {
	g := s.Gameplay
	if g.Round == nil {
		return Fault("round_required")
	}
	if q.Type == "secret" && q.SelectionMode == "" {
		q.SelectionMode = "exceptCurrent"
	}
	if err := validateQuestion(q); err != nil {
		return err
	}
	if err := g.Rules.Validate(); err != nil {
		return err
	}
	if !betweenQuestions(*s) && g.Stage.Kind != "select_final" {
		return Fault("invalid_phase")
	}
	ids := playerIDs(*s)
	if len(ids) < 2 {
		return Fault("not_enough_players")
	}
	if len(s.Asked) >= 2000 {
		return Fault("question_limit")
	}
	if slices.Contains(s.Asked, q.ID) && !replay {
		return Fault("question_reused")
	}
	if g.Round != nil && g.Round.ID != q.RoundID {
		return Fault("wrong_round")
	}
	if g.Final != nil {
		if g.Stage.Kind != "select_final" || len(g.Final.Themes) != 1 || g.Final.Themes[0].QuestionID != q.ID || g.Final.Themes[0].ID != q.ThemeID || q.Type != "stakeAll" {
			return Fault("invalid_final_question")
		}
		ids = slices.Clone(g.Final.EligibleIDs)
	}
	if g.ChooserID == "" {
		g.ChooserID = ids[0]
	}
	a := &ActiveQuestion{Spec: q, InstanceID: g.nextID("question"), Rules: g.Rules, RulesVersion: g.RulesVersion, Price: q.Price, ContentIndex: -1, AnswerIndex: -1, ChooserBefore: g.ChooserID, Players: []Submission{}}
	for _, id := range ids {
		p, _ := s.Participant(id)
		if q.Type == "stakeAll" && p.Score <= 0 {
			continue
		}
		a.Players = append(a.Players, Submission{ID: id, Maximum: p.Score})
	}
	g.Question = a
	g.Content = nil
	s.Question = nil
	s.Winner = ""
	s.Asked = append(s.Asked, q.ID)
	switch q.Type {
	case "secret":
		setStage(s, "select_recipient", g.ChooserID, now, g.Rules.SelectionSeconds)
	case "stake":
		startAuction(s, now)
	case "stakeAll":
		if len(a.Players) == 0 {
			resolveQuestion(s, now, false)
		} else {
			setStage(s, "collect_wagers", "", now, a.Rules.WagerSeconds)
		}
	case "noRisk":
		a.RecipientID = g.ChooserID
		present(s, now)
	default:
		present(s, now)
	}
	return nil
}
func present(s *State, now int64) {
	q := s.Gameplay.Question
	q.ContentIndex = 0
	presentStage(s, now)
}
func presentStage(s *State, now int64) {
	q := s.Gameplay.Question
	if q.Spec.Question[q.ContentIndex].Media != nil {
		prepareContent(s, "question", q.ContentIndex, "present_question", now)
		return
	}
	s.Gameplay.Content = nil
	q.Shown = true
	setStage(s, "present_question", "", now, q.Rules.SelectionSeconds)
	step := q.Spec.Question[q.ContentIndex]
	if step.DurationMS > 0 {
		s.Gameplay.Stage.DeadlineMS = now + step.DurationMS
	}
}
func answerSeconds(s State) int {
	q := s.Gameplay.Question
	if q.Spec.AnswerSeconds > 0 {
		return q.Spec.AnswerSeconds
	}
	if s.Gameplay.Final != nil {
		return q.Rules.FinalSeconds
	}
	return q.Rules.SpecialSeconds
}
func openAnswers(s *State, now int64) {
	q := s.Gameplay.Question
	if ordinary(q) {
		setStage(s, "buzz_open", "", now, answerSeconds(*s))
	} else if multi(q) {
		setStage(s, "collect_answers", "", now, answerSeconds(*s))
	} else {
		s.Winner = q.RecipientID
		setStage(s, "collect_answer", q.RecipientID, now, answerSeconds(*s))
	}
}
func advanceQuestion(s *State, now int64) error {
	g := s.Gameplay
	q := g.Question
	switch g.Stage.Kind {
	case "present_question":
		if q.ContentIndex+1 < len(q.Spec.Question) {
			q.ContentIndex++
			presentStage(s, now)
		} else {
			openAnswers(s, now)
		}
	case "present_answer":
		if q.AnswerIndex+1 < len(q.Spec.Answer) {
			q.AnswerIndex++
			presentAnswerStage(s, now)
		} else {
			seconds := q.Rules.SelectionSeconds
			if q.AfterAnswerKind == "resolved" {
				seconds = resolvedSeconds(*s)
				q.AfterAnswerTurn = s.Gameplay.ChooserID
			}
			setStage(s, q.AfterAnswerKind, q.AfterAnswerTurn, now, seconds)
		}
	case "collect_answer", "collect_answers":
		closeAnswers(s, now)
	default:
		return Fault("invalid_phase")
	}
	return nil
}
func closeAnswers(s *State, now int64) {
	q := s.Gameplay.Question
	q.CollectionClosed = true
	if q.Spec.Type == "stakeAll" {
		setStage(s, "reveal", nextUnjudged(q), now, q.Rules.SelectionSeconds)
	} else {
		setStage(s, "judge", s.Winner, now, q.Rules.SelectionSeconds)
	}
}
func nextUnjudged(q *ActiveQuestion) string {
	for _, p := range q.Players {
		if p.Correct == nil {
			return p.ID
		}
	}
	return ""
}
func resolveQuestion(s *State, now int64, reveal bool) {
	q := s.Gameplay.Question
	q.CollectionClosed = true
	if reveal && startAnswerPresentation(s, now, "resolved", s.Gameplay.ChooserID) {
		return
	}
	setStage(s, "resolved", s.Gameplay.ChooserID, now, resolvedSeconds(*s))
}
func startAnswerPresentation(s *State, now int64, after, turn string) bool {
	q := s.Gameplay.Question
	if q.AnswerRevealed {
		return false
	}
	q.AnswerRevealed = true
	if len(q.Spec.Answer) == 0 {
		return false
	}
	q.AnswerIndex = 0
	q.AfterAnswerKind = after
	q.AfterAnswerTurn = turn
	presentAnswerStage(s, now)
	return true
}
func presentAnswerStage(s *State, now int64) {
	q := s.Gameplay.Question
	if q.Spec.Answer[q.AnswerIndex].Media != nil {
		prepareContent(s, "answer", q.AnswerIndex, "present_answer", now)
		return
	}
	s.Gameplay.Content = nil
	setStage(s, "present_answer", "", now, q.Rules.SelectionSeconds)
	step := q.Spec.Answer[q.AnswerIndex]
	if step.DurationMS > 0 {
		s.Gameplay.Stage.DeadlineMS = now + step.DurationMS
	}
}
func applyBuzz(s *State, id string, now int64, entropy uint64) error {
	g := s.Gameplay
	q := g.Question
	p := submission(q, id)
	if p == nil || !ordinary(q) || p.Attempted || p.BlockedUntilMS > now {
		return Fault("cannot_buzz")
	}
	switch g.Stage.Kind {
	case "present_question":
		if !q.Rules.FalseStart {
			return Fault("buzzer_closed")
		}
		p.BlockedUntilMS = now + int64(q.Rules.FalseStartSeconds)*1000
		return nil
	case "buzz_open":
		if q.Rules.BuzzerMode == "random" {
			q.Candidates = []string{id}
			q.Seed = entropy
			g.Stage.Kind = "buzz_window"
			g.Stage.Expired = false
			g.Stage.DeadlineMS = now + int64(q.Rules.WindowMS)
		} else {
			winBuzz(s, id, now)
		}
	case "buzz_window":
		if slices.Contains(q.Candidates, id) {
			return Fault("already_submitted")
		}
		q.Candidates = append(q.Candidates, id)
	default:
		return Fault("buzzer_closed")
	}
	return nil
}
func winBuzz(s *State, id string, now int64) {
	s.Gameplay.Question.Candidates = nil
	s.Winner = id
	q := s.Gameplay.Question
	q.CollectionClosed = false
	setStage(s, "collect_answer", id, now, q.Rules.AnswerSeconds)
}
func answer(s *State, id, text string, now int64) error {
	g := s.Gameplay
	q := g.Question
	p := submission(q, id)
	if p == nil || p.Answer != nil || g.Stage.Kind != "collect_answer" && g.Stage.Kind != "collect_answers" || g.Stage.Kind == "collect_answer" && g.Stage.TurnID != id {
		return Fault("cannot_answer")
	}
	p.Answer = &text
	if g.Stage.Kind == "collect_answer" {
		closeAnswers(s, now)
	} else {
		all := true
		for _, other := range q.Players {
			all = all && other.Answer != nil
		}
		if all {
			closeAnswers(s, now)
		}
	}
	return nil
}
func scoreDelta(q *ActiveQuestion, p *Submission, correct bool) int64 {
	amount := q.Price
	if q.Spec.Type == "stakeAll" && p.Wager != nil {
		amount = *p.Wager
	}
	if correct {
		if q.Spec.Type == "noRisk" {
			return amount * 2
		}
		return amount
	}
	if q.Spec.Type == "noRisk" || !q.Rules.Penalties {
		return 0
	}
	return -amount
}
func changeScore(s *State, id string, delta int64) error {
	for i := range s.Participants {
		if s.Participants[i].ID == id && s.Participants[i].Role == Player {
			value := s.Participants[i].Score + delta
			if value < -1000000000 || value > 1000000000 {
				return Fault("score_limit")
			}
			s.Participants[i].Score = value
			return nil
		}
	}
	return Fault("invalid_target")
}
func judgeAnswer(s *State, target string, correct bool, now int64) error {
	g := s.Gameplay
	q := g.Question
	p := submission(q, target)
	if p == nil || p.Correct != nil || g.Stage.Kind != "judge" && g.Stage.Kind != "collect_answer" {
		return Fault("cannot_judge")
	}
	if !multi(q) && s.Winner != target {
		return Fault("invalid_target")
	}
	if q.Spec.Type == "stakeAll" && (!p.Revealed || g.Stage.TurnID != target) {
		return Fault("not_revealed")
	}
	delta := scoreDelta(q, p, correct)
	if err := changeScore(s, target, delta); err != nil {
		return err
	}
	p.Correct = &correct
	p.Attempted = true
	q.Judged = true
	g.Decisions = append(g.Decisions, Decision{ID: g.nextID("judgement"), ParticipantID: target, Correct: correct, Delta: delta, QuestionID: q.Spec.ID, InstanceID: q.InstanceID})
	trimHistory(g)
	if ordinary(q) {
		if correct {
			g.ChooserID = target
			resolveQuestion(s, now, true)
		} else {
			available := false
			for _, p := range q.Players {
				available = available || !p.Attempted
			}
			if available {
				s.Winner = ""
				q.CollectionClosed = false
				setStage(s, "buzz_open", "", now, answerSeconds(*s))
			} else {
				resolveQuestion(s, now, true)
			}
		}
	} else if !multi(q) {
		if q.Spec.Type == "stake" && correct {
			g.ChooserID = target
		}
		resolveQuestion(s, now, true)
	} else if nextUnjudged(q) == "" {
		resolveQuestion(s, now, q.Spec.Type != "stakeAll")
	} else if q.Spec.Type == "stakeAll" {
		setStage(s, "reveal", nextUnjudged(q), now, q.Rules.SelectionSeconds)
	}
	return nil
}
func trimHistory(g *Session) {
	if len(g.Decisions) > 64 {
		cut := len(g.Decisions) - 64
		retained := []Decision{}
		for _, d := range g.Decisions[:cut] {
			if g.Question != nil && d.InstanceID == g.Question.InstanceID && stringsJudgement(d.ID) {
				retained = append(retained, d)
			}
		}
		g.Decisions = append(retained, g.Decisions[cut:]...)
	}
	if len(g.Appeals) > 64 {
		g.Appeals = slices.Clone(g.Appeals[len(g.Appeals)-64:])
	}
}

func resolvedSeconds(s State) int {
	if s.Gameplay.Final != nil {
		return 0
	}
	return s.Gameplay.Question.Rules.SelectionSeconds
}
