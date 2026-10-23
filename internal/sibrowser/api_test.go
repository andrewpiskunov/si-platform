package sibrowser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"siplatform/internal/ingest"
	"testing"
)

func TestAPIReportsModerationBlockSeparatelyFromCredentialDenial(t *testing.T) {
	blocked := true
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		if blocked {
			w.Write([]byte(`{"error":"blocked"}`))
		} else {
			w.Write([]byte(`{"error":"forbidden"}`))
		}
	}))
	defer h.Close()
	c, e := newAPI(h.URL, "token", false)
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.publish(context.Background(), "1")
	var f *Fault
	if !errors.As(e, &f) || f.Code != "publication_suppressed" {
		t.Fatal(e)
	}
	blocked = false
	_, e = c.publish(context.Background(), "1")
	if !errors.As(e, &f) || f.Code != "api_http_403" {
		t.Fatal(e)
	}
}

func TestExpiredUploadConflictRemainsRetryable(t *testing.T) {
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		w.Write([]byte(`{"error":"conflict"}`))
	}))
	defer h.Close()
	data := []byte("PK\x03\x04test archive")
	sum := sha256.Sum256(data)
	file := filepath.Join(t.TempDir(), "1.siq")
	if e := os.WriteFile(file, data, 0600); e != nil {
		t.Fatal(e)
	}
	c, e := newAPI(h.URL, "token", false)
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.upload(context.Background(), file, ingest.ExternalInput{ExternalID: "1", Name: "1.siq", Size: 16, SHA256: hex.EncodeToString(sum[:]), Attribution: "A"}, ingest.Upload{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 16, Status: "uploading"})
	var f *Fault
	if !errors.As(e, &f) || !f.Retryable || f.Code != "upload_interrupted" {
		t.Fatal(e)
	}
}
