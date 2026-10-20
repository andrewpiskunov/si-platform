// Package game defines deterministic room transitions, independent of transport and storage.
package game

import (
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Role string

const (
	Host      Role = "host"
	Player    Role = "player"
	Spectator Role = "spectator"
)

type Phase string

const (
	Lobby     Phase = "lobby"
	Open      Phase = "open"
	Answering Phase = "answering"
	Resolved  Phase = "resolved"
	Paused    Phase = "paused"
	Finished  Phase = "finished"
)

type Participant struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Role  Role   `json:"role"`
	Score int64  `json:"score"`
}
type Question struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Answer string `json:"answer,omitempty"`
	Value  int64  `json:"value"`
}
type State struct {
	ID           string        `json:"room_id"`
	Seq          int64         `json:"seq"`
	Phase        Phase         `json:"phase"`
	ResumePhase  Phase         `json:"resume_phase,omitempty"`
	Participants []Participant `json:"participants"`
	Question     *Question     `json:"question,omitempty"`
	Winner       string        `json:"winner,omitempty"`
	Asked        []string      `json:"asked,omitempty"`
}
type View struct {
	ID           string        `json:"room_id"`
	Seq          int64         `json:"seq"`
	Phase        Phase         `json:"phase"`
	Participants []Participant `json:"participants"`
	Question     *Question     `json:"question,omitempty"`
	Winner       string        `json:"winner,omitempty"`
	You          string        `json:"you"`
}

type Fault string

func (e Fault) Error() string { return string(e) }
func Code(err error) string {
	if err == nil {
		return "ok"
	}
	var f Fault
	if errors.As(err, &f) {
		return string(f)
	}
	return "unavailable"
}
func (s State) Clone() State {
	s.Participants = slices.Clone(s.Participants)
	s.Asked = slices.Clone(s.Asked)
	if s.Question != nil {
		q := *s.Question
		s.Question = &q
	}
	return s
}
func (s State) Participant(id string) (Participant, bool) {
	for _, p := range s.Participants {
		if p.ID == id {
			return p, true
		}
	}
	return Participant{}, false
}
func ValidName(name string) bool {
	if name != strings.TrimSpace(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 32 {
		return false
	}
	return !strings.ContainsFunc(name, unicode.IsControl)
}
func AddParticipant(s State, p Participant) (State, error) {
	if !ValidName(p.Name) || p.ID == "" || (p.Role != Player && p.Role != Spectator) {
		return s, Fault("invalid_participant")
	}
	if s.Phase == Finished {
		return s, Fault("finished")
	}
	if _, exists := s.Participant(p.ID); exists {
		return s, Fault("duplicate_participant")
	}
	count := 0
	for _, existing := range s.Participants {
		if existing.Role == p.Role {
			count++
		}
	}
	limit := 12
	if p.Role == Spectator {
		limit = 20
	}
	if count >= limit {
		return s, Fault("room_full")
	}
	// New players join only between questions. Spectators can join at any phase.
	if p.Role == Player && s.Phase != Lobby && s.Phase != Resolved {
		return s, Fault("question_in_progress")
	}
	n := s.Clone()
	p.Score = 0
	n.Participants = append(n.Participants, p)
	return n, nil
}
func Project(s State, id string) View {
	n := s.Clone()
	p, _ := n.Participant(id)
	if n.Question != nil && p.Role != Host && n.Phase != Resolved && n.Phase != Finished {
		n.Question.Answer = ""
	}
	return View{ID: n.ID, Seq: n.Seq, Phase: n.Phase, Participants: n.Participants, Question: n.Question, Winner: n.Winner, You: id}
}

// Pause suspends only an active question; safe to call repeatedly on disconnect/restart.
func Pause(s State) (State, bool) {
	if s.Phase != Open && s.Phase != Answering {
		return s, false
	}
	n := s.Clone()
	n.ResumePhase, n.Phase = n.Phase, Paused
	return n, true
}
