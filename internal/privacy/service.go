// Package privacy exports explicitly selected account data to a caller-owned
// staging writer. Callers must not publish the archive until Export succeeds.
package privacy

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"siplatform/internal/accounts"
	"siplatform/internal/blob"
)

var (
	ErrUnauthorized = errors.New("privacy: unauthorized")
	ErrForbidden    = errors.New("privacy: forbidden")
	ErrNotFound     = errors.New("privacy: not found")
	ErrInvalid      = errors.New("privacy: invalid")
	ErrBusy         = errors.New("privacy: busy")
	ErrTooLarge     = errors.New("privacy: too large")
	ErrUnavailable  = errors.New("privacy: unavailable")
)

const MaxBytes int64 = 20 << 30
const MaxObjects = 100000
const MaxDuration = 15 * time.Minute

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var keyPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Options struct {
	Kind, ID     string
	MetadataOnly bool
}
type Subject struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}
type SubjectPage struct {
	Items      []Subject `json:"items"`
	NextCursor string    `json:"next_cursor"`
}
type Service struct {
	pool       *pgxpool.Pool
	objects    blob.Store
	maxBytes   int64
	maxObjects int
}

func New(pool *pgxpool.Pool, objects blob.Store) *Service {
	return &Service{pool: pool, objects: objects, maxBytes: MaxBytes, maxObjects: MaxObjects}
}

