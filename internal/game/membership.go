package game

import "slices"

type RemovalPayload struct {
	TargetID string `json:"target_id"`
	Mode     string `json:"mode"`
}

// ActiveParticipant is the membership check for authority and admission. Participant
// deliberately continues to resolve frozen historical references.
func (s State) ActiveParticipant(id string) (Participant, bool) {
	p, ok := s.Participant(id)
	return p, ok && !p.Removed
}

// ApplyRemovalAt accepts a trusted group resolved by storage, never client IDs.
func ApplyRemovalAt(s State, actorID string, c Command, targets []string, nowMS int64) (State, error) {
	raw, err := c.Payload()
	if err != nil {
		return s, err
	}
	x, ok := raw.(*RemovalPayload)
	if !ok || s.Gameplay == nil {
		return s, Fault("invalid_command")
	}
	actor, ok := s.ActiveParticipant(actorID)
	if !ok {
		return s, Fault("unauthorized")
	}
	if actor.Role != Host {
		return s, Fault("forbidden")
	}
	if s.Phase == Finished {
		return s, Fault("finished")
	}
	if *c.ExpectedSeq != s.Seq {
		return s, Fault("stale_state")
	}
	if len(targets) == 0 || !slices.Contains(targets, x.TargetID) || x.Mode == "kick" && (len(targets) != 1 || targets[0] != x.TargetID) {
		return s, Fault("invalid_target")
	}
	removedPlayer := false
	seen := map[string]bool{}
	for _, id := range targets {
		p, exists := s.ActiveParticipant(id)
		if !exists || seen[id] {
			return s, Fault("invalid_target")
		}
		if p.Role == Host || id == actorID {
			return s, Fault("forbidden")
		}
		seen[id] = true
		removedPlayer = removedPlayer || p.Role == Player
	}
	n := s.Clone()
	for i := range n.Participants {
		if seen[n.Participants[i].ID] {
			n.Participants[i].Removed = true
		}
	}
	if removedPlayer && !betweenQuestions(n) {
		n, _ = PauseAt(n, nowMS)
	}
	if n.Gameplay.Content != nil {
		for i := range n.Gameplay.Content.Readiness {
			if seen[n.Gameplay.Content.Readiness[i].ID] {
				n.Gameplay.Content.Readiness[i].Status = "unavailable"
				if n.Gameplay.Stage.Kind == "scheduled_content" {
					n.Gameplay.Stage.Kind = "prepare_content"
					n.Gameplay.Stage.RemainingMS = 10000
					n.Gameplay.Stage.Expired = false
					n.Gameplay.PausedTimer = true
					n.Gameplay.Content.StartAtMS = 0
				}
			}
		}
	}
	repairChooser(&n)
	return n, nil
}

func repairChooser(s *State) {
	g := s.Gameplay
	if g == nil || !betweenQuestions(*s) || g.Final != nil {
		return
	}
	if p, ok := s.ActiveParticipant(g.ChooserID); ok && p.Role == Player {
		return
	}
	old := g.ChooserID
	g.ChooserID = ""
	if ids := playerIDs(*s); len(ids) > 0 {
		g.ChooserID = ids[0]
	}
	if g.Stage.TurnID == old {
		g.Stage.TurnID = g.ChooserID
	}
}

// PruneParticipants bounds the live roster after storage has archived its deltas.
// Frozen references and the engine's bounded private history remain resolvable.
func PruneParticipants(s State) State {
	n := s.Clone()
	keep := map[string]bool{}
	mark := func(ids ...string) {
		for _, id := range ids {
			keep[id] = true
		}
	}
	mark(n.Winner)
	if g := n.Gameplay; g != nil {
		trimHistory(g)
		mark(g.ChooserID, g.Stage.TurnID)
		if q := g.Question; q != nil {
			mark(q.ChooserBefore, q.RecipientID, q.AfterAnswerTurn)
			mark(q.Candidates...)
			for _, p := range q.Players {
				mark(p.ID)
			}
			if a := q.Auction; a != nil {
				mark(a.LeaderID)
				mark(a.ActiveIDs...)
				mark(a.Order...)
			}
		}
		if f := g.Final; f != nil {
			mark(f.EligibleIDs...)
			mark(f.Order...)
		}
		if c := g.Content; c != nil {
			for _, r := range c.Readiness {
				mark(r.ID)
			}
		}
		for _, d := range g.Decisions {
			mark(d.ParticipantID)
		}
		for _, a := range g.Appeals {
			mark(a.ParticipantID)
		}
	}
	n.Participants = slices.DeleteFunc(n.Participants, func(p Participant) bool { return p.Removed && !keep[p.ID] })
	return n
}
