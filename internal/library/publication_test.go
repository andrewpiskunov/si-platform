package library

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"siplatform/internal/accounts"
	"siplatform/internal/packs"
)

func rights(download bool) Rights {
	return Rights{License: "CC BY 4.0", SourceURL: "original", Attribution: "Author", AllowDownload: download, Confirmed: true}
}
func publish(t *testing.T, s *Service, owner, mod accounts.Identity, d Draft, download bool) Card {
	t.Helper()
	if _, err := s.Submit(ctx, owner, d.Card.ID, d.Card.Revision, rights(download)); err != nil {
		t.Fatal("submit", err)
	}
	card, err := s.Decide(ctx, mod, d.Card.ID, d.Card.Revision, "approve", "")
	if err != nil {
		t.Fatal("approve", err)
	}
	return card
}

// Editing after approval must not change catalogue metadata, download rights or playable answers.
func TestPublicationPinsRevisionAndWithdrawalKeepsStartedGame(t *testing.T) {
	s, p := setup(t)
	owner := identity(t, p, "owner", "author", "session")
	mod := identity(t, p, "moderator", "moderator", "session")
	other := identity(t, p, "other", "author", "session")
	d := create(t, s, owner, "Public first")
	publish(t, s, owner, mod, d, false)
	second, err := s.Update(ctx, owner, d.Card.ID, 1, Save{Document: document("Private second")})
	if err != nil {
		t.Fatal(err)
	}
	card, err := s.Public(ctx, d.Card.ID)
	if err != nil || card.Name != "Public first" || card.Revision != 1 || card.Status != "published" {
		t.Fatal(card, err)
	}
	play, err := s.ForPlay(ctx, other, d.Card.ID, 0)
	if err != nil || play.Card.Revision != 1 {
		t.Fatal(play, err)
	}
	if _, err = s.ForPlay(ctx, other, d.Card.ID, 2); !errors.Is(err, ErrNotFound) {
		t.Fatal("private revision played", err)
	}
	if _, err = s.ForDownload(ctx, accounts.Identity{}, d.Card.ID, 1); !errors.Is(err, ErrForbidden) {
		t.Fatal("download rights ignored", err)
	}
	if _, err = s.ForDownload(ctx, owner, d.Card.ID, 2); err != nil {
		t.Fatal("owner export", err)
	}
	if err = s.Withdraw(ctx, owner, d.Card.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Public(ctx, d.Card.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("withdrawal still public", err)
	}
	if _, err = s.ForPlay(ctx, other, d.Card.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("new play after withdrawal", err)
	}
	if _, err = s.Pinned(ctx, d.Card.ID, 1); err != nil {
		t.Fatal("started game broken", err)
	}
	publish(t, s, owner, mod, second, true)
	got, err := s.ForDownload(ctx, accounts.Identity{}, d.Card.ID, 0)
	if err != nil || got.Card.Revision != 2 {
		t.Fatal("public download", got, err)
	}
	if _, err = s.ForDownload(ctx, accounts.Identity{}, d.Card.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("old rights reused", err)
	}
}

// Invalid workflow transitions, stale moderation and API tokens must not publish unreviewed content.
func TestModerationTransitionsRolesAndBlocking(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	m := identity(t, p, "moderator", "moderator", "session")
	reader := credential(t, p, "owner", "api", "drafts:read", "drafts:write", "drafts:export")
	d := create(t, s, a, "Review")
	if _, err := s.Submit(ctx, reader, d.Card.ID, 1, rights(true)); !errors.Is(err, ErrForbidden) {
		t.Fatal("token publication", err)
	}
	if _, err := s.Decide(ctx, a, d.Card.ID, 1, "approve", ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("author approval", err)
	}
	a.User.Role = "admin"
	if _, err := s.Queue(ctx, a); !errors.Is(err, ErrForbidden) {
		t.Fatal("forged moderator", err)
	}
	if _, err := s.Submit(ctx, a, d.Card.ID, 1, Rights{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing rights", err)
	}
	bad := rights(true)
	bad.SourceURL = "javascript:alert(1)"
	if _, err := s.Submit(ctx, a, d.Card.ID, 1, bad); !errors.Is(err, ErrInvalid) {
		t.Fatal("unsafe source", err)
	}
	card, err := s.Submit(ctx, a, d.Card.ID, 1, rights(true))
	if err != nil || card.Status != "pending" {
		t.Fatal(card, err)
	}
	queue, err := s.Queue(ctx, m)
	if err != nil || len(queue) != 1 || queue[0].Revision != 1 {
		t.Fatal(queue, err)
	}
	if _, err = s.Get(ctx, m, d.Card.ID, 1); err != nil {
		t.Fatal("moderator preview", err)
	}
	if _, err = s.Decide(ctx, m, d.Card.ID, 1, "reject", ""); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty rejection reason", err)
	}
	card, err = s.Decide(ctx, m, d.Card.ID, 1, "reject", "Missing rights evidence")
	if err != nil || card.Status != "rejected" {
		t.Fatal(card, err)
	}
	if _, err = s.Decide(ctx, m, d.Card.ID, 1, "approve", ""); !errors.Is(err, ErrConflict) {
		t.Fatal("stale review accepted", err)
	}
	publish(t, s, a, m, d, true)
	if _, err = s.Submit(ctx, a, d.Card.ID, 1, rights(false)); !errors.Is(err, ErrConflict) {
		t.Fatal("published rights mutated", err)
	}
	card, err = s.Decide(ctx, m, d.Card.ID, 1, "block", "Verified infringement")
	if err != nil || card.Status != "blocked" {
		t.Fatal(card, err)
	}
	for _, fn := range []func() (Draft, error){func() (Draft, error) { return s.ForPlay(ctx, a, d.Card.ID, 1) }, func() (Draft, error) { return s.ForDownload(ctx, a, d.Card.ID, 1) }, func() (Draft, error) { return s.Pinned(ctx, d.Card.ID, 1) }} {
		if _, err := fn(); !errors.Is(err, ErrBlocked) {
			t.Fatal("blocked content accessible", err)
		}
	}
	if _, err = s.Submit(ctx, a, d.Card.ID, 1, rights(true)); !errors.Is(err, ErrBlocked) {
		t.Fatal("blocked resubmission", err)
	}
	if _, err = s.Public(ctx, d.Card.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("blocked catalogue", err)
	}
	var count int
	if err = p.QueryRow(ctx, "SELECT count(*) FROM library_decisions WHERE pack_id=$1", d.Card.ID).Scan(&count); err != nil || count != 3 {
		t.Fatal("missing audit", count, err)
	}
}

// Missing validation gates would turn a recoverable but unplayable draft into a publication or export.
func TestValidationGatesAndExportScope(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	invalid := document("Broken")
	invalid.Rounds[0].Themes[0].Questions[0].Right = nil
	d, err := s.Create(ctx, a, Save{Document: invalid})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(ctx, a, d.Card.ID, 1, rights(true)); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid published", err)
	}
	if _, err = s.ForPlay(ctx, a, d.Card.ID, 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid played", err)
	}
	if _, err = s.ForDownload(ctx, a, d.Card.ID, 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid exported", err)
	}
	valid := create(t, s, a, "Valid")
	read := credential(t, p, "owner", "api", "drafts:read")
	export := credential(t, p, "owner", "api", "drafts:export")
	if _, err = s.ForDownload(ctx, read, valid.Card.ID, 0); !errors.Is(err, ErrForbidden) {
		t.Fatal("read exported", err)
	}
	if _, err = s.ForDownload(ctx, export, valid.Card.ID, 0); err != nil {
		t.Fatal("export scope rejected", err)
	}
}

