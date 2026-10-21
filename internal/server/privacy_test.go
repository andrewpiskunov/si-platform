package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"siplatform/examples"
	"siplatform/internal/library"
)

func privacyRequest(t *testing.T, url, token, accept string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("private export cacheable")
	}
	return resp, data
}

func TestPrivacyHTTPExportAndOwnerScope(t *testing.T) {
	staging := t.TempDir()
	app, h, auth := platform(t)
	app.options.ExportDirectory = staging
	ctx := context.Background()
	owner, err := auth.Register(ctx, "privacy_author", "long-enough-password", "Privacy Author")
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.Register(ctx, "privacy_other", "long-enough-password", "Private Other")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := auth.Authenticate(ctx, owner.Token)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("private own export bytes")
	sum := sha256.Sum256(payload)
	key := hex.EncodeToString(sum[:])
	if err = app.options.Objects.Put(ctx, key, bytes.NewReader(payload), int64(len(payload)), "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	draft, err := app.options.Library.Create(ctx, actor, library.Save{Document: examples.Demo(), OriginalKey: key})
	if err != nil {
		t.Fatal(err)
	}
	url := h.URL + "/api/privacy/export"
	resp, data := privacyRequest(t, url, owner.Token, "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/zip" || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment;") || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("export headers: %v %s", resp.Header, data)
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var metadata strings.Builder
	found := false
	for _, f := range archive.File {
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		if f.Name == "objects/"+key {
			found = bytes.Equal(b, payload)
		} else {
			metadata.Write(b)
		}
	}
	if !found || !strings.Contains(metadata.String(), draft.Card.ID) || !strings.Contains(metadata.String(), "Privacy Author") {
		t.Fatal("own stored content missing")
	}
	for _, secret := range []string{owner.Token, other.Token, "Private Other", "password_hash", "token_hash", "credential_id"} {
		if strings.Contains(metadata.String(), secret) {
			t.Fatalf("export included forbidden data %q", secret)
		}
	}
	api(t, "GET", url+"?kind=pack&id="+draft.Card.ID, other.Token, nil, 404)
	api(t, "GET", url, "", nil, 401)
	var token struct {
		Token string `json:"token"`
	}
	if err = json.Unmarshal(api(t, "POST", h.URL+"/api/account/tokens", owner.Token, map[string]any{"name": "Limited", "scopes": []string{"drafts:read", "drafts:export"}}, 201), &token); err != nil {
		t.Fatal(err)
	}
	api(t, "GET", url, token.Token, nil, 403)
	api(t, "GET", h.URL+"/api/privacy/subjects?kind=pack", token.Token, nil, 403)
	for _, query := range []string{"?kind=pack", "?metadata_only=maybe", "?user_id=" + actor.User.ID, "?kind=pack&kind=theme&id=" + draft.Card.ID} {
		api(t, "GET", url+query, owner.Token, nil, 400)
	}
	resp, data = privacyRequest(t, url+"?metadata_only=true", owner.Token, "")
	if resp.StatusCode != 200 {
		t.Fatalf("metadata export: %d %s", resp.StatusCode, data)
	}
	archive, err = zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range archive.File {
		if strings.HasPrefix(f.Name, "objects/") {
			t.Fatal("metadata export contains objectbytes")
		}
	}
	var subjects struct {
		Items []struct {
			ID string `json:"id"`
		}
		NextCursor string `json:"next_cursor"`
	}
	if err = json.Unmarshal(api(t, "GET", h.URL+"/api/privacy/subjects?kind=pack", owner.Token, nil, 200), &subjects); err != nil || len(subjects.Items) != 1 || subjects.Items[0].ID != draft.Card.ID {
		t.Fatal("own subjects", subjects, err)
	}
	api(t, "GET", h.URL+"/api/privacy/subjects?kind=pack&after=bad", owner.Token, nil, 400)
	// No ZIP response is committed when preparation fails.
	if err = app.options.Objects.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	resp, data = privacyRequest(t, url, owner.Token, "text/html")
	if resp.StatusCode != 503 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") || resp.Header.Get("Content-Disposition") != "" || !bytes.Contains(data, []byte("/account")) {
		t.Fatalf("native error: %d %s", resp.StatusCode, data)
	}
	resp, data = privacyRequest(t, url, "", "text/html")
	if resp.StatusCode != 401 || !bytes.Contains(data, []byte("Войдите")) {
		t.Fatalf("anonymous native error %d %s", resp.StatusCode, data)
	}
	// Share the existing export admission budget with SIQ downloads.
	app.exports <- struct{}{}
	app.exports <- struct{}{}
	resp, data = privacyRequest(t, url, owner.Token, "")
	<-app.exports
	<-app.exports
	if resp.StatusCode != 429 || !bytes.Contains(data, []byte("busy")) {
		t.Fatalf("export budget %d %s", resp.StatusCode, data)
	}
	if _, err = app.store.Pool().Exec(ctx, "UPDATE account_credentials SET revoked_at=now() WHERE id=$1", actor.SessionID); err != nil {
		t.Fatal(err)
	}
	api(t, "GET", url, owner.Token, nil, 401)
	leftovers, err := filepath.Glob(filepath.Join(staging, "si-account-export-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatal("export staging files leaked", leftovers, err)
	}
}
