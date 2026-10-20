package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"siplatform/internal/game"
	"siplatform/internal/storage/db"
)

type Outcome struct {
	State     game.State
	Code      string
	Seq       int64
	Duplicate bool
}

func (s *Store) Execute(ctx context.Context, roomID, actorID string, c game.Command) (Outcome, error) {
	if err := c.Validate(); err != nil {
		return Outcome{}, err
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return Outcome{}, err
	}
	hash := digest(string(payload))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Outcome{}, err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	row, err := q.LockRoom(ctx, roomID)
	if err != nil {
		return Outcome{}, normalize(err)
	}
	state, err := decode(row)
	if err != nil {
		return Outcome{}, err
	}
	if _, ok := state.Participant(actorID); !ok {
		return Outcome{}, game.Fault("unauthorized")
	}
	previous, err := q.FindCommand(ctx, db.FindCommandParams{RoomID: roomID, ActorID: actorID, CommandID: c.ID})
	if err == nil {
		out := Outcome{State: state, Code: previous.Code, Seq: previous.Seq, Duplicate: true}
		if previous.RequestHash != hash {
			out.Code = "command_conflict"
			out.Duplicate = false
		}
		return out, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Outcome{}, err
	}
	next, transitionErr := game.Apply(state, actorID, c)
	code := game.Code(transitionErr)
	if transitionErr == nil {
		next.Seq++
		if err = saveState(ctx, q, next, c.Type, actorID, payload); err != nil {
			return Outcome{}, err
		}
	}
	if err = q.SaveCommand(ctx, db.SaveCommandParams{RoomID: roomID, ActorID: actorID, CommandID: c.ID, RequestHash: hash, Code: code, Seq: next.Seq}); err != nil {
		return Outcome{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Outcome{}, err
	}
	return Outcome{State: next, Code: code, Seq: next.Seq}, nil
}

func (s *Store) Pause(ctx context.Context, roomID, reason string) (game.State, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return game.State{}, err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	row, err := q.LockRoom(ctx, roomID)
	if err != nil {
		return game.State{}, normalize(err)
	}
	state, err := decode(row)
	if err != nil {
		return game.State{}, err
	}
	next, changed := game.Pause(state)
	if !changed {
		return state, nil
	}
	next.Seq++
	payload, err := json.Marshal(map[string]string{"reason": reason})
	if err != nil {
		return game.State{}, err
	}
	if err = saveState(ctx, q, next, "paused", "system", payload); err != nil {
		return game.State{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return game.State{}, err
	}
	return next, nil
}

// Recover is called under exclusive process ownership, before accepting clients.
func (s *Store) Recover(ctx context.Context) error {
	ids, err := db.New(s.pool).ListRooms(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = s.Pause(ctx, id, "server_restarted"); err != nil {
			return err
		}
	}
	return nil
}
