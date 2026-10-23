package sibrowser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"siplatform/examples"
	"siplatform/internal/accounts"
	"siplatform/internal/ingest"
	"siplatform/internal/library"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
	"strings"
	"testing"
)

func TestMetadataModePreservesBootstrap(t *testing.T) {
	ctx := context.Background()
	p := testutil.Pool(t)
	if e := postgres.Migrate(ctx, p); e != nil {
		t.Fatal(e)
	}
	_, e := p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash,role) VALUES('owner','owner','Owner','x','admin')`)
	if e != nil {
		t.Fatal(e)
	}
	if e = Configure(ctx, p, "owner", 100, 1<<30); e != nil {
		t.Fatal(e)
	}
	_, e = p.Exec(ctx, `UPDATE external_sources SET next_page=17`)
	if e != nil {
		t.Fatal(e)
	}
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected network", r.URL.Path) }))
	defer h.Close()
	report, e := Run(ctx, p, Options{Mode: "metadata", APIURL: h.URL, Token: "token", Directory: t.TempDir(), Source: &Source{base: h.URL, http: h.Client()}})
	if e != nil || !report.Complete {
		t.Fatal(report, e)
	}
	var page int
	var complete bool
	p.QueryRow(ctx, `SELECT next_page,bootstrap_complete FROM external_sources`).Scan(&page, &complete)
	if page != 17 || complete {
		t.Fatal(page, complete)
	}
}

func TestMetadataCoverPhases(t *testing.T) {
	for _, scenario := range []string{"first", "html-failure", "changed-size", "changed-same-size"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			p := testutil.Pool(t)
			if e := postgres.Migrate(ctx, p); e != nil {
				t.Fatal(e)
			}
			_, e := p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash,role) VALUES('owner','owner','Owner','x','admin'); INSERT INTO account_credentials(id,user_id,token_hash,kind,scopes,expires_at) VALUES('cred','owner','hash','session','{}',now()+interval '1 day')`)
			if e != nil {
				t.Fatal(e)
			}
			lib := library.New(p)
			d, e := lib.Create(ctx, accounts.Identity{User: accounts.User{ID: "owner"}, SessionID: "cred"}, library.Save{Document: examples.Demo()})
			if e != nil {
				t.Fatal(e)
			}
			if e = Configure(ctx, p, "owner", 100, 1<<30); e != nil {
				t.Fatal(e)
			}
			_, e = p.Exec(ctx, `UPDATE library_packs SET published_revision=1 WHERE id=$1;`, d.Card.ID)
			if e != nil {
				t.Fatal(e)
			}
			_, e = p.Exec(ctx, `INSERT INTO source_items(source,external_id,metadata,state,pack_id) VALUES('sibrowser','123','{}','published',$1)`, d.Card.ID)
			if e != nil {
				t.Fatal(e)
			}
			lease, e := acquire(ctx, p)
			if e != nil {
				t.Fatal(e)
			}
			defer lease.close()
			payload := []byte("new-cover-bytes")
			old := []byte("old-cover-bytes")
			if scenario == "changed-size" {
				old = []byte("old")
			}
			hash := sha256.Sum256(old)
			m := library.SourceMetadata{Name: "Test", Filename: "test.siq", Authors: []library.SourceLink{{Name: "A", URL: Origin + "/authors/A"}}, CoverURL: Origin + "/packages/123/logo.webp"}
			snapshot, _ := json.Marshal(m)
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/packages/123/logo.webp" {
					w.Write(payload)
					return
				}
				if scenario == "html-failure" {
					w.Write([]byte("broken markup"))
					return
				}
				fmt.Fprint(w, `<article><h1 itemprop="name headline">Test</h1><span itemprop="alternateName">test.siq</span><a href="/authors/A">A</a><a href="/packages/123/direct_download">Download</a><img itemprop="image" src="/packages/123/logo.webp"><table><tr><td>Категории</td></tr></table></article>`)
			}))
			defer source.Close()
			oldID := strings.Repeat("a", 32)
			newID := strings.Repeat("b", 32)
			created, attached := 0, 0
			apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var u ingest.Upload
				switch {
				case r.URL.Path == "/api/external-imports/sibrowser/metadata":
					json.NewEncoder(w).Encode(map[string]bool{"ok": true})
					return
				case r.URL.Path == "/api/external-imports/sibrowser/cover":
					attached++
					json.NewEncoder(w).Encode(map[string]bool{"ok": true})
					return
				case r.URL.Path == "/api/media-uploads":
					created++
					u = ingest.Upload{ID: newID, Size: int64(len(payload)), Status: "uploading"}
				case r.Method == "GET" && r.URL.Path == "/api/uploads/"+oldID:
					u = ingest.Upload{ID: oldID, Size: int64(len(old)), Offset: 2, Status: "uploading"}
					if scenario == "html-failure" {
						u.Status = "ready"
					}
				case r.Method == "PATCH":
					if strings.Contains(r.URL.Path, oldID) {
						t.Error("resumed stale cover")
					}
					u = ingest.Upload{ID: newID, Size: int64(len(payload)), Offset: int64(len(payload)), Status: "uploading"}
				case strings.HasSuffix(r.URL.Path, "/complete"):
					u = ingest.Upload{ID: newID, Size: int64(len(payload)), Offset: int64(len(payload)), Status: "ready"}
				default:
					t.Error("unexpected", r.URL.Path)
				}
				json.NewEncoder(w).Encode(u)
			}))
			defer apiServer.Close()
			api, _ := newAPI(apiServer.URL, "token", false)
			src := &Source{base: source.URL, http: source.Client()}
			task, ok, e := lease.metadataTask("", "", false, false)
			if e != nil || !ok {
				t.Fatal(task, ok, e)
			}
			if scenario != "first" {
				task.snapshot = snapshot
				task.coverDue = true
				task.upload = oldID
				task.digest = hex.EncodeToString(hash[:])
			}
			if strings.HasPrefix(scenario, "changed") {
				task.metadataDue = false
			}
			report := Report{}
			if e = refreshMetadata(ctx, lease, src, api, task, &report); e != nil {
				t.Fatal(e)
			}
			if attached != 1 {
				t.Fatalf("cover not attached: created=%d report=%+v", created, report)
			}
			if strings.HasPrefix(scenario, "changed") && created != 1 {
				t.Fatal("did not restart changed cover")
			}
			if scenario == "html-failure" && len(report.MetadataErrors) != 1 {
				t.Fatal(report)
			}
		})
	}
}
