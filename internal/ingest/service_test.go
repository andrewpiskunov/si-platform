package ingest

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"siplatform/internal/accounts"
	"siplatform/internal/blob"
	"siplatform/internal/library"
	"siplatform/internal/packs"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
)

var bg = context.Background()

type fixture struct {
	service *Service
	pool    *pgxpool.Pool
	objects blob.Store
	lib     *library.Service
	dir     string
	owner   accounts.Identity
}

func setup(t *testing.T) fixture {
	t.Helper()
	pool := testutil.Pool(t)
	if err := postgres.Migrate(bg, pool); err != nil {
		t.Fatal(err)
	}
	objects, err := blob.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	lib := library.New(pool)
	s, err := New(pool, objects, lib, Options{Directory: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return fixture{s, pool, objects, lib, dir, actor(t, pool, "owner", "session")}
}
func actor(t *testing.T, p *pgxpool.Pool, id, kind string, scopes ...string) accounts.Identity {
	t.Helper()
	credential := id + "-" + kind + strings.Join(scopes, "-")
	sum := sha256.Sum256([]byte(credential))
	if _, err := p.Exec(bg, `INSERT INTO account_users(id,username,display_name,password_hash) VALUES($1,$1,$1,'unused') ON CONFLICT DO NOTHING`, id); err != nil {
		t.Fatal(err)
	}
	if scopes == nil {
		scopes = []string{}
	}
	if _, err := p.Exec(bg, `INSERT INTO account_credentials(id,user_id,token_hash,kind,scopes,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '1 day')`, credential, id, hex.EncodeToString(sum[:]), kind, scopes); err != nil {
		t.Fatal(err)
	}
	return accounts.Identity{User: accounts.User{ID: id, Role: "author", DisplayName: id}, SessionID: credential, APIToken: kind == "api", Scopes: scopes}
}
func createUpload(t *testing.T, f fixture, data []byte) Upload {
	t.Helper()
	u, err := f.service.Create(bg, f.owner, "quiz.siq", int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	u, err = f.service.Append(bg, f.owner, u.ID, 0, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return u
}
func queue(t *testing.T, f fixture, data []byte) Upload {
	t.Helper()
	u := createUpload(t, f, data)
	u, err := f.service.Complete(bg, f.owner, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
func awaitStatus(t *testing.T, f fixture, id, status string) Upload {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		u, err := f.service.Get(bg, f.owner, id)
		if err != nil {
			t.Fatal(err)
		}
		if u.Status == status {
			return u
		}
		if u.Status == "failed" && status != "failed" {
			t.Fatalf("import failed: %+v", u)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("upload never reached %s", status)
	return Upload{}
}

const textXML = `<package name="Imported" version="5.0" language="ru"><rounds><round name="Round"><themes><theme name="Theme"><questions><question price="100"><params><param name="question" type="content"><item type="text">Question?</item></param></params><right><answer>Secret</answer></right></question></questions></theme></themes></round></rounds></package>`

func archive(t *testing.T, xml string, assets map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	files := map[string]string{"content.xml": xml}
	for name, data := range assets {
		files[name] = data
	}
	for name, data := range files {
		out, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(out, data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// Missing offset CAS/truncation or length validation would duplicate or corrupt resumed bytes.
func TestResumePartialBodiesAndConcurrentAppend(t *testing.T) {
	f := setup(t)
	u, err := f.service.Create(bg, f.owner, "quiz.siq", 8)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Append(bg, f.owner, u.ID, 0, strings.NewReader("abc"), 4); !errors.Is(err, ErrInvalid) {
		t.Fatal("short body", err)
	}
	if _, err = f.service.Append(bg, f.owner, u.ID, 0, strings.NewReader("abcde"), 4); !errors.Is(err, ErrInvalid) {
		t.Fatal("long body", err)
	}
	got, err := f.service.Get(bg, f.owner, u.ID)
	if err != nil || got.Offset != 0 {
		t.Fatal(got, err)
	}
	u, err = f.service.Append(bg, f.owner, u.ID, 0, strings.NewReader("abcd"), 4)
	if err != nil || u.Offset != 4 {
		t.Fatal(u, err)
	}
	// A crash can leave bytes on disk after fsync but before the DB offset commits.
	if err = os.WriteFile(filepath.Join(f.dir, u.ID), []byte("abcdjunk"), 0600); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := f.service.Append(bg, f.owner, u.ID, 4, strings.NewReader("efgh"), 4)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	good, bad := 0, 0
	for err := range errs {
		if err == nil {
			good++
		} else if errors.Is(err, ErrConflict) {
			bad++
		} else {
			t.Fatal(err)
		}
	}
	if good != 1 || bad != 1 {
		t.Fatal("append raced", good, bad)
	}
	data, err := os.ReadFile(filepath.Join(f.dir, u.ID))
	if err != nil || string(data) != "abcdefgh" {
		t.Fatal(string(data), err)
	}
	if _, err = f.service.Append(bg, f.owner, u.ID, 8, strings.NewReader("x"), 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("overflow", err)
	}
}

// Missing authorization, validation or quota checks would expose/allocate private uploads.
func TestUploadAuthorizationLimitsAndExpiry(t *testing.T) {
	f := setup(t)
	foreign := actor(t, f.pool, "other", "session")
	read := actor(t, f.pool, "owner", "api", "drafts:read")
	write := actor(t, f.pool, "owner", "api", "drafts:write")
	for _, size := range []int64{-1, 0, 250<<20 + 1} {
		if _, err := f.service.Create(bg, f.owner, "quiz.siq", size); !errors.Is(err, ErrInvalid) {
			t.Fatal(size, err)
		}
	}
	if _, err := f.service.Create(bg, accounts.Identity{}, "quiz.siq", 10); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	if _, err := f.service.Create(bg, read, "quiz.siq", 10); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	u, err := f.service.Create(bg, write, "quiz.siq", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Get(bg, read, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Get(bg, write, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Get(bg, foreign, u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = f.service.Append(bg, foreign, u.ID, 0, strings.NewReader("a"), 1); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = f.service.Append(bg, read, u.ID, 0, strings.NewReader("a"), 1); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err = f.service.Append(bg, f.owner, u.ID, 0, strings.NewReader("a"), 4<<20+1); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err = f.service.Complete(bg, f.owner, u.ID); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = f.service.Create(bg, f.owner, "two.siq", 10); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Create(bg, f.owner, "three.siq", 10); !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(bg, "UPDATE uploads SET expires_at=now()-interval '1 second' WHERE id=$1", u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Create(bg, f.owner, "three.siq", 10); err != nil {
		t.Fatal("expired quota", err)
	}
	if _, err = f.service.Append(bg, f.owner, u.ID, 0, strings.NewReader("a"), 1); !errors.Is(err, ErrConflict) {
		t.Fatal("expired write", err)
	}
	if _, err = f.pool.Exec(bg, "UPDATE account_credentials SET revoked_at=now() WHERE id=$1", write.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Get(bg, write, u.ID); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("stale identity", err)
	}
}

// Missing cancellation checks could commit a partially read request.
func TestCanceledAppendPreservesOffset(t *testing.T) {
	f := setup(t)
	u, err := f.service.Create(bg, f.owner, "quiz.siq", 4)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err = f.service.Append(ctx, f.owner, u.ID, 0, strings.NewReader("abcd"), 4); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	got, err := f.service.Get(bg, f.owner, u.ID)
	if err != nil || got.Offset != 0 {
		t.Fatal(got, err)
	}
}

// Missing durable queue/import-id recovery creates duplicate drafts or loses original bytes.
func TestWorkerImportsOriginalAndRecoversIdempotently(t *testing.T) {
	f := setup(t)
	data := archive(t, textXML, nil)
	u := queue(t, f, data)
	if u.Status != "queued" {
		t.Fatal(u)
	}
	again, err := f.service.Complete(bg, f.owner, u.ID)
	if err != nil || again.Status != "queued" {
		t.Fatal(again, err)
	}
	f.service.Start(bg)
	u = awaitStatus(t, f, u.ID, "ready")
	draft, err := f.lib.Get(bg, f.owner, u.DraftID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Document.Name != "Imported" || !packs.Playable(draft.Issues) {
		t.Fatal(draft)
	}
	r, err := f.objects.Get(bg, draft.OriginalKey, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	original, err := io.ReadAll(r)
	r.Close()
	if err != nil || !bytes.Equal(original, data) {
		t.Fatal("original changed", err)
	}
	f.service.Close()
	// Simulate crash after Create but before final upload status/temporary cleanup.
	if err = os.WriteFile(filepath.Join(f.dir, u.ID), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(bg, "UPDATE uploads SET status='processing',draft_id='',staged_deleted=false WHERE id=$1", u.ID); err != nil {
		t.Fatal(err)
	}
	next, err := New(f.pool, f.objects, f.lib, Options{Directory: f.dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	f.service = next
	next.Start(bg)
	recovered := awaitStatus(t, f, u.ID, "ready")
	if recovered.DraftID != u.DraftID {
		t.Fatal("duplicate draft", recovered, u)
	}
	mine, err := f.lib.Mine(bg, f.owner)
	if err != nil || len(mine) != 1 {
		t.Fatal(mine, err)
	}
	next.Close()
	if _, err = os.Stat(filepath.Join(f.dir, u.ID)); !os.IsNotExist(err) {
		t.Fatal("staged file retained", err)
	}
}

// Invalid containers fail; recoverable document errors and unverified media remain visible in drafts.
func TestMalformedAndMediaDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, xml string
		files     map[string]string
		failed    bool
		code      string
	}{
		{name: "bad container", failed: true},
		{name: "incomplete document", xml: `<package name="Broken" version="5.0"/>`, code: "missing_rounds"},
		{name: "unverified audio", xml: strings.Replace(textXML, `<item type="text">Question?</item>`, `<item type="audio" isRef="true">song.mp3</item>`, 1), files: map[string]string{"Audio/song.mp3": "ID3audio"}, code: "media_pending"},
		{name: "active image", xml: strings.Replace(textXML, `<item type="text">Question?</item>`, `<item type="image" isRef="true">image.svg</item>`, 1), files: map[string]string{"Images/image.svg": `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`}, code: "unsafe_media"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			data := []byte("malformed")
			if !tc.failed {
				data = archive(t, tc.xml, tc.files)
			}
			u := queue(t, f, data)
			f.service.Start(bg)
			if tc.failed {
				u = awaitStatus(t, f, u.ID, "failed")
				if u.Error != "invalid_archive" {
					t.Fatal(u)
				}
				return
			}
			u = awaitStatus(t, f, u.ID, "ready")
			d, err := f.lib.Get(bg, f.owner, u.DraftID, 0)
			if err != nil {
				t.Fatal(err)
			}
			if packs.Playable(d.Issues) {
				t.Fatal("unsafe draft playable", d.Issues)
			}
			if tc.code != "missing_rounds" {
				found := false
				for _, issue := range d.Issues {
					if issue.Code == tc.code {
						found = true
					}
				}
				if !found {
					t.Fatal("missing diagnosis", tc.code, d.Issues)
				}
			}
			for _, asset := range d.Assets {
				if strings.Contains(asset.Name, "svg") && asset.ContentType != "application/octet-stream" {
					t.Fatal(asset)
				}
			}
		})
	}
}

// Queued work must not import after its originating credential is revoked/expired or its account deleted.
func TestQueuedWorkRechecksAuthorization(t *testing.T) {
	for _, mutation := range []string{"revoked_at=now()", "expires_at=now()-interval '1 second'", "deleted"} {
		t.Run(mutation, func(t *testing.T) {
			f := setup(t)
			u := queue(t, f, archive(t, textXML, nil))
			if mutation == "deleted" {
				if _, err := f.pool.Exec(bg, "UPDATE account_users SET deleted_at=now(),username=NULL,password_hash=NULL WHERE id=$1", f.owner.User.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := f.pool.Exec(bg, "UPDATE account_credentials SET "+mutation+" WHERE id=$1", f.owner.SessionID); err != nil {
					t.Fatal(err)
				}
			}
			f.service.Start(bg)
			deadline := time.Now().Add(10 * time.Second)
			var status, code string
			for time.Now().Before(deadline) {
				if err := f.pool.QueryRow(bg, "SELECT status,error FROM uploads WHERE id=$1", u.ID).Scan(&status, &code); err != nil {
					t.Fatal(err)
				}
				if status == "failed" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if status != "failed" || code != "authorization_expired" {
				t.Fatal(status, code)
			}
			var count int
			if err := f.pool.QueryRow(bg, "SELECT count(*) FROM library_packs").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatal("imported revoked job", count)
			}
		})
	}
}

// Advisory-locked quotas must apply across service instances and different authors.
func TestGlobalQuota(t *testing.T) {
	f := setup(t)
	for i := range 8 {
		a := actor(t, f.pool, fmt.Sprintf("user%d", i), "session")
		for range 2 {
			if _, err := f.service.Create(bg, a, "quiz.siq", 250<<20); err != nil {
				t.Fatal(i, err)
			}
		}
	}
	if _, err := f.service.Create(bg, f.owner, "overflow.siq", 1); !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
}

// A failed leader must stop before saving a draft; a standby alone may recover its job.
func TestLeaseLossCancelsOldWorkerAndStandbyRecovers(t *testing.T) {
	f := setup(t)
	gate := &gatedStore{Store: f.objects, entered: make(chan struct{}, 2)}
	f.service.objects = gate
	u := queue(t, f, archive(t, textXML, nil))
	f.service.Start(bg)
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	standby, err := New(f.pool, f.objects, f.lib, Options{Directory: f.dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(standby.Close)
	standby.Start(bg)
	// Only the lease holder may reset processing jobs or start additional workers.
	time.Sleep(100 * time.Millisecond)
	var status string
	if err = f.pool.QueryRow(bg, "SELECT status FROM uploads WHERE id=$1", u.ID).Scan(&status); err != nil || status != "processing" {
		t.Fatal(status, err)
	}
	var terminated bool
	err = f.pool.QueryRow(bg, `SELECT pg_terminate_backend(pid) FROM pg_locks WHERE locktype='advisory' AND granted
 AND classid=((hashtextextended(current_database()||':'||current_schema()||':si-ingest-owner',0)>>32)&4294967295)::oid
 AND objid=(hashtextextended(current_database()||':'||current_schema()||':si-ingest-owner',0)&4294967295)::oid`).Scan(&terminated)
	if err != nil || !terminated {
		t.Fatal("terminate lease", terminated, err)
	}
	f.service = standby
	u = awaitStatus(t, f, u.ID, "ready")
	mine, err := f.lib.Mine(bg, f.owner)
	if err != nil || len(mine) != 1 || mine[0].ID != u.DraftID {
		t.Fatal(mine, err)
	}
}

type gatedStore struct {
	blob.Store
	entered chan struct{}
}

func (g *gatedStore) Put(ctx context.Context, key string, r io.Reader, size int64, ct string) error {
	select {
	case g.entered <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	<-ctx.Done()
	return ctx.Err()
}

// Cleanup must release expired input bytes and crash orphans while preserving unrelated directory entries.
func TestCleanupRemovesOnlyExpiredOwnedFiles(t *testing.T) {
	f := setup(t)
	u := createUpload(t, f, []byte("input"))
	if _, err := f.pool.Exec(bg, "UPDATE uploads SET expires_at=now()-interval '1 second' WHERE id=$1", u.ID); err != nil {
		t.Fatal(err)
	}
	orphan := strings.Repeat("a", 32)
	foreign := "user-document.txt"
	for _, name := range []string{orphan, foreign} {
		path := filepath.Join(f.dir, name)
		if err := os.WriteFile(path, []byte("preserve unless owned"), 0600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-25 * time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	f.service.Start(bg)
	awaitStatus(t, f, u.ID, "failed")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, first := os.Stat(filepath.Join(f.dir, u.ID))
		_, second := os.Stat(filepath.Join(f.dir, orphan))
		if os.IsNotExist(first) && os.IsNotExist(second) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.service.Close()
	for _, name := range []string{u.ID, orphan} {
		if _, err := os.Stat(filepath.Join(f.dir, name)); !os.IsNotExist(err) {
			t.Error("staged bytes retained", name, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(f.dir, foreign)); err != nil || string(data) != "preserve unless owned" {
		t.Fatal("unrelated file removed", err)
	}
}

// Derived validation errors must disappear when the author repairs the imported document.
func TestImportedDocumentRepairClearsValidationIssues(t *testing.T) {
	f := setup(t)
	xml := strings.Replace(textXML, "<answer>Secret</answer>", "", 1)
	u := queue(t, f, archive(t, xml, nil))
	f.service.Start(bg)
	u = awaitStatus(t, f, u.ID, "ready")
	d, err := f.lib.Get(bg, f.owner, u.DraftID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if packs.Playable(d.Issues) {
		t.Fatal("missing answer passed validation")
	}
	d.Document.Rounds[0].Themes[0].Questions[0].Right = []string{"Repaired"}
	repaired, err := f.lib.Update(bg, f.owner, d.Card.ID, d.Card.Revision, library.Save{Document: d.Document, Assets: d.Assets, OriginalKey: d.OriginalKey, Issues: d.AssetIssues})
	if err != nil || !packs.Playable(repaired.Issues) {
		t.Fatal("derived issues became permanent", repaired.Issues, err)
	}
}

// Invalid data has no draft/storage quota and must never become a permanent blob.
func TestMalformedArchiveDoesNotStoreOriginal(t *testing.T) {
	f := setup(t)
	data := []byte("invalid SIQ bytes")
	u := queue(t, f, data)
	f.service.Start(bg)
	awaitStatus(t, f, u.ID, "failed")
	sum := sha256.Sum256(data)
	if _, err := f.objects.Stat(bg, hex.EncodeToString(sum[:])); !errors.Is(err, blob.ErrNotFound) {
		t.Fatal("malformed input retained", err)
	}
}

// Failed jobs must register their objects before storage; GC must preserve shared and active hashes.
func TestFailedBlobLedgerCleanupPreservesSharedObjects(t *testing.T) {
	f := setup(t)
	data := archive(t, textXML, nil)
	ready := queue(t, f, data)
	f.service.Start(bg)
	ready = awaitStatus(t, f, ready.ID, "ready")
	f.service.Close()
	s, err := New(f.pool, f.objects, f.lib, Options{Directory: f.dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	f.service = s
	d, err := f.lib.Get(bg, f.owner, ready.DraftID, 0)
	if err != nil {
		t.Fatal(err)
	}
	write := actor(t, f.pool, "owner", "api", "drafts:write")
	session := f.owner
	f.owner = write
	failedData := archive(t, strings.Replace(textXML, "Imported", "Failed", 1), nil)
	u := queue(t, f, failedData)
	f.owner = session
	s.objects = &revokingStore{Store: f.objects, pool: f.pool, credential: write.SessionID}
	s.Start(bg)
	u = awaitStatus(t, f, u.ID, "failed")
	if u.Error != "authorization_expired" {
		t.Fatal(u)
	}
	s.Close()
	next, err := New(f.pool, f.objects, f.lib, Options{Directory: f.dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	f.service = next
	sum := sha256.Sum256(failedData)
	failedKey := hex.EncodeToString(sum[:])
	var keys []string
	if err = f.pool.QueryRow(bg, "SELECT stored_keys FROM uploads WHERE id=$1", u.ID).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != failedKey {
		t.Fatal("missing durable object ledger", keys)
	}
	if _, err = f.objects.Stat(bg, failedKey); err != nil {
		t.Fatal("failed original not uploaded", err)
	}
	if _, err = f.pool.Exec(bg, "UPDATE uploads SET stored_keys=array_append(stored_keys,$2),updated_at=now()-interval '2 hours' WHERE id=$1", u.ID, d.OriginalKey); err != nil {
		t.Fatal(err)
	}
	// An active upload reserves the same hash before Put and must exclude concurrent GC.
	active, err := next.Create(bg, session, "active.siq", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(bg, "UPDATE uploads SET stored_keys=$2 WHERE id=$1", active.ID, []string{failedKey}); err != nil {
		t.Fatal(err)
	}
	var epoch int64
	if err = f.pool.QueryRow(bg, "SELECT epoch FROM ingest_owner").Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	next.cleanup(bg, epoch)
	if _, err = f.objects.Stat(bg, failedKey); err != nil {
		t.Fatal("active hash deleted", err)
	}
	if _, err = f.pool.Exec(bg, "UPDATE uploads SET status='failed',stored_keys='{}',updated_at=now() WHERE id=$1", active.ID); err != nil {
		t.Fatal(err)
	}
	next.cleanup(bg, epoch)
	if _, err = f.objects.Stat(bg, failedKey); !errors.Is(err, blob.ErrNotFound) {
		t.Fatal("orphan failed hash retained", err)
	}
	if _, err = f.objects.Stat(bg, d.OriginalKey); err != nil {
		t.Fatal("shared original removed", err)
	}
}

type revokingStore struct {
	blob.Store
	pool       *pgxpool.Pool
	credential string
	once       sync.Once
}

func (s *revokingStore) Put(ctx context.Context, key string, r io.Reader, size int64, ct string) error {
	if err := s.Store.Put(ctx, key, r, size, ct); err != nil {
		return err
	}
	var err error
	s.once.Do(func() {
		_, err = s.pool.Exec(ctx, "UPDATE account_credentials SET revoked_at=now() WHERE id=$1", s.credential)
	})
	return err
}
