package game

import "unicode/utf8"

func startReveal(s *State, start int64) {
	g := s.Gameplay
	q := g.Question
	if q == nil || !q.Rules.ProgressiveReveal || g.Stage.Kind != "present_question" || q.ContentIndex < 0 || q.ContentIndex >= len(q.Spec.Question) {
		return
	}
	step := q.Spec.Question[q.ContentIndex]
	if step.Placement == "replic" || step.Media != nil && step.Media.Kind != "image" || step.Media == nil && step.Text == "" {
		return
	}
	duration := g.Stage.DeadlineMS - start
	if duration <= 0 {
		duration = int64(q.Rules.ReadingSeconds) * 1000
	}
	if duration <= 0 {
		duration = 10000
	}
	g.Reveal = &RevealState{StartAtMS: start, DurationMS: min(duration, 120000)}
}

func projectReveal(s State, host bool, now int64) *RevealView {
	g := s.Gameplay
	q := g.Question
	if g.Reveal == nil || q == nil || !q.Rules.ProgressiveReveal || g.Stage.Kind != "present_question" || s.Phase == Finished {
		return nil
	}
	step := q.Spec.Question[q.ContentIndex]
	if step.Placement == "replic" {
		return nil
	}
	if s.Phase == Paused {
		now = g.PausedAtMS
	}
	r := &RevealView{Kind: "text", StartAtMS: g.Reveal.StartAtMS, DurationMS: g.Reveal.DurationMS, ElapsedMS: min(g.Reveal.DurationMS, max(int64(0), now-g.Reveal.StartAtMS))}
	if step.Media != nil {
		if step.Media.Kind != "image" {
			return nil
		}
		r.Kind = "image"
	} else {
		// Taking the difference includes exactly the public separating newline,
		// even when preceding fragments include empty text or private replicas.
		r.TextOffset = utf8.RuneCountInString(visibleText(q.Spec.Question, q.ContentIndex, host)) - utf8.RuneCountInString(step.Text)
	}
	return r
}
