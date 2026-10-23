package sibrowser

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"siplatform/examples"
	"siplatform/internal/accounts"
	"siplatform/internal/blob"
	"siplatform/internal/ingest"
	"siplatform/internal/library"
	"siplatform/internal/server"
	"siplatform/internal/siq"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRealAPIImportPublicCatalogueAndReplay(t *testing.T) {
	ctx := context.Background()
	p := testutil.Pool(t)
	if e := postgres.Migrate(ctx, p); e != nil {
		t.Fatal(e)
	}
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	sum := sha256.Sum256([]byte(token))
	if _, e := p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash,role) VALUES('operator','operator','Operator','unused','admin');`); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `INSERT INTO account_credentials(id,user_id,token_hash,kind,scopes,expires_at) VALUES('credential','operator',$1,'api',ARRAY['drafts:read','drafts:write'],now()+interval '1 day')`, hex.EncodeToString(sum[:])); e != nil {
		t.Fatal(e)
	}
	if e := Configure(ctx, p, "operator", 1000, 1<<40); e != nil {
		t.Fatal(e)
	}
	objects, e := blob.NewLocal(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	lib := library.New(p)
	uploads, e := ingest.New(p, objects, lib, ingest.Options{Directory: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	defer uploads.Close()
	uploads.Start(ctx)
	app := server.New(postgres.New(p), server.Options{Accounts: accounts.New(p, accounts.Options{}), Library: lib, Objects: objects, Uploads: uploads})
	defer app.Close()
	var expireNext atomic.Bool
	handler := app.Handler()
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" && expireNext.Swap(false) {
			if _, err := p.Exec(ctx, `UPDATE uploads SET status='failed',error='expired',expires_at=now()-interval '1 minute' WHERE id=$1`, strings.TrimPrefix(r.URL.Path, "/api/uploads/")); err != nil {
				t.Error(err)
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer apiServer.Close()
	var archive bytes.Buffer
	if e = siq.Write(ctx, &archive, examples.Demo(), nil); e != nil {
		t.Fatal(e)
	}
	downloads := 0
	sourceID := "123"
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/packages/"+sourceID {
			fmt.Fprintf(w, `<article><h1 itemprop="name headline">Synthetic quiz</h1><span itemprop="alternateName">test.siq</span><a href="/authors/alice">Alice</a><a href="/packages/%s/direct_download">Download</a><table><tr><td>Категории</td></tr></table><p class="pt-2">Source description</p></article>`, sourceID)
			return
		}
		if r.URL.Path != "/" {
			downloads++
			w.Write(archive.Bytes())
			return
		}
		fmt.Fprintf(w, `<div id="pagination"></div><div id="packages"><article><h1><a href="/packages/%s">Synthetic quiz</a></h1><a href="/authors/alice">Alice</a></article></div>`, sourceID)
	}))
	defer src.Close()
	opts := Options{Mode: "full", APIURL: apiServer.URL, Token: token, Directory: t.TempDir(), Source: &Source{base: src.URL, http: src.Client()}}
	report, e := Run(ctx, p, opts)
	if e != nil || report.Published != 1 {
		t.Fatal(report, e)
	}
	cards, e := lib.Catalogue(ctx, library.Filter{})
	if e != nil || len(cards) != 1 {
		t.Fatal(cards, e)
	}
	card := cards[0]
	detail, err := lib.PublicSourceMetadata(ctx, card.ID)
	if err != nil || detail.Metadata.Name != "Synthetic quiz" || len(detail.Metadata.Description) != 1 {
		t.Fatal(detail, err)
	}
	opts.Mode = "metadata"
	opts.ForceMetadata = true
	refreshed, err := Run(ctx, p, opts)
	if err != nil || refreshed.MetadataUpdated != 1 || downloads != 1 {
		t.Fatal(refreshed, err, downloads)
	}
	opts.ForceMetadata = false

	if card.Author != "Alice" || card.Rights.Confirmed || card.AllowDownload || card.Rights.SourceURL != "https://www.sibrowser.ru/packages/123" {
		t.Fatal(card)
	}
	if _, e = p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash) VALUES('player','player','Player','unused'); INSERT INTO account_credentials(id,user_id,token_hash,kind,scopes,expires_at) VALUES('player-session','player','player-hash','session','{}',now()+interval '1 day')`); e != nil {
		t.Fatal(e)
	}
	if _, e = lib.ForPlay(ctx, accounts.Identity{User: accounts.User{ID: "player"}, SessionID: "player-session"}, card.ID, 0); e != nil {
		t.Fatal(e)
	}
	// Simulate a crash after publication but before recording its result.
	if _, e = p.Exec(ctx, `UPDATE source_items SET state='imported' WHERE external_id='123'`); e != nil {
		t.Fatal(e)
	}
	opts.Mode = "incremental"
	report, e = Run(ctx, p, opts)
	if e != nil || report.Published != 1 || downloads != 1 {
		t.Fatal(report, e, downloads)
	}
	var count int
	if e = p.QueryRow(ctx, `SELECT count(*) FROM library_decisions`).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	client, e := newAPI(apiServer.URL, token, false)
	if e != nil {
		t.Fatal(e)
	}
	// A PAT does not acquire the existing session-only moderation capability.
	var out any
	e = client.call(ctx, "POST", "/api/moderation/"+card.ID, []byte(`{"revision":1,"decision":"approve","reason":"test"}`), nil, &out)
	if e == nil {
		t.Fatal("PAT gained moderation")
	}
	// Expire a new upload precisely between receipt lookup and PATCH. The next
	// ordinary daily run must recover it, without --retry-rejected or a new receipt.
	sourceID = "124"
	expireNext.Store(true)
	if _, e = Run(ctx, p, opts); e != ErrIncomplete {
		t.Fatal("expected interrupted upload", e)
	}
	var state, originalUpload string
	if e = p.QueryRow(ctx, `SELECT state FROM source_items WHERE external_id='124'`).Scan(&state); e != nil || state != "retryable" {
		t.Fatal(state, e)
	}
	if e = p.QueryRow(ctx, `SELECT upload_id FROM catalogue_imports WHERE namespace='sibrowser' AND external_id='124'`).Scan(&originalUpload); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE source_items SET next_attempt=now() WHERE external_id='124'`); e != nil {
		t.Fatal(e)
	}
	if report, e = Run(ctx, p, opts); e != nil || report.Published != 1 {
		t.Fatal(report, e)
	}
	var recoveredUpload string
	if e = p.QueryRow(ctx, `SELECT upload_id FROM catalogue_imports WHERE namespace='sibrowser' AND external_id='124'`).Scan(&recoveredUpload); e != nil || recoveredUpload != originalUpload {
		t.Fatal(recoveredUpload, e)
	}
	if _, e = p.Exec(ctx, `UPDATE account_credentials SET revoked_at=now() WHERE id='credential'`); e != nil {
		t.Fatal(e)
	}
	if _, e = client.publish(ctx, "123"); e == nil {
		t.Fatal("revoked token published")
	}
	raw, _ := json.Marshal(card)
	if bytes.Contains(raw, []byte(token)) {
		t.Fatal("secret exposed")
	}
}
