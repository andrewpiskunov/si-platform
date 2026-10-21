package privacy

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"siplatform/internal/accounts"
	"siplatform/internal/blob"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
	"siplatform/migrations"
)

func setup(t *testing.T) (*Service, *pgxpool.Pool, accounts.Identity) {
	t.Helper()
	p := testutil.Pool(t)
	ctx := context.Background()
	if e := postgres.Migrate(ctx, p); e != nil {
		t.Fatal(e)
	}
	a := accounts.Identity{User: accounts.User{ID: strings.Repeat("a", 32)}, SessionID: "session"}
	_, e := p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash) VALUES($1,'author','Own name','SECRET-password')`, a.User.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = p.Exec(ctx, `INSERT INTO account_credentials(id,user_id,token_hash,kind,expires_at) VALUES('session',$1,'SECRET-token','session',now()+interval '1 day')`, a.User.ID)
	if e != nil {
		t.Fatal(e)
	}
	return New(p, &memoryStore{data: map[string][]byte{}}), p, a
}
func unpack(t *testing.T, b []byte) map[string]string {
	t.Helper()
	z, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if e != nil {
		t.Fatal(e)
	}
	out := map[string]string{}
	for _, f := range z.File {
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		v, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		out[f.Name] = string(v)
	}
	return out
}
func TestExportProfileAndAuth(t *testing.T) {
	s, p, a := setup(t)
	var b bytes.Buffer
	if e := s.Export(context.Background(), a, Options{}, &b); e != nil {
		t.Fatal(e)
	}
	files := unpack(t, b.Bytes())
	if !strings.Contains(files["profile.ndjson"], "Own name") {
		t.Fatal(files)
	}
	for _, v := range files {
		if strings.Contains(v, "SECRET") || strings.Contains(v, "token_hash") {
			t.Fatal("credential leak")
		}
	}
	var n int
	if e := p.QueryRow(context.Background(), `SELECT count(*) FROM account_export_leases`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	a.APIToken = true
	if e := s.Export(context.Background(), a, Options{}, io.Discard); e != ErrForbidden {
		t.Fatal(e)
	}
	a.APIToken = false
	if _, e := p.Exec(context.Background(), `UPDATE account_credentials SET revoked_at=now()`); e != nil {
		t.Fatal(e)
	}
	if e := s.Export(context.Background(), a, Options{}, io.Discard); e != ErrUnauthorized {
		t.Fatal(e)
	}
}

type memoryStore struct {
	data   map[string][]byte
	before func()
}

func (m *memoryStore) Put(context.Context, string, io.Reader, int64, string) error { return nil }
func (m *memoryStore) Delete(context.Context, string) error                        { return nil }
func (m *memoryStore) Stat(_ context.Context, k string) (blob.Metadata, error) {
	v, ok := m.data[k]
	if !ok {
		return blob.Metadata{}, blob.ErrNotFound
	}
	return blob.Metadata{Size: int64(len(v))}, nil
}
func (m *memoryStore) Get(_ context.Context, k string, _, _ int64) (io.ReadCloser, error) {
	if m.before != nil {
		m.before()
	}
	v, ok := m.data[k]
	if !ok {
		return nil, blob.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(v)), nil
}
func key(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

func insertPack(t *testing.T, p *pgxpool.Pool, owner, id, k string, blocked, deleted bool) {
	t.Helper()
	ctx := context.Background()
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	_, e = tx.Exec(ctx, `INSERT INTO library_packs(id,owner_id,current_revision,blocked,deleted_at) VALUES($1,$2,2,$3,CASE WHEN $4 THEN now() END)`, id, owner, blocked, deleted)
	if e != nil {
		t.Fatal(e)
	}
	for i := 1; i <= 2; i++ {
		_, e = tx.Exec(ctx, `INSERT INTO library_revisions(pack_id,revision,card,document,assets,original_key,issues,size_bytes) VALUES($1,$2,jsonb_build_object('name','Own pack'),jsonb_build_object('name',$3::text),'[]',$4,'[]',4)`, id, i, fmt.Sprintf("revision-%d", i), k)
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
}
func insertUpload(t *testing.T, p *pgxpool.Pool, a accounts.Identity, id, k string) {
	t.Helper()
	_, e := p.Exec(context.Background(), `INSERT INTO uploads(id,owner_id,credential_id,name,size,status,stored_keys) VALUES($1,$2,$3,'../../unsafe.siq',4,'ready',$4)`, id, a.User.ID, a.SessionID, []string{k})
	if e != nil {
		t.Fatal(e)
	}
}
func TestAllRevisionsDeletedDedupFilterAndBlockedAlias(t *testing.T) {
	s, p, a := setup(t)
	body := []byte("data")
	k := key(body)
	s.objects = &memoryStore{data: map[string][]byte{k: body}}
	id := strings.Repeat("1", 32)
	uid := strings.Repeat("2", 32)
	insertPack(t, p, a.User.ID, id, k, false, true)
	insertUpload(t, p, a, uid, k)
	var b bytes.Buffer
	if e := s.Export(context.Background(), a, Options{}, &b); e != nil {
		t.Fatal(e)
	}
	f := unpack(t, b.Bytes())
	if f["objects/"+k] != "data" || !strings.Contains(f["pack_revisions.ndjson"], "revision-1") || !strings.Contains(f["pack_revisions.ndjson"], "revision-2") {
		t.Fatal(f)
	}
	page, e := s.Subjects(context.Background(), a, "pack", "")
	if e != nil || len(page.Items) != 1 || !page.Items[0].Deleted {
		t.Fatal(page, e)
	}
	b.Reset()
	if e = s.Export(context.Background(), a, Options{Kind: "upload", ID: uid}, &b); e != nil {
		t.Fatal(e)
	}
	f = unpack(t, b.Bytes())
	if strings.Contains(f["pack_revisions.ndjson"], "revision-1") {
		t.Fatal("filter leaked other content")
	}
	b.Reset()
	if e = s.Export(context.Background(), a, Options{MetadataOnly: true}, &b); e != nil {
		t.Fatal(e)
	}
	f = unpack(t, b.Bytes())
	if _, ok := f["objects/"+k]; ok {
		t.Fatal("metadata included blob")
	}
	if _, e = p.Exec(context.Background(), `UPDATE library_packs SET blocked=true WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	b.Reset()
	if e = s.Export(context.Background(), a, Options{Kind: "upload", ID: uid}, &b); e != nil {
		t.Fatal(e)
	}
	f = unpack(t, b.Bytes())
	if _, ok := f["objects/"+k]; ok {
		t.Fatal("blocked alias exported")
	}
	if !strings.Contains(f["objects.ndjson"], "blocked") {
		t.Fatal(f)
	}
	if e = s.Export(context.Background(), a, Options{Kind: "pack", ID: strings.Repeat("f", 32)}, io.Discard); e != ErrNotFound {
		t.Fatal(e)
	}
}
func TestBlobIntegrityLeaseAndRevalidation(t *testing.T) {
	for _, mode := range []string{"missing", "hash", "size", "cancel", "busy", "revoked", "blocked", "snapshot", "bytes", "objects"} {
		t.Run(mode, func(t *testing.T) {
			s, p, a := setup(t)
			body := []byte("data")
			k := key(body)
			id := strings.Repeat("1", 32)
			insertPack(t, p, a.User.ID, id, k, false, false)
			m := &memoryStore{data: map[string][]byte{k: body}}
			s.objects = m
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := ErrUnavailable
			switch mode {
			case "missing":
				delete(m.data, k)
			case "hash":
				m.data[k] = []byte("evil")
			case "size":
				m.before = func() { m.data[k] = []byte("extra data") }
			case "cancel":
				m.before = cancel
				want = context.Canceled
			case "busy":
				m.before = func() {
					if e := s.Export(ctx, a, Options{}, io.Discard); e != ErrBusy {
						t.Errorf("concurrent lease: %v", e)
					}
				}
				want = nil
			case "revoked":
				m.before = func() {
					_, e := p.Exec(ctx, `UPDATE account_credentials SET revoked_at=now()`)
					if e != nil {
						t.Fatal(e)
					}
				}
				want = ErrUnauthorized
			case "blocked":
				m.before = func() {
					_, e := p.Exec(ctx, `UPDATE library_packs SET blocked=true`)
					if e != nil {
						t.Fatal(e)
					}
				}
				want = ErrForbidden
			case "snapshot":
				m.before = func() {
					_, e := p.Exec(ctx, `UPDATE account_users SET display_name='AFTER SNAPSHOT'`)
					if e != nil {
						t.Fatal(e)
					}
				}
				want = nil
			case "bytes":
				s.maxBytes = 16
				want = ErrTooLarge
			case "objects":
				s.maxObjects = 0
				want = ErrTooLarge
			}
			var b bytes.Buffer
			if e := s.Export(ctx, a, Options{}, &b); e != want {
				t.Fatalf("got %v want %v", e, want)
			}
			if mode == "snapshot" && strings.Contains(unpack(t, b.Bytes())["profile.ndjson"], "AFTER SNAPSHOT") {
				t.Fatal("snapshot changed")
			}
			var n int
			if e := p.QueryRow(context.Background(), `SELECT count(*) FROM account_export_leases`).Scan(&n); e != nil || n != 0 {
				t.Fatal("leaked lease", n, e)
			}
		})
	}
}

