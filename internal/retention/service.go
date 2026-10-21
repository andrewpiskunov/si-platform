// Package retention performs bounded, retryable metadata and physical cleanup.
package retention

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"siplatform/internal/blob"
	"siplatform/internal/blobrefs"
)

const batchSize = 25
const redacted = "[удалено вместе с аккаунтом]"

type Stats struct{ Packs, Themes, Uploads, Audits, Credentials, MCPReceipts, Redacted, ObjectsDeleted, ObjectsFailed, ObjectsReferenced int }
type Service struct {
	pool    *pgxpool.Pool
	objects blob.Store
}

func New(pool *pgxpool.Pool, objects blob.Store) *Service {
	return &Service{pool: pool, objects: objects}
}

type item struct{ kind, id, owner string }

// Sweep bounds each category independently so one backlog cannot starve another.
// Minimal anonymous account tombstones remain for foreign moderation references.
func (s *Service) Sweep(ctx context.Context) (st Stats, err error) {
	queries := []string{
		`SELECT 'mcp',p.idempotency_key,p.owner_id FROM mcp_receipts p JOIN account_users a ON a.id=p.owner_id WHERE a.deleted_at<=now()-interval '30 days'`,
		`SELECT 'pack',p.id,p.owner_id FROM library_packs p JOIN account_users a ON a.id=p.owner_id WHERE (p.deleted_at<=now()-interval '30 days' OR a.deleted_at<=now()-interval '30 days') AND NOT EXISTS(SELECT 1 FROM room_packs r WHERE r.pack_id=p.id)`,
		`SELECT 'theme',p.id,p.owner_id FROM themes p JOIN account_users a ON a.id=p.owner_id WHERE (p.deleted_at<=now()-interval '30 days' OR a.deleted_at<=now()-interval '30 days') AND NOT EXISTS(SELECT 1 FROM room_theme_pins r WHERE r.theme_id=p.id)`,
		`SELECT 'upload',p.id,p.owner_id FROM uploads p WHERE p.status IN ('ready','failed') AND p.staged_deleted AND p.updated_at<=now()-interval '30 days'`,
		`SELECT 'audit',p.id::text,p.user_id FROM account_audit p JOIN account_users a ON a.id=p.user_id WHERE (a.deleted_at IS NULL AND p.created_at<=now()-interval '90 days') OR a.deleted_at<=now()-interval '30 days'`,
		`SELECT 'credential',p.id,p.user_id FROM account_credentials p WHERE LEAST(p.expires_at,p.revoked_at)<=now()-interval '30 days' AND NOT EXISTS(SELECT 1 FROM uploads u WHERE u.credential_id=p.id)`,
		`SELECT 'redact',a.id,a.id FROM account_users a WHERE a.deleted_at<=now()-interval '30 days' AND (a.display_name<>'[удалено вместе с аккаунтом]' OR EXISTS(SELECT 1 FROM library_reports r WHERE (r.reporter_id=a.id AND r.reason<>'[удалено вместе с аккаунтом]') OR (r.resolved_by=a.id AND r.resolution_reason<>'[удалено вместе с аккаунтом]')) OR EXISTS(SELECT 1 FROM library_decisions d WHERE d.moderator_id=a.id AND d.reason<>'[удалено вместе с аккаунтом]') OR EXISTS(SELECT 1 FROM theme_moderation m WHERE m.actor_id=a.id AND m.reason<>'[удалено вместе с аккаунтом]'))`,
	}
	for _, q := range queries {
		rows, e := s.pool.Query(ctx, `SELECT kind,id,owner FROM (`+q+`) AS eligible(kind,id,owner) WHERE NOT EXISTS(SELECT 1 FROM account_export_leases l WHERE l.owner_id=eligible.owner AND l.expires_at>clock_timestamp()) ORDER BY id LIMIT $1`, batchSize)
		if e != nil {
			return st, e
		}
		var items []item
		for rows.Next() {
			var i item
			if e = rows.Scan(&i.kind, &i.id, &i.owner); e != nil {
				break
			}
			items = append(items, i)
		}
		rowErr := rows.Err()
		rows.Close()
		if e != nil {
			return st, e
		}
		if rowErr != nil {
			return st, rowErr
		}
		for _, i := range items {
			if e = s.metadata(ctx, i, &st); e != nil {
				return st, e
			}
		}
	}
	err = s.objectsPass(ctx, &st)
	return
}
func (s *Service) metadata(ctx context.Context, i item, st *Stats) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, blobrefs.GuardSQL); e != nil {
		return e
	}
	var deleted bool
	if e = tx.QueryRow(ctx, `SELECT COALESCE(deleted_at<=now()-interval '30 days',false) FROM account_users WHERE id=$1 FOR UPDATE`, i.owner).Scan(&deleted); errors.Is(e, pgx.ErrNoRows) {
		return nil
	} else if e != nil {
		return e
	}
	var exporting bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM account_export_leases WHERE owner_id=$1 AND expires_at>clock_timestamp())`, i.owner).Scan(&exporting); e != nil {
		return e
	}
	if exporting {
		return nil
	}
	var n int64
	switch i.kind {
	case "pack", "theme":
		table, idcol, versions, pins, pincol := "library_packs", "pack_id", "library_revisions", "room_packs", "pack_id"
		if i.kind == "theme" {
			table, idcol, versions, pins, pincol = "themes", "theme_id", "theme_versions", "room_theme_pins", "theme_id"
		}
		var eligible bool
		e = tx.QueryRow(ctx, `SELECT (COALESCE(deleted_at<=now()-interval '30 days',false) OR $2) AND NOT EXISTS(SELECT 1 FROM `+pins+` WHERE `+pincol+`=$1) FROM `+table+` WHERE id=$1 FOR UPDATE`, i.id, deleted).Scan(&eligible)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		if !eligible {
			return nil
		}
		// Lock referenced revisions as well: FK pin insertion must wait, or deletion
		// safely fails/rolls back if a concurrent pin won the race.
		fields := "jsonb_build_object('original_key',original_key,'assets',assets)"
		if i.kind == "theme" {
			fields = "prepared"
		}
		rows, e := tx.Query(ctx, `SELECT `+fields+` FROM `+versions+` v WHERE `+idcol+`=$1 FOR UPDATE`, i.id)
		if e != nil {
			return e
		}
		var docs [][]byte
		for rows.Next() {
			var b []byte
			if e = rows.Scan(&b); e != nil {
				break
			}
			docs = append(docs, b)
		}
		re := rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if re != nil {
			return re
		}
		if e = enqueue(ctx, tx, docs); e != nil {
			return e
		}
		tables := []string{"library_media_receipts", "library_reports", "library_decisions", versions, table}
		if i.kind == "theme" {
			tables = []string{"theme_receipts", "theme_moderation", versions, table}
		}
		for _, t := range tables {
			col := idcol
			if t == table {
				col = "id"
			}
			r, e := tx.Exec(ctx, `DELETE FROM `+t+` WHERE `+col+`=$1`, i.id)
			if e != nil {
				return e
			}
			n = r.RowsAffected()
		}
	case "upload":
		var b []byte
		e = tx.QueryRow(ctx, `SELECT jsonb_build_object('stored_keys',stored_keys,'result_asset',result_asset,'result_theme',result_theme) FROM uploads u WHERE id=$1 AND status IN ('ready','failed') AND staged_deleted AND updated_at<=now()-interval '30 days' FOR UPDATE`, i.id).Scan(&b)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		if e = enqueue(ctx, tx, [][]byte{b}); e != nil {
			return e
		}
		for _, t := range []string{"library_media_receipts", "theme_receipts"} {
			if _, e = tx.Exec(ctx, `DELETE FROM `+t+` WHERE upload_id=$1`, i.id); e != nil {
				return e
			}
		}
		r, e := tx.Exec(ctx, `DELETE FROM uploads WHERE id=$1`, i.id)
		if e != nil {
			return e
		}
		n = r.RowsAffected()
	case "audit":
		r, e := tx.Exec(ctx, `DELETE FROM account_audit WHERE id=$1::bigint AND ($2 OR (EXISTS(SELECT 1 FROM account_users WHERE id=$3 AND deleted_at IS NULL) AND created_at<=now()-interval '90 days'))`, i.id, deleted, i.owner)
		if e != nil {
			return e
		}
		n = r.RowsAffected()
	case "mcp":
		if !deleted {
			return nil
		}
		r, err := tx.Exec(ctx, `DELETE FROM mcp_receipts WHERE owner_id=$1 AND idempotency_key=$2`, i.owner, i.id)
		if err != nil {
			return err
		}
		n = r.RowsAffected()
	case "credential":
		r, e := tx.Exec(ctx, `DELETE FROM account_credentials WHERE id=$1 AND LEAST(expires_at,revoked_at)<=now()-interval '30 days' AND NOT EXISTS(SELECT 1 FROM uploads WHERE credential_id=$1)`, i.id)
		if e != nil {
			return e
		}
		n = r.RowsAffected()
	case "redact":
		if !deleted {
			return nil
		}
		for _, q := range []string{
			`UPDATE account_users SET display_name=$2 WHERE id=$1 AND display_name<>$2`,
			`UPDATE library_reports SET reason=$2 WHERE id IN (SELECT id FROM library_reports WHERE reporter_id=$1 AND reason<>$2 LIMIT 100)`,
			`UPDATE library_reports SET resolution_reason=$2 WHERE id IN (SELECT id FROM library_reports WHERE resolved_by=$1 AND resolution_reason<>$2 LIMIT 100)`,
			`UPDATE library_decisions SET reason=$2 WHERE id IN (SELECT id FROM library_decisions WHERE moderator_id=$1 AND reason<>$2 LIMIT 100)`,
			`UPDATE theme_moderation SET reason=$2 WHERE id IN (SELECT id FROM theme_moderation WHERE actor_id=$1 AND reason<>$2 LIMIT 100)`,
		} {
			r, e := tx.Exec(ctx, q, i.id, redacted)
			if e != nil {
				return e
			}
			n += r.RowsAffected()
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	switch i.kind {
	case "pack":
		st.Packs += int(n)
	case "theme":
		st.Themes += int(n)
	case "upload":
		st.Uploads += int(n)
	case "audit":
		st.Audits += int(n)
	case "mcp":
		st.MCPReceipts += int(n)
	case "credential":
		st.Credentials += int(n)
	case "redact":
		st.Redacted += int(n)
	}
	return nil
}

var hashKey = regexp.MustCompile(`^[0-9a-f]{64}$`)

func enqueue(ctx context.Context, tx pgx.Tx, docs [][]byte) error {
	keys := map[string]bool{}
	var walk func(any, string)
	walk = func(v any, name string) {
		switch x := v.(type) {
		case map[string]any:
			for k, v := range x {
				walk(v, k)
			}
		case []any:
			for _, v := range x {
				walk(v, name)
			}
		case string:
			if (name == "key" || name == "original_key" || name == "stored_keys") && hashKey.MatchString(x) {
				keys[x] = true
			}
		}
	}
	for _, b := range docs {
		var v any
		if e := json.Unmarshal(b, &v); e != nil {
			return e
		}
		walk(v, "")
	}
	for k := range keys {
		if _, e := tx.Exec(ctx, `INSERT INTO retention_candidates(key) VALUES($1) ON CONFLICT(key) DO UPDATE SET retry_at=LEAST(retention_candidates.retry_at,excluded.retry_at)`, k); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) objectsPass(ctx context.Context, st *Stats) error {
	rows, e := s.pool.Query(ctx, `SELECT key FROM retention_candidates WHERE retry_at<=clock_timestamp() ORDER BY retry_at,key LIMIT 100`)
	if e != nil {
		return e
	}
	var keys []string
	for rows.Next() {
		var k string
		if e = rows.Scan(&k); e != nil {
			break
		}
		keys = append(keys, k)
	}
	re := rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if re != nil {
		return re
	}
	for _, k := range keys {
		if e = s.object(ctx, k, st); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) object(ctx context.Context, k string, st *Stats) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, blobrefs.GuardSQL); e != nil {
		return e
	}
	var exists bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM retention_candidates WHERE key=$1 AND retry_at<=clock_timestamp())`, k).Scan(&exists); e != nil {
		return e
	}
	if !exists {
		return nil
	}
	refs, e := blobrefs.Check(ctx, tx, k)
	if e != nil {
		return e
	}
	if refs.Content || refs.Active || refs.Ready {
		// Preserve the candidate: another owner may eventually drop the last ref.
		if _, e = tx.Exec(ctx, `UPDATE retention_candidates SET retry_at=clock_timestamp()+interval '1 hour' WHERE key=$1`, k); e != nil {
			return e
		}
		if e = tx.Commit(ctx); e == nil {
			st.ObjectsReferenced++
		}
		return e
	}
	objCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	e = blob.DeleteGuarded(objCtx, s.objects, k, func(check context.Context) error {
		_, err := tx.Exec(check, "SELECT 1")
		return err
	})
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e != nil && !errors.Is(e, blob.ErrNotFound) {
		code := "delete_failed"
		if errors.Is(e, context.DeadlineExceeded) {
			code = "delete_timeout"
		}
		if _, e = tx.Exec(ctx, `UPDATE retention_candidates SET attempts=attempts+1,last_error=$2,retry_at=clock_timestamp()+interval '1 minute' WHERE key=$1`, k, code); e != nil {
			return e
		}
		if e = tx.Commit(ctx); e == nil {
			st.ObjectsFailed++
		}
		return e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM retention_candidates WHERE key=$1`, k); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e == nil {
		st.ObjectsDeleted++
	}
	return e
}
