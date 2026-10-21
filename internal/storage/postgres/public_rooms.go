package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"

	"siplatform/internal/game"
)

// PublicRoom is intentionally independent of State/View: adding an authenticated
// field to the game must not accidentally publish it to anonymous visitors.
type PublicRoom struct {
	RoomID           string     `json:"room_id"`
	Title            string     `json:"title"`
	Phase            game.Phase `json:"phase"`
	Players          int        `json:"players"`
	Spectators       int        `json:"spectators"`
	PlayerLimit      int        `json:"player_limit"`
	SpectatorLimit   int        `json:"spectator_limit"`
	CanJoinPlayer    bool       `json:"can_join_player"`
	CanJoinSpectator bool       `json:"can_join_spectator"`
}
type PublicRooms struct {
	Items     []PublicRoom `json:"items"`
	NextAfter string       `json:"next_after,omitempty"`
}

func (s *Store) ListPublicRooms(ctx context.Context, after string, limit int) (PublicRooms, error) {
	if after != "" {
		decoded, err := hex.DecodeString(after)
		if err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != after {
			return PublicRooms{}, game.Fault("invalid_input")
		}
	}
	if limit < 1 || limit > 50 {
		return PublicRooms{}, game.Fault("invalid_input")
	}
	// One statement reads a consistent page, without room actors or account/pack
	// lookups. Counts describe occupied seats; no socket presence is queried.
	rows, err := s.pool.Query(ctx, `SELECT r.id,p.title,r.snapshot FROM rooms r JOIN room_packs p ON p.room_id=r.id
		WHERE r.visibility='public' AND r.snapshot->>'phase'<>'finished' AND r.id>$1 ORDER BY r.id LIMIT $2`, after, limit+1)
	if err != nil {
		return PublicRooms{}, err
	}
	defer rows.Close()
	out := PublicRooms{Items: []PublicRoom{}}
	for rows.Next() {
		var item PublicRoom
		var raw []byte
		if err = rows.Scan(&item.RoomID, &item.Title, &raw); err != nil {
			return PublicRooms{}, err
		}
		if len(out.Items) == limit {
			out.NextAfter = out.Items[limit-1].RoomID
			break
		}
		var state game.State
		if err = json.Unmarshal(raw, &state); err != nil {
			return PublicRooms{}, err
		}
		item.Phase = state.Phase
		item.PlayerLimit, item.SpectatorLimit = 12, 20
		for _, p := range state.Participants {
			switch p.Role {
			case game.Player:
				item.Players++
			case game.Spectator:
				item.Spectators++
			}
		}
		item.CanJoinPlayer = game.CanJoinRole(state, game.Player)
		item.CanJoinSpectator = game.CanJoinRole(state, game.Spectator)
		out.Items = append(out.Items, item)
	}
	return out, rows.Err()
}
