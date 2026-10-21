package library

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"siplatform/internal/accounts"
	"siplatform/internal/packs"
)

// Historical preparation failures must not prevent using a replacement, while
// the original revision and a later reverted reference remain blocked.
func TestReplacedFailedMediaRecomputesReadinessAcrossRevisions(t *testing.T) {
	s, p := setup(t)
	owner := identity(t, p, "owner", "author", "session")
	mod := identity(t, p, "moderator", "moderator", "session")
	other := identity(t, p, "other", "author", "session")
	broken := Asset{Name: "Images/broken.png", Key: strings.Repeat("a", 64), Size: 3, ContentType: "image/png"}
	original := []packs.Issue{{Severity: "error", Code: "media_invalid", Path: broken.Name, Message: "Invalid media"}}
	doc := document("Repair imported media")
	doc.Rounds[0].Themes[0].Questions[0].Params[0].Items = []packs.Content{{Type: "image", Value: "broken.png", IsRef: true}}
	d, err := s.Create(ctx, owner, Save{Document: doc, Assets: []Asset{broken}, Issues: original, OriginalKey: strings.Repeat("d", 64)})
	if err != nil {
		t.Fatal(err)
	}
	replacement := preparedImage()
	uploadID := strings.Repeat("b", 32)
	readyMedia(t, p, owner, uploadID, replacement)
	d, err = s.AttachAsset(ctx, owner, d.Card.ID, d.Card.Revision, uploadID, replacement)
	if err != nil {
		t.Fatal(err)
	}
	if packs.Playable(d.PlayIssues) {
		t.Fatal("attachment alone resolved the damaged reference", d.PlayIssues)
	}
	if _, err = s.ForPlay(ctx, owner, d.Card.ID, d.Card.Revision); !errors.Is(err, ErrInvalid) {
		t.Fatal("unresolved damaged media played", err)
	}
	doc.Rounds[0].Themes[0].Questions[0].Params[0].Items[0].Value = "photo.png"
	d, err = s.UpdateDocument(ctx, owner, d.Card.ID, d.Card.Revision, doc)
	if err != nil {
		t.Fatal(err)
	}
	if !packs.Playable(d.PlayIssues) {
		t.Fatal("replacement not ready", d.PlayIssues)
	}
	if _, err = s.ForPlay(ctx, owner, d.Card.ID, d.Card.Revision); err != nil {
		t.Fatalf("valid replacement is still blocked by obsolete importer error: %v (issues=%+v)", err, d.Issues)
	}
	if !reflect.DeepEqual(d.AssetIssues, original) || len(d.Issues) != 1 || d.Issues[0].Severity != "warning" {
		t.Fatal("historical diagnostic not preserved separately from readiness", d.AssetIssues, d.Issues)
	}
	// Simulate a revision saved before this fix: reads must derive readiness
	// from its own document, without a migration or an edit to that revision.
	stale, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, "UPDATE library_revisions SET issues=$3 WHERE pack_id=$1 AND revision=$2", d.Card.ID, d.Card.Revision, stale); err != nil {
		t.Fatal(err)
	}
	s = New(p)
	loaded, err := s.Get(ctx, owner, d.Card.ID, d.Card.Revision)
	if err != nil || !reflect.DeepEqual(loaded.AssetIssues, original) || !packs.Playable(loaded.Issues) {
		t.Fatal("persisted revision retained obsolete blocking error", loaded, err)
	}
	if _, err = s.ForPlay(ctx, owner, d.Card.ID, d.Card.Revision); err != nil {
		t.Fatal("reloaded replacement blocked", err)
	}
	download, err := s.ForDownload(ctx, owner, d.Card.ID, d.Card.Revision)
	if err != nil || download.OriginalKey != strings.Repeat("d", 64) || len(download.Assets) != 2 || !reflect.DeepEqual(download.Assets[0], broken) {
		t.Fatal("repaired export must retain original source bytes and metadata", download, err)
	}
	publish(t, s, owner, mod, loaded, true)
	if _, err = s.ForPlay(ctx, other, d.Card.ID, 0); err != nil {
		t.Fatal("published replacement blocked", err)
	}
	if _, err = s.ForDownload(ctx, accounts.Identity{}, d.Card.ID, 0); err != nil {
		t.Fatal("published repaired export blocked", err)
	}
	old, err := s.Get(ctx, owner, d.Card.ID, 1)
	if err != nil || packs.Playable(old.Issues) || packs.Playable(old.PlayIssues) || !reflect.DeepEqual(old.AssetIssues, original) {
		t.Fatal("original revision diagnostics changed", old, err)
	}
	if _, err = s.ForPlay(ctx, owner, d.Card.ID, 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("old damaged revision played", err)
	}
	doc.Rounds[0].Themes[0].Questions[0].Params[0].Items[0].Value = "BROKEN%2EPNG"
	reverted, err := s.UpdateDocument(ctx, owner, d.Card.ID, d.Card.Revision, doc)
	if err != nil || packs.Playable(reverted.PlayIssues) || packs.Playable(reverted.Issues) || !reflect.DeepEqual(reverted.AssetIssues, original) {
		t.Fatal("reverting a canonical reference did not restore blocking diagnostics", reverted, err)
	}
	if _, err = s.ForPlay(ctx, owner, d.Card.ID, reverted.Card.Revision); !errors.Is(err, ErrInvalid) {
		t.Fatal("reverted damaged media played", err)
	}
	if _, err = s.ForDownload(ctx, owner, d.Card.ID, reverted.Card.Revision); !errors.Is(err, ErrInvalid) {
		t.Fatal("reverted damaged media exported", err)
	}
	if _, err = s.Submit(ctx, owner, d.Card.ID, reverted.Card.Revision, rights(true)); !errors.Is(err, ErrInvalid) {
		t.Fatal("reverted damaged media submitted", err)
	}
	if _, err = s.ForPlay(ctx, other, d.Card.ID, d.Card.Revision); err != nil {
		t.Fatal("later revert changed the published revision", err)
	}
}

