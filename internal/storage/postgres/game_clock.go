package postgres

import (
	"context"
	"encoding/json"
	"siplatform/internal/game"
	"siplatform/internal/storage/db"
	"time"
)

// Advance applies only a due persisted deadline. Duplicate/stale callbacks do not
// advance sequence or reroll winners. The same row/owner locks fence client work.
func (s *Store) Advance(ctx context.Context, roomID string) (game.State, error) {
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
		return state, err
	}
	now := time.Now().UnixMilli()
	next, changed := game.AdvanceAt(state, now)
	if !changed {
		return state, nil
	}
	next.Seq++
	payload, err := json.Marshal(struct {
		ServerTimeMS int64       `json:"server_time_ms"`
		Public       PublicEvent `json:"public"`
	}{now, publicDelta(state, next)})
	if err != nil {
		return state, err
	}
	if err = saveState(ctx, q, next, "timer_expired", "system", payload); err != nil {
		return state, err
	}
	if err = tx.Commit(ctx); err != nil {
		return state, err
	}
	return next, nil
}
func publicDelta(before, after game.State) PublicEvent {
	out := PublicEvent{Decisions: []game.Decision{}, Appeals: []game.Appeal{}, Changes: []ScoreChange{}}
	seen := map[string]bool{}
	priorAppeals := map[string]game.Appeal{}
	if before.Gameplay != nil {
		for _, a := range before.Gameplay.Appeals {
			priorAppeals[a.ID] = a
		}
		for _, d := range before.Gameplay.Decisions {
			seen[d.ID] = true
		}
	}
	if after.Gameplay != nil {
		for _, a := range after.Gameplay.Appeals {
			if prior, ok := priorAppeals[a.ID]; !ok || prior != a {
				out.Appeals = append(out.Appeals, a)
			}
		}
		for _, d := range after.Gameplay.Decisions {
			if !seen[d.ID] {
				out.Decisions = append(out.Decisions, d)
			}
		}
	}
	for _, p := range after.Participants {
		if prior, ok := before.Participant(p.ID); ok && p.Score != prior.Score {
			out.Changes = append(out.Changes, ScoreChange{ParticipantID: p.ID, Delta: p.Score - prior.Score, Score: p.Score})
		}
	}
	return out
}
