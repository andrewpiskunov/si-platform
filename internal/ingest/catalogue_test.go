package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
)

func catalogueInput(data []byte) CatalogueInput {
	sum := sha256.Sum256(data)
	return CatalogueInput{Namespace: "own-pilot", ExternalID: "001", Name: "pilot.siq", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), SourceURL: "https://example.org/own/001", License: "CC-BY-4.0", Attribution: "Pilot author", PermissionBasis: "Self-authored acceptance fixture"}
}
func TestCatalogueConcurrentReplayAndRights(t *testing.T) {
	f := setup(t)
	data := archive(t, textXML, nil)
	in := catalogueInput(data)
	if _, e := f.service.CreateCatalogue(bg, f.owner, in); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, e := f.pool.Exec(bg, `UPDATE account_users SET role='admin' WHERE id=$1`, f.owner.User.ID); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, e := f.service.CreateCatalogue(bg, f.owner, in)
			if e != nil {
				t.Error(e)
			}
			ids <- u.ID
		}()
	}
	wg.Wait()
	close(ids)
	id := ""
	for v := range ids {
		if id == "" {
			id = v
		}
		if id != v {
			t.Fatal("duplicate", id, v)
		}
	}
	changed := in
	changed.Attribution = "Different"
	if _, e := f.service.CreateCatalogue(bg, f.owner, changed); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e := f.service.Append(bg, f.owner, id, 0, bytes.NewReader(data), int64(len(data))); e != nil {
		t.Fatal(e)
	}
	if _, e := f.service.Complete(bg, f.owner, id); e != nil {
		t.Fatal(e)
	}
	f.service.Start(bg)
	done := awaitStatus(t, f, id, "ready")
	d, e := f.lib.Get(bg, f.owner, done.DraftID, 0)
	if e != nil {
		t.Fatal(e)
	}
	if d.Card.Rights.SourceURL != in.SourceURL || d.Card.Rights.License != in.License || d.Card.Rights.Attribution != in.Attribution || d.Card.Rights.Confirmed || d.Card.Status != "draft" {
		t.Fatal(d.Card)
	}
	again, e := f.service.CreateCatalogue(bg, f.owner, in)
	if e != nil || again.DraftID != done.DraftID {
		t.Fatal(again, e)
	}
	if e = f.lib.Delete(bg, f.owner, done.DraftID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.service.CreateCatalogue(bg, f.owner, in); !errors.Is(e, ErrNotFound) {
		t.Fatal("resurrection", e)
	}
}
func TestCatalogueChecksumFailureIsTerminal(t *testing.T) {
	f := setup(t)
	data := archive(t, textXML, nil)
	in := catalogueInput(data)
	in.SHA256 = hex.EncodeToString(make([]byte, 32))
	if _, e := f.pool.Exec(bg, `UPDATE account_users SET role='admin' WHERE id=$1`, f.owner.User.ID); e != nil {
		t.Fatal(e)
	}
	u, e := f.service.CreateCatalogue(bg, f.owner, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.service.Append(bg, f.owner, u.ID, 0, bytes.NewReader(data), int64(len(data))); e != nil {
		t.Fatal(e)
	}
	if _, e = f.service.Complete(bg, f.owner, u.ID); e != nil {
		t.Fatal(e)
	}
	f.service.Start(bg)
	done := awaitStatus(t, f, u.ID, "failed")
	if done.Error != "checksum_mismatch" {
		t.Fatal(done)
	}
	again, e := f.service.CreateCatalogue(bg, f.owner, in)
	if e != nil || again.ID != u.ID || again.Status != "failed" {
		t.Fatal(again, e)
	}
	var n int
	if e = f.pool.QueryRow(bg, `SELECT count(*) FROM library_packs`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}

func TestCatalogueLiveAuthorizationAndValidation(t *testing.T) {
	for _, mode := range []string{"scope", "revoked", "demoted", "invalid_url", "empty_permission"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			data := archive(t, textXML, nil)
			in := catalogueInput(data)
			if _, e := f.pool.Exec(bg, `UPDATE account_users SET role='admin' WHERE id=$1`, f.owner.User.ID); e != nil {
				t.Fatal(e)
			}
			a := actor(t, f.pool, "owner", "api", "drafts:read", "drafts:write")
			if _, e := f.service.CreateCatalogue(bg, a, in); e != nil {
				t.Fatal(e)
			}
			want := ErrForbidden
			switch mode {
			case "scope":
				a = actor(t, f.pool, "owner", "api", "drafts:read")
			case "revoked":
				if _, e := f.pool.Exec(bg, `UPDATE account_credentials SET revoked_at=now() WHERE id=$1`, a.SessionID); e != nil {
					t.Fatal(e)
				}
				want = ErrUnauthorized
			case "demoted":
				if _, e := f.pool.Exec(bg, `UPDATE account_users SET role='author' WHERE id='owner'`); e != nil {
					t.Fatal(e)
				}
			case "invalid_url":
				in.SourceURL = "https://secret:password@example.org"
				want = ErrInvalid
			case "empty_permission":
				in.PermissionBasis = " "
				want = ErrInvalid
			}
			if _, e := f.service.CreateCatalogue(bg, a, in); !errors.Is(e, want) {
				t.Fatal(e, want)
			}
		})
	}
}