// Only obsolete file preparation errors may become advisory. Current references
// and document/source diagnostics must retain their blocking meaning.
func TestMediaDiagnosticsKeepCurrentReferencesAndDocumentErrors(t *testing.T) {
	for _, name := range []string{"answer", "nested", "logo", "document", "source", "unknown_asset_issue", "source_same_issue", "unattributed_media_issue"} {
		t.Run(name, func(t *testing.T) {
			s, p := setup(t)
			owner := identity(t, p, "owner", "author", "session")
			broken := Asset{Name: "Images/broken.png", Key: strings.Repeat("a", 64), Size: 3, ContentType: "image/png"}
			issue := packs.Issue{Severity: "error", Code: "media_invalid", Path: broken.Name, Message: "Invalid media"}
			doc := document("Keep genuine errors")
			item := packs.Content{Type: "image", Value: "BROKEN%2EPNG", IsRef: true}
			q := &doc.Rounds[0].Themes[0].Questions[0]
			switch name {
			case "answer":
				q.Params = append(q.Params, packs.Parameter{Name: "answer", Type: "content", Items: []packs.Content{item}})
			case "nested":
				q.Params = append(q.Params, packs.Parameter{Name: "theme", Type: "group", Params: []packs.Parameter{{Name: "inner", Type: "content", Items: []packs.Content{item}}}})
			case "logo":
				doc.Logo = "@BROKEN%2EPNG"
			case "document":
				q.Right = nil
			case "source":
				doc.ImportIssues = []packs.Issue{{Severity: "error", Code: "unsupported_script", Path: "document", Message: "Unsupported imported script"}}
			case "unknown_asset_issue":
				issue.Code = "unknown_import_rule"
			case "source_same_issue":
				doc.ImportIssues = []packs.Issue{issue}
			case "unattributed_media_issue":
				issue.Path = "document"
			}
			d, err := s.Create(ctx, owner, Save{Document: doc, Assets: []Asset{broken}, Issues: []packs.Issue{issue}})
			if err != nil {
				t.Fatal(err)
			}
			if packs.Playable(d.Issues) {
				t.Fatal("actual error suppressed", d.Issues)
			}
			if name == "answer" && packs.Playable(d.PlayIssues) {
				t.Fatal("unresolved answer media not blocking playback", d.PlayIssues)
			}
			if _, err = New(p).ForPlay(ctx, owner, d.Card.ID, 1); !errors.Is(err, ErrInvalid) {
				t.Fatal("actual error played after load", err)
			}
		})
	}
}

func TestPreparedAssetRetainsHistoricalFailureWithoutBlocking(t *testing.T) {
	s, p := setup(t)
	owner := identity(t, p, "owner", "author", "session")
	asset := preparedImage()
	pending := asset
	pending.Prepared = nil
	issue := packs.Issue{Severity: "error", Code: "media_unavailable", Path: asset.Name, Message: "Preparation unavailable"}
	doc := document("Retry preparation")
	doc.Rounds[0].Themes[0].Questions[0].Params[0].Items = []packs.Content{{Type: "image", Value: "photo.png", IsRef: true}}
	d, err := s.Create(ctx, owner, Save{Document: doc, Assets: []Asset{pending}, Issues: []packs.Issue{issue}})
	if err != nil {
		t.Fatal(err)
	}
	d, err = s.Update(ctx, owner, d.Card.ID, 1, Save{Document: doc, Assets: []Asset{asset}, Issues: []packs.Issue{issue}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = New(p).ForPlay(ctx, owner, d.Card.ID, d.Card.Revision); err != nil {
		t.Fatal("prepared asset still blocked by earlier preparation failure", err)
	}
	if !reflect.DeepEqual(d.AssetIssues, []packs.Issue{issue}) {
		t.Fatal("original diagnostic lost", d.AssetIssues)
	}
}
