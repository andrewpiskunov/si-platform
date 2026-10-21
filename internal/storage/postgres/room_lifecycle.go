package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"siplatform/internal/game"
	"siplatform/internal/storage/db"
)

// Retention describes eligibility for the next bounded housekeeping pass.
type Retention struct {
	CreatedAt      time.Time `json:"created_at"`
	LastActivityAt time.Time `json:"last_activity_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

const roomExpirySQL = `LEAST(created_at + interval '2160 hours', CASE
 WHEN finished_at IS NOT NULL THEN finished_at + interval '720 hours'
 WHEN snapshot->>'phase'='lobby' THEN last_activity_at + interval '168 hours'
 ELSE last_activity_at + interval '720 hours' END)`

func (s *Store) Retention(ctx context.Context, id string) (Retention, error) {
	var out Retention
	err := s.pool.QueryRow(ctx, `SELECT created_at,last_activity_at,`+roomExpirySQL+` FROM rooms WHERE id=$1`, id).Scan(&out.CreatedAt, &out.LastActivityAt, &out.ExpiresAt)
	return out, normalize(err)
}

func (s *Store) ExpiredRooms(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if limit < 1 || limit > 50 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT id FROM rooms WHERE `+roomExpirySQL+` <= $1 ORDER BY created_at,id LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// RetireRoom rechecks eligibility under the same owner and room locks as every
// gameplay mutation. On any commit error the caller must discard its actor and
// re-read durable state before accepting further work; success is not assumed.
func (s *Store) RetireRoom(ctx context.Context, id string, now time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err = s.checkOwner(ctx, db.New(tx)); err != nil {
		return false, err
	}
	var expires time.Time
	err = tx.QueryRow(ctx, `SELECT `+roomExpirySQL+` FROM rooms WHERE id=$1 FOR UPDATE`, id).Scan(&expires)
	if err == pgx.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if expires.After(now) {
		return false, nil
	}
	if _, err = tx.Exec(ctx, `DELETE FROM room_packs WHERE room_id=$1`, id); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM room_theme_pins WHERE room_id=$1`, id); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM rooms WHERE id=$1`, id); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func roomExpiry(row db.Room, phase game.Phase) time.Time {
	expiry := row.CreatedAt.Time.Add(90 * 24 * time.Hour)
	idle := row.LastActivityAt.Time.Add(30 * 24 * time.Hour)
	if row.FinishedAt.Valid {
		idle = row.FinishedAt.Time.Add(30 * 24 * time.Hour)
	} else if phase == game.Lobby {
		idle = row.LastActivityAt.Time.Add(7 * 24 * time.Hour)
	}
	if idle.Before(expiry) {
		return idle
	}
	return expiry
}
