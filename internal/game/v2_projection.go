package game

import (
	"slices"
	"strings"
)

func ProjectAt(s State, id string, nowMS int64) View {
	if _, ok := s.ActiveParticipant(id); !ok {
		return View{ID: s.ID, Seq: s.Seq, Phase: s.Phase, You: id}
	}
	if s.Gameplay == nil {
		return projectLegacy(s, id)
	}
	n := s.Clone()
	g := n.Gameplay
	p, _ := n.Participant(id)
	host := p.Role == Host
	v := View{Settings: n.Settings.Normalized(), Appearance: n.Appearance, ID: n.ID, Seq: n.Seq, Phase: n.Phase, Participants: n.Participants, Winner: n.Winner, You: id}
	gv := &SessionView{Version: 2, RulesVersion: g.RulesVersion, Rules: g.Rules, ChooserID: g.ChooserID, ServerTimeMS: nowMS, Stage: g.Stage, Players: []PlayerView{}, Submissions: []SubmissionView{}, Decisions: []Decision{}, Appeals: []Appeal{}, AllowedActions: allowedActions(n, id, nowMS), Readiness: []Readiness{}}
	v.Gameplay = gv
	if gv.ChooserID == "" {
		ids := playerIDs(n)
		if len(ids) > 0 {
			gv.ChooserID = ids[0]
		}
	}
	if g.Round != nil {
		gv.Round = &RoundView{ID: g.Round.ID, Name: g.Round.Name, Type: g.Round.Type}
	}
	if g.Final != nil {
		f := g.Final
		gv.Final = &FinalView{Themes: []ThemeView{}, EligibleIDs: slices.Clone(f.EligibleIDs), Order: slices.Clone(f.Order)}
		for _, t := range f.Themes {
			gv.Final.Themes = append(gv.Final.Themes, ThemeView{ID: t.ID, Name: t.Name})
		}
	}
	q := g.Question
	if q != nil {
		qv := &QuestionView{ID: q.Spec.ID, InstanceID: q.InstanceID, Type: q.Spec.Type, ContentIndex: q.ContentIndex, ContentTotal: len(q.Spec.Question), AnswerIndex: q.AnswerIndex, AnswerTotal: len(q.Spec.Answer)}
		gv.Question = qv
		if m, ok := CurrentMedia(n, id); ok {
			c := g.Content
			qv.Media = &MediaView{ID: m.ID, Kind: m.Kind, DurationMS: m.DurationMS, Width: m.Width, Height: m.Height, Standard: MediaVariantView{MIME: m.Standard.MIME, Size: m.Standard.Size}, Economy: MediaVariantView{MIME: m.Economy.MIME, Size: m.Economy.Size}, StartAtMS: c.StartAtMS, PausedPositionMS: c.PausedPositionMS}
			step, _ := currentContentStep(n)
			qv.Media.PlaybackEndPositionMS = playbackEndPosition(step)
			qv.Media.StoppedPositionMS = c.StoppedPositionMS
			gv.Readiness = append(gv.Readiness, c.Readiness...)
		}
		if q.Spec.Type != "secret" || q.RecipientID != "" {
			qv.Theme = q.Spec.Theme
			qv.Price = q.Price
		}
		if q.Shown {
			qv.Text = visibleText(q.Spec.Question, q.ContentIndex, host)
			if host || q.AnswerRevealed {
				answer := strings.Join(q.Spec.Right, " / ")
				explanation := visibleText(q.Spec.Answer, q.AnswerIndex, host)
				if explanation != "" {
					if answer != "" {
						answer += "\n"
					}
					answer += explanation
				}
				qv.Answer = &answer
			}
		}
		for _, sub := range q.Players {
			gv.Players = append(gv.Players, PlayerView{ID: sub.ID, WagerSubmitted: sub.Wager != nil, AnswerSubmitted: sub.Answer != nil, Judged: sub.Correct != nil, BlockedUntilMS: sub.BlockedUntilMS, Attempted: sub.Attempted})
			if sub.ID == id {
				gv.Self = SelfView{Wager: sub.Wager, Answer: sub.Answer}
			}
			sv := SubmissionView{ID: sub.ID, Revealed: sub.Revealed, Correct: sub.Correct}
			if sub.Revealed || sub.ID == id {
				sv.Wager = sub.Wager
			}
			if sub.Revealed || sub.ID == id || host && q.CollectionClosed {
				sv.Answer = sub.Answer
			}
			gv.Submissions = append(gv.Submissions, sv)
		}
		if q.Auction != nil {
			a := q.Auction
			gv.Auction = &AuctionView{LeaderID: a.LeaderID, Amount: a.Amount, AllIn: a.AllIn, ActiveIDs: slices.Clone(a.ActiveIDs)}
		}
	}
	start := max(0, len(g.Decisions)-20)
	gv.Decisions = append(gv.Decisions, g.Decisions[start:]...)
	start = max(0, len(g.Appeals)-20)
	gv.Appeals = append(gv.Appeals, g.Appeals[start:]...)
	for i := range gv.Decisions {
		gv.Decisions[i].Reason = displayReason(gv.Decisions[i].Reason)
	}
	for i := range gv.Appeals {
		gv.Appeals[i].Reason = displayReason(gv.Appeals[i].Reason)
		gv.Appeals[i].Resolution = displayReason(gv.Appeals[i].Resolution)
	}
	return v
}
func visibleText(steps []TextStep, last int, host bool) string {
	lines := []string{}
	for i, st := range steps {
		if i > last {
			break
		}
		if (host || st.Placement != "replic") && (st.Text != "" || st.Media == nil) {
			lines = append(lines, st.Text)
		}
	}
	return strings.Join(lines, "\n")
}
func intPtr(v int64) *int64 { return &v }
func allowedActions(s State, id string, now int64) []AllowedAction {
	out := []AllowedAction{}
	g := s.Gameplay
	if g == nil || s.Phase == Finished {
		return out
	}
	actor, exists := s.ActiveParticipant(id)
	if !exists {
		return out
	}
	host := actor.Role == Host
	q := g.Question
	kind := g.Stage.Kind
	expired := deadlinePassed(g, now)
	add := func(t string, targets ...string) {
		out = append(out, AllowedAction{Type: t, Targets: targets, RequiresSeq: t != "buzz" && t != "answer" && t != "wager" && t != "media_ready"})
	}
	bounded := func(t string, minimum, maximum, step int64) {
		out = append(out, AllowedAction{Type: t, Minimum: intPtr(minimum), Maximum: intPtr(maximum), Step: intPtr(step), RequiresSeq: t != "wager"})
	}
	if host {
		targets := []string{}
		for _, p := range s.Participants {
			if !p.Removed && p.Role != Host {
				targets = append(targets, p.ID)
			}
		}
		if len(targets) > 0 {
			add("remove_participant", targets...)
		}
	}
	if s.Phase == Paused {
		if host {
			add("set_appearance")
			add("set_room_settings")
			add("resume")
			add("finish")
			targets := []string{}
			for _, p := range s.Participants {
				if p.Role == Spectator && !p.Removed {
					targets = append(targets, p.ID)
				}
			}
			if len(targets) > 0 {
				add("transfer_host", targets...)
			}
		}
		return out
	}
	if host {
		if s.Phase == Lobby {
			add("set_appearance")
			add("set_room_settings")
		}
		add("finish")
		if s.Phase != Lobby {
			add("pause")
		}
		if kind != "buzz_window" && kind != "prepare_content" && kind != "scheduled_content" && (g.Stage.DeadlineMS > 0 || g.Stage.Expired) {
			bounded("extend", 5, 120, 1)
		}
		if kind != "resolved" && (q == nil || !q.Judged) {
			add("restart_stage")
		}
	}
	chooser := g.ChooserID
	if chooser == "" {
		ids := playerIDs(s)
		if len(ids) > 0 {
			chooser = ids[0]
		}
	}
	if betweenQuestions(s) {
		if host {
			add("set_rules")
			add("begin_round")
			if !wagered(q) {
				targets := playerIDs(s)
				add("adjust_score", targets...)
			} else if kind == "resolved" {
				add("adjust_score", playerIDs(s)...)
			}
		}
		if (host || id == chooser && !expired) && g.Final == nil && g.Round != nil {
			add("begin_question")
		}
	}
	if kind == "select_final" && host && g.Final != nil && len(g.Final.Themes) == 1 {
		add("begin_question", g.Final.Themes[0].QuestionID)
	}
	if kind == "eliminate_theme" && (host || id == g.Stage.TurnID && !expired) {
		targets := []string{}
		for _, t := range g.Final.Themes {
			targets = append(targets, t.ID)
		}
		add("eliminate_theme", targets...)
	}
	if actor.Role == Player {
		targets := []string{}
		for _, d := range g.Decisions {
			if !stringsJudgement(d.ID) {
				continue
			}
			duplicate := slices.ContainsFunc(g.Appeals, func(a Appeal) bool { return a.ParticipantID == id && a.JudgementID == d.ID })
			if !duplicate {
				targets = append(targets, d.ID)
			}
		}
		if len(targets) > 0 {
			add("appeal", targets...)
		}
	}
	if host {
		targets := []string{}
		for _, a := range g.Appeals {
			if !a.Resolved {
				targets = append(targets, a.ID)
			}
		}
		if len(targets) > 0 {
			add("resolve_appeal", targets...)
		}
		targets = nil
		for _, d := range g.Decisions {
			if isCorrectable(g, d) {
				targets = append(targets, d.ID)
			}
		}
		if len(targets) > 0 {
			add("correct_judgement", targets...)
		}
	}
	if q == nil {
		return out
	}
	sub := submission(q, id)
	switch kind {
	case "prepare_content", "scheduled_content":
		if mediaReadinessActor(s, id) && (kind == "prepare_content" || !expired) {
			add("media_ready")
		}
		if host && kind == "prepare_content" && expired {
			add("wait_media")
			add("continue_media")
			add("skip_question")
		}
	case "select_recipient":
		if host || id == chooser && !expired {
			targets := []string{}
			for _, p := range q.Players {
				if q.Spec.SelectionMode != "exceptCurrent" || p.ID != chooser {
					targets = append(targets, p.ID)
				}
			}
			add("select_recipient", targets...)
		}
	case "select_price":
		if host || id == q.RecipientID && !expired {
			r := secretRange(s)
			bounded("select_price", r.Minimum, r.Maximum, r.Step)
		}
	case "auction":
		if !expired && (host || id == g.Stage.TurnID) {
			a := q.Auction
			p, _ := s.Participant(g.Stage.TurnID)
			minimum := auctionMinimum(q)
			modes := []string{}
			if a.LeaderID != "" {
				modes = append(modes, "pass")
			}
			if p.Score > a.Amount && p.Score >= q.Price {
				modes = append(modes, "all_in")
			}
			if !a.AllIn && p.Score >= minimum {
				modes = append(modes, "bid")
			}
			out = append(out, AllowedAction{Type: "bid", Minimum: intPtr(minimum), Maximum: intPtr(p.Score), Step: intPtr(a.Step), Modes: modes, RequiresSeq: true})
		}
	case "collect_wagers":
		if !expired && sub != nil && sub.Wager == nil {
			bounded("wager", 1, sub.Maximum, 1)
		}
	case "present_answer":
		if host {
			add("advance")
		}
	case "present_question":
		if host {
			add("advance")
			if ordinary(q) {
				add("open_buzzer")
			}
		}
		if ordinary(q) && sub != nil && !sub.Attempted && sub.BlockedUntilMS <= now && q.Rules.FalseStart && !expired {
			add("buzz")
		}
	case "buzz_open", "buzz_window":
		if !expired && sub != nil && !sub.Attempted && sub.BlockedUntilMS <= now && !slices.Contains(q.Candidates, id) {
			add("buzz")
		}
		if host && kind == "buzz_open" {
			add("reveal")
		}
	case "collect_answer", "collect_answers":
		if !expired && sub != nil && sub.Answer == nil && (kind == "collect_answers" || g.Stage.TurnID == id) {
			add("answer")
		}
		if host {
			add("advance")
			if kind == "collect_answer" {
				add("judge", g.Stage.TurnID)
			}
		}
	case "judge":
		if host {
			targets := []string{}
			for _, p := range q.Players {
				if p.Correct == nil && (multi(q) || p.ID == s.Winner) && (q.Spec.Type != "stakeAll" || p.Revealed) {
					targets = append(targets, p.ID)
				}
			}
			if len(targets) > 0 {
				add("judge", targets...)
			}
			if q.Spec.Type != "stakeAll" {
				add("reveal")
			}
		}
	case "reveal":
		if host {
			add("reveal", g.Stage.TurnID)
		}
	case "resolved":
		if host && q.Shown && !q.AnswerRevealed && q.Spec.Type != "stakeAll" {
			add("reveal")
		}
	}
	return out
}
