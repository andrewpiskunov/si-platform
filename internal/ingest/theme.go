package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/jackc/pgx/v5"
	"siplatform/internal/accounts"
	"siplatform/internal/library"
	"siplatform/internal/media"
	"siplatform/internal/themes"
)

func (s *Service) CreateTheme(ctx context.Context, actor accounts.Identity, name string, size int64) (Upload, error) {
	if size > 5<<20 {
		return Upload{}, ErrInvalid
	}
	return s.create(ctx, actor, name, size, "theme", "", nil)
}

// The common draft-token policy does not confer theme rights. This second gate
// runs under the same locked live account for creation and every upload action.
func requireThemeSession(ctx context.Context, tx pgx.Tx, actor accounts.Identity, purpose string) error {
	if purpose != "theme" {
		return nil
	}
	var kind string
	err := tx.QueryRow(ctx, `SELECT kind FROM account_credentials WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>clock_timestamp()`, actor.SessionID, actor.User.ID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return safeError(ctx, err)
	}
	if kind != "session" {
		return ErrForbidden
	}
	return nil
}

func themeDiagnostics(err error) []themes.Issue {
	issues := themes.Issues(err)
	if len(issues) == 0 {
		return []themes.Issue{{Path: "archive", Code: "invalid", Message: "Не удалось проверить тему"}}
	}
	if len(issues) > 50 {
		issues = issues[:50]
	}
	for i := range issues {
		issues[i].Path = themeTextLimit(issues[i].Path, 512)
		issues[i].Code = themeTextLimit(issues[i].Code, 64)
		issues[i].Message = themeTextLimit(issues[i].Message, 1000)
	}
	return issues
}
func themeTextLimit(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

func (s *Service) processTheme(ctx context.Context, j job, epoch int64) (*themes.Prepared, []themes.Issue, string) {
	if ctx.Err() != nil {
		return nil, nil, importError(ctx, "import_failed")
	}
	if _, err := s.liveActor(ctx, j); err != nil {
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrForbidden) {
			return nil, nil, "authorization_expired"
		}
		return nil, nil, importError(ctx, "storage_unavailable")
	}
	file, err := s.openFile(j.ID, os.O_RDONLY)
	if err != nil {
		return nil, nil, "staging_unavailable"
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() != j.Size {
		return nil, nil, "staging_unavailable"
	}
	archive, err := themes.Read(ctx, file, j.Size)
	if err != nil {
		return nil, themeDiagnostics(err), importError(ctx, "theme_invalid")
	}
	result, err := themes.Prepare(ctx, s.media, archive)
	if err != nil {
		return nil, themeDiagnostics(err), importError(ctx, "theme_invalid")
	}
	defer result.Close()
	prepared := result.Prepared
	if err = prepared.Validate(); err != nil {
		return nil, themeDiagnostics(err), "theme_invalid"
	}
	if prepared.OriginalSize != j.Size {
		return nil, themeDiagnostics(themes.ErrInvalid), "theme_invalid"
	}
	expected := map[string]media.Variant{}
	keys := []string{prepared.OriginalKey}
	for _, asset := range prepared.Assets {
		for _, variant := range []media.Variant{asset.Prepared.Standard, asset.Prepared.Economy} {
			if prior, exists := expected[variant.Key]; exists && prior != variant {
				return nil, themeDiagnostics(themes.ErrInvalid), "theme_invalid"
			}
			expected[variant.Key] = variant
			keys = append(keys, variant.Key)
		}
	}
	found := map[string]bool{}
	for _, output := range result.Files {
		variant, ok := expected[output.Key]
		if !ok || variant.Size != output.Size || variant.MIME != output.MIME {
			return nil, themeDiagnostics(themes.ErrInvalid), "theme_invalid"
		}
		found[output.Key] = true
	}
	if len(found) != len(expected) {
		return nil, themeDiagnostics(themes.ErrInvalid), "theme_invalid"
	}
	// Reserve archive and every derivative together, before the first blob write.
	if err = s.reserveObjects(ctx, j.ID, epoch, keys); err != nil {
		return nil, nil, importError(ctx, "storage_unavailable")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, nil, "staging_unavailable"
	}
	if err = s.objects.Put(ctx, prepared.OriginalKey, contextReader{ctx, file}, j.Size, "application/zip"); err != nil {
		return nil, nil, importError(ctx, "storage_unavailable")
	}
	written := map[string]bool{}
	for _, output := range result.Files {
		if written[output.Key] {
			continue
		}
		source, err := os.Open(output.Path)
		if err != nil {
			return nil, nil, "storage_unavailable"
		}
		err = s.objects.Put(ctx, output.Key, contextReader{ctx, source}, output.Size, output.MIME)
		closeErr := source.Close()
		if err != nil || closeErr != nil {
			return nil, nil, importError(ctx, "storage_unavailable")
		}
		written[output.Key] = true
	}
	if ctx.Err() != nil {
		return nil, nil, importError(ctx, "import_failed")
	}
	return &prepared, nil, ""
}

func (s *Service) finishTheme(ctx context.Context, j job, epoch int64, prepared *themes.Prepared, issues []themes.Issue, code string) {
	if ctx.Err() != nil {
		return
	}
	status := "failed"
	var result any
	actor := accounts.Identity{User: accounts.User{ID: j.owner}, SessionID: j.credential}
	tx, err := s.begin(ctx, actor, true)
	if err == nil {
		defer tx.Rollback(context.Background())
		err = requireThemeSession(ctx, tx, actor, "theme")
	}
	if err != nil {
		if !errors.Is(err, ErrUnauthorized) && !errors.Is(err, ErrForbidden) {
			return
		}
		if tx != nil {
			_ = tx.Rollback(context.Background())
		}
		tx, err = s.pool.Begin(ctx)
		if err != nil {
			return
		}
		defer tx.Rollback(context.Background())
		code = "authorization_expired"
		issues = nil
	}
	// Fence the whole terminal transaction against a lease epoch change, rather
	// than relying only on a statement snapshot after expensive blob writes.
	var current int64
	if err = tx.QueryRow(ctx, `SELECT epoch FROM ingest_owner WHERE singleton=true FOR SHARE`).Scan(&current); err != nil || current != epoch {
		return
	}
	if code == "" {
		if prepared == nil {
			code = "theme_invalid"
			issues = themeDiagnostics(themes.ErrInvalid)
		} else if err = prepared.Validate(); err != nil {
			code = "theme_invalid"
			issues = themeDiagnostics(err)
		} else {
			_, storageLimit, limitErr := library.AccountLimits(ctx, tx, j.owner)
			if limitErr != nil {
				return
			}
			used, usageErr := library.StorageUsage(ctx, tx, j.owner)
			if usageErr != nil {
				return
			}
			if used+prepared.SizeBytes > storageLimit {
				code = "draft_quota"
			} else {
				raw, encodeErr := json.Marshal(prepared)
				if encodeErr != nil {
					return
				}
				result = raw
				status = "ready"
			}
		}
	}
	if issues == nil {
		issues = []themes.Issue{}
	}
	diagnostics, err := json.Marshal(issues)
	if err != nil {
		return
	}
	changed, err := tx.Exec(ctx, `UPDATE uploads SET status=$3,error=$4,result_theme=$5,diagnostic_issues=$6,updated_at=now() WHERE id=$1 AND lease_epoch=$2 AND status='processing' AND purpose='theme'`, j.ID, epoch, status, code, result, diagnostics)
	if err == nil && tx.Commit(ctx) == nil && changed.RowsAffected() == 1 {
		s.removeStage(ctx, j.ID)
	}
}
