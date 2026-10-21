package retention

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"io"
	"siplatform/internal/blobrefs"
	"siplatform/migrations"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"siplatform/internal/blob"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
)

type fakeStore struct {
	blob.Store
	fail     bool
	deleted  []string
	onDelete func(context.Context) error
}

func (s *fakeStore) Delete(ctx context.Context, k string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.fail {
		return errors.New("provider SECRET")
	}
	s.deleted = append(s.deleted, k)
	if s.onDelete != nil {
		return s.onDelete(ctx)
	}
	return nil
}
func (*fakeStore) Put(context.Context, string, io.Reader, int64, string) error { return nil }
func fixture(t *testing.T) (*Service, *pgxpool.Pool, *fakeStore) {
	t.Helper()
	p := testutil.Pool(t)
	if e := postgres.Migrate(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	exec(t, p, `INSERT INTO account_users(id,username,display_name,password_hash) VALUES('a','aaa','Name','secret'),('b','bbb','Other','secret'); INSERT INTO account_credentials(id,user_id,token_hash,kind,expires_at) VALUES('c','a','secret','session',now()+interval '1 day')`)
	b := &fakeStore{}
	return New(p, b), p, b
}
func exec(t *testing.T, p *pgxpool.Pool, q string, args ...any) {
	t.Helper()
	if _, e := p.Exec(context.Background(), q, append([]any{pgx.QueryExecModeSimpleProtocol}, args...)...); e != nil {
		t.Fatal(e)
	}
}
func pack(t *testing.T, p *pgxpool.Pool, id, owner, k, age string) {
	t.Helper()
	tx, e := p.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	_, e = tx.Exec(context.Background(), `INSERT INTO library_packs(id,owner_id,current_revision,deleted_at) VALUES($1,$2,1,now()-$3::interval)`, id, owner, age)
	if e != nil {
		t.Fatal(e)
	}
	_, e = tx.Exec(context.Background(), `INSERT INTO library_revisions(pack_id,revision,card,document,assets,original_key,issues,size_bytes) VALUES($1,1,'{}','{}','[]',$2,'[]',1)`, id, k)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func count(t *testing.T, p *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if e := p.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestDurableDeleteRetry(t *testing.T) {
	s, p, b := fixture(t)
	pack(t, p, "old", "a", strings.Repeat("a", 64), "31 days")
	b.fail = true
	st, e := s.Sweep(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if st.Packs != 1 || st.ObjectsFailed != 1 || count(t, p, "library_packs") != 0 || count(t, p, "retention_candidates") != 1 {
		t.Fatalf("%+v", st)
	}
	var code string
	if e = p.QueryRow(context.Background(), `SELECT last_error FROM retention_candidates`).Scan(&code); e != nil || strings.Contains(code, "SECRET") {
		t.Fatal(code, e)
	}
	b.fail = false
	exec(t, p, `UPDATE retention_candidates SET retry_at=now()`)
	st, e = s.Sweep(context.Background())
	if e != nil || st.ObjectsDeleted != 1 || count(t, p, "retention_candidates") != 0 {
		t.Fatal(st, e)
	}
}
func TestSharedReferencesAndExport(t *testing.T) {
	s, p, b := fixture(t)
	k := strings.Repeat("b", 64)
	pack(t, p, "old", "a", k, "31 days")
	pack(t, p, "young", "b", k, "29 days")
	exec(t, p, `INSERT INTO account_export_leases VALUES('a',$1,now()+interval '1 hour')`, strings.Repeat("a", 32))
	st, e := s.Sweep(context.Background())
	if e != nil || st.Packs != 0 {
		t.Fatal(st, e)
	}
	exec(t, p, `DELETE FROM account_export_leases`)
	st, e = s.Sweep(context.Background())
	if e != nil || st.Packs != 1 || len(b.deleted) != 0 {
		t.Fatal(st, e, b.deleted)
	}
	exec(t, p, `UPDATE library_packs SET deleted_at=now()-interval '31 days'`)
	st, e = s.Sweep(context.Background())
	if e != nil || st.Packs != 1 || len(b.deleted) != 1 {
		t.Fatal(st, e, b.deleted)
	}
}

func TestUploadResultsAndCredentialFK(t *testing.T) {
	s, p, b := fixture(t)
	k := strings.Repeat("c", 64)
	u := strings.Repeat("1", 32)
	exec(t, p, `UPDATE account_credentials SET expires_at=now()-interval '31 days'; INSERT INTO uploads(id,owner_id,credential_id,name,size,status,purpose,media_kind,result_asset,staged_deleted,updated_at) VALUES($1,'a','c','x',1,'ready','media','image',jsonb_build_object('prepared',jsonb_build_object('standard',jsonb_build_object('key',$2::text))),false,now()-interval '31 days')`, u, k)
	st, e := s.Sweep(context.Background())
	if e != nil || st.Uploads != 0 || st.Credentials != 0 || len(b.deleted) != 0 {
		t.Fatal(st, e)
	}
	exec(t, p, `UPDATE uploads SET staged_deleted=true`)
	st, e = s.Sweep(context.Background())
	if e != nil || st.Uploads != 1 || st.Credentials != 1 || len(b.deleted) != 1 {
		t.Fatal(st, e, b.deleted)
	}
}
func TestPinsAndThemeVariants(t *testing.T) {
	s, p, b := fixture(t)
	k := strings.Repeat("d", 64)
	pack(t, p, "p", "a", k, "31 days")
	tid := strings.Repeat("2", 32)
	tk := strings.Repeat("e", 64)
	exec(t, p, `INSERT INTO themes(id,owner_id,current_revision,deleted_at) VALUES($1,'a',1,now()-interval '31 days'); INSERT INTO theme_versions(theme_id,revision,prepared,size_bytes) VALUES($1,1,jsonb_build_object('assets',jsonb_build_array(jsonb_build_object('prepared',jsonb_build_object('economy',jsonb_build_object('key',$2::text))))),1)`, tid, tk)
	exec(t, p, `INSERT INTO rooms(id,snapshot,seq,invite_hash) VALUES('r','{}',1,'invite')`)
	exec(t, p, `INSERT INTO room_packs(room_id,owner_id,pack_id,revision,title,theme) VALUES('r','b','p',1,'title','classic'); INSERT INTO room_theme_pins(room_id,theme_id,revision,hash) VALUES('r',$1,1,$2)`, tid, tk)
	st, e := s.Sweep(context.Background())
	if e != nil || st.Packs != 0 || st.Themes != 0 {
		t.Fatal(st, e)
	}
	exec(t, p, `DELETE FROM room_theme_pins; DELETE FROM room_packs`)
	st, e = s.Sweep(context.Background())
	if e != nil || st.Packs != 1 || st.Themes != 1 || len(b.deleted) != 2 {
		t.Fatal(st, e, b.deleted)
	}
}
func TestDeletedActorRedactionAndAudits(t *testing.T) {
	s, p, _ := fixture(t)
	pack(t, p, "foreign", "b", strings.Repeat("f", 64), "1 day")
	exec(t, p, `INSERT INTO library_reports(id,pack_id,revision,reporter_id,reason,status,resolution,resolution_reason,resolved_by,resolved_at) VALUES('r','foreign',1,'b','other text','resolved','dismissed','own resolution','a',now()),('own','foreign',1,'a','own complaint','open','','',NULL,NULL); INSERT INTO library_decisions(pack_id,revision,moderator_id,decision,reason) VALUES('foreign',1,'a','reject','own decision'),('foreign',1,'b','approve','other decision'); INSERT INTO account_audit(user_id,action,created_at) VALUES('a','login',now()),('b','login',now()-interval '91 days'),('b','login',now()-interval '89 days'); UPDATE account_users SET deleted_at=now()-interval '31 days',username=NULL,password_hash=NULL WHERE id='a'`)
	st, e := s.Sweep(context.Background())
	if e != nil || st.Audits != 2 || st.Redacted != 4 || count(t, p, "library_packs") != 1 {
		t.Fatal(st, e)
	}
	var reason, resolution string
	if e = p.QueryRow(context.Background(), `SELECT reason,resolution_reason FROM library_reports WHERE id='r'`).Scan(&reason, &resolution); e != nil || reason != "other text" || resolution != redacted {
		t.Fatal(reason, resolution, e)
	}
	st, e = s.Sweep(context.Background())
	if e != nil || st.Redacted != 0 {
		t.Fatal(st, e)
	}
}
func TestActiveAndReadyReferencesAcrossOwners(t *testing.T) {
	for _, status := range []string{"processing", "ready", "failed"} {
		t.Run(status, func(t *testing.T) {
			s, p, b := fixture(t)
			k := strings.Repeat("a", 64)
			pack(t, p, "p", "b", k, "31 days")
			exec(t, p, `INSERT INTO uploads(id,owner_id,credential_id,name,size,status,purpose,media_kind,result_asset) VALUES($1,'a','c','x',1,$2,'media','image',jsonb_build_object('key',$3::text))`, strings.Repeat("3", 32), status, k)
			if status == "failed" {
				exec(t, p, `INSERT INTO account_export_leases VALUES('a',$1,now()+interval '1 hour')`, strings.Repeat("4", 32))
			}
			st, e := s.Sweep(context.Background())
			if e != nil || st.Packs != 1 || st.ObjectsReferenced != 1 || len(b.deleted) != 0 {
				t.Fatal(st, e, b.deleted)
			}
		})
	}
}
func TestCanceledSweepAndBatchBound(t *testing.T) {
	s, p, b := fixture(t)
	for i := 0; i < batchSize+1; i++ {
		pack(t, p, fmt.Sprintf("p%02d", i), "a", fmt.Sprintf("%064x", i), "31 days")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := s.Sweep(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if count(t, p, "library_packs") != batchSize+1 || len(b.deleted) != 0 {
		t.Fatal("canceled work mutated data")
	}
	st, e := s.Sweep(context.Background())
	if e != nil || st.Packs != batchSize || count(t, p, "library_packs") != 1 {
		t.Fatal(st, e)
	}
}

func TestAttachedUploadReceiptsPreserveRevisionBytes(t *testing.T) {
	s, p, b := fixture(t)
	k := strings.Repeat("b", 64)
	pack(t, p, "attached", "a", k, "29 days")
	u := strings.Repeat("5", 32)
	exec(t, p, `INSERT INTO uploads(id,owner_id,credential_id,name,size,status,stored_keys,staged_deleted,updated_at) VALUES($1,'a','c','x',1,'ready',ARRAY[$2],true,now()-interval '31 days'); INSERT INTO library_media_receipts(pack_id,upload_id,revision) VALUES('attached',$1,1)`, u, k)
	st, e := s.Sweep(context.Background())
	if e != nil || st.Uploads != 1 || len(b.deleted) != 0 || count(t, p, "library_media_receipts") != 0 || count(t, p, "library_revisions") != 1 {
		t.Fatal(st, e, b.deleted)
	}
}
func TestBoundariesAndDeletedOwner(t *testing.T) {
	s, p, _ := fixture(t)
	pack(t, p, "old", "a", strings.Repeat("6", 64), "30 days 1 second")
	pack(t, p, "young", "a", strings.Repeat("7", 64), "29 days 23 hours 59 minutes")
	pack(t, p, "published", "b", strings.Repeat("8", 64), "0 days")
	exec(t, p, `UPDATE library_packs SET deleted_at=NULL WHERE id='published'; UPDATE library_revisions SET status='published' WHERE pack_id='published'`)
	st, e := s.Sweep(context.Background())
	if e != nil || st.Packs != 1 || count(t, p, "library_packs") != 2 {
		t.Fatal(st, e)
	}
	exec(t, p, `UPDATE account_users SET deleted_at=now()-interval '30 days 1 second',username=NULL,password_hash=NULL WHERE id='b'`)
	st, e = s.Sweep(context.Background())
	if e != nil || st.Packs != 1 || count(t, p, "library_packs") != 1 {
		t.Fatal(st, e)
	}
}
func TestPreparedVariantContentReference(t *testing.T) {
	s, p, b := fixture(t)
	k := strings.Repeat("9", 64)
	pack(t, p, "old", "a", k, "31 days")
	pack(t, p, "prepared", "b", "", "1 day")
	exec(t, p, `UPDATE library_revisions SET assets=jsonb_build_array(jsonb_build_object('prepared',jsonb_build_object('standard',jsonb_build_object('key',$1::text)))) WHERE pack_id='prepared'`, k)
	st, e := s.Sweep(context.Background())
	if e != nil || st.ObjectsReferenced != 1 || len(b.deleted) != 0 {
		t.Fatal(st, e)
	}
}

func TestDeleteCancellationLeavesRetryCandidate(t *testing.T) {
	s, p, b := fixture(t)
	k := strings.Repeat("a", 64)
	exec(t, p, `INSERT INTO retention_candidates(key) VALUES($1)`, k)
	ctx, cancel := context.WithCancel(context.Background())
	b.onDelete = func(context.Context) error { cancel(); return nil }
	if _, e := s.Sweep(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if count(t, p, "retention_candidates") != 1 {
		t.Fatal("lost candidate after canceled delete")
	}
	b.onDelete = nil
	st, e := s.Sweep(context.Background())
	if e != nil || st.ObjectsDeleted != 1 || len(b.deleted) != 2 {
		t.Fatal(st, e)
	}
}
func TestDeleteFencesConcurrentReservation(t *testing.T) {
	s, p, b := fixture(t)
	k := strings.Repeat("c", 64)
	exec(t, p, `INSERT INTO retention_candidates(key) VALUES($1)`, k)
	started := make(chan struct{})
	done := make(chan error, 1)
	b.onDelete = func(_ context.Context) error {
		ctx := context.Background()
		go func() {
			tx, e := p.Begin(ctx)
			if e != nil {
				done <- e
				return
			}
			defer tx.Rollback(context.Background())
			close(started)
			_, e = tx.Exec(ctx, blobrefs.GuardSQL)
			if e == nil {
				e = tx.Commit(ctx)
			}
			done <- e
		}()
		<-started
		select {
		case e := <-done:
			return fmt.Errorf("reservation passed delete guard: %v", e)
		case <-time.After(50 * time.Millisecond):
			return nil
		}
	}
	st, e := s.Sweep(context.Background())
	if e != nil || st.ObjectsDeleted != 1 {
		t.Fatal(st, e)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("writer did not resume")
	}
}
func TestMigration15DownUp(t *testing.T) {
	p := testutil.Pool(t)
	db := stdlib.OpenDB(*p.Config().ConnConfig)
	defer db.Close()
	provider, e := goose.NewProvider(goose.DialectPostgres, db, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if _, e = provider.UpTo(ctx, 15); e != nil {
		t.Fatal(e)
	}
	exec(t, p, `INSERT INTO retention_candidates(key) VALUES($1)`, strings.Repeat("a", 64))
	if _, e = provider.DownTo(ctx, 14); e != nil {
		t.Fatal(e)
	}
	if _, e = provider.UpTo(ctx, 15); e != nil {
		t.Fatal(e)
	}
	if count(t, p, "retention_candidates") != 0 {
		t.Fatal("migration recreated stale data")
	}
}

func TestMetadataCommitFailureKeepsReferences(t *testing.T) {
	s, p, b := fixture(t)
	pack(t, p, "p", "a", strings.Repeat("d", 64), "31 days")
	exec(t, p, `CREATE FUNCTION reject_retention_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected commit failure'; END $$; CREATE CONSTRAINT TRIGGER reject_retention_commit AFTER DELETE ON library_packs DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_retention_commit()`)
	if _, e := s.Sweep(context.Background()); e == nil {
		t.Fatal("expected commit failure")
	}
	if count(t, p, "library_packs") != 1 || count(t, p, "retention_candidates") != 0 || len(b.deleted) != 0 {
		t.Fatal("partial metadata commit")
	}
	exec(t, p, `DROP TRIGGER reject_retention_commit ON library_packs`)
	st, e := s.Sweep(context.Background())
	if e != nil || st.Packs != 1 || st.ObjectsDeleted != 1 {
		t.Fatal(st, e)
	}
}

func TestDeleteHasShortTimeoutAndSafeRetry(t *testing.T) {
	s, p, b := fixture(t)
	exec(t, p, `INSERT INTO retention_candidates(key) VALUES($1)`, strings.Repeat("e", 64))
	b.onDelete = func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 3*time.Second {
			return errors.New("missing timeout")
		}
		return context.DeadlineExceeded
	}
	st, e := s.Sweep(context.Background())
	if e != nil || st.ObjectsFailed != 1 {
		t.Fatal(st, e)
	}
	var code string
	var attempts int
	if e = p.QueryRow(context.Background(), `SELECT last_error,attempts FROM retention_candidates`).Scan(&code, &attempts); e != nil || code != "delete_timeout" || attempts != 1 {
		t.Fatal(code, attempts, e)
	}
}
func TestExportDefersAllOwnerMetadata(t *testing.T) {
	s, p, _ := fixture(t)
	exec(t, p, `UPDATE account_users SET deleted_at=now()-interval '31 days',username=NULL,password_hash=NULL WHERE id='a'; UPDATE account_credentials SET expires_at=now()-interval '31 days'; INSERT INTO account_audit(user_id,action) VALUES('a','login'); INSERT INTO account_export_leases VALUES('a',$1,now()+interval '1 hour')`, strings.Repeat("f", 32))
	st, e := s.Sweep(context.Background())
	if e != nil || st.Audits != 0 || st.Credentials != 0 || st.Redacted != 0 {
		t.Fatal(st, e)
	}
	exec(t, p, `UPDATE account_export_leases SET expires_at=now()-interval '1 second'`)
	st, e = s.Sweep(context.Background())
	if e != nil || st.Audits != 1 || st.Credentials != 1 || st.Redacted != 1 {
		t.Fatal(st, e)
	}
}

func TestThemeModerationRedactsOnlyDeletedActor(t *testing.T) {
	s, p, _ := fixture(t)
	exec(t, p, `INSERT INTO themes(id,owner_id,current_revision) VALUES($1,'b',1); INSERT INTO theme_versions(theme_id,revision,prepared,size_bytes) VALUES($1,1,'{}',1); INSERT INTO theme_moderation(theme_id,actor_id,blocked,reason) VALUES($1,'a',true,'own private reason'),($1,'b',false,'foreign text'); UPDATE account_users SET deleted_at=now()-interval '31 days',username=NULL,password_hash=NULL WHERE id='a'`, strings.Repeat("a", 32))
	st, e := s.Sweep(context.Background())
	if e != nil || st.Redacted != 2 || st.Themes != 0 {
		t.Fatal(st, e)
	}
	var own, other string
	if e = p.QueryRow(context.Background(), `SELECT reason FROM theme_moderation WHERE actor_id='a'`).Scan(&own); e != nil {
		t.Fatal(e)
	}
	if e = p.QueryRow(context.Background(), `SELECT reason FROM theme_moderation WHERE actor_id='b'`).Scan(&other); e != nil || own != redacted || other != "foreign text" {
		t.Fatal(own, other, e)
	}
}
func TestObjectBatchBound(t *testing.T) {
	s, p, b := fixture(t)
	exec(t, p, `INSERT INTO retention_candidates(key) SELECT lpad(to_hex(n),64,'0') FROM generate_series(1,101) n`)
	st, e := s.Sweep(context.Background())
	if e != nil || st.ObjectsDeleted != 100 || len(b.deleted) != 100 || count(t, p, "retention_candidates") != 1 {
		t.Fatal(st, e)
	}
}
