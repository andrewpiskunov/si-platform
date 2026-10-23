package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"siplatform/examples"
	"siplatform/internal/library"
	"strings"
	"testing"
)

func TestPublicEngagementAndDownloads(t *testing.T) {
	app, h, auth := platform(t)
	ctx := context.Background()
	owner, e := auth.Register(ctx, "owner", "long-enough-password", "Owner")
	if e != nil {
		t.Fatal(e)
	}
	var d library.Draft
	if e = json.Unmarshal(api(t, "POST", h.URL+"/api/drafts", owner.Token, map[string]any{"document": examples.Demo()}, 201), &d); e != nil {
		t.Fatal(e)
	}
	if _, e = app.store.Pool().Exec(ctx, `UPDATE library_packs SET published_revision=1 WHERE id=$1`, d.Card.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = app.store.Pool().Exec(ctx, `UPDATE library_revisions SET rights='{"allow_download":true}' WHERE pack_id=$1`, d.Card.ID); e != nil {
		t.Fatal(e)
	}
	path := "/api/catalogue/" + d.Card.ID
	api(t, "PUT", h.URL+path+"/vote", "", map[string]any{"rating": 5, "liked": true}, 401)
	for i := 0; i < 2; i++ {
		api(t, "PUT", h.URL+path+"/vote", owner.Token, map[string]any{"rating": 5, "liked": true}, 200)
	}
	var v library.Vote
	json.Unmarshal(api(t, "GET", h.URL+path+"/vote", owner.Token, nil, 200), &v)
	if v.Rating == nil || *v.Rating != 5 || !v.Liked {
		t.Fatal(v)
	}
	api(t, "PUT", h.URL+path+"/vote", owner.Token, map[string]any{"rating": 0, "liked": true}, 400)
	for _, tc := range []struct {
		method, rng string
		status      int
		count       int64
	}{{"GET", "", 200, 1}, {"GET", "bytes=0-20", 206, 1}, {"GET", "bytes=999999999-", 416, 1}, {"HEAD", "", 200, 1}} {
		req := httptest.NewRequest(tc.method, path+"/download", nil)
		if tc.rng != "" {
			req.Header.Set("Range", tc.rng)
		}
		w := httptest.NewRecorder()
		app.Handler().ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
		g, e := app.options.Library.Engagement(ctx, d.Card.ID)
		if e != nil || g.LocalDownloads != tc.count {
			t.Fatal(tc, g, e)
		}
	}
	api(t, "PUT", h.URL+path+"/vote", owner.Token, map[string]any{"rating": nil, "liked": false}, 200)
	g, e := app.options.Library.Engagement(ctx, d.Card.ID)
	if e != nil || g.LocalLikes != 0 || g.Rating != nil {
		t.Fatal(g, e)
	}
	// Cross-origin changes rejected before handler.
	req := httptest.NewRequest(http.MethodPut, path+"/vote", nil)
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}

type brokenDownloadWriter struct{ header http.Header }

func (w *brokenDownloadWriter) Header() http.Header         { return w.header }
func (w *brokenDownloadWriter) WriteHeader(int)             {}
func (w *brokenDownloadWriter) Write(b []byte) (int, error) { return 0, io.ErrClosedPipe }
func TestFailedDownloadDoesNotCount(t *testing.T) {
	app, _, auth := platform(t)
	ctx := context.Background()
	session, e := auth.Register(ctx, "owner", "long-enough-password", "Owner")
	if e != nil {
		t.Fatal(e)
	}
	actor, e := auth.Authenticate(ctx, session.Token)
	if e != nil {
		t.Fatal(e)
	}
	d, e := app.options.Library.Create(ctx, actor, library.Save{Document: examples.Demo()})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = app.store.Pool().Exec(ctx, `UPDATE library_packs SET published_revision=1 WHERE id=$1`, d.Card.ID); e != nil {
		t.Fatal(e)
	}
	app.export(&brokenDownloadWriter{header: make(http.Header)}, httptest.NewRequest("GET", "/download", nil), d, true)
	app.export(httptest.NewRecorder(), httptest.NewRequest("GET", "/export", nil), d, false)
	g, e := app.options.Library.Engagement(ctx, d.Card.ID)
	if e != nil || g.LocalDownloads != 0 {
		t.Fatal(g, e)
	}
}

type flushFailureWriter struct{ *httptest.ResponseRecorder }

func (w *flushFailureWriter) FlushError() error { return io.ErrClosedPipe }
func TestDownloadFlushFailureDoesNotCount(t *testing.T) {
	app, _, auth := platform(t)
	ctx := context.Background()
	session, e := auth.Register(ctx, "owner", "long-enough-password", "Owner")
	if e != nil {
		t.Fatal(e)
	}
	actor, e := auth.Authenticate(ctx, session.Token)
	if e != nil {
		t.Fatal(e)
	}
	d, e := app.options.Library.Create(ctx, actor, library.Save{Document: examples.Demo()})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = app.store.Pool().Exec(ctx, `UPDATE library_packs SET published_revision=1 WHERE id=$1`, d.Card.ID); e != nil {
		t.Fatal(e)
	}
	app.export(&flushFailureWriter{httptest.NewRecorder()}, httptest.NewRequest("GET", "/download", nil), d, true)
	g, e := app.options.Library.Engagement(ctx, d.Card.ID)
	if e != nil || g.LocalDownloads != 0 {
		t.Fatal(g, e)
	}
}
func TestOwnerPublicDownloadUsesPublishedRevision(t *testing.T) {
	app, h, auth := platform(t)
	ctx := context.Background()
	session, e := auth.Register(ctx, "owner", "long-enough-password", "Owner")
	if e != nil {
		t.Fatal(e)
	}
	actor, e := auth.Authenticate(ctx, session.Token)
	if e != nil {
		t.Fatal(e)
	}
	d, e := app.options.Library.Create(ctx, actor, library.Save{Document: examples.Demo()})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = app.store.Pool().Exec(ctx, `UPDATE library_packs SET published_revision=1 WHERE id=$1`, d.Card.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = app.store.Pool().Exec(ctx, `UPDATE library_revisions SET rights='{"allow_download":true}' WHERE pack_id=$1`, d.Card.ID); e != nil {
		t.Fatal(e)
	}
	doc := d.Document
	doc.Name = "PRIVATE UNPUBLISHED TITLE"
	if _, e = app.options.Library.Update(ctx, actor, d.Card.ID, 1, library.Save{Document: doc}); e != nil {
		t.Fatal(e)
	}
	data := api(t, "GET", h.URL+"/api/catalogue/"+d.Card.ID+"/download", session.Token, nil, 200)
	z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range z.File {
		if f.Name == "content.xml" {
			r, e := f.Open()
			if e != nil {
				t.Fatal(e)
			}
			b, e := io.ReadAll(r)
			r.Close()
			if e != nil {
				t.Fatal(e)
			}
			if strings.Contains(string(b), doc.Name) {
				t.Fatal("exported unpublished current revision")
			}
		}
	}
}
