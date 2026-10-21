package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"siplatform/examples"
	"siplatform/internal/catalogueimport"
	"siplatform/internal/ingest"
	"siplatform/internal/library"
	"siplatform/internal/siq"
	"testing"
)

func TestCatalogueCLIPilotAndRepeat(t *testing.T) {
	app, initial, auth := platform(t)
	initial.Close()
	ctx := context.Background()
	uploads, e := ingest.New(app.store.Pool(), app.options.Objects, app.options.Library, ingest.Options{Directory: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(uploads.Close)
	app.options.Uploads = uploads
	h := httptest.NewServer(app.Handler())
	t.Cleanup(h.Close)
	uploads.Start(ctx)
	owner, e := auth.Register(ctx, "catalogue_operator", "long-enough-password", "Operator")
	if e != nil {
		t.Fatal(e)
	}
	var buf bytes.Buffer
	if e = siq.Write(ctx, &buf, examples.Demo(), nil); e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(buf.Bytes())
	in := ingest.CatalogueInput{Namespace: "self-authored", ExternalID: "pilot-1", Name: "own.siq", Size: int64(buf.Len()), SHA256: hex.EncodeToString(sum[:]), SourceURL: "https://example.org/own", License: "CC0", Attribution: "Own pilot", PermissionBasis: "Self authored fixture"}
	api(t, "POST", h.URL+"/api/catalogue-imports", "", in, 401)
	api(t, "POST", h.URL+"/api/catalogue-imports", owner.Token, in, 403)
	if _, e = app.store.Pool().Exec(ctx, `UPDATE account_users SET role='admin' WHERE id=$1`, owner.User.ID); e != nil {
		t.Fatal(e)
	}
	var pat struct {
		Token string `json:"token"`
	}
	if e = json.Unmarshal(api(t, "POST", h.URL+"/api/account/tokens", owner.Token, map[string]any{"name": "Import pilot", "scopes": []string{"drafts:write", "drafts:read"}}, 201), &pat); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	if e = os.WriteFile(filepath.Join(dir, "own.siq"), buf.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	manifest := catalogueimport.Manifest{Items: []catalogueimport.Item{{CatalogueInput: in, File: "own.siq"}}}
	raw, _ := json.Marshal(manifest)
	name := filepath.Join(dir, "manifest.json")
	if e = os.WriteFile(name, raw, 0600); e != nil {
		t.Fatal(e)
	}
	var first string
	for range 2 {
		report, e := catalogueimport.Run(ctx, name, h.URL, pat.Token)
		if e != nil || len(report) != 1 || report[0].Status != "ready" {
			t.Fatal(report, e)
		}
		if first == "" {
			first = report[0].DraftID
		}
		if first != report[0].DraftID {
			t.Fatal("duplicate", report)
		}
	}
	var d library.Draft
	if e = json.Unmarshal(api(t, "GET", h.URL+"/api/drafts/"+first, pat.Token, nil, 200), &d); e != nil {
		t.Fatal(e)
	}
	if d.Card.Rights.SourceURL != in.SourceURL || d.Card.Rights.Confirmed || d.Card.Status != "draft" {
		t.Fatal(d.Card)
	}
	api(t, "GET", h.URL+"/api/catalogue/"+first, "", nil, 404)
	var n int
	if e = app.store.Pool().QueryRow(ctx, `SELECT count(*) FROM library_packs WHERE owner_id=$1`, owner.User.ID).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	if _, e = app.store.Pool().Exec(ctx, `UPDATE account_credentials SET revoked_at=now() WHERE user_id=$1 AND kind='api'`, owner.User.ID); e != nil {
		t.Fatal(e)
	}
	api(t, "POST", h.URL+"/api/catalogue-imports", pat.Token, in, 401)
}
