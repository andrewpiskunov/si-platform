package themes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"siplatform/internal/accounts"
	"siplatform/internal/library"
)

type Service struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

type themeState struct {
	id, owner        string
	current          int
	blocked, deleted bool
}
type serviceAccess struct {
	tx    pgx.Tx
	actor accounts.Identity
	theme themeState
}

func serviceError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrUnavailable
}
func serviceHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func lockTheme(ctx context.Context, tx pgx.Tx, id string) (themeState, error) {
	var state themeState
	err := tx.QueryRow(ctx, `SELECT id,owner_id,current_revision,blocked,deleted_at IS NOT NULL FROM themes WHERE id=$1 FOR UPDATE`, id).Scan(&state.id, &state.owner, &state.current, &state.blocked, &state.deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, ErrNotFound
	}
	return state, serviceError(ctx, err)
}

// Live account rows precede theme locks, matching account deletion and room
// application. Moderator and owner accounts are locked in deterministic order.
func (s *Service) begin(ctx context.Context, actor accounts.Identity, id string, allowDeleted bool) (*serviceAccess, error) {
	if actor.User.ID == "" || actor.SessionID == "" {
		return nil, ErrUnauthorized
	}
	if id != "" && !serviceHex(id, 32) {
		return nil, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	a := &serviceAccess{tx: tx, actor: actor}
	ok := false
	defer func() {
		if !ok {
			_ = tx.Rollback(context.Background())
		}
	}()
	ids := []string{actor.User.ID}
	owner := ""
	if id != "" {
		err = tx.QueryRow(ctx, `SELECT owner_id FROM themes WHERE id=$1`, id).Scan(&owner)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, serviceError(ctx, err)
		}
		ids = append(ids, owner)
	}
	rows, err := tx.Query(ctx, `SELECT id,role FROM account_users WHERE id=ANY($1) AND deleted_at IS NULL ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	alive := map[string]bool{}
	for rows.Next() {
		var user, role string
		if err = rows.Scan(&user, &role); err != nil {
			break
		}
		alive[user] = true
		if user == actor.User.ID {
			a.actor.User.Role = role
		}
	}
	rows.Close()
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	if err = rows.Err(); err != nil {
		return nil, serviceError(ctx, err)
	}
	if !alive[actor.User.ID] {
		return nil, ErrUnauthorized
	}
	if owner != "" && !alive[owner] {
		return nil, ErrNotFound
	}
	var kind string
	err = tx.QueryRow(ctx, `SELECT kind FROM account_credentials WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>clock_timestamp()`, actor.SessionID, actor.User.ID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	if kind != "session" {
		return nil, ErrForbidden
	}
	if id != "" {
		a.theme, err = lockTheme(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if a.theme.deleted && !allowDeleted {
			return nil, ErrNotFound
		}
	}
	ok = true
	return a, nil
}
func (a *serviceAccess) close() { _ = a.tx.Rollback(context.Background()) }
func (a *serviceAccess) owned() error {
	if a.theme.owner != a.actor.User.ID {
		return ErrNotFound
	}
	return nil
}

func loadVersion(ctx context.Context, tx pgx.Tx, state themeState, revision int) (Version, error) {
	var v Version
	var raw []byte
	if revision == 0 {
		revision = state.current
	}
	err := tx.QueryRow(ctx, `SELECT prepared,size_bytes FROM theme_versions WHERE theme_id=$1 AND revision=$2`, state.id, revision).Scan(&raw, &v.Card.SizeBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, serviceError(ctx, err)
	}
	if json.Unmarshal(raw, &v.Prepared) != nil {
		return v, ErrInvalid
	}
	if err = v.Prepared.Validate(); err != nil {
		return v, err
	}
	v.Card.ID = state.id
	v.Card.OwnerID = state.owner
	v.Card.Revision = revision
	v.Card.Blocked = state.blocked
	v.Card.Name = v.Prepared.Manifest.Name
	v.Card.Author = v.Prepared.Manifest.Author
	return v, nil
}
func (s *Service) List(ctx context.Context, actor accounts.Identity) ([]Card, error) {
	a, err := s.begin(ctx, actor, "", false)
	if err != nil {
		return nil, err
	}
	defer a.close()
	rows, err := a.tx.Query(ctx, `SELECT t.id,t.owner_id,v.prepared->'manifest'->>'name',v.prepared->'manifest'->>'author',v.revision,v.size_bytes,t.blocked FROM themes t JOIN theme_versions v ON v.theme_id=t.id AND v.revision=t.current_revision WHERE t.owner_id=$1 AND t.deleted_at IS NULL ORDER BY t.updated_at DESC,t.id`, a.actor.User.ID)
	if err != nil {
		return nil, serviceError(ctx, err)
	}
	defer rows.Close()
	result := []Card{}
	for rows.Next() {
		var c Card
		if err = rows.Scan(&c.ID, &c.OwnerID, &c.Name, &c.Author, &c.Revision, &c.SizeBytes, &c.Blocked); err != nil {
			return nil, serviceError(ctx, err)
		}
		result = append(result, c)
	}
	return result, serviceError(ctx, rows.Err())
}
func (s *Service) Get(ctx context.Context, actor accounts.Identity, id string, revision int) (Version, error) {
	if revision < 0 {
		return Version{}, ErrInvalid
	}
	a, err := s.begin(ctx, actor, id, false)
	if err != nil {
		return Version{}, err
	}
	defer a.close()
	if err = a.owned(); err != nil {
		return Version{}, err
	}
	// A blocked owner may still inspect and retrieve their own original. New
	// application and participant delivery use the stricter Pinned checks.
	return loadVersion(ctx, a.tx, a.theme, revision)
}
func (s *Service) Attach(ctx context.Context, actor accounts.Identity, id string, baseRevision int, uploadID string) (Version, error) {
	a, err := s.begin(ctx, actor, id, false)
	if err != nil {
		return Version{}, err
	}
	defer a.close()
	if id != "" {
		if err = a.owned(); err != nil {
			return Version{}, err
		}
	}
	if !serviceHex(uploadID, 32) {
		return Version{}, ErrInvalid
	}
	var receiptID string
	var receiptRevision int
	err = a.tx.QueryRow(ctx, `SELECT r.theme_id,r.revision FROM theme_receipts r JOIN uploads u ON u.id=r.upload_id WHERE r.upload_id=$1 AND u.owner_id=$2`, uploadID, a.actor.User.ID).Scan(&receiptID, &receiptRevision)
	if err == nil {
		if id != "" && id != receiptID {
			return Version{}, ErrConflict
		}
		if id == "" {
			a.theme, err = lockTheme(ctx, a.tx, receiptID)
			if err != nil {
				return Version{}, err
			}
		}
		if a.theme.deleted {
			return Version{}, ErrNotFound
		}
		if a.theme.blocked {
			return Version{}, ErrBlocked
		}
		return loadVersion(ctx, a.tx, a.theme, receiptRevision)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Version{}, serviceError(ctx, err)
	}
	if a.theme.blocked {
		return Version{}, ErrBlocked
	}
	if id == "" && baseRevision != 0 || id != "" && baseRevision != a.theme.current {
		return Version{}, ErrConflict
	}
	if id != "" && a.theme.current >= 100 {
		return Version{}, ErrQuota
	}
	var raw []byte
	var size int64
	err = a.tx.QueryRow(ctx, `SELECT result_theme,size FROM uploads WHERE id=$1 AND owner_id=$2 AND purpose='theme' AND status='ready' FOR UPDATE`, uploadID, a.actor.User.ID).Scan(&raw, &size)
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	if err != nil {
		return Version{}, serviceError(ctx, err)
	}
	var prepared Prepared
	if json.Unmarshal(raw, &prepared) != nil || prepared.OriginalSize != size {
		return Version{}, ErrInvalid
	}
	if err = prepared.Validate(); err != nil {
		return Version{}, err
	}
	used, err := library.StorageUsage(ctx, a.tx, a.actor.User.ID)
	if err != nil {
		return Version{}, serviceError(ctx, err)
	}
	// The ready upload already pays these bytes. A global receipt transfers that
	// charge exactly once into retained version storage, including after deletion.
	_, storageLimit, err := library.AccountLimits(ctx, a.tx, a.actor.User.ID)
	if err != nil {
		return Version{}, serviceError(ctx, err)
	}
	if used > storageLimit {
		return Version{}, ErrQuota
	}
	if id == "" {
		var count int
		if err = a.tx.QueryRow(ctx, `SELECT count(*) FROM themes WHERE owner_id=$1`, a.actor.User.ID).Scan(&count); err != nil {
			return Version{}, serviceError(ctx, err)
		}
		if count >= 100 {
			return Version{}, ErrQuota
		}
		var random [16]byte
		if _, err = rand.Read(random[:]); err != nil {
			return Version{}, ErrUnavailable
		}
		id = hex.EncodeToString(random[:])
		if _, err = a.tx.Exec(ctx, `INSERT INTO themes(id,owner_id,current_revision) VALUES($1,$2,1)`, id, a.actor.User.ID); err != nil {
			return Version{}, serviceError(ctx, err)
		}
		a.theme = themeState{id: id, owner: a.actor.User.ID, current: 1}
	} else {
		a.theme.current++
	}
	if _, err = a.tx.Exec(ctx, `INSERT INTO theme_versions(theme_id,revision,prepared,size_bytes) VALUES($1,$2,$3,$4)`, id, a.theme.current, raw, prepared.SizeBytes); err != nil {
		return Version{}, serviceError(ctx, err)
	}
	if _, err = a.tx.Exec(ctx, `UPDATE themes SET current_revision=$2,updated_at=clock_timestamp() WHERE id=$1`, id, a.theme.current); err != nil {
		return Version{}, serviceError(ctx, err)
	}
	if _, err = a.tx.Exec(ctx, `INSERT INTO theme_receipts(upload_id,theme_id,revision) VALUES($1,$2,$3)`, uploadID, id, a.theme.current); err != nil {
		return Version{}, serviceError(ctx, err)
	}
	v, err := loadVersion(ctx, a.tx, a.theme, a.theme.current)
	if err != nil {
		return Version{}, err
	}
	return v, serviceError(ctx, a.tx.Commit(ctx))
}
func (s *Service) Delete(ctx context.Context, actor accounts.Identity, id string) error {
	a, err := s.begin(ctx, actor, id, false)
	if err != nil {
		return err
	}
	defer a.close()
	if err = a.owned(); err != nil {
		return err
	}
	if _, err = a.tx.Exec(ctx, `UPDATE themes SET deleted_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		return serviceError(ctx, err)
	}
	return serviceError(ctx, a.tx.Commit(ctx))
}
func (s *Service) Block(ctx context.Context, actor accounts.Identity, id string, blocked bool, reason string) error {
	a, err := s.begin(ctx, actor, id, true)
	if err != nil {
		return err
	}
	defer a.close()
	if a.actor.User.Role != "moderator" && a.actor.User.Role != "admin" {
		return ErrForbidden
	}
	if !utf8.ValidString(reason) || strings.TrimSpace(reason) == "" || utf8.RuneCountInString(reason) > 2000 {
		return ErrInvalid
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return ErrInvalid
		}
	}
	if _, err = a.tx.Exec(ctx, `UPDATE themes SET blocked=$2,updated_at=clock_timestamp() WHERE id=$1`, id, blocked); err != nil {
		return serviceError(ctx, err)
	}
	if _, err = a.tx.Exec(ctx, `INSERT INTO theme_moderation(theme_id,actor_id,blocked,reason) VALUES($1,$2,$3,$4)`, id, a.actor.User.ID, blocked, reason); err != nil {
		return serviceError(ctx, err)
	}
	return serviceError(ctx, a.tx.Commit(ctx))
}

