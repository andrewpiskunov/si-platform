package postgres

import (
	"context"
	"encoding/json"
	"time"

	"siplatform/internal/game"
	"siplatform/internal/storage/db"
)

// RotateInvite changes admission for future participants without revoking existing sessions.
func (s *Store) RotateInvite(ctx context.Context, roomID, actorID string) (string, game.State, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", game.State{}, err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	if err = s.checkOwner(ctx, q); err != nil {
		return "", game.State{}, err
	}
	row, err := q.LockRoom(ctx, roomID)
	if err != nil {
		return "", game.State{}, normalize(err)
	}
	state, err := decode(row)
	if err != nil {
		return "", state, err
	}
	actor, ok := state.Participant(actorID)
	if !ok {
		return "", state, game.Fault("unauthorized")
	}
	if actor.Role != game.Host {
		return "", state, game.Fault("forbidden")
	}
	if state.Phase == game.Finished {
		return "", state, game.Fault("finished")
	}
	token := randomHex(32)
	if err = q.SetInvite(ctx, db.SetInviteParams{ID: roomID, InviteHash: digest(token)}); err != nil {
		return "", state, err
	}
	state.Seq++
	if err = saveState(ctx, q, state, "invite_rotated", actorID, []byte(`{}`)); err != nil {
		return "", state, err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", game.State{}, err
	}
	return token, state, nil
}

type ScoreChange struct {
	ParticipantID string `json:"participant_id"`
	Delta         int64  `json:"delta"`
	Score         int64  `json:"score"`
}
type PublicEvent struct {
	Appeals   []game.Appeal   `json:"appeals"`
	Decisions []game.Decision `json:"decisions"`
	Changes   []ScoreChange   `json:"changes"`
}
type ResultEvent struct {
	Seq       int64     `json:"seq"`
	Kind      string    `json:"kind"`
	ActorID   string    `json:"actor_id"`
	CreatedAt time.Time `json:"created_at"`
	PublicEvent
}
type RoomResults struct {
	RoomID       string             `json:"room_id"`
	Seq          int64              `json:"seq"`
	Phase        game.Phase         `json:"phase"`
	Participants []game.Participant `json:"participants"`
	Items        []ResultEvent      `json:"items"`
	NextAfterSeq int64              `json:"next_after_seq,omitempty"`
}

// Results exposes only explicitly recorded public deltas, never raw command payloads.
func (s *Store) Results(ctx context.Context, roomID, actorID string, after int64, limit int) (RoomResults, error) {
	if after < 0 || limit < 1 || limit > 100 {
		return RoomResults{}, game.Fault("invalid_input")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RoomResults{}, err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	row, err := q.GetRoom(ctx, roomID)
	if err != nil {
		return RoomResults{}, normalize(err)
	}
	state, err := decode(row)
	if err != nil {
		return RoomResults{}, err
	}
	if _, ok := state.Participant(actorID); !ok {
		return RoomResults{}, game.Fault("unauthorized")
	}
	events, err := q.RoomEvents(ctx, db.RoomEventsParams{RoomID: roomID, Seq: after, Seq_2: state.Seq, Limit: int32(limit + 1)})
	if err != nil {
		return RoomResults{}, err
	}
	out := RoomResults{RoomID: roomID, Seq: state.Seq, Phase: state.Phase, Participants: state.Participants, Items: []ResultEvent{}}
	if len(events) > limit {
		events = events[:limit]
		out.NextAfterSeq = events[len(events)-1].Seq
	}
	for _, e := range events {
		var data struct {
			Public PublicEvent `json:"public"`
		}
		if err = json.Unmarshal(e.Payload, &data); err != nil {
			return RoomResults{}, err
		}
		if data.Public.Appeals == nil {
			data.Public.Appeals = []game.Appeal{}
		}
		if data.Public.Decisions == nil {
			data.Public.Decisions = []game.Decision{}
		}
		if data.Public.Changes == nil {
			data.Public.Changes = []ScoreChange{}
		}
		out.Items = append(out.Items, ResultEvent{Seq: e.Seq, Kind: e.Kind, ActorID: e.ActorID, CreatedAt: e.CreatedAt.Time, PublicEvent: data.Public})
	}
	return out, nil
}