// Public filtering must use the approved revision only and serialization must contain no private answers or assets.
func TestCatalogueFiltersAndPrivacy(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	m := identity(t, p, "moderator", "moderator", "session")
	d := create(t, s, a, "Astronomy")
	publish(t, s, a, m, d, true)
	other := document("History")
	other.Language = "en"
	other.Difficulty = 7
	other.Tags = []string{"past"}
	second, err := s.Create(ctx, a, Save{Document: other, Assets: []Asset{{Name: "Images/a.png", Key: strings.Repeat("a", 64), Size: 10, ContentType: "image/png"}}})
	if err != nil {
		t.Fatal(err)
	}
	publish(t, s, a, m, second, true)
	create(t, s, a, "Unpublished")
	no, yes := false, true
	three := 3
	for _, tc := range []struct {
		filter Filter
		name   string
		count  int
	}{
		{Filter{}, "", 2}, {Filter{Query: "astrON"}, "Astronomy", 1}, {Filter{Language: "en"}, "History", 1}, {Filter{Tag: "science"}, "Astronomy", 1}, {Filter{Difficulty: &three}, "Astronomy", 1}, {Filter{HasMedia: &no}, "Astronomy", 1}, {Filter{HasMedia: &yes}, "History", 1}, {Filter{MaxBytes: 1}, "", 0}, {Filter{Offset: 1, Limit: 1}, "", 1},
	} {
		cards, err := s.Catalogue(ctx, tc.filter)
		if err != nil || len(cards) != tc.count {
			t.Fatal(tc, cards, err)
		}
		if tc.name != "" && cards[0].Name != tc.name {
			t.Fatal(tc, cards)
		}
	}
	cards, err := s.Catalogue(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cards)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret answer", "original_key", "assets", "document", "questions"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("private field %q leaked: %s", secret, data)
		}
	}
	for _, f := range []Filter{{Offset: -1}, {Offset: 100001}, {Limit: -1}, {MaxBytes: -1}} {
		if _, err := s.Catalogue(ctx, f); !errors.Is(err, ErrInvalid) {
			t.Fatal("bad filter", f, err)
		}
	}
}

