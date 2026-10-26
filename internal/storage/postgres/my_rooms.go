package postgres

import (
	"context"
	"encoding/json"
	"time"

	"siplatform/internal/game"
	"siplatform/internal/storage/db"
)

type MyRoom struct {
	RoomID     string     `json:"room_id"`
	Title      string     `json:"title"`
	PackName   string     `json:"pack_name"`
	Phase      game.Phase `json:"phase"`
	Role       game.Role  `json:"role"`
	CanReturn  bool       `json:"can_return"`
	Players    int        `json:"players"`
	Spectators int        `json:"spectators"`
	Visibility string     `json:"visibility"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
}
type MyRooms struct {
	Items      []MyRoom `json:"items"`
	NextOffset int      `json:"next_offset,omitempty"`
}

func (s *Store) MyRooms(ctx context.Context, user string, offset int) (MyRooms, error) {
	if offset < 0 || offset > 100000 {
		return MyRooms{}, game.Fault("invalid_input")
	}
	// The original creation event identifies the creator's seat even after host transfer.
	rows, err := s.pool.Query(ctx, `SELECT r.id,p.title,COALESCE(v.document->>'name',p.demo_document->>'name','Пакет вопросов'),r.snapshot,e.actor_id,r.created_at,r.expires_at
 FROM (SELECT *, `+roomExpirySQL+` AS expires_at FROM rooms) r JOIN room_packs p ON p.room_id=r.id
 JOIN game_events e ON e.room_id=r.id AND e.seq=1 AND e.kind='room_created'
 LEFT JOIN library_revisions v ON v.pack_id=p.pack_id AND v.revision=p.revision
 WHERE p.owner_id=$1 AND r.expires_at>now()
 ORDER BY r.created_at DESC,r.id DESC LIMIT 51 OFFSET $2`, user, offset)
	if err != nil {
		return MyRooms{}, err
	}
	defer rows.Close()
	out := MyRooms{Items: []MyRoom{}}
	for rows.Next() {
		if len(out.Items) == 50 {
			out.NextOffset = offset + 50
			break
		}
		var item MyRoom
		var raw []byte
		var creator string
		if err = rows.Scan(&item.RoomID, &item.Title, &item.PackName, &raw, &creator, &item.CreatedAt, &item.ExpiresAt); err != nil {
			return out, err
		}
		var state game.State
		if err = json.Unmarshal(raw, &state); err != nil {
			return out, err
		}
		item.Phase = state.Phase
		item.Visibility = state.Settings.Normalized().Visibility
		if p, ok := state.ActiveParticipant(creator); ok {
			item.Role = p.Role
			item.CanReturn = true
		}
		for _, p := range state.Participants {
			if p.Removed {
				continue
			}
			if p.Role == game.Player {
				item.Players++
			}
			if p.Role == game.Spectator {
				item.Spectators++
			}
		}
		out.Items = append(out.Items, item)
	}
	return out, rows.Err()
}

func (s *Store) RecoverMyRoom(ctx context.Context, actor AppearanceActor, room, guest string) (Credentials, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Credentials{}, err
	}
	defer tx.Rollback(ctx)
	// Account/session locks use the same order as game creation and account deletion.
	var user string
	err = tx.QueryRow(ctx, `SELECT id FROM account_users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, actor.UserID).Scan(&user)
	if err != nil {
		return Credentials{}, game.Fault("unauthorized")
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM account_credentials WHERE id=$1 AND user_id=$2 AND kind='session' AND revoked_at IS NULL AND expires_at>clock_timestamp())`, actor.CredentialID, user).Scan(&valid)
	if err != nil {
		return Credentials{}, err
	}
	if !valid {
		return Credentials{}, game.Fault("unauthorized")
	}
	q := db.New(tx)
	if err = s.checkOwner(ctx, q); err != nil {
		return Credentials{}, err
	}
	var creator string
	err = tx.QueryRow(ctx, `SELECT e.actor_id FROM room_packs p JOIN game_events e ON e.room_id=p.room_id AND e.seq=1 AND e.kind='room_created' WHERE p.room_id=$1 AND p.owner_id=$2`, room, user).Scan(&creator)
	if err != nil {
		return Credentials{}, normalize(err)
	}
	row, err := q.LockRoom(ctx, room)
	if err != nil {
		return Credentials{}, normalize(err)
	}
	state, err := decode(row)
	if err != nil {
		return Credentials{}, err
	}
	if _, ok := state.ActiveParticipant(creator); !ok {
		return Credentials{}, game.Fault("forbidden")
	}
	c := Credentials{RoomID: room, ParticipantID: creator, Token: randomHex(32)}
	// One token per seat: rotation restores access without creating a second participant.
	_, err = tx.Exec(ctx, `INSERT INTO sessions(token_hash,room_id,participant_id) VALUES($1,$2,$3)
 ON CONFLICT(room_id,participant_id) DO UPDATE SET token_hash=EXCLUDED.token_hash,guest_hash=NULL`, digest(c.Token), room, creator)
	if err != nil {
		return Credentials{}, err
	}
	if err = bindGuest(ctx, q, c, guest); err != nil {
		return Credentials{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Credentials{}, err
	}
	return c, nil
}
