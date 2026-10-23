package privacy

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"

	"github.com/jackc/pgx/v5"
	"siplatform/internal/library"
	"siplatform/internal/media"
	"siplatform/internal/themes"
)

type objectSet map[string]int64

func (s objectSet) add(k string, size int64) error {
	if k == "" {
		return nil
	}
	if !keyPattern.MatchString(k) || size < -1 {
		return ErrUnavailable
	}
	if old, ok := s[k]; ok {
		if old >= 0 && size >= 0 && old != size {
			return ErrUnavailable
		}
		if old >= 0 {
			return nil
		}
	}
	s[k] = size
	return nil
}
func (s objectSet) prepared(p *media.Prepared) error {
	if p == nil {
		return nil
	}
	for _, v := range []media.Variant{p.Standard, p.Economy} {
		if e := s.add(v.Key, v.Size); e != nil {
			return e
		}
	}
	return nil
}
func (s objectSet) assets(raw []byte) error {
	var a []library.Asset
	if e := json.Unmarshal(raw, &a); e != nil {
		return e
	}
	for _, v := range a {
		if e := s.add(v.Key, v.Size); e != nil {
			return e
		}
		if e := s.prepared(v.Prepared); e != nil {
			return e
		}
	}
	return nil
}
func (s objectSet) theme(raw []byte) error {
	if string(raw) == "null" || len(raw) == 0 {
		return nil
	}
	var p themes.Prepared
	if e := json.Unmarshal(raw, &p); e != nil {
		return e
	}
	if e := s.add(p.OriginalKey, p.OriginalSize); e != nil {
		return e
	}
	for _, a := range p.Assets {
		if e := s.prepared(&a.Prepared); e != nil {
			return e
		}
	}
	return nil
}

// Only these fixed columns are selected; new database columns cannot silently
// enter exports. Nested documents are authored content, not database row dumps.
type projection struct{ name, sql string }

