package postgres

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"time"

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
	if c.Type == "set_appearance" {
		return Outcome{}, game.Fault("forbidden")
	}
	return s.execute(ctx, roomID, actorID, c, nil)
}

func (s *Store) execute(ctx context.Context, roomID, actorID string, c game.Command, themeActor *AppearanceActor) (Outcome, error) {
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
	var appearance *game.Appearance
	if themeActor != nil {
		p, _ := c.Payload()
		a := p.(*game.AppearancePayload).Appearance
		if err = authorizeAppearance(ctx, tx, *themeActor, a); err != nil {
			return Outcome{}, err
		}
		appearance = &a
	}
	q := db.New(tx)
	if err = s.checkOwner(ctx, q); err != nil {
		return Outcome{}, err
	}
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
	now := time.Now().UnixMilli()
	var seed [8]byte
	_, _ = rand.Read(seed[:])
	entropy := binary.LittleEndian.Uint64(seed[:])
	next, transitionErr := game.ApplyAt(state, actorID, c, now, entropy)
	code := game.Code(transitionErr)
	if transitionErr == nil {
		if state.Settings.Visibility == "public" && next.Settings.Visibility == "private" {
			// No invite secret enters a receipt, event, or broadcast. The host
			// explicitly issues a fresh link with RotateInvite after closing.
			// This runs only once: command replay returns above this branch.
			if err = q.SetInvite(ctx, db.SetInviteParams{ID: roomID, InviteHash: digest(randomHex(32))}); err != nil {
				return Outcome{}, err
			}
		}
		if appearance != nil {
			if err = saveAppearance(ctx, tx, roomID, *appearance); err != nil {
				return Outcome{}, err
			}
		}
		next.Seq++
		eventPayload := payload
		if c.Version == 2 {
			eventPayload, err = json.Marshal(struct {
				Command      game.Command `json:"command"`
				ServerTimeMS int64        `json:"server_time_ms"`
				Entropy      uint64       `json:"entropy"`
				Public       PublicEvent  `json:"public"`
			}{c, now, entropy, publicDelta(state, next)})
			if err != nil {
				return Outcome{}, err
			}
		}
		if err = saveState(ctx, q, next, c.Type, actorID, eventPayload); err != nil {
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
	if err = s.checkOwner(ctx, q); err != nil {
		return game.State{}, err
	}
	row, err := q.LockRoom(ctx, roomID)
	if err != nil {
		return game.State{}, normalize(err)
	}
	state, err := decode(row)
	if err != nil {
		return game.State{}, err
	}
	now := time.Now().UnixMilli()
	next, changed := game.PauseAt(state, now)
	if !changed {
		return state, nil
	}
	next.Seq++
	payload, err := json.Marshal(map[string]any{"reason": reason, "server_time_ms": now})
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
