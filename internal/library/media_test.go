package library

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"siplatform/internal/accounts"
	"siplatform/internal/media"
	"siplatform/internal/packs"
)

func preparedImage() Asset {
	return Asset{Name: "Images/photo.png", Key: strings.Repeat("1", 64), ContentType: "image/png", Size: 30, Prepared: &media.Prepared{Kind: "image", Width: 32, Height: 24, Standard: media.Variant{Key: strings.Repeat("2", 64), MIME: "image/png", Size: 40}, Economy: media.Variant{Key: strings.Repeat("3", 64), MIME: "image/png", Size: 20}}}
}
func readyMedia(t *testing.T, p *pgxpool.Pool, a accounts.Identity, id string, asset Asset) {
	t.Helper()
	data, err := json.Marshal(asset)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Exec(ctx, `INSERT INTO uploads(id,owner_id,credential_id,name,size,offset_bytes,status,purpose,media_kind,result_asset) VALUES($1,$2,$3,'photo.png',$4,$4,'ready','media','image',$5)`, id, a.User.ID, a.SessionID, asset.Size, data)
	if err != nil {
		t.Fatal(err)
	}
}

// Attachment must preserve the document and history; retried receipts must not add revisions.
func TestAttachPreparedMediaIsImmutableAndIdempotent(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	d := create(t, s, a, "Before")
	asset := preparedImage()
	uploadID := strings.Repeat("a", 32)
	readyMedia(t, p, a, uploadID, asset)
	attached, err := s.AttachAsset(ctx, a, d.Card.ID, 1, uploadID, asset)
	if err != nil || attached.Card.Revision != 2 || attached.Document.Name != "Before" || len(attached.Assets) != 1 {
		t.Fatal(attached, err)
	}
	if attached.Assets[0].Prepared == nil || attached.Assets[0].Prepared.Standard.Key != asset.Prepared.Standard.Key {
		t.Fatal("manifest lost", attached.Assets)
	}
	original, err := s.Get(ctx, a, d.Card.ID, 1)
	if err != nil || len(original.Assets) != 0 {
		t.Fatal("old revision changed", original, err)
	}
	doc := attached.Document
	doc.Name = "Edited after attach"
	current, err := s.UpdateDocument(ctx, a, d.Card.ID, 2, doc)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := s.AttachAsset(ctx, a, d.Card.ID, 1, uploadID, asset)
	if err != nil || repeated.Card.Revision != 3 || repeated.Document.Name != current.Document.Name || len(repeated.Assets) != 1 {
		t.Fatal("retry changed draft", repeated, err)
	}
	history, err := s.History(ctx, a, d.Card.ID)
	if err != nil || len(history) != 3 {
		t.Fatal(history, err)
	}
}