// Reports require a public pinned revision, record the reporter and resolve when moderators decide.
func TestReportsDeletionAndDeletedOwners(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	m := identity(t, p, "moderator", "moderator", "session")
	b := identity(t, p, "other", "author", "session")
	d := create(t, s, a, "Reportable")
	publish(t, s, a, m, d, true)
	if _, err := s.Report(ctx, accounts.Identity{}, d.Card.ID, 1, "Reason"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("anonymous report", err)
	}
	if _, err := s.Report(ctx, b, d.Card.ID, 1, " "); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty report", err)
	}
	report, err := s.Report(ctx, b, d.Card.ID, 1, "Rights concern")
	if err != nil || report.ReporterID != "other" || report.Status != "open" || report.Revision != 1 {
		t.Fatal(report, err)
	}
	if _, err = s.Report(ctx, b, d.Card.ID, 1, "Again"); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate report", err)
	}
	if _, err = s.Reports(ctx, b); !errors.Is(err, ErrForbidden) {
		t.Fatal("author report list", err)
	}
	reports, err := s.Reports(ctx, m)
	if err != nil || len(reports) != 1 {
		t.Fatal(reports, err)
	}
	if err = s.Delete(ctx, a, d.Card.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pinned(ctx, d.Card.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted pinned", err)
	}
	if _, err = s.Public(ctx, d.Card.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted public", err)
	}
	d = create(t, s, a, "Account deleted")
	publish(t, s, a, m, d, true)
	if _, err = p.Exec(ctx, "UPDATE account_users SET deleted_at=now(),username=NULL,password_hash=NULL WHERE id=$1", a.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Public(ctx, d.Card.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted owner public", err)
	}
	if _, err = s.ForDownload(ctx, b, d.Card.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted owner download", err)
	}
	if _, err = s.Create(ctx, a, Save{Document: document("Deleted")}); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("deleted owner wrote", err)
	}
}

