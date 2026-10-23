//go:build linux

package sibrowser

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"siplatform/examples"
	"siplatform/internal/accounts"
	"siplatform/internal/blob"
	"siplatform/internal/ingest"
	"siplatform/internal/library"
	"siplatform/internal/media"
	"siplatform/internal/server"
	"siplatform/internal/siq"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
)

func TestRealSourceCoverSandbox(t *testing.T) {
	if os.Getenv("MEDIA_INTEGRATION") != "1" {
		t.Skip("requires Linux FFmpeg/bubblewrap")
	}
	ctx := context.Background()
	p := testutil.Pool(t)
	if e := postgres.Migrate(ctx, p); e != nil {
		t.Fatal(e)
	}
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	sum := sha256.Sum256([]byte(token))
	_, e := p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash,role) VALUES('operator','operator','Operator','unused','admin')`)
	if e != nil {
		t.Fatal(e)
	}
	_, e = p.Exec(ctx, `INSERT INTO account_credentials(id,user_id,token_hash,kind,scopes,expires_at) VALUES('credential','operator',$1,'api',ARRAY['drafts:read','drafts:write'],now()+interval '1 day')`, hex.EncodeToString(sum[:]))
	if e != nil {
		t.Fatal(e)
	}
	if e = Configure(ctx, p, "operator", 100, 1<<30); e != nil {
		t.Fatal(e)
	}
	processor, e := media.New(media.Options{Directory: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	objects, e := blob.NewLocal(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	lib := library.New(p)
	uploads, e := ingest.New(p, objects, lib, ingest.Options{Directory: t.TempDir(), Media: processor})
	if e != nil {
		t.Fatal(e)
	}
	defer uploads.Close()
	uploads.Start(ctx)
	app := server.New(postgres.New(p), server.Options{Accounts: accounts.New(p, accounts.Options{}), Library: lib, Objects: objects, Uploads: uploads})
	defer app.Close()
	api := httptest.NewServer(app.Handler())
	defer api.Close()
	var archive, cover bytes.Buffer
	if e = siq.Write(ctx, &archive, examples.Demo(), nil); e != nil {
		t.Fatal(e)
	}
	pixels := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			pixels.Set(x, y, color.RGBA{80, 150, 220, 255})
		}
	}
	if e = png.Encode(&cover, pixels); e != nil {
		t.Fatal(e)
	}
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<div id="pagination"></div><div id="packages"><article><h1><a href="/packages/123">Test</a></h1><a href="/authors/A">A</a></article></div>`)
		case "/packages/123/direct_download":
			w.Write(archive.Bytes())
		case "/packages/123/logo.webp":
			w.Write(cover.Bytes())
		case "/packages/123":
			fmt.Fprint(w, `<article><h1 itemprop="name headline">Test</h1><span itemprop="alternateName">test.siq</span><a href="/authors/A">A</a><a href="/packages/123/direct_download">Download</a><img itemprop="image" src="/packages/123/logo.webp"><table><tr><td>Категории</td></tr></table></article>`)
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer source.Close()
	report, e := Run(ctx, p, Options{Mode: "full", APIURL: api.URL, Token: token, Directory: t.TempDir(), Source: &Source{base: source.URL, http: source.Client()}})
	if e != nil || report.Published != 1 || report.MetadataUpdated != 1 || report.CoversUpdated != 1 || len(report.MetadataErrors) != 0 {
		t.Fatal(report, e)
	}
	cards, e := lib.Catalogue(ctx, library.Filter{})
	if e != nil || len(cards) != 1 || cards[0].CoverURL == "" {
		t.Fatal(cards, e)
	}
	resp, e := http.Get(api.URL + cards[0].CoverURL)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatal(resp.StatusCode, resp.Header)
	}
	cfg, e := png.DecodeConfig(bytes.NewReader(raw))
	if e != nil || cfg.Width != 32 || cfg.Height != 32 {
		t.Fatal(cfg, e)
	}
	if _, e = p.Exec(ctx, `UPDATE library_packs SET published_revision=NULL WHERE id=$1`, cards[0].ID); e != nil {
		t.Fatal(e)
	}
	hidden, e := http.Get(api.URL + cards[0].CoverURL)
	if e != nil {
		t.Fatal(e)
	}
	defer hidden.Body.Close()
	if hidden.StatusCode != 404 {
		t.Fatal("withdrawn cover exposed", hidden.StatusCode)
	}
}
