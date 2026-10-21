package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"siplatform/examples"
	"siplatform/internal/ingest"
	"siplatform/internal/library"
)

func TestMediaUploadHTTPTrustBoundary(t *testing.T) {
	app, initial, auth := platform(t)
	initial.Close()
	uploads, err := ingest.New(app.store.Pool(), app.options.Objects, app.options.Library, ingest.Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(uploads.Close)
	app.options.Uploads = uploads
	h := httptest.NewServer(app.Handler())
	t.Cleanup(h.Close)
	ctx := context.Background()
	owner, err := auth.Register(ctx, "media_owner", "long-enough-password", "Author")
	if err != nil {
		t.Fatal(err)
	}
	other, err := auth.Register(ctx, "media_intruder", "long-enough-password", "Other")
	if err != nil {
		t.Fatal(err)
	}
	var draft library.Draft
	json.Unmarshal(api(t, "POST", h.URL+"/api/drafts", owner.Token, map[string]any{"document": examples.Demo()}, 201), &draft)
	createURL := h.URL + "/api/media-uploads"
	request := map[string]any{"name": "photo.png", "kind": "image", "size": 100}
	api(t, "POST", createURL, "", request, 401)
	api(t, "POST", createURL, owner.Token, map[string]any{"name": "../photo.png", "kind": "image", "size": 100}, 400)
	api(t, "POST", createURL, owner.Token, map[string]any{"name": "photo.png", "kind": "image", "size": 100, "prepared": map[string]any{"key": "forged"}}, 400)
	var upload ingest.Upload
	json.Unmarshal(api(t, "POST", createURL, owner.Token, request, 201), &upload)
	if upload.Purpose != "media" || upload.Status != "uploading" || upload.Asset != nil {
		t.Fatal(upload)
	}
	path := h.URL + "/api/drafts/" + draft.Card.ID + "/assets"
	attach := map[string]any{"upload_id": upload.ID, "base_revision": draft.Card.Revision}
	api(t, "POST", path, other.Token, attach, 404)
	api(t, "POST", path, owner.Token, attach, 409)
	asset, _ := mediaImage(t, app, "photo.png", 70)
	raw, err := json.Marshal(asset)
	if err != nil {
		t.Fatal(err)
	}
	// The worker's durable trusted result is represented here; HTTP must never
	// accept client-supplied derivative keys, sizes or prepared metadata.
	if _, err = app.store.Pool().Exec(ctx, "UPDATE uploads SET status='ready',result_asset=$2 WHERE id=$1", upload.ID, raw); err != nil {
		t.Fatal(err)
	}
	api(t, "POST", path, owner.Token, map[string]any{"upload_id": upload.ID, "base_revision": 1, "asset": asset}, 400)
	var attached library.Draft
	json.Unmarshal(api(t, "POST", path, owner.Token, attach, 200), &attached)
	if attached.Card.Revision != 2 || len(attached.Assets) != 1 || attached.Assets[0].Prepared.Standard.Key != asset.Prepared.Standard.Key {
		t.Fatal(attached)
	}
	json.Unmarshal(api(t, "POST", path, owner.Token, attach, 200), &attached)
	if attached.Card.Revision != 2 {
		t.Fatal("duplicate attachment created revision")
	}
	if _, err = app.store.Pool().Exec(ctx, "UPDATE account_credentials SET revoked_at=now() WHERE user_id=$1", owner.User.ID); err != nil {
		t.Fatal(err)
	}
	api(t, "POST", path, owner.Token, attach, 401)
}
