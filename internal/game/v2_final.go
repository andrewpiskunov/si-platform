package game

import (
	"encoding/json"
	"slices"
)

func beginRound(s *State, r RoundSpec, now int64) error {
	g := s.Gameplay
	if !serializedTextFits(r.Name, 256) {
		return Fault("invalid_round")
	}
	themeViews := []ThemeView{}
	for _, t := range r.Themes {
		if !serializedTextFits(t.Name, 256) {
			return Fault("invalid_round")
		}
		themeViews = append(themeViews, ThemeView{ID: t.ID, Name: t.Name})
	}
	if r.Type == "final" {
		encoded, _ := json.Marshal(themeViews)
		if len(encoded) > 8192 {
			return Fault("invalid_round")
		}
	}
	if !betweenQuestions(*s) || !ValidID(r.ID) || r.Type != "standard" && r.Type != "final" || len(r.Themes) == 0 || r.MinimumPrice < 0 || r.MaximumPrice < r.MinimumPrice || r.MaximumPrice > 100000 {
		return Fault("invalid_round")
	}
	seen := map[string]bool{}
	for _, t := range r.Themes {
		if !ValidID(t.ID) || !ValidID(t.QuestionID) || seen[t.ID] {
			return Fault("invalid_round")
		}
		seen[t.ID] = true
	}
	ids := playerIDs(*s)
	if len(ids) < 2 {
		return Fault("not_enough_players")
	}
	if g.ChooserID == "" {
		g.ChooserID = ids[0]
	}
	g.Round = &r
	g.Question = nil
	g.Final = nil
	s.Winner = ""
	if r.Type != "final" {
		setStage(s, "select_question", g.ChooserID, now, g.Rules.SelectionSeconds)
		return nil
	}
	eligible := []string{}
	for _, id := range ids {
		p, _ := s.Participant(id)
		if p.Score > 0 {
			eligible = append(eligible, id)
		}
	}
	order := scoreOrder(*s, eligible)
	g.Final = &FinalState{Themes: slices.Clone(r.Themes), EligibleIDs: eligible, Order: order}
	if len(order) == 0 {
		setStage(s, "resolved", "", now, 0)
		return nil
	}
	deletions := len(r.Themes) - 1
	if deletions == 0 {
		setStage(s, "select_final", "", now, g.Rules.SelectionSeconds)
		return nil
	}
	start := (len(order) - 1 - (deletions-1)%len(order) + len(order)) % len(order)
	g.Final.Turn = start
	setStage(s, "eliminate_theme", order[start], now, g.Rules.SelectionSeconds)
	return nil
}
func eliminateTheme(s *State, id string, now int64) error {
	g := s.Gameplay
	f := g.Final
	if g.Stage.Kind != "eliminate_theme" || f == nil || len(f.Themes) < 2 {
		return Fault("invalid_phase")
	}
	found := false
	f.Themes = slices.DeleteFunc(f.Themes, func(t ThemeSpec) bool {
		if t.ID == id {
			found = true
			return true
		}
		return false
	})
	if !found {
		return Fault("invalid_theme")
	}
	if len(f.Themes) == 1 {
		setStage(s, "select_final", "", now, g.Rules.SelectionSeconds)
	} else {
		f.Turn = (f.Turn + 1) % len(f.Order)
		setStage(s, "eliminate_theme", f.Order[f.Turn], now, g.Rules.SelectionSeconds)
	}
	return nil
}