// A trusted result still requires ownership, live credential, exact stored metadata and the current base.
func TestAttachMediaChecksOwnerCredentialConflictAndStoredResult(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	b := identity(t, p, "other", "author", "session")
	read := credential(t, p, "owner", "api", "drafts:read")
	write := credential(t, p, "owner", "api", "drafts:write")
	d := create(t, s, a, "Before")
	asset := preparedImage()
	ownID := strings.Repeat("a", 32)
	foreignID := strings.Repeat("b", 32)
	readyMedia(t, p, a, ownID, asset)
	readyMedia(t, p, b, foreignID, asset)
	if _, err := s.AttachAsset(ctx, a, d.Card.ID, 1, foreignID, asset); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign upload", err)
	}
	if _, err := s.AttachAsset(ctx, b, d.Card.ID, 1, foreignID, asset); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign draft", err)
	}
	if _, err := s.AttachAsset(ctx, read, d.Card.ID, 1, ownID, asset); !errors.Is(err, ErrForbidden) {
		t.Fatal("read scope", err)
	}
	forged := preparedImage()
	forged.Prepared.Standard.Key = strings.Repeat("f", 64)
	if _, err := s.AttachAsset(ctx, a, d.Card.ID, 1, ownID, forged); !errors.Is(err, ErrInvalid) {
		t.Fatal("forged stored result", err)
	}
	changed, err := s.UpdateDocument(ctx, a, d.Card.ID, 1, document("Unrelated edit"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachAsset(ctx, a, d.Card.ID, 1, ownID, asset); !errors.Is(err, ErrConflict) {
		t.Fatal("stale base", err)
	}
	got, err := s.Get(ctx, a, d.Card.ID, 0)
	if err != nil || got.Document.Name != changed.Document.Name || len(got.Assets) != 0 {
		t.Fatal("conflict overwrote local edit", got, err)
	}
	if _, err = p.Exec(ctx, "UPDATE account_credentials SET revoked_at=now() WHERE id=$1", write.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachAsset(ctx, write, d.Card.ID, 2, ownID, asset); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoked write", err)
	}
	if _, err = s.AttachAsset(ctx, a, d.Card.ID, 2, ownID, asset); err != nil {
		t.Fatal("valid retry", err)
	}
}

// Concurrent delivery of the same upload is one attachment transaction and one receipt.
func TestConcurrentAttachHasOneRevision(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	d := create(t, s, a, "Before")
	asset := preparedImage()
	id := strings.Repeat("a", 32)
	readyMedia(t, p, a, id, asset)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			out, err := s.AttachAsset(ctx, a, d.Card.ID, 1, id, asset)
			if err == nil && out.Card.Revision != 2 {
				err = ErrConflict
			}
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	history, err := s.History(ctx, a, d.Card.ID)
	if err != nil || len(history) != 2 {
		t.Fatal(history, err)
	}
}

// Invalid manifests must never produce playable metadata; derivative bytes count toward logical quota.
func TestPreparedManifestValidationAndQuota(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	for name, mutate := range map[string]func(*Asset){
		"hash":       func(a *Asset) { a.Prepared.Standard.Key = "wrong" },
		"mime":       func(a *Asset) { a.Prepared.Economy.MIME = "image/svg+xml" },
		"size":       func(a *Asset) { a.Prepared.Standard.Size = 100<<20 + 1 },
		"dimensions": func(a *Asset) { a.Prepared.Width = 16000001; a.Prepared.Height = 1 },
		"kind":       func(a *Asset) { a.Prepared.Kind = "audio" },
		"duration":   func(a *Asset) { a.Prepared.DurationMS = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			asset := preparedImage()
			mutate(&asset)
			if _, err := s.Create(ctx, a, Save{Document: document("Bad"), Assets: []Asset{asset}}); !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
	d := create(t, s, a, "Quota")
	asset := preparedImage()
	id := strings.Repeat("a", 32)
	readyMedia(t, p, a, id, asset)
	// The original (30 bytes) fits, but its verified variants (60 bytes) exceed remaining quota.
	docBytes, err := json.Marshal(d.Document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, "UPDATE library_revisions SET size_bytes=$2 WHERE pack_id=$1", d.Card.ID, int64(10<<30-len(docBytes)-50)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachAsset(ctx, a, d.Card.ID, 1, id, asset); !errors.Is(err, ErrQuota) {
		t.Fatal("derivatives escaped quota", err)
	}
	if _, err = p.Exec(ctx, "UPDATE library_revisions SET size_bytes=$2 WHERE pack_id=$1", d.Card.ID, int64(len(docBytes))); err != nil {
		t.Fatal(err)
	}
	attached, err := s.AttachAsset(ctx, a, d.Card.ID, 1, id, asset)
	if err != nil || attached.Card.SizeBytes != int64(len(docBytes))+90 {
		t.Fatal(attached.Card.SizeBytes, err)
	}
}

// Referenced media needs a verified manifest; exports still use the untouched original asset.
func TestPreparedMediaReadiness(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	doc := document("Picture")
	doc.Rounds[0].Themes[0].Questions[0].Params[0].Items = []packs.Content{{Type: "image", Value: "photo.png", IsRef: true}}
	asset := preparedImage()
	pending := asset
	pending.Prepared = nil
	d, err := s.Create(ctx, a, Save{Document: doc, Assets: []Asset{pending}})
	if err != nil {
		t.Fatal(err)
	}
	if packs.Playable(d.PlayIssues) {
		t.Fatal("unprepared media marked playable")
	}
	ready, err := s.Update(ctx, a, d.Card.ID, 1, Save{Document: doc, Assets: []Asset{asset}})
	if err != nil || !packs.Playable(ready.PlayIssues) {
		t.Fatal(ready.PlayIssues, err)
	}
	if ready.Assets[0].Key != asset.Key || ready.Assets[0].Size != 30 || ready.Assets[0].ContentType != "image/png" {
		t.Fatal("original fields overwritten", ready.Assets)
	}
}
