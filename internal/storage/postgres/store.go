package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"siplatform/internal/game"
	"siplatform/internal/storage/db"
)

type Store struct {
	pool  *pgxpool.Pool
	epoch atomic.Int64
}

func New(pool *pgxpool.Pool) *Store  { return &Store{pool: pool} }
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

type Credentials struct {
	RoomID        string `json:"room_id"`
	ParticipantID string `json:"participant_id"`
	Token         string `json:"token"`
	InviteToken   string `json:"invite_token,omitempty"`
}

func randomHex(size int) string {
	b := make([]byte, size)
	// crypto/rand.Read always fills the buffer and terminates on an entropy failure.
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
func digest(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}
func decode(row db.Room) (game.State, error) {
	var state game.State
	err := json.Unmarshal(row.Snapshot, &state)
	if err == nil && (state.ID != row.ID || state.Seq != row.Seq) {
		err = errors.New("room snapshot sequence mismatch")
	}
	return state, err
}
func normalize(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return game.Fault("not_found")
	}
	return err
}
func (s *Store) Load(ctx context.Context, id string) (game.State, error) {
	row, err := db.New(s.pool).GetRoom(ctx, id)
	if err != nil {
		return game.State{}, normalize(err)
	}
	return decode(row)
}
func (s *Store) Authenticate(ctx context.Context, roomID, token string) (string, error) {
	if len(token) != 64 {
		return "", game.Fault("unauthorized")
	}
	id, err := db.New(s.pool).GetSession(ctx, db.GetSessionParams{TokenHash: digest(token), RoomID: roomID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", game.Fault("unauthorized")
	}
	return id, err
}
func (s *Store) Create(ctx context.Context, name string) (Credentials, error) {
	return s.create(ctx, name, nil)
}
func (s *Store) CreateGame(ctx context.Context, name string, setup GameSetup) (Credentials, error) {
	return s.create(ctx, name, &setup)
}
func (s *Store) create(ctx context.Context, name string, setup *GameSetup) (Credentials, error) {
	if !game.ValidName(name) {
		return Credentials{}, game.Fault("invalid_name")
	}
	c := Credentials{RoomID: randomHex(16), ParticipantID: randomHex(16), Token: randomHex(32), InviteToken: randomHex(32)}
	state := game.State{ID: c.RoomID, Seq: 1, Phase: game.Lobby, Participants: []game.Participant{{ID: c.ParticipantID, Name: name, Role: game.Host}}}
	if setup != nil {
		a := setup.selectedAppearance()
		state.Appearance = &a
	}
	if setup != nil && setup.Rules != nil {
		if err := setup.Rules.Validate(); err != nil {
			return Credentials{}, err
		}
		state.Gameplay = game.NewSession(*setup.Rules)
	}
	b, err := json.Marshal(state)
	if err != nil {
		return Credentials{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Credentials{}, err
	}
	defer tx.Rollback(ctx)
	if setup != nil {
		if err := setup.authorize(ctx, tx); err != nil {
			return Credentials{}, err
		}
	}
	q := db.New(tx)
	if err = s.checkOwner(ctx, q); err != nil {
		return Credentials{}, err
	}
	if err = q.LockCreate(ctx); err != nil {
		return Credentials{}, err
	}
	count, err := q.CountRooms(ctx)
	if err != nil {
		return Credentials{}, err
	}
	if count >= 1000 {
		return Credentials{}, game.Fault("room_limit")
	}
	if err = q.CreateRoom(ctx, db.CreateRoomParams{ID: c.RoomID, Snapshot: b, Seq: 1, InviteHash: digest(c.InviteToken)}); err != nil {
		return Credentials{}, err
	}
	if err = q.AddSession(ctx, db.AddSessionParams{TokenHash: digest(c.Token), RoomID: c.RoomID, ParticipantID: c.ParticipantID}); err != nil {
		return Credentials{}, err
	}
	if err = q.AddEvent(ctx, db.AddEventParams{RoomID: c.RoomID, Seq: 1, Kind: "room_created", ActorID: c.ParticipantID, Payload: []byte(`{}`)}); err != nil {
		return Credentials{}, err
	}
	if setup != nil {
		if err := setup.save(ctx, tx, c.RoomID); err != nil {
			return Credentials{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Credentials{}, err
	}
	return c, nil
}
func (s *Store) Join(ctx context.Context, roomID, invite, name string, role game.Role) (Credentials, error) {
	if len(invite) != 64 {
		return Credentials{}, game.Fault("unauthorized")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Credentials{}, err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	if err = s.checkOwner(ctx, q); err != nil {
		return Credentials{}, err
	}
	row, err := q.LockRoom(ctx, roomID)
	if err != nil {
		return Credentials{}, normalize(err)
	}
	if subtle.ConstantTimeCompare([]byte(row.InviteHash), []byte(digest(invite))) != 1 {
		return Credentials{}, game.Fault("unauthorized")
	}
	state, err := decode(row)
	if err != nil {
		return Credentials{}, err
	}
	c := Credentials{RoomID: roomID, ParticipantID: randomHex(16), Token: randomHex(32)}
	next, err := game.AddParticipant(state, game.Participant{ID: c.ParticipantID, Name: name, Role: role})
	if err != nil {
		return Credentials{}, err
	}
	next.Seq++
	if err = saveState(ctx, q, next, "participant_joined", c.ParticipantID, []byte(`{}`)); err != nil {
		return Credentials{}, err
	}
	if err = q.AddSession(ctx, db.AddSessionParams{TokenHash: digest(c.Token), RoomID: roomID, ParticipantID: c.ParticipantID}); err != nil {
		return Credentials{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Credentials{}, err
	}
	return c, nil
}
func saveState(ctx context.Context, q *db.Queries, state game.State, kind, actor string, payload []byte) error {
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err = q.UpdateRoom(ctx, db.UpdateRoomParams{ID: state.ID, Snapshot: b, Seq: state.Seq}); err != nil {
		return err
	}
	return q.AddEvent(ctx, db.AddEventParams{RoomID: state.ID, Seq: state.Seq, Kind: kind, ActorID: actor, Payload: payload})
}