// Pinned is a trusted room-delivery primitive. Ordinary library deletion keeps
// immutable versions available; moderation and owner deletion revoke delivery.
func (s *Service) Pinned(ctx context.Context, id string, revision int, hash string) (Version, error) {
	if !serviceHex(id, 32) || revision < 1 || !serviceHex(hash, 64) {
		return Version{}, ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Version{}, serviceError(ctx, err)
	}
	defer tx.Rollback(context.Background())
	var state themeState
	err = tx.QueryRow(ctx, `SELECT owner_id FROM themes WHERE id=$1`, id).Scan(&state.owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	if err != nil {
		return Version{}, serviceError(ctx, err)
	}
	var owner string
	err = tx.QueryRow(ctx, `SELECT id FROM account_users WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, state.owner).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	if err != nil {
		return Version{}, serviceError(ctx, err)
	}
	err = tx.QueryRow(ctx, `SELECT id,owner_id,current_revision,blocked FROM themes WHERE id=$1 FOR SHARE`, id).Scan(&state.id, &state.owner, &state.current, &state.blocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	if err != nil {
		return Version{}, serviceError(ctx, err)
	}
	if state.blocked {
		return Version{}, ErrBlocked
	}
	v, err := loadVersion(ctx, tx, state, revision)
	if err != nil {
		return Version{}, err
	}
	if v.Prepared.Hash != hash {
		return Version{}, ErrNotFound
	}
	return v, nil
}
