package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"siplatform/examples"
	"siplatform/internal/library"
)

func TestPrivateFilesRangesAndBlock(t *testing.T) {
	app, h, auth := platform(t)
	ctx := context.Background()
	session, e := auth.Register(ctx, "files_author", "long-enough-password", "Автор")
	if e != nil {
		t.Fatal(e)
	}
	actor, e := auth.Authenticate(ctx, session.Token)
	if e != nil {
		t.Fatal(e)
	}
	data := []byte("private-payload-for-range-test")
	sum := sha256.Sum256(data)
	key := hex.EncodeToString(sum[:])
	if e = app.options.Objects.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "application/octet-stream"); e != nil {
		t.Fatal(e)
	}
	d, e := app.options.Library.Create(ctx, actor, library.Save{Document: examples.Demo(), OriginalKey: key, Assets: []library.Asset{{Name: "Images/test.png", Key: key, Size: int64(len(data)), ContentType: "application/octet-stream"}}})
	if e != nil {
		t.Fatal(e)
	}
	url := h.URL + "/api/drafts/" + d.Card.ID + "/assets/" + key
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+session.Token)
	req.Header.Set("Range", "bytes=2-8")
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	part, e := io.ReadAll(resp.Body)
	resp.Body.Close()
	if e != nil {
		t.Fatal(e)
	}
	if resp.StatusCode != 206 || !bytes.Equal(part, data[2:9]) || resp.Header.Get("Content-Range") != "bytes 2-8/30" {
		t.Fatalf("range: %d %q %q", resp.StatusCode, part, resp.Header.Get("Content-Range"))
	}
	req.Header.Del("Range")
	req.Header.Set("If-None-Match", resp.Header.Get("ETag"))
	resp, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 304 {
		t.Fatal("conditional response ignored")
	}
	api(t, "GET", url, "", nil, 401)
	var limited struct {
		Token string `json:"token"`
	}
	json.Unmarshal(api(t, "POST", h.URL+"/api/account/tokens", session.Token, map[string]any{"name": "reader", "scopes": []string{"drafts:read"}}, 201), &limited)
	api(t, "GET", h.URL+"/api/drafts/"+d.Card.ID+"/original", limited.Token, nil, 403)
	// The moderation decision revokes every byte-serving route, including original
	// and individual assets, even for the owner who can still inspect diagnostics.
	if _, e = app.store.Pool().Exec(ctx, "UPDATE library_packs SET blocked=true WHERE id=$1", d.Card.ID); e != nil {
		t.Fatal(e)
	}
	api(t, "GET", url, session.Token, nil, 403)
	api(t, "GET", h.URL+"/api/drafts/"+d.Card.ID+"/original", session.Token, nil, 403)
}
