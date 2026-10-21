package ingest

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"siplatform/internal/blob"
	"siplatform/internal/library"
	"siplatform/internal/retention"
	"siplatform/internal/themes"
)

func themeArchive(t *testing.T, image bool) []byte {
	t.Helper()
	manifest := `{"schema_version":1,"name":"Private theme","author":"Author","provenance":"Original","font":"sans","palette":{"background":"#FFFFFF","surface":"#FFFFFF","surface_alt":"#FFFFFF","border":"#000000","text":"#000000","muted":"#000000","accent":"#000000","accent_text":"#FFFFFF","danger":"#000000","success":"#000000","focus":"#000000"},"cards":{"radius":8,"border_width":1},"buttons":{"radius":8,"border_width":1}}`
	if image {
		manifest = strings.TrimSuffix(manifest, "}") + `,"background":"assets/photo.png"}`
	}
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	f, err := w.Create("theme.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.WriteString(f, manifest); err != nil {
		t.Fatal(err)
	}
	if image {
		f, err = w.Create("assets/photo.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(pngMedia(t)); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func queueTheme(t *testing.T, f fixture, data []byte) Upload {
	t.Helper()
	u, err := f.service.CreateTheme(bg, f.owner, "theme.zip", int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.service.Append(bg, f.owner, u.ID, 0, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	u, err = f.service.Complete(bg, f.owner, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// Scope-bearing API tokens cannot create, inspect, append or complete theme jobs.
func TestThemeUploadRequiresSessionOnEveryOperation(t *testing.T) {
	f := setup(t)
	api := actor(t, f.pool, "owner", "api", "drafts:read", "drafts:write", "drafts:export")
	api.APIToken = false
	if _, err := f.service.CreateTheme(bg, api, "theme.zip", 4); !errors.Is(err, ErrForbidden) {
		t.Fatal("API created theme", err)
	}
	if _, err := f.service.CreateTheme(bg, f.owner, "theme.zip", 5<<20+1); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversize theme", err)
	}
	u, err := f.service.CreateTheme(bg, f.owner, "theme.zip", 4)
	if err != nil || u.Purpose != "theme" {
		t.Fatal(u, err)
	}
	for name, fn := range map[string]func() error{
		"get":      func() error { _, e := f.service.Get(bg, api, u.ID); return e },
		"append":   func() error { _, e := f.service.Append(bg, api, u.ID, 0, strings.NewReader("data"), 4); return e },
		"complete": func() error { _, e := f.service.Complete(bg, api, u.ID); return e },
	} {
		if err := fn(); !errors.Is(err, ErrForbidden) {
			t.Fatal(name, err)
		}
	}
	foreign := actor(t, f.pool, "other", "session")
	if _, err = f.service.Get(bg, foreign, u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign upload", err)
	}
	if _, err = f.service.Append(bg, f.owner, u.ID, 0, strings.NewReader("data"), 4); err != nil {
		t.Fatal(err)
	}
}

// Palette-only jobs need no processor, survive restart, and attach the exact
// durable result while preserving the original archive bytes.
func TestThemePaletteUploadSurvivesRestartAndAttaches(t *testing.T) {
	f := setup(t)
	data := themeArchive(t, false)
	u := queueTheme(t, f, data)
	f.service.Close()
	next, err := New(f.pool, f.objects, f.lib, Options{Directory: f.dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	f.service = next
	next.Start(bg)
	u = awaitStatus(t, f, u.ID, "ready")
	if u.Theme == nil || u.Asset != nil || u.DraftID != "" || len(u.Issues) != 0 || u.Theme.Validate() != nil {
		t.Fatal(u)
	}
	sum := sha256.Sum256(data)
	if u.Theme.OriginalKey != hex.EncodeToString(sum[:]) || u.Theme.SizeBytes != int64(len(data)) {
		t.Fatal(u.Theme)
	}
	r, err := f.objects.Get(bg, u.Theme.OriginalKey, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	r.Close()
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("source archive changed", err)
	}
	v, err := themes.New(f.pool).Attach(bg, f.owner, "", 0, u.ID)
	if err != nil || v.Prepared.Hash != u.Theme.Hash {
		t.Fatal(v, err)
	}
}

func TestThemeInvalidArchiveKeepsSafeDurableDiagnostics(t *testing.T) {
	f := setup(t)
	u := queueTheme(t, f, []byte("not a zip"))
	f.service.Start(bg)
	u = awaitStatus(t, f, u.ID, "failed")
	if u.Error != "theme_invalid" || u.Theme != nil || u.Asset != nil || len(u.Issues) == 0 || u.Issues[0].Path == "" || u.Issues[0].Message == "" {
		t.Fatal(u)
	}
	var keys []string
	if err := f.pool.QueryRow(bg, "SELECT stored_keys FROM uploads WHERE id=$1", u.ID).Scan(&keys); err != nil || len(keys) != 0 {
		t.Fatal("invalid archive stored objects", keys, err)
	}
	loaded, err := f.service.Get(bg, f.owner, u.ID)
	if err != nil || len(loaded.Issues) != len(u.Issues) {
		t.Fatal("diagnostics not persisted", loaded, err)
	}
}

func TestRealThemeImageReservesArchiveAndEveryOutputBeforePut(t *testing.T) {
	f := setup(t)
	f.service.media = realProcessor(t)
	data := themeArchive(t, true)
	u := queueTheme(t, f, data)
	gate := &mediaPutGate{Store: f.objects, entered: make(chan string, 1)}
	f.service.objects = gate
	f.service.Start(bg)
	select {
	case <-gate.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("theme never published")
	}
	var keys []string
	if err := f.pool.QueryRow(bg, "SELECT stored_keys FROM uploads WHERE id=$1", u.ID).Scan(&keys); err != nil || len(keys) < 2 {
		t.Fatal("all source/derivative reservations must precede first Put", keys, err)
	}
	sum := sha256.Sum256(data)
	source := hex.EncodeToString(sum[:])
	found := false
	for _, key := range keys {
		found = found || key == source
	}
	if !found {
		t.Fatal("source not reserved", keys)
	}
	f.service.Close()
	var status string
	if err := f.pool.QueryRow(bg, "SELECT status FROM uploads WHERE id=$1", u.ID).Scan(&status); err != nil || status != "processing" {
		t.Fatal("cancel finalized processing job", status, err)
	}
	next, err := New(f.pool, f.objects, f.lib, Options{Directory: f.dir, Media: f.service.media})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	f.service = next
	next.Start(bg)
	u = awaitStatus(t, f, u.ID, "ready")
	if u.Theme == nil || len(u.Theme.Assets) != 1 || u.Theme.SizeBytes <= int64(len(data)) {
		t.Fatal(u)
	}
	for _, asset := range u.Theme.Assets {
		for _, variant := range []struct {
			key  string
			size int64
		}{{asset.Prepared.Standard.Key, asset.Prepared.Standard.Size}, {asset.Prepared.Economy.Key, asset.Prepared.Economy.Size}} {
			meta, err := f.objects.Stat(bg, variant.key)
			if err != nil || meta.Size != variant.size || meta.ContentType != "image/png" {
				t.Fatal(meta, err)
			}
		}
	}
}

func TestThemeCompletionRechecksAuthorizationFencingAndCancellation(t *testing.T) {
	for _, mode := range []string{"revoked", "api", "epoch", "canceled", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			data := themeArchive(t, false)
			u := queueTheme(t, f, data)
			j, err := f.service.claim(bg, 0)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := themes.Read(bg, bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			result, err := themes.Prepare(bg, nil, parsed)
			if err != nil {
				t.Fatal(err)
			}
			defer result.Close()
			finishCtx := bg
			switch mode {
			case "revoked":
				_, err = f.pool.Exec(bg, "UPDATE account_credentials SET revoked_at=now() WHERE id=$1", f.owner.SessionID)
			case "api":
				api := actor(t, f.pool, "owner", "api", "drafts:write")
				j.credential = api.SessionID
			case "epoch":
				_, err = f.pool.Exec(bg, "UPDATE ingest_owner SET epoch=1")
			case "canceled":
				c, cancel := context.WithCancel(bg)
				cancel()
				finishCtx = c
			case "timeout":
				c, cancel := context.WithDeadline(bg, time.Now().Add(-time.Second))
				defer cancel()
				_, _, code := f.service.processTheme(c, j, 0)
				if code != "import_timeout" {
					t.Fatal("deadline not enforced", code)
				}
				finishCtx = c
			}
			if err != nil {
				t.Fatal(err)
			}
			f.service.finishTheme(finishCtx, j, 0, &result.Prepared, nil, "")
			var status, code string
			var raw []byte
			if err = f.pool.QueryRow(bg, "SELECT status,error,result_theme FROM uploads WHERE id=$1", u.ID).Scan(&status, &code, &raw); err != nil {
				t.Fatal(err)
			}
			if mode == "revoked" || mode == "api" {
				if status != "failed" || code != "authorization_expired" {
					t.Fatal(status, code)
				}
			} else if status != "processing" {
				t.Fatal("stale/canceled worker mutated job", status)
			}
			if len(raw) != 0 {
				t.Fatal("unauthorized result persisted", string(raw))
			}
		})
	}
}

func TestThemeReadyQuotaIncludesArchiveAndDerivatives(t *testing.T) {
	f := setup(t)
	f.service.media = realProcessor(t)
	doc, err := f.lib.Create(bg, f.owner, library.Save{Document: archiveDocument(t)})
	if err != nil {
		t.Fatal(err)
	}
	data := themeArchive(t, true)
	if _, err = f.pool.Exec(bg, "UPDATE library_revisions SET size_bytes=$2 WHERE pack_id=$1", doc.Card.ID, int64(10<<30-len(data)-1)); err != nil {
		t.Fatal(err)
	}
	u := queueTheme(t, f, data)
	f.service.Start(bg)
	u = awaitStatus(t, f, u.ID, "failed")
	if u.Error != "draft_quota" || u.Theme != nil {
		t.Fatal("derivatives escaped shared quota", u)
	}
}

func TestThemeVersionsProtectArchiveAndDerivativesFromGC(t *testing.T) {
	f := setup(t)
	f.service.media = realProcessor(t)
	u := queueTheme(t, f, themeArchive(t, true))
	f.service.Start(bg)
	u = awaitStatus(t, f, u.ID, "ready")
	s := themes.New(f.pool)
	v, err := s.Attach(bg, f.owner, "", 0, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(bg, f.owner, v.Card.ID); err != nil {
		t.Fatal(err)
	}
	f.service.Close()
	next, err := New(f.pool, f.objects, f.lib, Options{Directory: f.dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	var keys []string
	if err = f.pool.QueryRow(bg, "SELECT stored_keys FROM uploads WHERE id=$1", u.ID).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if _, err = f.pool.Exec(bg, "UPDATE uploads SET status='failed',result_theme=NULL,updated_at=now()-interval '2 hours' WHERE id=$1", u.ID); err != nil {
		t.Fatal(err)
	}
	var epoch int64
	if err = f.pool.QueryRow(bg, "SELECT epoch FROM ingest_owner").Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	next.cleanup(bg, epoch)
	if _, err = retention.New(f.pool, f.objects).Sweep(bg); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if _, err = f.objects.Stat(bg, key); err != nil {
			t.Fatal("retained theme object deleted", key, err)
		}
	}
	if _, err = s.Pinned(bg, v.Card.ID, 1, v.Prepared.Hash); err != nil {
		t.Fatal("retained pin unavailable", err)
	}
}

func TestThemeRevokedDuringPublicationFailsAndLeavesCollectibleObjects(t *testing.T) {
	f := setup(t)
	data := themeArchive(t, false)
	u := queueTheme(t, f, data)
	f.service.objects = &revokingStore{Store: f.objects, pool: f.pool, credential: f.owner.SessionID}
	f.service.Start(bg)
	deadline := time.Now().Add(10 * time.Second)
	var status, code string
	for time.Now().Before(deadline) {
		if err := f.pool.QueryRow(bg, "SELECT status,error FROM uploads WHERE id=$1", u.ID).Scan(&status, &code); err != nil {
			t.Fatal(err)
		}
		if status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != "failed" || code != "authorization_expired" {
		t.Fatal(status, code)
	}
	f.service.Close()
	next, err := New(f.pool, f.objects, f.lib, Options{Directory: f.dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	var keys []string
	if err = f.pool.QueryRow(bg, "SELECT stored_keys FROM uploads WHERE id=$1", u.ID).Scan(&keys); err != nil || len(keys) != 1 {
		t.Fatal(keys, err)
	}
	if _, err = f.pool.Exec(bg, "UPDATE uploads SET updated_at=now()-interval '2 hours' WHERE id=$1", u.ID); err != nil {
		t.Fatal(err)
	}
	var epoch int64
	if err = f.pool.QueryRow(bg, "SELECT epoch FROM ingest_owner").Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	next.cleanup(bg, epoch)
	if _, err = retention.New(f.pool, f.objects).Sweep(bg); err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if _, err = f.objects.Stat(bg, key); !errors.Is(err, blob.ErrNotFound) {
			t.Fatal("failed theme object leaked", err)
		}
	}
}
