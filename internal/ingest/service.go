// Package ingest provides bounded resumable uploads and durable private SIQ imports.
package ingest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"siplatform/internal/accounts"
	"siplatform/internal/blob"
	"siplatform/internal/library"
	"siplatform/internal/media"
	"siplatform/internal/themes"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not_found")
	ErrConflict     = errors.New("conflict")
	ErrInvalid      = errors.New("invalid")
	ErrQuota        = errors.New("quota")
	ErrUnavailable  = errors.New("unavailable")
)

const maxUpload int64 = 250 << 20
const maxChunk int64 = 4 << 20
const uploadColumns = `id,offset_bytes,size,status,draft_id,error,purpose,result_asset,result_theme,diagnostic_issues`

type Options struct {
	Directory string
	Media     *media.Processor
}
type Upload struct {
	ID      string           `json:"id"`
	Offset  int64            `json:"offset"`
	Size    int64            `json:"size"`
	Status  string           `json:"status"`
	DraftID string           `json:"draft_id,omitempty"`
	Error   string           `json:"error,omitempty"`
	Purpose string           `json:"purpose"`
	Asset   *library.Asset   `json:"asset,omitempty"`
	Theme   *themes.Prepared `json:"theme,omitempty"`
	Issues  []themes.Issue   `json:"issues,omitempty"`
}
type Service struct {
	pool            *pgxpool.Pool
	objects         blob.Store
	lib             *library.Service
	media           *media.Processor
	root            *os.Root
	ctx             context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	started, closed bool
	done            chan struct{}
}

func New(pool *pgxpool.Pool, objects blob.Store, lib *library.Service, options Options) (*Service, error) {
	if pool == nil || objects == nil || lib == nil || strings.TrimSpace(options.Directory) == "" {
		return nil, ErrInvalid
	}
	path, err := filepath.Abs(options.Directory)
	if err != nil || path == string(filepath.Separator) {
		return nil, ErrInvalid
	}
	if err = os.MkdirAll(path, 0700); err != nil {
		return nil, ErrUnavailable
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalid
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{pool: pool, objects: objects, lib: lib, media: options.Media, root: root, ctx: ctx, cancel: cancel, done: make(chan struct{})}, nil
}
func (s *Service) Close() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.cancel()
		if !s.started {
			close(s.done)
		}
	}
	done := s.done
	s.mu.Unlock()
	<-done
	// os.Root.Close is safe for repeated calls and concurrent operations.
	_ = s.root.Close()
}
func safeError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrUnavailable
}
func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", ErrUnavailable
	}
	return hex.EncodeToString(b[:]), nil
}
func validName(name string) bool {
	if strings.TrimSpace(name) == "" || len(name) > 255 || !utf8.ValidString(name) || strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func scanUpload(row pgx.Row) (Upload, error) {
	var u Upload
	var asset, theme, issues []byte
	err := row.Scan(&u.ID, &u.Offset, &u.Size, &u.Status, &u.DraftID, &u.Error, &u.Purpose, &asset, &theme, &issues)
	if err == nil && len(asset) > 0 {
		err = json.Unmarshal(asset, &u.Asset)
	}
	if err == nil && len(theme) > 0 {
		err = json.Unmarshal(theme, &u.Theme)
	}
	if err == nil && len(issues) > 0 {
		err = json.Unmarshal(issues, &u.Issues)
	}
	return u, err
}

// Authorization is repeated in the transaction, under the account lock used by
// credential revocation/deletion. Caller identity never supplies effective scopes.
func (s *Service) begin(ctx context.Context, actor accounts.Identity, write bool) (pgx.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	if actor.User.ID == "" || actor.SessionID == "" {
		return nil, ErrUnauthorized
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, safeError(ctx, err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = tx.Rollback(context.Background())
		}
	}()
	var id string
	err = tx.QueryRow(ctx, `SELECT id FROM account_users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, actor.User.ID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, safeError(ctx, err)
	}
	var kind string
	var scopes []string
	err = tx.QueryRow(ctx, `SELECT kind,scopes FROM account_credentials WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>clock_timestamp()`, actor.SessionID, actor.User.ID).Scan(&kind, &scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, safeError(ctx, err)
	}
	if kind == "api" {
		allowed := false
		for _, scope := range scopes {
			if scope == "drafts:write" || (!write && scope == "drafts:read") {
				allowed = true
			}
		}
		if !allowed {
			return nil, ErrForbidden
		}
	}
	ok = true
	return tx, nil
}
func owned(ctx context.Context, tx pgx.Tx, owner, id string) (Upload, error) {
	if !validID(id) {
		return Upload{}, ErrNotFound
	}
	u, err := scanUpload(tx.QueryRow(ctx, `SELECT `+uploadColumns+` FROM uploads WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, owner))
	if errors.Is(err, pgx.ErrNoRows) {
		return Upload{}, ErrNotFound
	}
	return u, safeError(ctx, err)
}
func (s *Service) Create(ctx context.Context, actor accounts.Identity, name string, size int64) (Upload, error) {
	return s.create(ctx, actor, name, size, "siq", "", nil)
}

