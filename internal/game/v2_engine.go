package game

import "slices"

func ApplyAt(s State, actorID string, c Command, nowMS int64, entropy uint64) (State, error) {
	if s.Gameplay == nil {
		if c.Version != 1 {
			return s, Fault("invalid_version")
		}
		return applyLegacy(s, actorID, c)
	}
	p, err := c.Payload()
	if err != nil {
		return s, err
	}
	actor, ok := s.Participant(actorID)
	if !ok {
		return s, Fault("unauthorized")
	}
	if s.Phase == Finished {
		return s, Fault("finished")
	}
	if c.ExpectedSeq != nil && *c.ExpectedSeq != s.Seq && c.Type != "buzz" && c.Type != "answer" && c.Type != "wager" {
		return s, Fault("stale_state")
	}
	allowed := false
	var target string
	switch x := p.(type) {
	case *RecipientPayload:
		target = x.TargetID
	case *EliminatePayload:
		target = x.ThemeID
	case *JudgePayload:
		target = x.TargetID
	case *CorrectionPayload:
		target = x.JudgementID
	case *AdjustPayload:
		target = x.TargetID
	case *AppealPayload:
		target = x.JudgementID
	case *ResolveAppealPayload:
		target = x.AppealID
	case *TransferPayload:
		target = x.TargetID
	case *RevealPayload:
		target = x.TargetID
	}
	for _, a := range allowedActions(s, actorID, nowMS) {
		if a.Type == c.Type {
			allowed = target == "" || len(a.Targets) == 0 || slices.Contains(a.Targets, target)
			break
		}
	}
	if !allowed {
		return s, Fault("forbidden")
	}
	var stage, instance string
	switch x := p.(type) {
	case *StagePayload:
		stage, instance = x.StageID, x.InstanceID
	case *ExtendPayload:
		stage = x.StageID
	case *RestartPayload:
		stage = x.StageID
	case *AnswerPayload:
		stage, instance = x.StageID, x.InstanceID
	case *AmountPayload:
		stage, instance = x.StageID, x.InstanceID
	case *BidPayload:
		stage, instance = x.StageID, x.InstanceID
	case *RecipientPayload:
		stage, instance = x.StageID, x.InstanceID
	case *EliminatePayload:
		stage = x.StageID
	case *RevealPayload:
		stage, instance = x.StageID, x.InstanceID
	case *JudgePayload:
		stage, instance = x.StageID, x.InstanceID
	}
	if stage != "" && stage != s.Gameplay.Stage.ID {
		return s, Fault("stale_stage")
	}
	if instance != "" && instance != s.Gameplay.Stage.InstanceID {
		return s, Fault("stale_question")
	}
	n := s.Clone()
	g := n.Gameplay
	switch x := p.(type) {
	case *BeginRoundPayload:
		err = beginRound(&n, x.Round, nowMS)
	case *BeginQuestionPayload:
		err = beginQuestion(&n, x.Question, x.Replay, nowMS)
	case *RulesPayload:
		if err = x.Rules.Validate(); err == nil {
			g.Rules = x.Rules
			g.RulesVersion++
		}
	case *ExtendPayload:
		g.Stage.Expired = false
		g.Stage.DeadlineMS = nowMS + int64(x.Seconds)*1000
	case *RestartPayload:
		err = restartStage(&n, nowMS)
	case *StagePayload:
		switch c.Type {
		case "buzz":
			err = applyBuzz(&n, actorID, nowMS, entropy)
		case "advance":
			err = advanceQuestion(&n, nowMS)
		case "open_buzzer":
			if !ordinary(g.Question) || g.Stage.Kind != "present_question" {
				err = Fault("invalid_phase")
			} else {
				openAnswers(&n, nowMS)
			}
		}
	case *AnswerPayload:
		err = answer(&n, actorID, x.Text, nowMS)
	case *AmountPayload:
		if c.Type == "wager" {
			err = submitWager(&n, actorID, *x.Amount, nowMS)
		} else {
			err = choosePrice(&n, *x.Amount, nowMS)
		}
	case *BidPayload:
		id := actorID
		if actor.Role == Host {
			id = g.Stage.TurnID
		}
		err = auctionBid(&n, id, x.Mode, x.Amount, nowMS)
	case *RecipientPayload:
		err = chooseRecipient(&n, x.TargetID, nowMS)
	case *EliminatePayload:
		err = eliminateTheme(&n, x.ThemeID, nowMS)
	case *RevealPayload:
		err = reveal(&n, x.TargetID, nowMS)
	case *JudgePayload:
		err = judgeAnswer(&n, x.TargetID, *x.Correct, nowMS)
	case *CorrectionPayload:
		err = correctJudgement(&n, x)
	case *AdjustPayload:
		err = changeScore(&n, x.TargetID, *x.Delta)
		if err == nil {
			qid, instance := "", ""
			if g.Question != nil {
				qid = g.Question.Spec.ID
				instance = g.Question.InstanceID
			}
			g.Decisions = append(g.Decisions, Decision{ID: g.nextID("adjustment"), ParticipantID: x.TargetID, Delta: *x.Delta, Reason: x.Reason, QuestionID: qid, InstanceID: instance})
			trimHistory(g)
		}
	case *AppealPayload:
		found := false
		for _, d := range g.Decisions {
			if d.ID == x.JudgementID && d.Supersedes == "" {
				found = true
			}
		}
		if !found {
			err = Fault("invalid_judgement")
			break
		}
		for _, a := range g.Appeals {
			if a.JudgementID == x.JudgementID && a.ParticipantID == actorID {
				err = Fault("already_submitted")
			}
		}
		if err == nil {
			g.Appeals = append(g.Appeals, Appeal{ID: g.nextID("appeal"), JudgementID: x.JudgementID, ParticipantID: actorID, Reason: x.Reason})
			trimHistory(g)
		}
	case *ResolveAppealPayload:
		found := false
		for i := range g.Appeals {
			if g.Appeals[i].ID == x.AppealID && !g.Appeals[i].Resolved {
				g.Appeals[i].Resolved = true
				g.Appeals[i].Resolution = x.Reason
				found = true
			}
		}
		if !found {
			err = Fault("invalid_appeal")
		}
	case *TransferPayload:
		target, exists := n.Participant(x.TargetID)
		if !exists || target.Role != Spectator {
			err = Fault("invalid_target")
			break
		}
		for i := range n.Participants {
			if n.Participants[i].ID == actorID {
				n.Participants[i].Role = Spectator
			}
			if n.Participants[i].ID == x.TargetID {
				n.Participants[i].Role = Host
			}
		}
	case *EmptyPayload:
		switch c.Type {
		case "pause":
			var changed bool
			n, changed = PauseAt(n, nowMS)
			if !changed {
				err = Fault("invalid_phase")
			}
		case "resume":
			n.Phase = Running
			n.ResumePhase = ""
			if g.Question != nil && g.PausedAtMS > 0 {
				for i := range g.Question.Players {
					p := &g.Question.Players[i]
					if p.BlockedUntilMS > g.PausedAtMS {
						p.BlockedUntilMS = nowMS + p.BlockedUntilMS - g.PausedAtMS
					} else {
						p.BlockedUntilMS = 0
					}
				}
			}
			g.PausedAtMS = 0
			if !g.Stage.Expired && g.PausedTimer {
				g.Stage.DeadlineMS = nowMS + g.Stage.RemainingMS
			}
			g.Stage.RemainingMS = 0
			g.PausedTimer = false
		case "finish":
			n.Phase = Finished
			n.ResumePhase = ""
			g.Stage.DeadlineMS = 0
			g.Stage.RemainingMS = 0
		}
	}
	if err != nil {
		return s, err
	}
	return n, nil
}
func PauseAt(s State, nowMS int64) (State, bool) {
	if s.Gameplay == nil {
		return pauseLegacy(s)
	}
	if s.Phase == Paused || s.Phase == Finished || s.Phase == Lobby {
		return s, false
	}
	n := s.Clone()
	g := n.Gameplay
	n.ResumePhase = n.Phase
	n.Phase = Paused
	g.PausedAtMS = nowMS
	g.Stage.RemainingMS = 0
	g.PausedTimer = g.Stage.DeadlineMS > 0
	if g.Stage.DeadlineMS > nowMS {
		g.Stage.RemainingMS = g.Stage.DeadlineMS - nowMS
	}
	g.Stage.DeadlineMS = 0
	return n, true
}
func NextDeadline(s State) int64 {
	if s.Gameplay == nil || s.Phase == Paused || s.Phase == Finished || s.Gameplay.Stage.Expired {
		return 0
	}
	deadline := s.Gameplay.Stage.DeadlineMS
	if q := s.Gameplay.Question; q != nil {
		for _, p := range q.Players {
			if p.BlockedUntilMS > 0 && (deadline == 0 || p.BlockedUntilMS < deadline) {
				deadline = p.BlockedUntilMS
			}
		}
	}
	return deadline
}
func AdvanceAt(s State, nowMS int64) (State, bool) {
	deadline := NextDeadline(s)
	if deadline == 0 || nowMS < deadline {
		return s, false
	}
	n := s.Clone()
	g := n.Gameplay
	q := g.Question
	// Permission changes need a persisted tick and fresh projection, even while
	// the question's own deadline is still in the future.
	if q != nil {
		for i := range q.Players {
			if p := &q.Players[i]; p.BlockedUntilMS > 0 && p.BlockedUntilMS <= nowMS {
				p.BlockedUntilMS = 0
			}
		}
	}
	if g.Stage.DeadlineMS == 0 || nowMS < g.Stage.DeadlineMS {
		return n, true
	}
	switch g.Stage.Kind {
	case "buzz_window":
		if q != nil && len(q.Candidates) > 0 {
			winBuzz(&n, q.Candidates[q.Seed%uint64(len(q.Candidates))], nowMS)
		} else {
			g.Stage.Expired = true
			g.Stage.DeadlineMS = 0
		}
	case "auction":
		if q.Auction.LeaderID == "" {
			acceptBid(&n, g.Stage.TurnID, q.Price, false, nowMS)
		} else {
			_ = auctionBid(&n, g.Stage.TurnID, "pass", nil, nowMS)
		}
	case "collect_wagers":
		closeWagers(&n, nowMS)
	case "collect_answer", "collect_answers":
		closeAnswers(&n, nowMS)
	case "present_question", "present_answer":
		var st TextStep
		if g.Stage.Kind == "present_answer" {
			st = q.Spec.Answer[q.AnswerIndex]
		} else {
			st = q.Spec.Question[q.ContentIndex]
		}
		if st.DurationMS > 0 && !st.WaitForFinish {
			_ = advanceQuestion(&n, nowMS)
		} else {
			g.Stage.Expired = true
			g.Stage.DeadlineMS = 0
		}
	default:
		g.Stage.Expired = true
		g.Stage.DeadlineMS = 0
	}
	return n, true
}
func restartStage(s *State, now int64) error {
	g := s.Gameplay
	q := g.Question
	if q != nil && q.Judged {
		return Fault("already_judged")
	}
	kind, turn := g.Stage.Kind, g.Stage.TurnID
	if q != nil {
		switch kind {
		case "collect_wagers":
			for i := range q.Players {
				p, _ := s.Participant(q.Players[i].ID)
				q.Players[i].Wager = nil
				q.Players[i].Maximum = p.Score
			}
			setStage(s, kind, "", now, q.Rules.WagerSeconds)
		case "collect_answer", "collect_answers":
			for i := range q.Players {
				q.Players[i].Answer = nil
				q.Players[i].Correct = nil
				q.Players[i].Revealed = false
			}
			q.CollectionClosed = false
			if multi(q) {
				setStage(s, "collect_answers", "", now, answerSeconds(*s))
			} else {
				seconds := answerSeconds(*s)
				if ordinary(q) {
					seconds = q.Rules.AnswerSeconds
				}
				setStage(s, "collect_answer", s.Winner, now, seconds)
			}
		case "judge", "reveal":
			setStage(s, kind, turn, now, q.Rules.SelectionSeconds)
		case "auction":
			for i := range q.Players {
				p, _ := s.Participant(q.Players[i].ID)
				q.Players[i].Maximum = p.Score
			}
			startAuction(s, now)
		case "buzz_open", "buzz_window":
			q.Candidates = nil
			q.Seed = 0
			setStage(s, "buzz_open", "", now, answerSeconds(*s))
		case "present_question":
			presentStage(s, now)
		case "present_answer":
			presentAnswerStage(s, now)
		case "select_recipient", "select_price":
			setStage(s, kind, turn, now, q.Rules.SelectionSeconds)
		default:
			return Fault("invalid_phase")
		}
	} else {
		if kind != "eliminate_theme" && kind != "select_question" && kind != "select_final" {
			return Fault("invalid_phase")
		}
		setStage(s, kind, turn, now, g.Rules.SelectionSeconds)
	}
	return nil
}
func correctJudgement(s *State, p *CorrectionPayload) error {
	g := s.Gameplay
	q := g.Question
	if q == nil || g.Stage.Kind != "resolved" {
		return Fault("dependent_question")
	}
	if slices.Contains(q.CorrectedIDs, p.JudgementID) {
		return Fault("already_corrected")
	}
	var old *Decision
	for i := range g.Decisions {
		d := &g.Decisions[i]
		if d.ID == p.JudgementID {
			old = d
		}
		if d.Supersedes == p.JudgementID {
			return Fault("already_corrected")
		}
	}
	if old == nil || old.InstanceID != q.InstanceID || old.Supersedes != "" || old.Correct == *p.Correct {
		return Fault("invalid_judgement")
	}
	sub := submission(q, old.ParticipantID)
	if sub == nil {
		return Fault("invalid_judgement")
	}
	delta := scoreDelta(q, sub, *p.Correct) - old.Delta
	if err := changeScore(s, old.ParticipantID, delta); err != nil {
		return err
	}
	sub.Correct = p.Correct
	q.CorrectedIDs = append(q.CorrectedIDs, p.JudgementID)
	g.Decisions = append(g.Decisions, Decision{ID: g.nextID("correction"), ParticipantID: old.ParticipantID, Correct: *p.Correct, Delta: delta, Reason: p.Reason, QuestionID: q.Spec.ID, InstanceID: q.InstanceID, Supersedes: p.JudgementID})
	if ordinary(q) || q.Spec.Type == "stake" {
		if *p.Correct {
			g.ChooserID = old.ParticipantID
		} else if g.ChooserID == old.ParticipantID {
			g.ChooserID = q.ChooserBefore
		}
	}
	trimHistory(g)
	return nil
}
func deadlinePassed(g *Session, now int64) bool {
	return g.Stage.Expired || g.Stage.DeadlineMS > 0 && now >= g.Stage.DeadlineMS
}
func isCorrectable(g *Session, d Decision) bool {
	if g.Question == nil || g.Stage.Kind != "resolved" || d.InstanceID != g.Question.InstanceID || d.Supersedes != "" || !stringsJudgement(d.ID) {
		return false
	}
	if slices.Contains(g.Question.CorrectedIDs, d.ID) {
		return false
	}
	return !slices.ContainsFunc(g.Decisions, func(other Decision) bool { return other.Supersedes == d.ID })
}
func stringsJudgement(id string) bool { return len(id) > 10 && id[:10] == "judgement-" }
