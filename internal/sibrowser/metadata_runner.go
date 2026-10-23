package sibrowser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"siplatform/internal/ingest"
	"siplatform/internal/library"
	"time"
)

type metadataTask struct {
	digest                string
	id                    string
	snapshot              []byte
	upload                string
	metadataDue, coverDue bool
}

func (l *lease) metadataTask(after, only string, force, retries bool) (metadataTask, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var t metadataTask
	e := l.conn.QueryRow(l.ctx, `SELECT i.external_id,COALESCE(m.snapshot,'null'),i.cover_upload,i.cover_sha256,
 ($3 OR i.metadata_next_attempt<=now()),($3 OR i.cover_next_attempt<=now())
 FROM source_items i JOIN library_packs p ON p.id=i.pack_id LEFT JOIN source_metadata m ON m.pack_id=p.id
 WHERE i.source='sibrowser' AND i.state='published' AND p.deleted_at IS NULL AND NOT p.blocked AND p.published_revision IS NOT NULL
 AND i.external_id>$1 AND ($2='' OR i.external_id=$2)
 AND ($3 OR i.metadata_next_attempt<=now() OR (m.pack_id IS NOT NULL AND i.cover_next_attempt<=now()))
 AND (NOT $4 OR i.metadata_error<>'' OR i.cover_error<>'')
 ORDER BY i.external_id LIMIT 1`, after, only, force, retries).Scan(&t.id, &t.snapshot, &t.upload, &t.digest, &t.metadataDue, &t.coverDue)
	if errors.Is(e, pgx.ErrNoRows) {
		return t, false, nil
	}
	return t, e == nil, e
}
func (l *lease) metadataResult(id, phase string, err error, upload string) error {
	code := ""
	delay := 24 * time.Hour
	if err != nil {
		code = "metadata_failed"
		var f *Fault
		if errors.As(err, &f) {
			code = f.Code
		}
		delay = time.Hour
	}
	return l.transaction(func(tx pgx.Tx) error {
		query := fmt.Sprintf(`UPDATE source_items SET %s_error=$2,%s_next_attempt=clock_timestamp()+$3*interval '1 second',%s_checked_at=clock_timestamp(),cover_upload=CASE WHEN $4='' THEN cover_upload ELSE $4 END WHERE source='sibrowser' AND external_id=$1`, phase, phase, phase)
		_, e := tx.Exec(l.ctx, query, id, code, delay.Seconds(), upload)
		return e
	})
}
func (l *lease) rememberCover(id, upload string, digest ...string) error {
	return l.transaction(func(tx pgx.Tx) error {
		_, e := tx.Exec(l.ctx, `UPDATE source_items SET cover_upload=$2,cover_sha256=$3 WHERE source='sibrowser' AND external_id=$1`, id, upload, func() string {
			if len(digest) > 0 {
				return digest[0]
			}
			return ""
		}())
		return e
	})
}
func (c *apiClient) saveMetadata(ctx context.Context, id string, epoch int64, m library.SourceMetadata) error {
	raw, _ := json.Marshal(map[string]any{"external_id": id, "epoch": epoch, "metadata": m})
	var out map[string]bool
	return c.call(ctx, "POST", "/api/external-imports/sibrowser/metadata", raw, nil, &out)
}
func (c *apiClient) attachCover(ctx context.Context, id string, epoch int64, upload, source string) error {
	raw, _ := json.Marshal(map[string]any{"external_id": id, "epoch": epoch, "upload_id": upload, "source_url": source})
	var out map[string]bool
	return c.call(ctx, "POST", "/api/external-imports/sibrowser/cover", raw, nil, &out)
}
func (c *apiClient) coverUpload(ctx context.Context, l *lease, src *Source, t metadataTask, m library.SourceMetadata) error {
	if m.CoverURL == "" {
		return c.attachCover(ctx, t.id, l.epoch, "", "")
	}
	var u ingest.Upload
	if t.upload != "" {
		e := c.call(ctx, "GET", "/api/uploads/"+t.upload, nil, nil, &u)
		if e != nil {
			var f *Fault
			if !errors.As(e, &f) || f.Code != "api_http_404" {
				return e
			}
			u = ingest.Upload{}
		}
		if u.Status == "failed" {
			u = ingest.Upload{}
		}
	}
	// Reuse only an unfinished upload. Successful prior snapshots must fetch the
	// image again on refresh because the source can change bytes at the same URL.
	if u.Status == "ready" && t.upload != "" {
		return c.attachCover(ctx, t.id, l.epoch, u.ID, m.CoverURL)
	}
	var data []byte
	if u.ID == "" || u.Status == "uploading" {
		var e error
		data, e = src.Cover(ctx, t.id, m.CoverURL)
		if e != nil {
			return e
		}
		sum := sha256.Sum256(data)
		digest := hex.EncodeToString(sum[:])
		if u.ID != "" && t.digest != digest {
			u = ingest.Upload{}
			if e = l.rememberCover(t.id, ""); e != nil {
				return e
			}
		}
		if u.ID == "" {
			raw, _ := json.Marshal(map[string]any{"name": "sibrowser-cover.webp", "size": len(data), "kind": "image"})
			if e = c.call(ctx, "POST", "/api/media-uploads", raw, nil, &u); e != nil {
				return e
			}
			if len(u.ID) != 32 {
				return fault("api_invalid_upload", false)
			}
			if e = l.rememberCover(t.id, u.ID, digest); e != nil {
				return e
			}
		}
		if u.Size != int64(len(data)) || u.Offset < 0 || u.Offset > u.Size {
			return fault("cover_changed_during_upload", true)
		}
		for u.Offset < u.Size {
			before := u.Offset
			end := min(before+4<<20, u.Size)
			old := u.ID
			if e = c.call(ctx, "PATCH", "/api/uploads/"+u.ID, data[before:end], &before, &u); e != nil {
				return e
			}
			if u.ID != old || u.Offset != end {
				return fault("api_invalid_offset", false)
			}
		}
		if e = c.call(ctx, "POST", "/api/uploads/"+u.ID+"/complete", nil, nil, &u); e != nil {
			return e
		}
	}
	for u.Status == "processing" || u.Status == "queued" {
		if e := wait(ctx, time.Second); e != nil {
			return e
		}
		if e := c.call(ctx, "GET", "/api/uploads/"+u.ID, nil, nil, &u); e != nil {
			return e
		}
	}
	if u.Status != "ready" {
		return fault("cover_"+safeCode(u.Error), true)
	}
	return c.attachCover(ctx, t.id, l.epoch, u.ID, m.CoverURL)
}
func refreshMetadata(ctx context.Context, l *lease, src *Source, api *apiClient, t metadataTask, report *Report) error {
	var m library.SourceMetadata
	_ = json.Unmarshal(t.snapshot, &m)
	if t.metadataDue || m.Name == "" {
		fresh, e := src.Metadata(ctx, t.id)
		if e == nil {
			e = api.saveMetadata(ctx, t.id, l.epoch, fresh)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if save := l.metadataResult(t.id, "metadata", e, ""); save != nil {
			return save
		}
		if e != nil {
			report.MetadataErrors = append(report.MetadataErrors, ItemError{t.id, "metadata", metadataCode(e)})
			if m.Name == "" {
				return nil
			}
		} else {
			m = fresh
			report.MetadataUpdated++
		}
	}
	if t.coverDue {
		coverCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		e := api.coverUpload(coverCtx, l, src, t, m)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if save := l.metadataResult(t.id, "cover", e, ""); save != nil {
			return save
		}
		if e != nil {
			report.MetadataErrors = append(report.MetadataErrors, ItemError{t.id, "cover", metadataCode(e)})
		} else {
			report.CoversUpdated++
			if e = l.rememberCover(t.id, ""); e != nil {
				return e
			}
		}
	}
	return nil
}
func metadataCode(e error) string {
	var f *Fault
	if errors.As(e, &f) {
		return f.Code
	}
	return "metadata_failed"
}
func metadataPass(ctx context.Context, l *lease, src *Source, api *apiClient, opt Options, report *Report, retries bool) error {
	after := ""
	n := 0
	for {
		t, ok, e := l.metadataTask(after, "", opt.ForceMetadata, retries)
		if e != nil {
			return e
		}
		if !ok {
			return nil
		}
		if opt.MaxItems > 0 && n >= opt.MaxItems {
			return ErrIncomplete
		}
		after = t.id
		n++
		if e = refreshMetadata(ctx, l, src, api, t, report); e != nil {
			return e
		}
	}
}
func metadataOne(ctx context.Context, l *lease, src *Source, api *apiClient, id string, report *Report) error {
	t, ok, e := l.metadataTask("", id, false, false)
	if e != nil || !ok {
		return e
	}
	return refreshMetadata(ctx, l, src, api, t, report)
}
