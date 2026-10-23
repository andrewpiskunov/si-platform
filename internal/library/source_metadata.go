package library

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"siplatform/internal/accounts"

	"siplatform/internal/media"
	"siplatform/internal/packs"
	"strings"
	"time"
)

func (s *Service) sourceAccess(ctx context.Context, actor accounts.Identity, id string, epoch int64) (*access, error) {
	var pack string
	e := s.pool.QueryRow(ctx, `SELECT pack_id FROM catalogue_imports WHERE namespace='sibrowser' AND external_id=$1 AND owner_id=$2 AND origin='external_import'`, id, actor.User.ID).Scan(&pack)
	if errors.Is(e, pgx.ErrNoRows) || e == nil && pack == "" {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	a, e := s.beginGuarded(ctx, actor, "drafts:write", false, pack, true)
	if e != nil {
		return nil, e
	}
	var current int64
	e = a.tx.QueryRow(ctx, `SELECT epoch FROM external_sources WHERE source='sibrowser' AND owner_id=$1 FOR SHARE`, actor.User.ID).Scan(&current)
	if e != nil || a.actor.User.Role != "admin" {
		a.close()
		return nil, ErrForbidden
	}
	if current != epoch {
		a.close()
		return nil, ErrConflict
	}
	if a.pack.blocked || a.pack.published == nil {
		a.close()
		return nil, ErrConflict
	}
	return a, nil
}
func (s *Service) SaveSourceMetadata(ctx context.Context, actor accounts.Identity, id string, epoch int64, m SourceMetadata) error {
	if e := m.Validate(id); e != nil {
		return e
	}
	a, e := s.sourceAccess(ctx, actor, id, epoch)
	if e != nil {
		return e
	}
	defer a.close()
	raw, _ := json.Marshal(m)
	now := time.Now().UTC()
	summary, _ := json.Marshal(struct {
		SourceSummary
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}{SourceSummary{id, m.Authors, m.PublishedDate, m.Downloads, now}, m.Name, sourceTags(m)})
	search := []string{m.Name}
	for _, a := range m.Authors {
		search = append(search, a.Name)
	}
	search = append(search, m.Description...)
	search = append(search, sourceTags(m)...)
	used, e := StorageUsage(ctx, a.tx, actor.User.ID)
	if e != nil {
		return e
	}
	_, limit, e := AccountLimits(ctx, a.tx, actor.User.ID)
	if e != nil {
		return e
	}
	var old int64
	if e = a.tx.QueryRow(ctx, `SELECT COALESCE((SELECT octet_length(snapshot::text) FROM source_metadata WHERE pack_id=$1),0)`, a.pack.id).Scan(&old); e != nil {
		return e
	}
	// JSONB text can add whitespace; charge its actual stored representation.
	var size int64
	if e = a.tx.QueryRow(ctx, `SELECT octet_length($1::jsonb::text)`, raw).Scan(&size); e != nil {
		return e
	}
	if used-old+size > limit {
		return ErrQuota
	}
	tags, _ := json.Marshal(sourceTags(m))
	_, e = a.tx.Exec(ctx, `INSERT INTO source_metadata(pack_id,external_id,snapshot,summary,search_text,tags,size_bytes) VALUES($1,$2,$3,$4,$5,$6,$7)
 ON CONFLICT(pack_id) DO UPDATE SET snapshot=EXCLUDED.snapshot,summary=EXCLUDED.summary,search_text=EXCLUDED.search_text,tags=EXCLUDED.tags,updated_at=clock_timestamp(),size_bytes=source_metadata.size_bytes-octet_length(source_metadata.snapshot::text)+EXCLUDED.size_bytes`, a.pack.id, id, raw, summary, strings.Join(search, "\n"), tags, size)
	if e != nil {
		return e
	}
	return a.tx.Commit(ctx)
}
func sourceTags(m SourceMetadata) []string {
	out := []string{}
	for _, t := range m.Tags {
		out = append(out, t.Name)
	}
	return out
}

type SourceDetail struct {
	Metadata      SourceMetadata `json:"metadata"`
	UpdatedAt     time.Time      `json:"updated_at"`
	OriginalBytes int64          `json:"original_bytes"`
	Document      DocumentInfo   `json:"document"`
}
type DocumentInfo struct {
	Name        string        `json:"name"`
	Date        string        `json:"date"`
	Publisher   string        `json:"publisher"`
	Language    string        `json:"language"`
	Restriction string        `json:"restriction"`
	Difficulty  int           `json:"difficulty"`
	Authors     []string      `json:"authors"`
	Sources     []string      `json:"sources"`
	Comments    string        `json:"comments"`
	Tags        []string      `json:"tags"`
	ContactURI  string        `json:"contact_uri"`
	Generator   string        `json:"generator"`
	Rounds      []SourceRound `json:"rounds"`
}

func (s *Service) PublicSourceMetadata(ctx context.Context, pack string) (SourceDetail, error) {
	var out SourceDetail
	var raw, doc []byte
	e := s.pool.QueryRow(ctx, `SELECT COALESCE((SELECT snapshot FROM source_metadata WHERE pack_id=p.id),'{}'),COALESCE((SELECT updated_at FROM source_metadata WHERE pack_id=p.id),r.updated_at),r.document,COALESCE((SELECT size FROM catalogue_imports WHERE pack_id=p.id LIMIT 1),0)`+publicJoin+` AND p.id=$1`, pack).Scan(&raw, &out.UpdatedAt, &doc, &out.OriginalBytes)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if e != nil {
		return out, e
	}
	if e = json.Unmarshal(raw, &out.Metadata); e != nil {
		return out, e
	}
	var d packs.Document
	if e = json.Unmarshal(doc, &d); e != nil {
		return out, e
	}
	out.Document = DocumentInfo{Name: d.Name, Date: d.Date, Publisher: d.Publisher, Language: d.Language, Restriction: d.Restriction, Difficulty: d.Difficulty, Authors: d.Info.Authors, Sources: d.Info.Sources, Comments: d.Info.Comments, Tags: d.Tags, ContactURI: d.ContactURI, Generator: d.Generator}
	for _, r := range d.Rounds {
		rr := SourceRound{Name: r.Name}
		for _, t := range r.Themes {
			rr.Themes = append(rr.Themes, SourceTheme{Name: t.Name})
		}
		out.Document.Rounds = append(out.Document.Rounds, rr)
	}
	return out, nil
}
func (s *Service) AttachSourceCover(ctx context.Context, actor accounts.Identity, id string, epoch int64, upload, source string) error {
	if !ValidSourceCover(id, source) {
		return ErrInvalid
	}
	a, e := s.sourceAccess(ctx, actor, id, epoch)
	if e != nil {
		return e
	}
	defer a.close()
	var pack string
	if e = a.tx.QueryRow(ctx, `SELECT pack_id FROM catalogue_imports WHERE namespace='sibrowser' AND external_id=$1 AND origin='external_import'`, id).Scan(&pack); e != nil {
		return e
	}
	var expected string
	if e = a.tx.QueryRow(ctx, `SELECT snapshot->>'cover_url' FROM source_metadata WHERE pack_id=$1 FOR UPDATE`, pack).Scan(&expected); e != nil {
		return e
	}
	if expected != source {
		return ErrConflict
	}
	var previousUpload string
	if e = a.tx.QueryRow(ctx, `SELECT cover_upload FROM source_metadata WHERE pack_id=$1`, pack).Scan(&previousUpload); e != nil {
		return e
	}
	if source != "" && previousUpload == upload {
		return nil
	}
	var reservation int64
	var prepared *media.Prepared
	if source != "" {
		var raw []byte
		if e = a.tx.QueryRow(ctx, `SELECT result_asset FROM uploads WHERE id=$1 AND owner_id=$2 AND credential_id=$3 AND purpose='media' AND status='ready'`, upload, actor.User.ID, actor.SessionID).Scan(&raw); errors.Is(e, pgx.ErrNoRows) {
			return ErrNotFound
		} else if e != nil {
			return e
		}
		var asset Asset
		if json.Unmarshal(raw, &asset) != nil || asset.Prepared == nil || asset.Prepared.Kind != "image" || asset.Size > 5<<20 {
			return ErrInvalid
		}
		if _, e = preparedSize(asset); e != nil {
			return e
		}
		prepared = asset.Prepared
		var consumed bool
		if e = a.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM source_cover_receipts WHERE upload_id=$1) OR EXISTS(SELECT 1 FROM library_media_receipts WHERE upload_id=$1)`, upload).Scan(&consumed); e != nil {
			return e
		}
		if !consumed {
			reservation = asset.Size + prepared.Standard.Size + prepared.Economy.Size
		}
	}
	raw, _ := json.Marshal(prepared)
	size := int64(0)
	if prepared != nil {
		size = prepared.Standard.Size + prepared.Economy.Size
	}
	// Atomically consume the ready-upload reservation into durable cover storage.
	used, e := StorageUsage(ctx, a.tx, actor.User.ID)
	if e != nil {
		return e
	}
	_, limit, e := AccountLimits(ctx, a.tx, actor.User.ID)
	if e != nil {
		return e
	}
	var old int64
	if e = a.tx.QueryRow(ctx, `SELECT size_bytes-octet_length(snapshot::text) FROM source_metadata WHERE pack_id=$1`, pack).Scan(&old); e != nil {
		return e
	}
	if used-old-reservation+size > limit {
		return ErrQuota
	}
	_, e = a.tx.Exec(ctx, `UPDATE source_metadata SET cover=$2,cover_source=$3,cover_upload=$4,size_bytes=octet_length(snapshot::text)+$5 WHERE pack_id=$1`, pack, raw, source, upload, size)
	if e != nil {
		return e
	}
	if source != "" {
		if _, e = a.tx.Exec(ctx, `INSERT INTO source_cover_receipts(upload_id,pack_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, upload, pack); e != nil {
			return e
		}
	}
	return a.tx.Commit(ctx)
}
func (s *Service) PublicCover(ctx context.Context, pack string) (media.Variant, error) {
	var cover, doc, assets []byte
	e := s.pool.QueryRow(ctx, `SELECT COALESCE((SELECT cover FROM source_metadata WHERE pack_id=p.id),'null'),r.document,r.assets`+publicJoin+` AND p.id=$1`, pack).Scan(&cover, &doc, &assets)
	if errors.Is(e, pgx.ErrNoRows) {
		return media.Variant{}, ErrNotFound
	}
	if e != nil {
		return media.Variant{}, e
	}
	var p *media.Prepared
	if json.Unmarshal(cover, &p) == nil && p != nil {
		return p.Standard, nil
	}
	var d packs.Document
	var as []Asset
	if json.Unmarshal(doc, &d) != nil || json.Unmarshal(assets, &as) != nil {
		return media.Variant{}, ErrInvalid
	}
	if strings.HasPrefix(d.Logo, "@") {
		want, _ := assetName("Images/" + strings.TrimPrefix(d.Logo, "@"))
		for _, a := range as {
			name, _ := assetName(a.Name)
			if name == want && a.Prepared != nil && a.Prepared.Kind == "image" {
				return a.Prepared.Standard, nil
			}
		}
	}
	return media.Variant{}, ErrNotFound
}
