package game

func DefaultRules() Rules {
	return Rules{Preset: "classic", SelectionSeconds: 30, AnswerSeconds: 10, SpecialSeconds: 30, WagerSeconds: 30, FinalSeconds: 60, Penalties: true, BuzzerMode: "first", WindowMS: 200, FalseStart: true, FalseStartSeconds: 3, NoRiskMultiplier: 2}
}
func (r Rules) Validate() error {
	if r.ReadingSeconds < 0 || r.ReadingSeconds > 120 || r.AutoOpen && r.ReadingSeconds < 1 {
		return Fault("invalid_rules")
	}
	if r.Preset != "classic" && r.Preset != "friends" && r.Preset != "custom" {
		return Fault("invalid_rules")
	}
	for _, n := range []int{r.SelectionSeconds, r.AnswerSeconds, r.SpecialSeconds, r.WagerSeconds, r.FinalSeconds} {
		if n < 5 || n > 120 {
			return Fault("invalid_rules")
		}
	}
	if r.BuzzerMode != "first" && r.BuzzerMode != "random" || r.WindowMS < 100 || r.WindowMS > 300 || r.FalseStartSeconds < 1 || r.FalseStartSeconds > 5 || r.NoRiskMultiplier != 2 {
		return Fault("invalid_rules")
	}
	return nil
}
func NewSession(rules Rules) *Session {
	return &Session{Version: 2, RulesVersion: 1, Rules: rules, Stage: Stage{ID: "stage-1", Kind: "select_question"}, Counter: 1, Decisions: []Decision{}, Appeals: []Appeal{}}
}
func betweenQuestions(s State) bool {
	if s.Gameplay == nil {
		return s.Phase == Lobby || s.Phase == Resolved
	}
	return s.Phase != Finished && (s.Gameplay.Stage.Kind == "select_question" || s.Gameplay.Stage.Kind == "resolved")
}