type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func auth(ctx context.Context, q querier, a accounts.Identity, lock bool) error {
	if a.APIToken {
		return ErrForbidden
	}
	if a.User.ID == "" || a.SessionID == "" {
		return ErrUnauthorized
	}
	var id string
	sql := `SELECT id FROM account_users WHERE id=$1 AND deleted_at IS NULL`
	if lock {
		sql += ` FOR UPDATE`
	}
	if e := q.QueryRow(ctx, sql, a.User.ID).Scan(&id); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrUnauthorized
		}
		return e
	}
	var valid bool
	e := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM account_credentials WHERE id=$1 AND user_id=$2 AND kind='session' AND revoked_at IS NULL AND expires_at>clock_timestamp())`, a.SessionID, a.User.ID).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return ErrUnauthorized
	}
	return nil
}
func safe(ctx context.Context, e error) error {
	if e == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, v := range []error{ErrUnauthorized, ErrForbidden, ErrNotFound, ErrInvalid, ErrBusy, ErrTooLarge, ErrUnavailable} {
		if errors.Is(e, v) {
			return v
		}
	}
	return ErrUnavailable
}
func (s *Service) acquire(ctx context.Context, a accounts.Identity) (string, error) {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return "", e
	}
	defer tx.Rollback(context.Background())
	if e = auth(ctx, tx, a, true); e != nil {
		return "", e
	}
	b := make([]byte, 16)
	if _, e = rand.Read(b); e != nil {
		return "", e
	}
	id := hex.EncodeToString(b)
	tag, e := tx.Exec(ctx, `INSERT INTO account_export_leases(owner_id,id,expires_at) VALUES($1,$2,clock_timestamp()+interval '20 minutes') ON CONFLICT(owner_id) DO UPDATE SET id=EXCLUDED.id,expires_at=EXCLUDED.expires_at WHERE account_export_leases.expires_at<=clock_timestamp()`, a.User.ID, id)
	if e != nil {
		return "", e
	}
	if tag.RowsAffected() != 1 {
		return "", ErrBusy
	}
	return id, tx.Commit(ctx)
}
func (s *Service) release(owner, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.pool.Exec(ctx, `DELETE FROM account_export_leases WHERE owner_id=$1 AND id=$2`, owner, id)
}
func (s *Service) Export(ctx context.Context, a accounts.Identity, o Options, w io.Writer) (err error) {
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	defer func() { err = safe(ctx, err) }()
	if (o.Kind == "") != (o.ID == "") || (o.Kind != "" && (!validKind(o.Kind) || !idPattern.MatchString(o.ID))) || w == nil {
		return ErrInvalid
	}
	lease, e := s.acquire(ctx, a)
	if e != nil {
		return e
	}
	defer s.release(a.User.ID, lease)
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if e = auth(ctx, tx, a, false); e != nil {
		return e
	}
	if o.Kind != "" {
		var exists bool
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+subjectTable(o.Kind)+` WHERE id=$1 AND owner_id=$2)`, o.ID, a.User.ID).Scan(&exists)
		if e != nil {
			return e
		}
		if !exists {
			return ErrNotFound
		}
	}
	blocking, e := blockingState(ctx, tx, a.User.ID, o)
	if e != nil {
		return e
	}
	limit := &boundedWriter{ctx: ctx, w: w, left: s.maxBytes}
	z := zip.NewWriter(limit)
	f, e := z.Create("FORMAT.txt")
	if e != nil {
		return e
	}
	if _, e = io.WriteString(f, "Account data export v1. NDJSON records reflect the saved snapshot at preparation start. Objects use objects/<sha256>; original names remain metadata. Temporary unfinished uploads, credentials, sessions, invitations and room data are excluded. Blocked objects are omitted with reasons in objects.ndjson. Metadata-only exports contain no object bytes. Do not interpret object keys as server URLs.\n"); e != nil {
		return e
	}
	var snapshotAt time.Time
	if e = tx.QueryRow(ctx, `SELECT transaction_timestamp()`).Scan(&snapshotAt); e != nil {
		return e
	}
	manifest, e := z.Create("manifest.json")
	if e != nil {
		return e
	}
	if e = json.NewEncoder(manifest).Encode(struct {
		Version      int       `json:"version"`
		SnapshotAt   time.Time `json:"snapshot_at"`
		Kind         string    `json:"kind"`
		ID           string    `json:"id"`
		MetadataOnly bool      `json:"metadata_only"`
	}{1, snapshotAt, o.Kind, o.ID, o.MetadataOnly}); e != nil {
		return e
	}
	included, e := s.writeData(ctx, tx, z, a.User.ID, o)
	if e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	if e = z.Close(); e != nil {
		return e
	}
	// No archive or object IO occurs while the live account is locked.
	final, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer final.Rollback(context.Background())
	if e = auth(ctx, final, a, true); e != nil {
		return e
	}
	currentBlocking, e := blockingState(ctx, final, a.User.ID, o)
	if e != nil {
		return e
	}
	if currentBlocking != blocking {
		return ErrForbidden
	}
	if !o.MetadataOnly {
		current, e := s.references(ctx, final, a.User.ID, Options{}, true, included)
		if e != nil {
			return e
		}
		for k := range included {
			if _, blocked := current[k]; blocked {
				return ErrForbidden
			}
		}
	}
	if e = auth(ctx, final, a, false); e != nil {
		return e
	}
	return final.Commit(ctx)
}
func validKind(k string) bool { return k == "pack" || k == "theme" || k == "upload" }
func subjectTable(k string) string {
	switch k {
	case "pack":
		return "library_packs"
	case "theme":
		return "themes"
	default:
		return "uploads"
	}
}
func (s *Service) Subjects(ctx context.Context, a accounts.Identity, kind, after string) (page SubjectPage, err error) {
	defer func() { err = safe(ctx, err) }()
	page.Items = []Subject{}
	if !validKind(kind) || (after != "" && !idPattern.MatchString(after)) {
		return page, ErrInvalid
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return page, e
	}
	defer tx.Rollback(context.Background())
	if err = auth(ctx, tx, a, true); err != nil {
		return page, err
	}
	query := `SELECT id,name,false FROM uploads WHERE owner_id=$1 AND id>$2 ORDER BY id LIMIT 51`
	if kind == "pack" {
		query = `SELECT p.id,coalesce(r.card->>'name',''),p.deleted_at IS NOT NULL FROM library_packs p JOIN library_revisions r ON r.pack_id=p.id AND r.revision=p.current_revision WHERE p.owner_id=$1 AND p.id>$2 ORDER BY p.id LIMIT 51`
	}
	if kind == "theme" {
		query = `SELECT p.id,coalesce(r.prepared->'manifest'->>'name',''),p.deleted_at IS NOT NULL FROM themes p JOIN theme_versions r ON r.theme_id=p.id AND r.revision=p.current_revision WHERE p.owner_id=$1 AND p.id>$2 ORDER BY p.id LIMIT 51`
	}
	rows, e := tx.Query(ctx, query, a.User.ID, after)
	if e != nil {
		return page, e
	}
	defer rows.Close()
	for rows.Next() {
		var v Subject
		if e = rows.Scan(&v.ID, &v.Name, &v.Deleted); e != nil {
			return page, e
		}
		page.Items = append(page.Items, v)
	}
	if len(page.Items) > 50 {
		page.Items = page.Items[:50]
		page.NextCursor = page.Items[49].ID
	}
	if e = rows.Err(); e != nil {
		return page, e
	}
	rows.Close()
	return page, tx.Commit(ctx)
}

type boundedWriter struct {
	ctx  context.Context
	w    io.Writer
	left int64
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if e := w.ctx.Err(); e != nil {
		return 0, e
	}
	if int64(len(p)) > w.left {
		return 0, ErrTooLarge
	}
	n, e := w.w.Write(p)
	w.left -= int64(n)
	return n, e
}

// A compact snapshot signature also catches newly blocked materials without
// files, including metadata-only preparations. Blob aliases are checked separately.
func blockingState(ctx context.Context, q querier, owner string, o Options) (string, error) {
	var state string
	e := q.QueryRow(ctx, `SELECT md5(coalesce(string_agg(x,',' ORDER BY x),'')) FROM (
 SELECT 'p:'||p.id||':'||r.revision::text AS x FROM library_packs p JOIN library_revisions r ON r.pack_id=p.id
 WHERE p.owner_id=$1 AND (p.blocked OR r.status='blocked') AND ($2='' OR ($2='pack' AND p.id=$3))
 UNION ALL SELECT 't:'||id FROM themes WHERE owner_id=$1 AND blocked AND ($2='' OR ($2='theme' AND id=$3))
 ) states`, owner, o.Kind, o.ID).Scan(&state)
	return state, e
}
