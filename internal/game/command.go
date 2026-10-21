package game

import (
	"encoding/json"
	"strings"
)

type Command struct {
	Data        json.RawMessage `json:"data,omitempty"`
	Version     int             `json:"v"`
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	QuestionID  string          `json:"question_id,omitempty"`
	Text        string          `json:"text,omitempty"`
	Answer      string          `json:"answer,omitempty"`
	Value       int64           `json:"value,omitempty"`
	Correct     *bool           `json:"correct,omitempty"`
	ExpectedSeq *int64          `json:"expected_seq,omitempty"`
}

func ValidID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (c Command) Validate() error {
	if c.Version == 2 {
		_, err := c.Payload()
		return err
	}
	if len(c.Data) != 0 {
		return Fault("invalid_command")
	}
	if c.Version != 1 || !ValidID(c.ID) {
		return Fault("invalid_command")
	}
	if c.ExpectedSeq != nil && *c.ExpectedSeq < 1 {
		return Fault("invalid_command")
	}
	switch c.Type {
	case "open_question":
		if !ValidID(c.QuestionID) || strings.TrimSpace(c.Text) == "" || len(c.Text) > 4096 || strings.TrimSpace(c.Answer) == "" || len(c.Answer) > 4096 || c.Value < 1 || c.Value > 100000 || c.Correct != nil {
			return Fault("invalid_command")
		}
	case "buzz", "judge":
		if !ValidID(c.QuestionID) || c.Text != "" || c.Answer != "" || c.Value != 0 || (c.Type == "judge") != (c.Correct != nil) {
			return Fault("invalid_command")
		}
	case "pause", "resume", "finish":
		if c.QuestionID != "" || c.Text != "" || c.Answer != "" || c.Value != 0 || c.Correct != nil {
			return Fault("invalid_command")
		}
	default:
		return Fault("invalid_command")
	}
	return nil
}

func Apply(s State, actorID string, c Command) (State, error) { return ApplyAt(s, actorID, c, 0, 0) }
func applyLegacy(s State, actorID string, c Command) (State, error) {
	if err := c.Validate(); err != nil {
		return s, err
	}
	actor, exists := s.ActiveParticipant(actorID)
	if !exists {
		return s, Fault("unauthorized")
	}
	if c.Type == "buzz" {
		if actor.Role != Player {
			return s, Fault("forbidden")
		}
	} else if actor.Role != Host {
		return s, Fault("forbidden")
	}
	if s.Phase == Finished {
		return s, Fault("finished")
	}
	if c.ExpectedSeq != nil && *c.ExpectedSeq != s.Seq {
		return s, Fault("stale_state")
	}
	n := s.Clone()
	switch c.Type {
	case "open_question":
		if s.Phase != Lobby && s.Phase != Resolved {
			return s, Fault("invalid_phase")
		}
		players := 0
		for _, p := range s.Participants {
			if p.Role == Player && !p.Removed {
				players++
			}
		}
		if players < 2 {
			return s, Fault("not_enough_players")
		}
		if len(s.Asked) >= 2000 {
			return s, Fault("question_limit")
		}
		for _, id := range s.Asked {
			if id == c.QuestionID {
				return s, Fault("question_reused")
			}
		}
		n.Question = &Question{ID: c.QuestionID, Text: c.Text, Answer: c.Answer, Value: c.Value}
		n.Winner, n.Phase, n.ResumePhase = "", Open, ""
		n.Asked = append(n.Asked, c.QuestionID)
	case "buzz":
		if s.Question == nil || s.Question.ID != c.QuestionID {
			return s, Fault("stale_question")
		}
		if s.Phase != Open {
			return s, Fault("invalid_phase")
		}
		n.Winner, n.Phase = actorID, Answering
	case "judge":
		if s.Question == nil || s.Question.ID != c.QuestionID {
			return s, Fault("stale_question")
		}
		if s.Phase != Answering {
			return s, Fault("invalid_phase")
		}
		delta := s.Question.Value
		if !*c.Correct {
			delta = -delta
		}
		for i := range n.Participants {
			if n.Participants[i].ID == s.Winner {
				n.Participants[i].Score += delta
			}
		}
		n.Phase = Resolved
	case "pause":
		var changed bool
		n, changed = Pause(s)
		if !changed {
			return s, Fault("invalid_phase")
		}
	case "resume":
		if s.Phase != Paused {
			return s, Fault("invalid_phase")
		}
		n.Phase, n.ResumePhase = s.ResumePhase, ""
	case "finish":
		n.Phase, n.ResumePhase = Finished, ""
	}
	return n, nil
}