var projections = []projection{
	{"catalogue_imports", `SELECT jsonb_build_object('namespace',namespace,'external_id',external_id,'sha256',sha256,'name',name,'size',size,'source_url',source_url,'license',license,'attribution',attribution,'permission_basis',permission_basis,'upload_id',upload_id,'pack_id',pack_id,'created_at',created_at) FROM catalogue_imports WHERE owner_id=$1 AND ($2='' OR ($2='pack' AND pack_id=$3)) ORDER BY created_at,id`},
	{"mcp_receipts", `SELECT jsonb_build_object('idempotency_key',idempotency_key,'operation',operation,'pack_id',pack_id,'result_revision',result_revision,'created_at',created_at) FROM mcp_receipts WHERE owner_id=$1 AND ($2='' OR ($2='pack' AND pack_id=$3)) ORDER BY created_at,idempotency_key`},
	{"ai_quotes", `SELECT jsonb_build_object('id',id,'request',request,'model',model,'provider_id',provider_id,'format_version',format_version,'input_rate',input_rate,'output_rate',output_rate,'input_tokens',input_tokens,'max_output_tokens',max_output_tokens,'maximum_cost',maximum_cost,'created_at',created_at,'expires_at',expires_at) FROM ai_quotes WHERE owner_id=$1 AND $2='' AND $3::text IS NOT NULL`},
	{"ai_jobs", `SELECT jsonb_build_object('id',id,'status',status,'request',request,'result',result,'model',model,'provider_id',provider_id,'format_version',format_version,'created_at',created_at,'completed_at',completed_at,'applied_pack_id',applied_pack_id,'month',month,'input_rate',input_rate,'output_rate',output_rate,'input_tokens',input_tokens,'max_output_tokens',max_output_tokens,'maximum_cost',maximum_cost,'actual_cost',actual_cost,'cancel_requested',cancel_requested,'error',error) FROM ai_jobs WHERE owner_id=$1 AND ($2='' OR ($2='pack' AND applied_pack_id=$3)) ORDER BY created_at,id`},
	{"ai_monthly_usage", `SELECT jsonb_build_object('month',month,'spent',spent,'reserved',reserved) FROM ai_monthly_usage WHERE owner_id=$1 AND $2='' AND $3::text IS NOT NULL ORDER BY month`},
	{"profile", `SELECT jsonb_build_object('id',id,'username',username,'display_name',display_name,'role',role,'created_at',created_at) FROM account_users WHERE id=$1 AND $2::text IS NOT NULL AND $3::text IS NOT NULL`},
	{"api_tokens", `SELECT jsonb_build_object('id',id,'name',name,'scopes',scopes,'created_at',created_at,'expires_at',expires_at,'revoked_at',revoked_at) FROM account_credentials WHERE user_id=$1 AND kind='api' AND $2::text IS NOT NULL AND $3::text IS NOT NULL ORDER BY id`},
	{"account_audit", `SELECT jsonb_build_object('id',id,'action',action,'target_id',CASE WHEN action IN ('token_created','token_revoked') THEN target_id ELSE '' END,'created_at',created_at) FROM account_audit WHERE user_id=$1 AND $2::text IS NOT NULL AND $3::text IS NOT NULL ORDER BY id`},
	{"packs", `SELECT jsonb_build_object('id',id,'import_id',import_id,'current_revision',current_revision,'published_revision',published_revision,'pending_revision',pending_revision,'blocked',blocked,'created_at',created_at,'deleted_at',deleted_at) FROM library_packs WHERE owner_id=$1 AND ($2='' OR ($2='pack' AND id=$3)) ORDER BY id`},
	{"package_votes", `SELECT jsonb_build_object('pack_id',v.pack_id,'rating',v.rating,'liked',v.liked,'updated_at',v.updated_at) FROM package_votes v WHERE v.user_id=$1 AND ($2='' OR ($2='pack' AND v.pack_id=$3)) ORDER BY v.pack_id`},
	{"source_metadata", `SELECT to_jsonb(m) FROM source_metadata m JOIN library_packs p ON p.id=m.pack_id WHERE p.owner_id=$1 AND ($2='' OR ($2='pack' AND p.id=$3)) ORDER BY m.pack_id`},
	{"pack_revisions", `SELECT jsonb_build_object('pack_id',r.pack_id,'revision',r.revision,'card',r.card,'document',r.document,'assets',r.assets,'original_key',r.original_key,'issues',r.issues,'asset_issues',r.asset_issues,'size_bytes',r.size_bytes,'status',r.status,'rights',r.rights,'updated_at',r.updated_at) FROM library_revisions r JOIN library_packs p ON p.id=r.pack_id WHERE p.owner_id=$1 AND ($2='' OR ($2='pack' AND p.id=$3)) ORDER BY r.pack_id,r.revision`},
	{"themes", `SELECT jsonb_build_object('id',id,'current_revision',current_revision,'blocked',blocked,'created_at',created_at,'updated_at',updated_at,'deleted_at',deleted_at) FROM themes WHERE owner_id=$1 AND ($2='' OR ($2='theme' AND id=$3)) ORDER BY id`},
	{"theme_versions", `SELECT jsonb_build_object('theme_id',v.theme_id,'revision',v.revision,'prepared',v.prepared,'size_bytes',v.size_bytes,'created_at',v.created_at) FROM theme_versions v JOIN themes t ON t.id=v.theme_id WHERE t.owner_id=$1 AND ($2='' OR ($2='theme' AND t.id=$3)) ORDER BY v.theme_id,v.revision`},
	{"uploads", `SELECT jsonb_build_object('id',id,'name',name,'size',size,'offset_bytes',offset_bytes,'status',status,'draft_id',draft_id,'object_keys',CASE WHEN status='ready' THEN stored_keys ELSE ARRAY[]::text[] END,'error',error,'purpose',purpose,'media_kind',media_kind,'result_asset',result_asset,'result_theme',result_theme,'diagnostic_issues',diagnostic_issues,'created_at',created_at,'updated_at',updated_at,'expires_at',expires_at) FROM uploads WHERE owner_id=$1 AND ($2='' OR ($2='upload' AND id=$3)) ORDER BY id`},
	{"reports", `SELECT jsonb_build_object('id',id,'pack_id',pack_id,'revision',revision,'reason',reason,'status',status,'created_at',created_at,'resolution',resolution,'resolution_reason',resolution_reason,'resolved_at',resolved_at) FROM library_reports WHERE reporter_id=$1 AND ($2='' OR ($2='pack' AND pack_id=$3)) ORDER BY id`},
	{"report_resolutions", `SELECT jsonb_build_object('id',id,'pack_id',pack_id,'revision',revision,'resolution',resolution,'resolution_reason',resolution_reason,'resolved_at',resolved_at) FROM library_reports WHERE (resolved_by=$1 OR (status='resolved' AND EXISTS(SELECT 1 FROM library_packs p WHERE p.id=pack_id AND p.owner_id=$1))) AND ($2='' OR ($2='pack' AND pack_id=$3)) ORDER BY id`},
	{"pack_decisions", `SELECT jsonb_build_object('id',d.id,'pack_id',d.pack_id,'revision',d.revision,'decision',d.decision,'reason',d.reason,'created_at',d.created_at,'own_action',d.moderator_id=$1) FROM library_decisions d JOIN library_packs p ON p.id=d.pack_id WHERE (d.moderator_id=$1 OR p.owner_id=$1) AND ($2='' OR ($2='pack' AND p.id=$3)) ORDER BY d.id`},
	{"theme_moderation", `SELECT jsonb_build_object('id',m.id,'theme_id',m.theme_id,'blocked',m.blocked,'reason',m.reason,'created_at',m.created_at,'own_action',m.actor_id=$1) FROM theme_moderation m JOIN themes t ON t.id=m.theme_id WHERE (m.actor_id=$1 OR t.owner_id=$1) AND ($2='' OR ($2='theme' AND t.id=$3)) ORDER BY m.id`},
}

