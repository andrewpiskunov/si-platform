// Package game defines deterministic room transitions, independent of transport and storage.
package game

import (
	"encoding/json"
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
	Lobby        Phase = "lobby"
	Running      Phase = "running"
	PhaseRunning Phase = Running
	Open         Phase = "open"
	Answering    Phase = "answering"
	Resolved     Phase = "resolved"
	Paused       Phase = "paused"
	Finished     Phase = "finished"
)

type Participant struct {
	Removed bool   `json:"removed,omitempty"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Role    Role   `json:"role"`
	Score   int64  `json:"score"`
}
type Question struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Answer string `json:"answer,omitempty"`
	Value  int64  `json:"value"`
}
type State struct {
	Settings     RoomSettings  `json:"settings"`
	Appearance   *Appearance   `json:"appearance,omitempty"`
	Gameplay     *Session      `json:"gameplay,omitempty"`
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
	Settings     RoomSettings  `json:"settings"`
	Appearance   *Appearance   `json:"appearance,omitempty"`
	Gameplay     *SessionView  `json:"gameplay,omitempty"`
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
	if s.Appearance != nil {
		a := *s.Appearance
		s.Appearance = &a
	}
	if s.Gameplay != nil {
		b, _ := json.Marshal(s.Gameplay)
		var g Session
		_ = json.Unmarshal(b, &g)
		s.Gameplay = &g
	}
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

// CanJoinRole describes admission using the same phase and seat rules as joining.
func CanJoinRole(s State, role Role) bool { return joinRoleError(s, role) == nil }

func joinRoleError(s State, role Role) error {
	if role != Player && role != Spectator {
		return Fault("invalid_participant")
	}
	if s.Phase == Finished {
		return Fault("finished")
	}
	count := 0
	for _, existing := range s.Participants {
		if existing.Role == role && !existing.Removed {
			count++
		}
	}
	limit := 12
	if role == Spectator {
		limit = 20
	}
	if count >= limit {
		return Fault("room_full")
	}
	// New players join only between questions. Spectators can join at any phase.
	if role == Player && !betweenQuestions(s) {
		return Fault("question_in_progress")
	}
	return nil
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
	if err := joinRoleError(s, p.Role); err != nil {
		return s, err
	}
	n := s.Clone()
	p.Score = 0
	p.Removed = false
	n.Participants = append(n.Participants, p)
	return n, nil
}
func Project(s State, id string) View { return ProjectAt(s, id, 0) }
func projectLegacy(s State, id string) View {
	n := s.Clone()
	p, _ := n.Participant(id)
	if n.Question != nil && p.Role != Host && n.Phase != Resolved && n.Phase != Finished {
		n.Question.Answer = ""
	}
	return View{Settings: n.Settings.Normalized(), Appearance: n.Appearance, ID: n.ID, Seq: n.Seq, Phase: n.Phase, Participants: n.Participants, Question: n.Question, Winner: n.Winner, You: id}
}

// Pause suspends only an active question; safe to call repeatedly on disconnect/restart.
func Pause(s State) (State, bool) { return PauseAt(s, 0) }
func pauseLegacy(s State) (State, bool) {
	if s.Phase != Open && s.Phase != Answering {
		return s, false
	}
	n := s.Clone()
	n.ResumePhase, n.Phase = n.Phase, Paused
	return n, true
}
