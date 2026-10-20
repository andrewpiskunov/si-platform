package server

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"siplatform/examples"
	"siplatform/internal/game"
	"siplatform/internal/library"
	"siplatform/internal/packs"
	"siplatform/internal/storage/postgres"
)

func (s *Server) createGame(w http.ResponseWriter, r *http.Request) {
	a, ok := s.identity(w, r)
	if !ok {
		return
	}
	if a.APIToken {
		appError(w, library.ErrForbidden)
		return
	}
	var req struct {
		HostName string `json:"host_name"`
		PackID   string `json:"pack_id"`
		Revision int    `json:"revision"`
		Title    string `json:"title"`
		Theme    string `json:"theme"`
	}
	if err := readJSON(w, r, &req); err != nil {
		appError(w, err)
		return
	}
	setup := postgres.GameSetup{ActorID: a.User.ID, CredentialID: a.SessionID, PackID: req.PackID, Revision: req.Revision, Title: strings.TrimSpace(req.Title), Theme: req.Theme}
	if setup.Title == "" {
		setup.Title = "Своя игра"
	}
	if setup.Theme == "" {
		setup.Theme = "classic"
	}
	if setup.PackID == "" {
		d := examples.Demo()
		setup.DemoDocument = &d
	} else {
		d, err := s.options.Library.ForPlay(r.Context(), a, setup.PackID, setup.Revision)
		if err != nil {
			appError(w, err)
			return
		}
		setup.Revision = d.Card.Revision
	}
	c, err := s.store.CreateGame(r.Context(), req.HostName, setup)
	if err != nil {
		appError(w, err)
		return
	}
	writeJSON(w, 201, c)
}
func (s *Server) roomIdentity(r *http.Request) (string, error) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", game.Fault("unauthorized")
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", game.Fault("unauthorized")
	}
	return s.store.Authenticate(r.Context(), chi.URLParam(r, "room"), parts[1])
}
func (s *Server) roomDocument(ctx context.Context, id string) (postgres.GamePin, packs.Document, error) {
	pin, e := s.store.GamePin(ctx, id)
	if e != nil {
		return pin, packs.Document{}, e
	}
	if pin.PackID == "" {
		if pin.DemoDocument == nil {
			return pin, packs.Document{}, game.Fault("unavailable")
		}
		return pin, *pin.DemoDocument, nil
	}
	d, e := s.options.Library.Pinned(ctx, pin.PackID, pin.Revision)
	return pin, d.Document, e
}

type boardQuestion struct {
	ID     string `json:"id"`
	Value  int    `json:"value"`
	Played bool   `json:"played"`
}
type boardTheme struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Questions []boardQuestion `json:"questions"`
}
type boardRound struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Type   string       `json:"type"`
	Themes []boardTheme `json:"themes"`
}

func (s *Server) board(w http.ResponseWriter, r *http.Request) {
	_, e := s.roomIdentity(r)
	if e != nil {
		appError(w, e)
		return
	}
	id := chi.URLParam(r, "room")
	pin, d, e := s.roomDocument(r.Context(), id)
	if e != nil {
		appError(w, e)
		return
	}
	state, e := s.store.Load(r.Context(), id)
	if e != nil {
		appError(w, e)
		return
	}
	rounds := make([]boardRound, 0, len(d.Rounds))
	for _, round := range d.Rounds {
		br := boardRound{ID: round.ID, Name: round.Name, Type: round.Type, Themes: []boardTheme{}}
		for _, theme := range round.Themes {
			bt := boardTheme{ID: theme.ID, Name: theme.Name, Questions: []boardQuestion{}}
			for _, q := range theme.Questions {
				bt.Questions = append(bt.Questions, boardQuestion{q.ID, q.Price, slices.Contains(state.Asked, q.ID)})
			}
			br.Themes = append(br.Themes, bt)
		}
		rounds = append(rounds, br)
	}
	writeJSON(w, 200, map[string]any{"title": pin.Title, "theme": pin.Theme, "pack_name": d.Name, "rounds": rounds})
}
func (s *Server) selectQuestion(w http.ResponseWriter, r *http.Request) {
	actor, e := s.roomIdentity(r)
	if e != nil {
		appError(w, e)
		return
	}
	var req struct {
		QuestionID  string `json:"question_id"`
		CommandID   string `json:"command_id"`
		ExpectedSeq int64  `json:"expected_seq"`
	}
	if e = readJSON(w, r, &req); e != nil {
		appError(w, e)
		return
	}
	if !game.ValidID(req.QuestionID) || !game.ValidID(req.CommandID) || req.ExpectedSeq < 1 {
		appError(w, library.ErrInvalid)
		return
	}
	id := chi.URLParam(r, "room")
	rm, e := s.getRoom(r.Context(), id)
	if e != nil {
		appError(w, e)
		return
	}
	_, e = rm.do(r.Context(), func(ctx context.Context, rm *room) (any, error) {
		state, e := s.store.Load(ctx, id)
		if e != nil {
			return nil, e
		}
		p, ok := state.Participant(actor)
		if !ok || p.Role != game.Host {
			return nil, game.Fault("forbidden")
		}
		connected := false
		for c := range rm.clients {
			if c.id == actor && c.ctx.Err() == nil {
				connected = true
			}
		}
		if !connected {
			return nil, game.Fault("host_offline")
		}
		if rm.limits[actor] == nil {
			rm.limits[actor] = newBucket()
		}
		if !rm.limits[actor].allow(time.Now()) {
			return nil, game.Fault("rate_limited")
		}
		_, doc, e := s.roomDocument(ctx, id)
		if e != nil {
			return nil, e
		}
		var question *packs.Question
		for _, rd := range doc.Rounds {
			for _, th := range rd.Themes {
				for _, q := range th.Questions {
					if q.ID == req.QuestionID {
						copy := q
						question = &copy
					}
				}
			}
		}
		if question == nil {
			return nil, game.Fault("not_found")
		}
		cmd, e := textCommand(*question, req.CommandID, req.ExpectedSeq)
		if e != nil {
			return nil, e
		}
		out, e := s.store.Execute(ctx, id, actor, cmd)
		if e != nil {
			return nil, e
		}
		if out.Code != "ok" {
			return nil, game.Fault(out.Code)
		}
		rm.broadcast(out.State, nil)
		return nil, nil
	})
	if e != nil {
		appError(w, e)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func textCommand(q packs.Question, id string, seq int64) (game.Command, error) {
	// The next rules/media stage expands this adapter; never flatten a special
	// question into an ordinary question with different scoring.
	if q.Type != "" && q.Type != "simple" && q.Type != "withButton" {
		return game.Command{}, game.Fault("unsupported_question")
	}
	var lines []string
	for _, item := range q.Content("question") {
		if item.Type != "text" || item.IsRef || item.Placement == "replic" {
			return game.Command{}, game.Fault("unsupported_question")
		}
		lines = append(lines, item.Value)
	}
	cmd := game.Command{Version: 1, ID: id, Type: "open_question", QuestionID: q.ID, Text: strings.Join(lines, "\n"), Answer: strings.Join(q.Right, " / "), Value: int64(q.Price), ExpectedSeq: &seq}
	return cmd, cmd.Validate()
}
