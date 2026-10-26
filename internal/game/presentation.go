package game

// Presentation stages are persisted game state. Their deadlines never overlap
// the selection, wager or answer timer. Old rule snapshots keep immediate play.
func presentationStage(kind string) bool {
	switch kind {
	case "round_intro", "round_themes", "question_price", "question_type":
		return true
	}
	return false
}

func nextPresentation(s *State, now int64) {
	g := s.Gameplay
	switch g.Stage.Kind {
	case "round_intro":
		setStage(s, "round_themes", "", now, 4)
	case "question_price":
		if !ordinary(g.Question) {
			setStage(s, "question_type", "", now, 2)
		} else {
			finishPresentation(s, now)
		}
	default:
		finishPresentation(s, now)
	}
}

func finishPresentation(s *State, now int64) {
	g := s.Gameplay
	if g.Stage.Kind == "round_intro" || g.Stage.Kind == "round_themes" {
		if g.IntroReturn == nil {
			return
		}
		next := *g.IntroReturn
		g.IntroReturn = nil
		setStage(s, next.Kind, next.TurnID, now, 0)
		if next.RemainingMS > 0 {
			g.Stage.DeadlineMS = now + next.RemainingMS
		}
	} else {
		activateQuestion(s, now)
	}
}