func (s *Service) writeData(ctx context.Context, tx pgx.Tx, z *zip.Writer, owner string, o Options) (objectSet, error) {
	// Account-wide blocked references protect aliases even for a single-upload export.
	blocked, refs := objectSet{}, objectSet{}
	var e error
	if !o.MetadataOnly {
		refs, e = s.references(ctx, tx, owner, o, false, nil)
		if e != nil {
			return nil, e
		}
		blocked, e = s.references(ctx, tx, owner, Options{}, true, refs)
		if e != nil {
			return nil, e
		}
	}
	var budget int64 = s.maxBytes
	for _, p := range projections {
		if e = writeProjection(ctx, tx, z, p, owner, o, &budget); e != nil {
			return nil, e
		}
	}
	keys := make([]string, 0, len(refs))
	for k := range refs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	manifest, e := z.Create("objects.ndjson")
	if e != nil {
		return nil, e
	}
	enc := json.NewEncoder(&budgetWriter{w: manifest, left: &budget})
	included := objectSet{}
	for _, k := range keys {
		reason := ""
		if _, ok := blocked[k]; ok {
			reason = "blocked"
		} else if o.MetadataOnly {
			reason = "metadata_only"
		}
		if e = enc.Encode(struct {
			Key     string `json:"key"`
			Path    string `json:"path"`
			Omitted string `json:"omitted,omitempty"`
		}{k, "objects/" + k, reason}); e != nil {
			return nil, e
		}
		if reason == "" {
			included[k] = refs[k]
		}
	}
	for _, k := range keys {
		size, ok := included[k]
		if !ok {
			continue
		}
		if s.objects == nil {
			return nil, ErrUnavailable
		}
		meta, e := s.objects.Stat(ctx, k)
		if e != nil {
			return nil, e
		}
		if meta.Size < 0 || (size >= 0 && size != meta.Size) {
			return nil, ErrUnavailable
		}
		size = meta.Size
		if size > budget {
			return nil, ErrTooLarge
		}
		r, e := s.objects.Get(ctx, k, 0, -1)
		if e != nil {
			return nil, e
		}
		w, e := z.Create("objects/" + k)
		if e != nil {
			r.Close()
			return nil, e
		}
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(&budgetWriter{w: w, left: &budget}, h), io.LimitReader(ctxReader{ctx, r}, size+1))
		closeErr := r.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if n != size || hex.EncodeToString(h.Sum(nil)) != k {
			return nil, ErrUnavailable
		}
	}
	return included, nil
}
func writeProjection(ctx context.Context, tx pgx.Tx, z *zip.Writer, p projection, owner string, o Options, budget *int64) error {
	rows, e := tx.Query(ctx, p.sql, owner, o.Kind, o.ID)
	if e != nil {
		return e
	}
	defer rows.Close()
	w, e := z.Create(p.name + ".ndjson")
	if e != nil {
		return e
	}
	b := &budgetWriter{w: w, left: budget}
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			return e
		}
		if _, e = b.Write(raw); e != nil {
			return e
		}
		if _, e = io.WriteString(b, "\n"); e != nil {
			return e
		}
	}
	return rows.Err()
}
func (s *Service) references(ctx context.Context, q querier, owner string, o Options, blockedOnly bool, wanted objectSet) (objectSet, error) {
	refs := objectSet{}
	coverRows, e := q.Query(ctx, `SELECT COALESCE(cover,'null') FROM source_metadata m JOIN library_packs p ON p.id=m.pack_id WHERE p.owner_id=$1 AND ($2='' OR ($2='pack' AND p.id=$3)) AND (NOT $4 OR p.blocked)`, owner, o.Kind, o.ID, blockedOnly)
	if e != nil {
		return nil, e
	}
	for coverRows.Next() {
		var raw []byte
		if e = coverRows.Scan(&raw); e != nil {
			break
		}
		var cover *media.Prepared
		if e = json.Unmarshal(raw, &cover); e != nil {
			break
		}
		rowRefs := objectSet{}
		if e = rowRefs.prepared(cover); e != nil {
			break
		}
		if e = mergeReferences(refs, rowRefs, wanted); e != nil {
			break
		}
	}
	re := coverRows.Err()
	coverRows.Close()
	if e != nil {
		return nil, e
	}
	if re != nil {
		return nil, re
	}
	rows, e := q.Query(ctx, `SELECT r.original_key,r.assets FROM library_revisions r JOIN library_packs p ON p.id=r.pack_id WHERE p.owner_id=$1 AND ($2='' OR ($2='pack' AND p.id=$3)) AND (NOT $4 OR p.blocked OR r.status='blocked')`, owner, o.Kind, o.ID, blockedOnly)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var original string
		var raw []byte
		if e = rows.Scan(&original, &raw); e != nil {
			break
		}
		rowRefs := objectSet{}
		if e = rowRefs.add(original, -1); e != nil {
			break
		}
		if e = rowRefs.assets(raw); e != nil {
			break
		}
		if e = mergeReferences(refs, rowRefs, wanted); e != nil {
			break
		}
		if len(refs) > s.maxObjects {
			e = ErrTooLarge
			break
		}
	}
	rowErr := rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if rowErr != nil {
		return nil, rowErr
	}
	rows, e = q.Query(ctx, `SELECT v.prepared FROM theme_versions v JOIN themes t ON t.id=v.theme_id WHERE t.owner_id=$1 AND ($2='' OR ($2='theme' AND t.id=$3)) AND (NOT $4 OR t.blocked)`, owner, o.Kind, o.ID, blockedOnly)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			break
		}
		rowRefs := objectSet{}
		if e = rowRefs.theme(raw); e != nil {
			break
		}
		if e = mergeReferences(refs, rowRefs, wanted); e != nil {
			break
		}
		if len(refs) > s.maxObjects {
			e = ErrTooLarge
			break
		}
	}
	rowErr = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if rowErr != nil {
		return nil, rowErr
	}
	if blockedOnly {
		return refs, nil
	}
	rows, e = q.Query(ctx, `SELECT stored_keys,result_asset,result_theme FROM uploads WHERE owner_id=$1 AND status='ready' AND ($2='' OR ($2='upload' AND id=$3))`, owner, o.Kind, o.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var keys []string
		var asset, theme []byte
		if e = rows.Scan(&keys, &asset, &theme); e != nil {
			return nil, e
		}
		for _, k := range keys {
			if e = refs.add(k, -1); e != nil {
				return nil, e
			}
		}
		if len(asset) > 0 && string(asset) != "null" {
			if e = refs.assets(append(append([]byte{'['}, asset...), ']')); e != nil {
				return nil, e
			}
		}
		if e = refs.theme(theme); e != nil {
			return nil, e
		}

		if len(refs) > s.maxObjects {
			return nil, ErrTooLarge
		}
	}
	return refs, rows.Err()
}

type budgetWriter struct {
	w    io.Writer
	left *int64
}

func (w *budgetWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > *w.left {
		return 0, ErrTooLarge
	}
	n, e := w.w.Write(p)
	*w.left -= int64(n)
	return n, e
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (r ctxReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	n, e := r.r.Read(p)
	if r.ctx.Err() != nil {
		return n, r.ctx.Err()
	}
	return n, e
}

func mergeReferences(dst, src, wanted objectSet) error {
	for k, size := range src {
		if wanted != nil {
			if _, ok := wanted[k]; !ok {
				continue
			}
		}
		if e := dst.add(k, size); e != nil {
			return e
		}
	}
	return nil
}