// Trusted importer warnings survive an edit independently of diagnostics derived from the old content.
func TestImporterIdempotencyAndPersistentIssues(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	in := Save{ImportID: strings.Repeat("a", 32), Document: document("Imported"), Issues: []packs.Issue{{Severity: "warning", Code: "media_pending", Path: "assets", Message: "Media requires review"}}}
	d, err := s.Create(ctx, a, in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Create(ctx, a, in)
	if err != nil || again.Card.ID != d.Card.ID {
		t.Fatal("duplicate import", again, err)
	}
	mine, err := s.Mine(ctx, a)
	if err != nil || len(mine) != 1 {
		t.Fatal(mine, err)
	}
	if len(d.AssetIssues) != 1 || d.AssetIssues[0].Code != "media_pending" || len(d.Issues) != 1 {
		t.Fatal("persistent diagnostics", d)
	}
}

// Head revision status cannot describe a prior publication or pending review;
// live pointers must survive editing and disappear after withdrawal.
func TestCardsTrackPublishedAndPendingRevisionsAcrossEdits(t *testing.T) {
	s, p := setup(t)
	owner := identity(t, p, "owner", "author", "session")
	mod := identity(t, p, "moderator", "moderator", "session")
	first := create(t, s, owner, "First")
	assertPointers := func(card Card, published, pending int) {
		t.Helper()
		if card.PublishedRevision != published || card.PendingRevision != pending {
			t.Fatalf("revision %d pointers published=%d pending=%d, want %d/%d", card.Revision, card.PublishedRevision, card.PendingRevision, published, pending)
		}
	}
	assertPointers(first.Card, 0, 0)
	pending, err := s.Submit(ctx, owner, first.Card.ID, 1, rights(true))
	if err != nil {
		t.Fatal(err)
	}
	assertPointers(pending, 0, 1)
	second, err := s.UpdateDocument(ctx, owner, first.Card.ID, 1, document("Second"))
	if err != nil {
		t.Fatal(err)
	}
	assertPointers(second.Card, 0, 1)
	approved, err := s.Decide(ctx, mod, first.Card.ID, 1, "approve", "")
	if err != nil {
		t.Fatal(err)
	}
	assertPointers(approved, 1, 0)
	third, err := s.UpdateDocument(ctx, owner, first.Card.ID, 2, document("Third"))
	if err != nil {
		t.Fatal(err)
	}
	assertPointers(third.Card, 1, 0)
	pending, err = s.Submit(ctx, owner, first.Card.ID, 2, rights(true))
	if err != nil {
		t.Fatal(err)
	}
	assertPointers(pending, 1, 2)
	fourth, err := s.UpdateDocument(ctx, owner, first.Card.ID, 3, document("Fourth"))
	if err != nil {
		t.Fatal(err)
	}
	assertPointers(fourth.Card, 1, 2)
	mine, err := s.Mine(ctx, owner)
	if err != nil || len(mine) != 1 {
		t.Fatal(mine, err)
	}
	assertPointers(mine[0], 1, 2)
	public, err := s.Public(ctx, first.Card.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertPointers(public, 1, 2)
	queue, err := s.Queue(ctx, mod)
	if err != nil || len(queue) != 1 {
		t.Fatal(queue, err)
	}
	assertPointers(queue[0], 1, 2)
	data, err := json.Marshal(fourth.Card)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"published_revision":1`) || !strings.Contains(string(data), `"pending_revision":2`) {
		t.Fatal("missing JSON publication pointers", string(data))
	}
	if err = s.Withdraw(ctx, owner, first.Card.ID); err != nil {
		t.Fatal(err)
	}
	current, err := s.Get(ctx, owner, first.Card.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	assertPointers(current.Card, 0, 0)
	history, err := s.History(ctx, owner, first.Card.ID)
	if err != nil || len(history) != 4 {
		t.Fatal(history, err)
	}
	for _, card := range history {
		assertPointers(card, 0, 0)
	}
}

// Private file access needs scopes and live block checks even when an imported
// draft is unplayable; editor diagnostics remain readable after blocking.
func TestForFileScopesDiagnosticsAndBlocking(t *testing.T) {
	s, p := setup(t)
	owner := identity(t, p, "owner", "author", "session")
	mod := identity(t, p, "moderator", "moderator", "session")
	other := identity(t, p, "other", "author", "session")
	read := credential(t, p, "owner", "api", "drafts:read")
	export := credential(t, p, "owner", "api", "drafts:export")
	write := credential(t, p, "owner", "api", "drafts:write")
	revoked := credential(t, p, "owner", "api", "drafts:read", "drafts:export")
	asset := Asset{Name: "Images/a.png", Key: strings.Repeat("a", 64), ContentType: "image/png", Size: 10}
	first, err := s.Create(ctx, owner, Save{Document: document("File source"), Assets: []Asset{asset}, OriginalKey: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	publish(t, s, owner, mod, first, true)
	broken := document("Diagnostic draft")
	broken.Rounds[0].Themes[0].Questions[0].Right = nil
	current, err := s.UpdateDocument(ctx, owner, first.Card.ID, 1, broken)
	if err != nil || packs.Playable(current.Issues) {
		t.Fatal(current, err)
	}
	for _, actor := range []accounts.Identity{owner, mod} {
		for _, original := range []bool{false, true} {
			file, err := s.ForFile(ctx, actor, first.Card.ID, 0, original)
			if err != nil || file.Card.Revision != 2 || file.OriginalKey != strings.Repeat("b", 64) || len(file.Assets) != 1 {
				t.Fatal("diagnostic files unavailable", actor.User.ID, original, file, err)
			}
		}
	}
	if _, err = s.ForFile(ctx, read, first.Card.ID, 2, false); err != nil {
		t.Fatal("asset read scope", err)
	}
	if _, err = s.ForFile(ctx, export, first.Card.ID, 2, true); err != nil {
		t.Fatal("original export scope", err)
	}
	for _, tc := range []struct {
		actor    accounts.Identity
		original bool
	}{{read, true}, {export, false}, {write, false}, {write, true}} {
		if _, err := s.ForFile(ctx, tc.actor, first.Card.ID, 2, tc.original); !errors.Is(err, ErrForbidden) {
			t.Fatal("wrong file scope accepted", tc.original, err)
		}
	}
	for _, original := range []bool{false, true} {
		if _, err = s.ForFile(ctx, other, first.Card.ID, 2, original); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign file access", err)
		}
		if _, err = s.ForFile(ctx, accounts.Identity{}, first.Card.ID, 2, original); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("anonymous file access", err)
		}
	}
	if _, err = p.Exec(ctx, "UPDATE account_credentials SET revoked_at=now() WHERE id=$1", revoked.SessionID); err != nil {
		t.Fatal(err)
	}
	for _, original := range []bool{false, true} {
		if _, err = s.ForFile(ctx, revoked, first.Card.ID, 2, original); !errors.Is(err, ErrUnauthorized) {
			t.Fatal("revoked file access", original, err)
		}
	}
	if _, err = s.Decide(ctx, mod, first.Card.ID, 1, "block", "Verified issue"); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []accounts.Identity{owner, mod} {
		for _, original := range []bool{false, true} {
			if _, err = s.ForFile(ctx, actor, first.Card.ID, 2, original); !errors.Is(err, ErrBlocked) {
				t.Fatal("blocked file access", actor.User.ID, original, err)
			}
		}
	}
	if _, err = s.ForFile(ctx, read, first.Card.ID, 2, false); !errors.Is(err, ErrBlocked) {
		t.Fatal("blocked asset scope", err)
	}
	if _, err = s.ForFile(ctx, export, first.Card.ID, 2, true); !errors.Is(err, ErrBlocked) {
		t.Fatal("blocked original scope", err)
	}
	for _, actor := range []accounts.Identity{owner, mod} {
		if _, err = s.Get(ctx, actor, first.Card.ID, 0); err != nil {
			t.Fatal("blocked editor diagnostics inaccessible", actor.User.ID, err)
		}
	}
}

// Engine limitations must prevent room creation without prohibiting otherwise
// valid SIQ publication, download, or inspecting an already pinned revision.
func TestPlayCompatibilityIsSeparateFromSIQValidity(t *testing.T) {
	s, p := setup(t)
	owner := identity(t, p, "owner", "author", "session")
	mod := identity(t, p, "moderator", "moderator", "session")
	other := identity(t, p, "other", "author", "session")
	zero := document("Zero price")
	zero.Rounds[0].Themes[0].Questions[0].Price = 0
	first, err := s.Create(ctx, owner, Save{Document: zero})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.PlayIssues) == 0 || packs.Playable(first.PlayIssues) || !packs.Playable(first.Issues) {
		t.Fatal("play and SIQ diagnostics mixed or absent", first.PlayIssues, first.Issues)
	}
	if _, err = s.ForPlay(ctx, owner, first.Card.ID, 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("unsupported owner room allowed", err)
	}
	if _, err = s.ForDownload(ctx, owner, first.Card.ID, 1); err != nil {
		t.Fatal("valid SIQ export blocked", err)
	}
	publish(t, s, owner, mod, first, true)
	if _, err = s.ForPlay(ctx, other, first.Card.ID, 0); !errors.Is(err, ErrInvalid) {
		t.Fatal("unsupported public room allowed", err)
	}
	download, err := s.ForDownload(ctx, accounts.Identity{}, first.Card.ID, 0)
	if err != nil || !packs.Playable(download.Issues) {
		t.Fatal("valid public SIQ export blocked", download, err)
	}
	pinned, err := s.Pinned(ctx, first.Card.ID, 1)
	if err != nil || len(pinned.PlayIssues) == 0 {
		t.Fatal("pinned inspection lost compatibility diagnostics", pinned, err)
	}
	loaded, err := s.Get(ctx, owner, first.Card.ID, 1)
	if err != nil || len(loaded.PlayIssues) == 0 {
		t.Fatal("stored revision lost derived play diagnostics", loaded, err)
	}
	data, err := json.Marshal(loaded)
	if err != nil || !strings.Contains(string(data), `"play_issues"`) {
		t.Fatal("missing play diagnostics JSON", string(data), err)
	}
	fixed, err := s.UpdateDocument(ctx, owner, first.Card.ID, 1, document("Supported"))
	if err != nil || len(fixed.PlayIssues) != 0 {
		t.Fatal("play diagnostics not recomputed", fixed, err)
	}
	if _, err = s.ForPlay(ctx, owner, first.Card.ID, 2); err != nil {
		t.Fatal("supported room rejected", err)
	}
}
