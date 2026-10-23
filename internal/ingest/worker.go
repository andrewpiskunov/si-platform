package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"siplatform/internal/accounts"
	"siplatform/internal/library"
	"siplatform/internal/packs"
	"siplatform/internal/siq"
)

const leaseSQL = `SELECT pg_try_advisory_lock(hashtextextended(current_database()||':'||current_schema()||':si-ingest-owner',0))`

// Start is idempotent. A standby waits for the singleton lease without changing
// jobs. The dedicated lease connection is never borrowed from the request pool.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.closed {
		return
	}
	s.started = true
	go func() { defer close(s.done); stop := context.AfterFunc(ctx, s.cancel); defer stop(); s.run() }()
}
func closeConn(conn *pgx.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = conn.Close(ctx)
}
func pause(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func (s *Service) run() {
	for s.ctx.Err() == nil {
		s.runLease()
		if !pause(s.ctx, 250*time.Millisecond) {
			return
		}
	}
}

// Each lease lifetime owns a worker/cleanup group. Its deferred cancellation and
// join finish before the supervisor may reacquire and advance the fencing epoch.
func (s *Service) runLease() {
	var conn *pgx.Conn
	for s.ctx.Err() == nil {
		candidate, err := pgx.ConnectConfig(s.ctx, s.pool.Config().ConnConfig.Copy())
		if err == nil {
			var locked bool
			err = candidate.QueryRow(s.ctx, leaseSQL).Scan(&locked)
			if err == nil && locked {
				conn = candidate
				break
			}
			closeConn(candidate)
		}
		if !pause(s.ctx, 250*time.Millisecond) {
			return
		}
	}
	if conn == nil {
		return
	}
	defer closeConn(conn)
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return
	}
	var epoch int64
	err = tx.QueryRow(ctx, `UPDATE ingest_owner SET epoch=epoch+1 WHERE singleton=true RETURNING epoch`).Scan(&epoch)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE uploads SET status='queued',updated_at=now() WHERE status='processing'`)
	}
	if err != nil {
		_ = tx.Rollback(context.Background())
		return
	}
	if err = tx.Commit(ctx); err != nil {
		return
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); s.worker(ctx, epoch) }()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			s.cleanup(ctx, epoch)
			if !pause(ctx, time.Minute) {
				return
			}
		}
	}()
	defer func() { cancel(); wg.Wait() }()
	heartbeat := time.NewTicker(250 * time.Millisecond)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			check, cancelPing := context.WithTimeout(ctx, time.Second)
			err = conn.Ping(check)
			cancelPing()
			if err != nil {
				return
			}
		}
	}
}

type job struct {
	Upload
	owner, credential string
	name, kind        string
	expires           time.Time
}

func (s *Service) claim(ctx context.Context, epoch int64) (job, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return job{}, err
	}
	defer tx.Rollback(context.Background())
	var current int64
	if err = tx.QueryRow(ctx, `SELECT epoch FROM ingest_owner WHERE singleton=true FOR SHARE`).Scan(&current); err != nil {
		return job{}, err
	}
	if current != epoch {
		return job{}, ErrUnavailable
	}
	var j job
	var rawAsset, rawTheme, rawIssues []byte
	err = tx.QueryRow(ctx, `UPDATE uploads SET status='processing',lease_epoch=$1,updated_at=now()
 WHERE id=(SELECT id FROM uploads WHERE status='queued' AND expires_at>clock_timestamp() ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1)
 RETURNING `+uploadColumns+`,owner_id,credential_id,expires_at,name,media_kind`, epoch).Scan(&j.ID, &j.Offset, &j.Size, &j.Status, &j.DraftID, &j.Error, &j.Purpose, &rawAsset, &rawTheme, &rawIssues, &j.owner, &j.credential, &j.expires, &j.name, &j.kind)
	if err != nil {
		return job{}, err
	}
	return j, tx.Commit(ctx)
}
func (s *Service) worker(ctx context.Context, epoch int64) {
	for ctx.Err() == nil {
		j, err := s.claim(ctx, epoch)
		if err != nil {
			if !pause(ctx, 100*time.Millisecond) {
				return
			}
			continue
		}
		deadline := time.Now().Add(5 * time.Minute)
		if j.expires.Before(deadline) {
			deadline = j.expires
		}
		jobCtx, cancel := context.WithDeadline(ctx, deadline)
		if j.Purpose == "theme" {
			theme, issues, code := s.processTheme(jobCtx, j, epoch)
			cancel()
			if ctx.Err() != nil {
				return
			}
			s.finishTheme(ctx, j, epoch, theme, issues, code)
			continue
		}
		draftID, asset, code := s.process(jobCtx, j, epoch)
		cancel()
		// A canceled lease leaves the processing state for the next lease owner.
		if ctx.Err() != nil {
			return
		}
		if code == "" && draftID == "" && asset == nil {
			code = "import_failed"
		}
		s.finish(ctx, j, epoch, draftID, asset, code)
	}
}
func (s *Service) liveActor(ctx context.Context, j job) (accounts.Identity, error) {
	a := accounts.Identity{SessionID: j.credential}
	var kind string
	err := s.pool.QueryRow(ctx, `SELECT u.id,u.display_name,u.role,c.kind,c.scopes FROM account_users u JOIN account_credentials c ON c.user_id=u.id
 WHERE u.id=$1 AND c.id=$2 AND u.deleted_at IS NULL AND c.revoked_at IS NULL AND c.expires_at>clock_timestamp()`, j.owner, j.credential).Scan(&a.User.ID, &a.User.DisplayName, &a.User.Role, &kind, &a.Scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrUnauthorized
	}
	if err != nil {
		return a, err
	}
	a.APIToken = kind == "api"
	if a.APIToken {
		if j.Purpose == "theme" {
			return a, ErrForbidden
		}
		allowed := false
		for _, scope := range a.Scopes {
			if scope == "drafts:write" {
				allowed = true
			}
		}
		if !allowed {
			return a, ErrForbidden
		}
	}
	return a, nil
}
func (s *Service) process(ctx context.Context, j job, epoch int64) (string, *library.Asset, string) {
	if j.Purpose == "media" {
		asset, code := s.processMedia(ctx, j, epoch)
		return "", asset, code
	}
	draftID, code := s.processArchive(ctx, j, epoch)
	return draftID, nil, code
}
func (s *Service) processArchive(ctx context.Context, j job, epoch int64) (string, string) {
	actor, err := s.liveActor(ctx, j)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrForbidden) {
			return "", "authorization_expired"
		}
		return "", "storage_unavailable"
	}
	file, err := s.openFile(j.ID, os.O_RDONLY)
	if err != nil {
		return "", "staging_unavailable"
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() != j.Size {
		return "", "staging_unavailable"
	}
	// Reject malformed containers before any object can outlive staged input.
	imported, err := siq.Read(ctx, file, j.Size, siq.DefaultLimits())
	if err != nil {
		return "", importError(ctx, "invalid_archive")
	}
	hash := sha256.New()
	if _, err = io.CopyBuffer(hash, contextReader{ctx, file}, make([]byte, 32<<10)); err != nil {
		return "", importError(ctx, "staging_unavailable")
	}
	key := hex.EncodeToString(hash.Sum(nil))
	var expected string
	checksumErr := s.pool.QueryRow(ctx, `SELECT sha256 FROM catalogue_imports WHERE upload_id=$1 AND owner_id=$2`, j.ID, actor.User.ID).Scan(&expected)
	if checksumErr != nil && !errors.Is(checksumErr, pgx.ErrNoRows) {
		return "", "storage_unavailable"
	}
	if checksumErr == nil && key != expected {
		return "", "checksum_mismatch"
	}
	keys := make([]string, 0, len(imported.Assets)+1)
	keys = append(keys, key)
	for _, asset := range imported.Assets {
		keys = append(keys, asset.SHA256)
	}
	if err = s.reserveObjects(ctx, j.ID, epoch, keys); err != nil {
		return "", importError(ctx, "storage_unavailable")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return "", "staging_unavailable"
	}
	if err = s.objects.Put(ctx, key, contextReader{ctx, file}, j.Size, "application/x-siq"); err != nil {
		return "", importError(ctx, "storage_unavailable")
	}
	assets := make([]library.Asset, 0, len(imported.Assets))
	// Library recomputes document/reference validation on each edit. Only media
	// processing diagnostics belong to persistent importer-supplied issues.
	var issues []packs.Issue
	for _, asset := range imported.Assets {
		reader, err := asset.Open()
		if err != nil {
			return "", "invalid_archive"
		}
		// Content sniffing uses at most 512 bytes and the rest is streamed directly.
		var head [512]byte
		n, readErr := io.ReadFull(reader, head[:])
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			_ = reader.Close()
			return "", "invalid_archive"
		}
		contentType := safeContentType(head[:n])
		err = s.objects.Put(ctx, asset.SHA256, io.MultiReader(strings.NewReader(string(head[:n])), reader), asset.Size, contentType)
		closeErr := reader.Close()
		if err != nil || closeErr != nil {
			return "", importError(ctx, "storage_unavailable")
		}
		stored := library.Asset{Name: asset.Name, Key: asset.SHA256, Size: asset.Size, ContentType: contentType}
		// siq provides canonical names, so case/percent/NFC aliases are already rejected.
		kind := archiveKind(asset.Key)
		if kind == "image" && !strings.HasPrefix(contentType, "image/") {
			issues = append(issues, packs.Issue{Severity: "error", Code: "unsafe_media", Path: asset.Name, Message: "Изображение имеет неподдерживаемый или небезопасный формат"})
		} else if kind != "" {
			source, openErr := asset.Open()
			if openErr != nil {
				return "", "invalid_archive"
			}
			prepared, code := s.prepareAsset(ctx, j, epoch, stored, kind, source)
			closeErr := source.Close()
			if ctx.Err() != nil {
				return "", importError(ctx, "import_failed")
			}
			if closeErr != nil {
				return "", "invalid_archive"
			}
			if code == "storage_unavailable" {
				return "", code
			}
			if code != "" {
				issues = append(issues, packs.Issue{Severity: "error", Code: code, Path: asset.Name, Message: mediaIssueMessage(code)})
			} else {
				stored = *prepared
				if stored.ContentType != contentType {
					source, openErr := asset.Open()
					if openErr != nil {
						return "", "invalid_archive"
					}
					putErr := s.objects.Put(ctx, stored.Key, source, stored.Size, stored.ContentType)
					closeErr := source.Close()
					if putErr != nil || closeErr != nil {
						return "", importError(ctx, "storage_unavailable")
					}
				}
			}
		}
		assets = append(assets, stored)
	}
	if err = ctx.Err(); err != nil {
		return "", importError(ctx, "import_failed")
	}
	var owns bool
	err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM uploads u JOIN ingest_owner o ON o.singleton=true WHERE u.id=$1 AND u.status='processing' AND u.lease_epoch=$2 AND o.epoch=$2 AND u.expires_at>clock_timestamp())`, j.ID, epoch).Scan(&owns)
	if err != nil || !owns {
		return "", "ownership_lost"
	}
	// Library.Create rechecks the credential under its account lock and uses
	// (owner, ImportID) to survive a crash before the upload status is committed.
	draft, err := s.lib.Create(ctx, actor, library.Save{Document: imported.Document, Assets: assets, OriginalKey: key, Issues: issues, ImportID: j.ID})
	if errors.Is(err, library.ErrUnauthorized) || errors.Is(err, library.ErrForbidden) {
		return "", "authorization_expired"
	}
	if errors.Is(err, library.ErrQuota) {
		return "", "draft_quota"
	}
	if err != nil {
		return "", importError(ctx, "import_failed")
	}
	return draft.Card.ID, ""
}
func importError(ctx context.Context, fallback string) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "import_timeout"
	}
	return fallback
}
func safeContentType(data []byte) string {
	detected := http.DetectContentType(data)
	switch detected {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return detected
	case "audio/mpeg", "audio/wave", "audio/aiff", "audio/basic", "audio/midi", "application/ogg", "video/mp4", "video/webm", "video/avi":
		return detected
	default:
		return "application/octet-stream"
	}
}
func (s *Service) removeStage(ctx context.Context, id string) {
	if !validID(id) {
		return
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(context.Background())
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM uploads WHERE id=$1 FOR UPDATE`, id).Scan(&status); err != nil || status != "ready" && status != "failed" {
		return
	}
	err = s.root.Remove(id)
	if err != nil && !os.IsNotExist(err) {
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE uploads SET staged_deleted=true WHERE id=$1`, id); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}
func (s *Service) cleanup(ctx context.Context, epoch int64) {
	// At most sixteen jobs can be active; terminal cleanup is paged in fixed batches.
	_, err := s.pool.Exec(ctx, `UPDATE uploads SET status='failed',error='expired',updated_at=now() WHERE status IN ('uploading','queued','processing') AND expires_at<=clock_timestamp()
 AND EXISTS(SELECT 1 FROM ingest_owner WHERE singleton=true AND epoch=$1)`, epoch)
	if err != nil {
		return
	}
	if ctx.Err() == nil {
		rows, err := s.pool.Query(ctx, `SELECT id FROM uploads WHERE status IN ('ready','failed') AND NOT staged_deleted ORDER BY updated_at LIMIT 100`)
		if err != nil {
			return
		}
		ids := make([]string, 0, 100)
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		rowErr := rows.Err()
		rows.Close()
		if err != nil || rowErr != nil {
			return
		}
		for _, id := range ids {
			s.removeStage(ctx, id)
		}
	}
	s.cleanupOrphans(ctx)
	s.cleanupObjects(ctx, epoch)
}