func TestPrivateRelatedMetadata(t *testing.T) {
	s, p, a := setup(t)
	ctx := context.Background()
	other := strings.Repeat("b", 32)
	own := strings.Repeat("1", 32)
	foreign := strings.Repeat("2", 32)
	_, e := p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash) VALUES($1,'other','FOREIGN PROFILE','FOREIGN PASSWORD')`, other)
	if e != nil {
		t.Fatal(e)
	}
	insertPack(t, p, a.User.ID, own, "", false, false)
	insertPack(t, p, other, foreign, "", false, false)
	_, e = p.Exec(ctx, `INSERT INTO account_credentials(id,user_id,token_hash,kind,name,scopes,expires_at) VALUES('own-api',$1,'API SECRET','api','my token','{drafts:read}',now()+interval '1 day')`, a.User.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = p.Exec(ctx, `INSERT INTO account_audit(user_id,action) VALUES($1,'registered')`, a.User.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = p.Exec(ctx, `INSERT INTO library_decisions(pack_id,revision,moderator_id,decision,reason) VALUES($1,1,$2,'approve','Decision on own content'),($3,1,$4,'reject','Own moderation')`, own, other, foreign, a.User.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = p.Exec(ctx, `INSERT INTO library_reports(id,pack_id,revision,reporter_id,reason,status,resolution,resolution_reason,resolved_by,resolved_at) VALUES('other-report',$1,1,$2,'FOREIGN REPORT','resolved','resolved','Own resolution',$3,now()),('own-report',$4,1,$3,'Own report','open','','',NULL,NULL),('other-own-content',$1,2,$2,'OTHER PRIVATE TEXT','resolved','dismissed','Resolution on own content',$2,now())`, own, other, a.User.ID, foreign)
	if e != nil {
		t.Fatal(e)
	}
	var b bytes.Buffer
	if e = s.Export(ctx, a, Options{MetadataOnly: true}, &b); e != nil {
		t.Fatal(e)
	}
	f := unpack(t, b.Bytes())
	all := ""
	for _, v := range f {
		all += v
	}
	for _, v := range []string{"FOREIGN PROFILE", "FOREIGN PASSWORD", "API SECRET", "FOREIGN REPORT", "OTHER PRIVATE TEXT", other} {
		if strings.Contains(all, v) {
			t.Fatalf("leaked %s", v)
		}
	}
	for _, v := range []string{"my token", "registered", "Decision on own content", "Own moderation", "Own resolution", "Own report", "Resolution on own content"} {
		if !strings.Contains(all, v) {
			t.Fatalf("missing %s", v)
		}
	}
	b.Reset()
	if e = s.Export(ctx, a, Options{Kind: "pack", ID: own, MetadataOnly: true}, &b); e != nil {
		t.Fatal(e)
	}
	f = unpack(t, b.Bytes())
	if strings.Contains(f["pack_decisions.ndjson"], "Own moderation") || strings.Contains(f["reports.ndjson"], "Own report") {
		t.Fatal("unrelated action in filtered export")
	}
	if e = s.Export(ctx, a, Options{Kind: "pack", ID: foreign}, io.Discard); e != ErrNotFound {
		t.Fatal(e)
	}
}
func TestMetadataOnlyBypassesObjectLimit(t *testing.T) {
	s, p, a := setup(t)
	insertPack(t, p, a.User.ID, strings.Repeat("1", 32), strings.Repeat("0", 64), false, false)
	s.maxObjects = 0
	if e := s.Export(context.Background(), a, Options{MetadataOnly: true}, io.Discard); e != nil {
		t.Fatal(e)
	}
}
func TestSubjectsPaginationAndValidation(t *testing.T) {
	s, p, a := setup(t)
	ctx := context.Background()
	for i := 0; i < 51; i++ {
		insertUpload(t, p, a, fmt.Sprintf("%032x", i), "")
	}
	page, e := s.Subjects(ctx, a, "upload", "")
	if e != nil || len(page.Items) != 50 || page.NextCursor != fmt.Sprintf("%032x", 49) {
		t.Fatal(page, e)
	}
	last, e := s.Subjects(ctx, a, "upload", page.NextCursor)
	if e != nil || len(last.Items) != 1 || last.NextCursor != "" {
		t.Fatal(last, e)
	}
	for _, c := range []struct{ k, a string }{{"bad", ""}, {"pack", "../"}, {"theme", strings.Repeat("A", 32)}} {
		if _, e = s.Subjects(ctx, a, c.k, c.a); e != ErrInvalid {
			t.Fatal(e)
		}
	}
	for _, o := range []Options{{Kind: "pack"}, {ID: strings.Repeat("0", 32)}, {Kind: "bad", ID: strings.Repeat("0", 32)}, {Kind: "pack", ID: "../"}} {
		if e = s.Export(ctx, a, o, io.Discard); e != ErrInvalid {
			t.Fatal(e)
		}
	}
	a.APIToken = true
	if _, e = s.Subjects(ctx, a, "upload", ""); e != ErrForbidden {
		t.Fatal(e)
	}
	a.APIToken = false
	if _, e = p.Exec(ctx, `UPDATE account_users SET username=NULL,password_hash=NULL,deleted_at=now()`); e != nil {
		t.Fatal(e)
	}
	if e = s.Export(ctx, a, Options{}, io.Discard); e != ErrUnauthorized {
		t.Fatal(e)
	}
	if _, e = s.Subjects(ctx, a, "upload", ""); e != ErrUnauthorized {
		t.Fatal(e)
	}
}
func TestExpiredLeaseReplacementAndCleanupOnWriterFailure(t *testing.T) {
	s, p, a := setup(t)
	ctx := context.Background()
	_, e := p.Exec(ctx, `INSERT INTO account_export_leases(owner_id,id,expires_at) VALUES($1,$2,now()-interval '1 second')`, a.User.ID, strings.Repeat("0", 32))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Export(ctx, a, Options{}, failureWriter{}); e != ErrUnavailable {
		t.Fatal(e)
	}
	var n int
	if e = p.QueryRow(ctx, `SELECT count(*) FROM account_export_leases`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if e = s.Export(ctx, a, Options{}, io.Discard); e != nil {
		t.Fatal(e)
	}
}

type failureWriter struct{}

func (failureWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestThemeRevisionsVariantsAndManifest(t *testing.T) {
	s, p, a := setup(t)
	ctx := context.Background()
	id := strings.Repeat("3", 32)
	body := []byte("theme bytes")
	k := key(body)
	s.objects = &memoryStore{data: map[string][]byte{k: body}}
	_, e := p.Exec(ctx, `INSERT INTO themes(id,owner_id,current_revision,deleted_at) VALUES($1,$2,2,now())`, id, a.User.ID)
	if e != nil {
		t.Fatal(e)
	}
	for rev := 1; rev <= 2; rev++ {
		_, e = p.Exec(ctx, `INSERT INTO theme_versions(theme_id,revision,prepared,size_bytes) VALUES($1,$2,jsonb_build_object('manifest',jsonb_build_object('name','My theme'),'original_key',$3::text,'original_size',$4::bigint,'assets',jsonb_build_array(jsonb_build_object('name','../../../theme.png','prepared',jsonb_build_object('standard',jsonb_build_object('key',$3::text,'size',$4::bigint),'economy',jsonb_build_object('key',$3::text,'size',$4::bigint))))),$4)`, id, rev, k, len(body))
		if e != nil {
			t.Fatal(e)
		}
	}
	var b bytes.Buffer
	if e = s.Export(ctx, a, Options{Kind: "theme", ID: id}, &b); e != nil {
		t.Fatal(e)
	}
	f := unpack(t, b.Bytes())
	if f["objects/"+k] != string(body) || strings.Count(f["theme_versions.ndjson"], "My theme") != 2 {
		t.Fatal(f)
	}
	for _, v := range []string{"snapshot_at", "metadata_only", "theme", id} {
		if !strings.Contains(f["manifest.json"], v) {
			t.Fatalf("manifest missing %s", v)
		}
	}
	page, e := s.Subjects(ctx, a, "theme", "")
	if e != nil || len(page.Items) != 1 || !page.Items[0].Deleted || page.Items[0].Name != "My theme" {
		t.Fatal(page, e)
	}
	_, e = p.Exec(ctx, `UPDATE theme_versions SET prepared=jsonb_set(prepared,'{original_size}','999')`)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Export(ctx, a, Options{}, io.Discard); e != ErrUnavailable {
		t.Fatal("size mismatch", e)
	}
}

func TestSelectedExportIgnoresUnrelatedBlockedObjectCount(t *testing.T) {
	s, p, a := setup(t)
	ctx := context.Background()
	data := []byte("selected")
	k := key(data)
	s.objects = &memoryStore{data: map[string][]byte{k: data}}
	insertPack(t, p, a.User.ID, strings.Repeat("1", 32), strings.Repeat("0", 64), true, false)
	insertPack(t, p, a.User.ID, strings.Repeat("2", 32), strings.Repeat("1", 64), true, false)
	id := strings.Repeat("3", 32)
	insertUpload(t, p, a, id, k)
	s.maxObjects = 1
	var b bytes.Buffer
	if e := s.Export(ctx, a, Options{Kind: "upload", ID: id}, &b); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(unpack(t, b.Bytes())["uploads.ndjson"], k) {
		t.Fatal("upload object mapping missing")
	}
}
func TestExportLeaseMigrationDownUp(t *testing.T) {
	ctx := context.Background()
	p := testutil.Pool(t)
	db := stdlib.OpenDB(*p.Config().ConnConfig)
	defer db.Close()
	provider, e := goose.NewProvider(goose.DialectPostgres, db, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = provider.UpTo(ctx, 14); e != nil {
		t.Fatal(e)
	}
	if _, e = provider.DownTo(ctx, 13); e != nil {
		t.Fatal(e)
	}
	var absent bool
	if e = p.QueryRow(ctx, `SELECT to_regclass('account_export_leases') IS NULL`).Scan(&absent); e != nil || !absent {
		t.Fatal(absent, e)
	}
	if _, e = provider.UpTo(ctx, 14); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = p.QueryRow(ctx, `SELECT count(*) FROM account_export_leases`).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
}

func TestAuditExcludesSessionIdentifiers(t *testing.T) {
	s, p, a := setup(t)
	ctx := context.Background()
	_, e := p.Exec(ctx, `INSERT INTO account_audit(user_id,action,target_id) VALUES($1,'logout','PRIVATE_SESSION_IDENTIFIER'),($1,'token_created','public-api-id')`, a.User.ID)
	if e != nil {
		t.Fatal(e)
	}
	var b bytes.Buffer
	if e = s.Export(ctx, a, Options{MetadataOnly: true}, &b); e != nil {
		t.Fatal(e)
	}
	audit := unpack(t, b.Bytes())["account_audit.ndjson"]
	if strings.Contains(audit, "PRIVATE_SESSION_IDENTIFIER") || !strings.Contains(audit, "logout") || !strings.Contains(audit, "created_at") || !strings.Contains(audit, "public-api-id") {
		t.Fatal(audit)
	}
}
