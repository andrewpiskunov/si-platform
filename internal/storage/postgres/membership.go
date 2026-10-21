package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"siplatform/internal/game"
	"siplatform/internal/storage/db"
)

func validHex(value string, size int) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == size && hex.EncodeToString(raw) == value
}
func guestDigest(guest string) (string, error) {
	if !validHex(guest, 32) {
		return "", game.Fault("invalid_input")
	}
	return digest(guest), nil
}
func bindGuest(ctx context.Context, q *db.Queries, c Credentials, guest string) error {
	if guest == "" {
		return nil
	}
	hash, err := guestDigest(guest)
	if err != nil {
		return err
	}
	banned, err := q.RoomGuestBanned(ctx, db.RoomGuestBannedParams{RoomID: c.RoomID, GuestHash: hash})
	if err != nil {
		return err
	}
	if banned {
		return game.Fault("guest_banned")
	}
	count, err := q.BindRoomGuest(ctx, db.BindRoomGuestParams{RoomID: c.RoomID, TokenHash: digest(c.Token), GuestHash: pgtype.Text{String: hash, Valid: true}})
	if err == nil && count != 1 {
		return game.Fault("guest_session_mismatch")
	}
	return err
}

// BindGuest only initializes an unbound session; a token cannot move its guest identity.
func (s *Store) BindGuest(ctx context.Context, roomID, token, guest string) error {
	if len(token) != 64 {
		return game.Fault("unauthorized")
	}
	if _, err := guestDigest(guest); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	if err = s.checkOwner(ctx, q); err != nil {
		return err
	}
	row, err := q.LockRoom(ctx, roomID)
	if err != nil {
		return normalize(err)
	}
	state, err := decode(row)
	if err != nil {
		return err
	}
	id, err := q.GetSession(ctx, db.GetSessionParams{RoomID: roomID, TokenHash: digest(token)})
	if errors.Is(err, pgx.ErrNoRows) {
		return game.Fault("unauthorized")
	}
	if err != nil {
		return err
	}
	if _, ok := state.ActiveParticipant(id); !ok {
		return game.Fault("unauthorized")
	}
	if err = bindGuest(ctx, q, Credentials{RoomID: roomID, Token: token}, guest); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func archiveParticipants(ctx context.Context, q *db.Queries, before, after game.State) error {
	previous := make(map[string]game.Participant, len(before.Participants))
	for _, p := range before.Participants {
		previous[p.ID] = p
	}
	changed := []game.Participant{}
	for _, p := range after.Participants {
		if old, ok := previous[p.ID]; !ok || old != p {
			changed = append(changed, p)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	raw, err := json.Marshal(changed)
	if err != nil {
		return err
	}
	return q.ArchiveRoomParticipants(ctx, db.ArchiveRoomParticipantsParams{RoomID: after.ID, Participants: raw})
}

// resolveRemoval never trusts client-supplied group IDs. Call with the room row locked.
func resolveRemoval(ctx context.Context, q *db.Queries, state game.State, actorID string, c game.Command, now int64) (game.State, error) {
	payload, err := c.Payload()
	if err != nil {
		return state, err
	}
	p := payload.(*game.RemovalPayload)
	// First validate authority, sequence, target and phase before looking up private identity.
	next, err := game.ApplyRemovalAt(state, actorID, c, []string{p.TargetID}, now)
	if err != nil {
		return state, err
	}
	ids := []string{p.TargetID}
	hash := ""
	if p.Mode == "ban_guest_session" {
		g, e := q.RoomGuestHash(ctx, db.RoomGuestHashParams{RoomID: state.ID, ParticipantID: p.TargetID})
		if errors.Is(e, pgx.ErrNoRows) || e == nil && !g.Valid {
			return state, game.Fault("guest_session_unavailable")
		}
		if e != nil {
			return state, e
		}
		hash = g.String
		ids, e = q.RoomGuestParticipants(ctx, db.RoomGuestParticipantsParams{RoomID: state.ID, GuestHash: g})
		if e != nil {
			return state, e
		}
		next, err = game.ApplyRemovalAt(state, actorID, c, ids, now)
		if err != nil {
			return state, err
		}
	}
	if hash != "" {
		if err = q.BanRoomGuest(ctx, db.BanRoomGuestParams{RoomID: state.ID, GuestHash: hash}); err != nil {
			return state, err
		}
	}
	if err = q.RevokeRoomParticipants(ctx, db.RevokeRoomParticipantsParams{RoomID: state.ID, Ids: ids}); err != nil {
		return state, err
	}
	return next, nil
}

type RoomRoster struct {
	Items     []game.Participant `json:"items"`
	NextAfter string             `json:"next_after,omitempty"`
}

func (s *Store) Roster(ctx context.Context, roomID, actorID, after string, limit int) (RoomRoster, error) {
	if after != "" && !validHex(after, 16) || limit < 1 || limit > 50 {
		return RoomRoster{}, game.Fault("invalid_input")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return RoomRoster{}, err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	row, err := q.GetRoom(ctx, roomID)
	if err != nil {
		return RoomRoster{}, normalize(err)
	}
	state, err := decode(row)
	if err != nil {
		return RoomRoster{}, err
	}
	if _, ok := state.ActiveParticipant(actorID); !ok {
		return RoomRoster{}, game.Fault("unauthorized")
	}
	rows, err := q.RoomRoster(ctx, db.RoomRosterParams{RoomID: roomID, AfterID: after, Limit: int32(limit + 1)})
	if err != nil {
		return RoomRoster{}, err
	}
	out := RoomRoster{Items: []game.Participant{}}
	if len(rows) > limit {
		rows = rows[:limit]
		out.NextAfter = rows[len(rows)-1].ParticipantID
	}
	for _, p := range rows {
		out.Items = append(out.Items, game.Participant{ID: p.ParticipantID, Name: p.Name, Role: game.Role(p.Role), Score: p.Score, Removed: p.Removed})
	}
	return out, nil
}
