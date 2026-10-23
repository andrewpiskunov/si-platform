package sibrowser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"siplatform/internal/ingest"
	"siplatform/internal/library"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
	"sync/atomic"
	"testing"
	"time"
)

// Proves a bounded bootstrap resumes saved discoveries, and a subsequent run
// does not download/import an already published package a second time.
func TestRunnerResumeAndIncremental(t *testing.T) {
	ctx := context.Background()
	p := testutil.Pool(t)
	if e := postgres.Migrate(ctx, p); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash,role) VALUES('owner','owner','Owner','unused','admin')`); e != nil {
		t.Fatal(e)
	}
	if e := Configure(ctx, p, "owner", 1000, 1<<40); e != nil {
		t.Fatal(e)
	}
	var downloads atomic.Int32
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			downloads.Add(1)
			w.Write([]byte("PK\x03\x04test archive"))
			return
		}
		page := r.URL.Query().Get("page")
		next := ""
		id := "124"
		if page == "1" {
			next = `<a rel="next" href="/?page=2">Next</a>`
		} else {
			id = "123"
		}
		fmt.Fprintf(w, `<div id="pagination">%s</div><div id="packages"><article><h1><a href="/packages/%s">Quiz</a></h1><a href="/authors/alice">Alice</a></article></div>`, next, id)
	}))
	defer src.Close()
	ready := map[string]bool{}
	var current string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("auth missing")
		}
		if r.URL.Path == "/api/external-imports/sibrowser/publish" {
			json.NewEncoder(w).Encode(library.Card{ID: current, Revision: 1, Status: "published"})
			return
		}
		if r.URL.Path == "/api/external-imports/sibrowser" {
			var in ingest.ExternalInput
			json.NewDecoder(r.Body).Decode(&in)
			current = in.ExternalID
		}
		u := ingest.Upload{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 16, Status: "uploading"}
		if r.Method == "PATCH" {
			u.Offset = 16
		}
		if r.URL.Path == "/api/uploads/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/complete" {
			ready[current] = true
		}
		if ready[current] {
			u.Status = "ready"
			u.DraftID = current
			u.Offset = 16
		}
		json.NewEncoder(w).Encode(u)
	}))
	defer api.Close()
	opt := Options{Mode: "full", APIURL: api.URL, Token: "token", Directory: t.TempDir(), Source: &Source{base: src.URL, http: src.Client(), maxArchive: 100}, MaxPages: 1}
	report, e := Run(ctx, p, opt)
	if e == nil || report.Complete || report.Published != 1 {
		t.Fatal(report, e)
	}
	opt.MaxPages = 0
	report, e = Run(ctx, p, opt)
	if e != nil || !report.Complete || report.Published != 1 {
		t.Fatal(report, e)
	}
	opt.Mode = "incremental"
	report, e = Run(ctx, p, opt)
	if e != nil || report.Published != 0 || downloads.Load() != 2 {
		t.Fatal(report, e, downloads.Load())
	}
	if e = Configure(ctx, p, "other", 1000, 1<<40); e == nil {
		t.Fatal("changed source owner")
	}
	lease, e := acquire(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	defer lease.close()
	if _, e = Run(ctx, p, opt); e != ErrBusy {
		t.Fatal("concurrent run", e)
	}
}

func TestLeaseLossCancelsAndPreventsCheckpointWrite(t *testing.T) {
	ctx := context.Background()
	p := testutil.Pool(t)
	if e := postgres.Migrate(ctx, p); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash,role) VALUES('owner','owner','Owner','unused','admin')`); e != nil {
		t.Fatal(e)
	}
	if e := Configure(ctx, p, "owner", 1000, 1<<40); e != nil {
		t.Fatal(e)
	}
	l, e := acquire(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	l.mu.Lock()
	pid := l.conn.PgConn().PID()
	l.mu.Unlock()
	if _, e = p.Exec(ctx, `SELECT pg_terminate_backend($1)`, pid); e != nil {
		t.Fatal(e)
	}
	select {
	case <-l.ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("lost lock did not cancel")
	}
	if _, e = l.discover(Page{Items: []Item{{ID: "1", Name: "One"}}, Next: 2}, true); e == nil {
		t.Fatal("stale checkpoint write")
	}
	var n int
	if e = p.QueryRow(ctx, `SELECT next_page FROM external_sources`).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	next, e := acquire(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	next.close()
}
