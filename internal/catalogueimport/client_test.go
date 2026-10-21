package catalogueimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"siplatform/internal/ingest"
	"strings"
	"testing"
)

func pilot(t *testing.T) (string, Manifest) {
	t.Helper()
	dir := t.TempDir()
	data := []byte("own archive bytes")
	sum := sha256.Sum256(data)
	if e := os.WriteFile(filepath.Join(dir, "own.siq"), data, 0600); e != nil {
		t.Fatal(e)
	}
	m := Manifest{Items: []Item{{File: "own.siq", CatalogueInput: ingest.CatalogueInput{Namespace: "pilot", ExternalID: "1", SHA256: hex.EncodeToString(sum[:]), SourceURL: "https://example.org/own", License: "CC0", Attribution: "Own", PermissionBasis: "Self authored"}}}}
	raw, _ := json.Marshal(m)
	name := filepath.Join(dir, "manifest.json")
	if e := os.WriteFile(name, raw, 0600); e != nil {
		t.Fatal(e)
	}
	return name, m
}
func TestPreflightRejectsWholeBatchBeforeRequests(t *testing.T) {
	name, m := pilot(t)
	m.Items = append(m.Items, Item{File: "../escape.siq", CatalogueInput: m.Items[0].CatalogueInput})
	raw, _ := json.Marshal(m)
	os.WriteFile(name, raw, 0600)
	var calls int
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
	defer h.Close()
	if _, e := Run(context.Background(), name, h.URL, "secret"); e == nil {
		t.Fatal("accepted invalid batch")
	}
	if calls != 0 {
		t.Fatal(calls)
	}
}
func TestResumeAndRepeatAndRefuseRedirect(t *testing.T) {
	name, _ := pilot(t)
	uploaded := int64(4)
	ready := false
	var patches int
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing auth")
		}
		u := ingest.Upload{ID: strings.Repeat("a", 32), Size: 17, Offset: uploaded, Status: "uploading", Purpose: "siq"}
		switch {
		case r.URL.Path == "/api/catalogue-imports":
		case r.Method == "PATCH":
			if r.Header.Get("Upload-Offset") != "4" {
				t.Error("offset", r.Header)
			}
			patches++
			uploaded = 17
			u.Offset = 17
		case strings.HasSuffix(r.URL.Path, "/complete"):
			ready = true
		default:
			t.Error("unexpected request", r.Method, r.URL.Path)
		}
		if ready {
			u.Status = "ready"
			u.DraftID = strings.Repeat("b", 32)
		}
		json.NewEncoder(w).Encode(u)
	}))
	defer h.Close()
	for range 2 {
		report, e := Run(context.Background(), name, h.URL, "secret")
		if e != nil || len(report) != 1 || report[0].Status != "ready" {
			t.Fatal(report, e)
		}
	}
	if patches != 1 {
		t.Fatal(patches)
	}
	var leaked bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	_, e := Run(context.Background(), name, redirect.URL, "secret")
	if e == nil || strings.Contains(e.Error(), "secret") || leaked {
		t.Fatal(e, leaked)
	}
}

func TestInvalidManifestVariantsAndEndpoints(t *testing.T) {
	for _, mode := range []string{"checksum", "traversal", "symlink", "duplicate", "unknown", "empty"} {
		t.Run(mode, func(t *testing.T) {
			name, m := pilot(t)
			switch mode {
			case "checksum":
				m.Items[0].SHA256 = strings.Repeat("0", 64)
			case "traversal":
				m.Items[0].File = "../outside.siq"
			case "symlink":
				outside := filepath.Join(t.TempDir(), "outside.siq")
				os.WriteFile(outside, []byte("own archive bytes"), 0600)
				os.Symlink(outside, filepath.Join(filepath.Dir(name), "link.siq"))
				m.Items[0].File = "link.siq"
			case "duplicate":
				m.Items = append(m.Items, m.Items[0])
			case "empty":
				m.Items = nil
			}
			raw, _ := json.Marshal(m)
			if mode == "unknown" {
				raw = []byte(`{"items":[],"token":"secret"}`)
			}
			os.WriteFile(name, raw, 0600)
			if _, e := Check(context.Background(), name); e == nil {
				t.Fatal("accepted", mode)
			}
		})
	}
	for _, base := range []string{"http://example.org", "https://user:secret@example.org", "https://example.org?token=secret", "file:///tmp", "https://example.org/api"} {
		if _, e := endpoint(base); e == nil {
			t.Fatal(base)
		}
	}
}
