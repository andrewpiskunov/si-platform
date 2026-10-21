package retention

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"net/http/httptest"
	"siplatform/internal/blob"
	"siplatform/internal/blobrefs"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDelayedRemoteDeleteCannotEraseNewReservation(t *testing.T) {
	_, p, _ := fixture(t)
	ctx := context.Background()
	payload := "same bytes uploaded again"
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))
	var present atomic.Bool
	present.Store(true)
	var replacement atomic.Bool
	releaseDelete := make(chan struct{})
	deleted := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "DELETE":
			<-releaseDelete // Remote operation continues after the client request deadline.
			if r.URL.Query().Get("versionId") != "old-generation" || !replacement.Load() {
				present.Store(false)
			}
			close(deleted)
			w.WriteHeader(204)
		case "PUT":
			_, _ = io.Copy(io.Discard, r.Body)
			present.Store(true)
			replacement.Store(true)
			w.Header().Set("x-amz-version-id", "new-generation")
			w.WriteHeader(200)
		case "GET":
			if r.URL.Query().Has("versions") {
				w.Header().Set("Content-Type", "application/xml")
				fmt.Fprintf(w, "<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>%s</Key><VersionId>old-generation</VersionId></Version></ListVersionsResult>", key)
			} else {
				w.WriteHeader(405)
			}
		default:
			w.WriteHeader(405)
		}
	}))
	defer remote.Close()
	objects, err := blob.NewS3(ctx, blob.S3Config{Endpoint: remote.URL, Region: "us-east-1", Bucket: "review", AccessKey: "key", SecretKey: "secret", PathStyle: true, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	exec(t, p, `INSERT INTO retention_candidates(key) VALUES($1)`, key)
	st, err := New(p, objects).Sweep(ctx)
	if err != nil || st.ObjectsFailed != 1 {
		close(releaseDelete)
		t.Fatalf("sweep: %+v %v", st, err)
	}
	// Repeat the actual writer ordering: guard, durable processing reservation,
	// commit, Put. No DELETE has completed yet, but the collector released guard.
	tx, err := p.Begin(ctx)
	if err != nil {
		close(releaseDelete)
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, blobrefs.GuardSQL); err != nil {
		close(releaseDelete)
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO uploads(id,owner_id,credential_id,name,size,status,stored_keys) VALUES($1,'a','c','again',1,'processing',ARRAY[$2])`, strings.Repeat("a", 32), key); err != nil {
		close(releaseDelete)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		close(releaseDelete)
		t.Fatal(err)
	}
	if err = objects.Put(ctx, key, strings.NewReader(payload), int64(len(payload)), "text/plain"); err != nil {
		close(releaseDelete)
		t.Fatal(err)
	}
	if !present.Load() {
		close(releaseDelete)
		t.Fatal("Put did not publish")
	}
	close(releaseDelete)
	<-deleted
	if !present.Load() {
		t.Fatal("confirmed: delayed remote DELETE erased successfully re-uploaded bytes with an active durable reservation")
	}
}

// Losing PostgreSQL during remote version enumeration must not allow deletion
// of a version published after its transaction-scoped guard disappeared.
func TestRemoteSnapshotAfterDatabaseGuardLossDoesNotDelete(t *testing.T) {
	_, pool, _ := fixture(t)
	cfg := pool.Config()
	cfg.MaxConns = 1
	gcPool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer gcPool.Close()
	var pid int
	if err = gcPool.QueryRow(context.Background(), "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte("new generation")))
	exec(t, pool, `INSERT INTO retention_candidates(key) VALUES($1)`, key)
	var deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deletes.Add(1)
			w.WriteHeader(204)
			return
		}
		if r.Method != "GET" || !r.URL.Query().Has("versions") {
			w.WriteHeader(405)
			return
		}
		if _, err := pool.Exec(context.Background(), "SELECT pg_terminate_backend($1)", pid); err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		// Real replacement reservation under the now-released same advisory guard.
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		defer tx.Rollback(context.Background())
		if _, err = tx.Exec(context.Background(), blobrefs.GuardSQL); err == nil {
			_, err = tx.Exec(context.Background(), `INSERT INTO uploads(id,owner_id,credential_id,name,size,status,stored_keys) VALUES($1,'a','c','replacement',1,'processing',ARRAY[$2])`, strings.Repeat("b", 32), key)
		}
		if err == nil {
			err = tx.Commit(context.Background())
		}
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, "<ListVersionsResult><IsTruncated>false</IsTruncated><Version><Key>%s</Key><VersionId>new-after-guard-loss</VersionId></Version></ListVersionsResult>", key)
	}))
	defer server.Close()
	store, err := blob.NewS3(context.Background(), blob.S3Config{Endpoint: server.URL, Region: "test", Bucket: "private", AccessKey: "key", SecretKey: "secret", PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = New(gcPool, store).Sweep(context.Background()); err == nil {
		t.Fatal("lost transaction accepted")
	}
	if deletes.Load() != 0 {
		t.Fatal("deleted replacement after database guard loss")
	}
}