func (s *Service) CreateMedia(ctx context.Context, actor accounts.Identity, name string, size int64, kind string) (Upload, error) {
	if kind != "image" && kind != "audio" && kind != "video" || size > 100<<20 {
		return Upload{}, ErrInvalid
	}
	if strings.ContainsAny(name, "%:") {
		return Upload{}, ErrInvalid
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".zip", ".siq", ".7z", ".rar", ".tar", ".gz":
		return Upload{}, ErrInvalid
	}
	return s.create(ctx, actor, name, size, "media", kind, nil)
}

func (s *Service) create(ctx context.Context, actor accounts.Identity, name string, size int64, purpose, kind string, catalogue *CatalogueInput) (Upload, error) {
	if !validName(name) || size <= 0 || size > maxUpload {
		return Upload{}, ErrInvalid
	}
	tx, err := s.begin(ctx, actor, true)
	if err != nil {
		return Upload{}, err
	}
	defer tx.Rollback(context.Background())
	if catalogue != nil {
		replay, found, e := catalogueReplay(ctx, tx, actor.User.ID, *catalogue)
		if e != nil {
			return Upload{}, e
		}
		if found {
			return replay, nil
		}
	}
	if purpose == "theme" {
		if err = requireThemeSession(ctx, tx, actor, purpose); err != nil {
			return Upload{}, err
		}
	}
	// All instances share the same quota lock, scoped to this database/schema.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_database()||':'||current_schema()||':si-ingest-quota',0))`); err != nil {
		return Upload{}, safeError(ctx, err)
	}
	if _, err = tx.Exec(ctx, `UPDATE uploads SET status='failed',error='expired',updated_at=now() WHERE status IN ('uploading','queued','processing') AND expires_at<=clock_timestamp()`); err != nil {
		return Upload{}, safeError(ctx, err)
	}
	var total, own int
	var storage int64
	err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE owner_id=$1),coalesce(sum(size),0) FROM uploads WHERE status IN ('uploading','queued','processing')`, actor.User.ID).Scan(&total, &own, &storage)
	if err != nil {
		return Upload{}, safeError(ctx, err)
	}
	if total >= 16 || own >= 2 || storage+size > 4<<30 {
		return Upload{}, ErrQuota
	}
	id, err := randomID()
	if err != nil {
		return Upload{}, err
	}
	file, err := s.root.OpenFile(id, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return Upload{}, ErrUnavailable
	}
	commitAttempted := false
	defer func() {
		if !commitAttempted {
			_ = s.root.Remove(id)
		}
	}()
	err = file.Sync()
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return Upload{}, ErrUnavailable
	}
	dir, err := s.root.Open(".")
	if err != nil {
		return Upload{}, ErrUnavailable
	}
	err = dir.Sync()
	_ = dir.Close()
	if err != nil {
		return Upload{}, ErrUnavailable
	}
	u, err := scanUpload(tx.QueryRow(ctx, `INSERT INTO uploads(id,owner_id,credential_id,name,size,purpose,media_kind) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+uploadColumns, id, actor.User.ID, actor.SessionID, name, size, purpose, kind))
	if err != nil {
		return Upload{}, safeError(ctx, err)
	}
	if catalogue != nil {
		if err = catalogueInsert(ctx, tx, actor.User.ID, id, *catalogue); err != nil {
			return Upload{}, err
		}
	}
	// Commit may succeed even if its acknowledgement is lost. Preserve staging
	// once attempted; cleanupOrphans removes files only after proving no DB row.
	commitAttempted = true
	if err = tx.Commit(ctx); err != nil {
		return Upload{}, safeError(ctx, err)
	}
	return u, nil
}
func (s *Service) Get(ctx context.Context, actor accounts.Identity, id string) (Upload, error) {
	tx, err := s.begin(ctx, actor, false)
	if err != nil {
		return Upload{}, err
	}
	defer tx.Rollback(context.Background())
	u, err := owned(ctx, tx, actor.User.ID, id)
	if err != nil {
		return Upload{}, err
	}
	if err = requireThemeSession(ctx, tx, actor, u.Purpose); err != nil {
		return Upload{}, err
	}
	return u, safeError(ctx, tx.Commit(ctx))
}
func (s *Service) openFile(id string, flag int) (*os.File, error) {
	if !validID(id) {
		return nil, ErrInvalid
	}
	before, err := s.root.Lstat(id)
	if err != nil || !before.Mode().IsRegular() {
		return nil, ErrUnavailable
	}
	file, err := s.root.OpenFile(id, flag, 0)
	if err != nil {
		return nil, ErrUnavailable
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, ErrUnavailable
	}
	return file, nil
}
func active(ctx context.Context, tx pgx.Tx, u Upload) error {
	if u.Status != "uploading" {
		return ErrConflict
	}
	var alive bool
	if err := tx.QueryRow(ctx, `SELECT expires_at>clock_timestamp() FROM uploads WHERE id=$1`, u.ID).Scan(&alive); err != nil {
		return safeError(ctx, err)
	}
	if !alive {
		return ErrConflict
	}
	return nil
}
func (s *Service) Append(ctx context.Context, actor accounts.Identity, id string, offset int64, body io.Reader, size int64) (Upload, error) {
	if offset < 0 || size <= 0 || size > maxChunk || body == nil {
		return Upload{}, ErrInvalid
	}
	tx, err := s.begin(ctx, actor, true)
	if err != nil {
		return Upload{}, err
	}
	defer tx.Rollback(context.Background())
	u, err := owned(ctx, tx, actor.User.ID, id)
	if err != nil {
		return Upload{}, err
	}
	if err = requireThemeSession(ctx, tx, actor, u.Purpose); err != nil {
		return Upload{}, err
	}
	if err = active(ctx, tx, u); err != nil {
		return Upload{}, err
	}
	if offset != u.Offset {
		return Upload{}, ErrConflict
	}
	if size > u.Size-u.Offset {
		return Upload{}, ErrInvalid
	}
	file, err := s.openFile(id, os.O_RDWR)
	if err != nil {
		return Upload{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() < u.Offset {
		return Upload{}, ErrUnavailable
	}
	if err = file.Truncate(u.Offset); err != nil {
		return Upload{}, ErrUnavailable
	}
	if _, err = file.Seek(u.Offset, io.SeekStart); err != nil {
		return Upload{}, ErrUnavailable
	}
	n, err := io.CopyBuffer(file, io.LimitReader(contextReader{ctx, body}, size+1), make([]byte, 32<<10))
	if err != nil || n != size {
		_ = file.Truncate(u.Offset)
		_ = file.Sync()
		if ctx.Err() != nil {
			return Upload{}, ctx.Err()
		}
		if err != nil {
			return Upload{}, ErrInvalid
		}
		return Upload{}, ErrInvalid
	}
	if err = file.Sync(); err != nil {
		return Upload{}, ErrUnavailable
	}
	u, err = scanUpload(tx.QueryRow(ctx, `UPDATE uploads SET offset_bytes=offset_bytes+$2,updated_at=now() WHERE id=$1 RETURNING `+uploadColumns, id, size))
	if err != nil {
		return Upload{}, safeError(ctx, err)
	}
	return u, safeError(ctx, tx.Commit(ctx))
}
func (s *Service) Complete(ctx context.Context, actor accounts.Identity, id string) (Upload, error) {
	tx, err := s.begin(ctx, actor, true)
	if err != nil {
		return Upload{}, err
	}
	defer tx.Rollback(context.Background())
	u, err := owned(ctx, tx, actor.User.ID, id)
	if err != nil {
		return Upload{}, err
	}
	if err = requireThemeSession(ctx, tx, actor, u.Purpose); err != nil {
		return Upload{}, err
	}
	if u.Status == "uploading" {
		if err = active(ctx, tx, u); err != nil {
			return Upload{}, err
		}
		if u.Offset != u.Size {
			return Upload{}, ErrConflict
		}
		// Completion keeps the credential that originally authorized this upload.
		u, err = scanUpload(tx.QueryRow(ctx, `UPDATE uploads SET status='queued',updated_at=now() WHERE id=$1 RETURNING `+uploadColumns, id))
		if err != nil {
			return Upload{}, safeError(ctx, err)
		}
	}
	return u, safeError(ctx, tx.Commit(ctx))
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	if cancel := r.ctx.Err(); cancel != nil {
		return n, cancel
	}
	return n, err
}
