package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"siplatform/internal/accounts"
	"siplatform/internal/packs"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
)

var ctx = context.Background()

func setup(t *testing.T) (*Service, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.Pool(t)
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return New(pool), pool
}
func identity(t *testing.T, pool *pgxpool.Pool, id, role, kind string, scopes ...string) accounts.Identity {
	t.Helper()
	_, err := pool.Exec(ctx, "INSERT INTO account_users(id,username,display_name,password_hash,role) VALUES($1,$1,$1,'test',$2)", id, role)
	if err != nil {
		t.Fatal(err)
	}
	return credential(t, pool, id, kind, scopes...)
}
func credential(t *testing.T, pool *pgxpool.Pool, id, kind string, scopes ...string) accounts.Identity {
	t.Helper()
	credentialID := fmt.Sprintf("%s-%s-%s", id, kind, strings.Join(scopes, "-"))
	sum := sha256.Sum256([]byte(credentialID))
	if scopes == nil {
		scopes = []string{}
	}
	_, err := pool.Exec(ctx, `INSERT INTO account_credentials(id,user_id,token_hash,kind,scopes,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '1 day')`, credentialID, id, hex.EncodeToString(sum[:]), kind, scopes)
	if err != nil {
		t.Fatal(err)
	}
	var role string
	if err = pool.QueryRow(ctx, "SELECT role FROM account_users WHERE id=$1", id).Scan(&role); err != nil {
		t.Fatal(err)
	}
	return accounts.Identity{User: accounts.User{ID: id, DisplayName: id, Role: role}, SessionID: credentialID, APIToken: kind == "api", Scopes: scopes}
}
func document(name string) packs.Document {
	return packs.Document{ID: "doc", Name: name, Version: "5.0", Language: "ru", Difficulty: 3, Tags: []string{"science"}, Rounds: []packs.Round{{ID: "r1", Name: "Round", Themes: []packs.Theme{{ID: "t1", Name: "Theme", Questions: []packs.Question{{ID: "q1", Price: 100, Right: []string{"secret answer"}, Params: []packs.Parameter{{Name: "question", Type: "content", Items: []packs.Content{{Type: "text", Value: "Question"}}}}}}}}}}}
}
func create(t *testing.T, s *Service, a accounts.Identity, name string) Draft {
	t.Helper()
	d, err := s.Create(ctx, a, Save{Document: document(name)})
	if err != nil {
		t.Fatal("create:", err)
	}
	return d
}

// Missing persistence or authorization would lose revision content or expose another author's answers.
func TestDraftPersistenceOwnershipAndHistory(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	b := identity(t, p, "other", "author", "session")
	d := create(t, s, a, "First")
	if d.Card.Revision != 1 || d.Card.Status != "draft" || d.Card.QuestionCount != 1 || d.Card.RoundCount != 1 || d.Card.OwnerID != "owner" {
		t.Fatalf("bad card: %+v", d.Card)
	}
	if _, err := s.Get(ctx, b, d.Card.ID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign read", err)
	}
	if _, err := s.Update(ctx, b, d.Card.ID, 1, Save{Document: document("Stolen")}); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign write", err)
	}
	if err := s.Delete(ctx, b, d.Card.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign delete", err)
	}
	update, err := s.Update(ctx, a, d.Card.ID, 1, Save{Document: document("Second")})
	if err != nil || update.Card.Revision != 2 {
		t.Fatal(update, err)
	}
	first, err := New(p).Get(ctx, a, d.Card.ID, 1)
	if err != nil || first.Document.Name != "First" {
		t.Fatal("immutable original", first, err)
	}
	current, err := s.Get(ctx, a, d.Card.ID, 0)
	if err != nil || current.Document.Name != "Second" {
		t.Fatal(current, err)
	}
	history, err := s.History(ctx, a, d.Card.ID)
	if err != nil || len(history) != 2 || history[0].Revision != 2 {
		t.Fatal(history, err)
	}
	mine, err := s.Mine(ctx, a)
	if err != nil || len(mine) != 1 || mine[0].Revision != 2 {
		t.Fatal(mine, err)
	}
}

// Missing pack/user locks or a stale-base check would allow both writes to replace each other.
func TestConcurrentUpdateHasSingleWinner(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	d := create(t, s, a, "Before")
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"One", "Two"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			<-start
			_, err := s.Update(ctx, a, d.Card.ID, 1, Save{Document: document(name)})
			results <- err
		}(name)
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
	history, err := s.History(ctx, a, d.Card.ID)
	if err != nil || len(history) != 2 {
		t.Fatal("lost or duplicate revision", history, err)
	}
}

// Trusting stale caller identity would bypass revocation, expiry and scope changes.
func TestCredentialsRecheckedAndScoped(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	d := create(t, s, a, "Private")
	read := credential(t, p, "owner", "api", "drafts:read")
	write := credential(t, p, "owner", "api", "drafts:write")
	if _, err := s.Get(ctx, read, d.Card.ID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, read, Save{Document: document("No")}); !errors.Is(err, ErrForbidden) {
		t.Fatal("read wrote", err)
	}
	if _, err := s.Get(ctx, write, d.Card.ID, 0); !errors.Is(err, ErrForbidden) {
		t.Fatal("write read", err)
	}
	read.Scopes = []string{"drafts:write"}
	if _, err := s.Create(ctx, read, Save{Document: document("No")}); !errors.Is(err, ErrForbidden) {
		t.Fatal("forged scope", err)
	}
	if _, err := p.Exec(ctx, "UPDATE account_credentials SET expires_at=now()-interval '1 second' WHERE id=$1", a.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, a, d.Card.ID, 1, Save{Document: document("Expired")}); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired wrote", err)
	}
	if _, err := p.Exec(ctx, "UPDATE account_credentials SET revoked_at=now() WHERE id=$1", read.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, read, d.Card.ID, 0); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoked read", err)
	}
	if _, err := s.Create(ctx, accounts.Identity{}, Save{Document: document("No")}); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("anonymous create", err)
	}
}

// Invalid drafts must remain recoverable while malformed storage references must not be accepted.
func TestDraftDiagnosticsAndAssetValidation(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	d, err := s.Create(ctx, a, Save{Document: packs.Document{Name: "Recoverable", Version: "5.0"}})
	if err != nil || packs.Playable(d.Issues) {
		t.Fatal("invalid draft lost diagnostics", d, err)
	}
	key := strings.Repeat("a", 64)
	for _, assets := range [][]Asset{
		{{Name: "Images/a.png", Key: "url", Size: 1}},
		{{Name: "../a", Key: key, Size: 1}},
		{{Name: "Images/a.png", Key: key, Size: -1}},
		{{Name: "Images/a.png", Key: key, Size: 1}, {Name: "images/A.png", Key: key, Size: 1}},
		{{Name: "Images/caf\u00e9.png", Key: key, Size: 1}, {Name: "Images/cafe\u0301.png", Key: key, Size: 1}},
	} {
		if _, err := s.Create(ctx, a, Save{Document: document("Bad"), Assets: assets}); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid assets", assets, err)
		}
	}
	in := document("Missing media")
	in.Rounds[0].Themes[0].Questions[0].Params[0].Items = []packs.Content{{Type: "image", Value: "missing.png", IsRef: true}}
	d, err = s.Create(ctx, a, Save{Document: in})
	if err != nil || packs.Playable(d.Issues) {
		t.Fatal("missing media not diagnosed", d.Issues, err)
	}
}

// Requiring read scope to preserve server-owned fields breaks write-only tokens;
// trusting those fields from an editor lets it discard importer diagnostics.
func TestUpdateDocumentPreservesTrustedFieldsWithWriteOnlyScope(t *testing.T) {
	s, p := setup(t)
	owner := identity(t, p, "owner", "author", "session")
	other := identity(t, p, "other", "author", "session")
	write := credential(t, p, "owner", "api", "drafts:write")
	read := credential(t, p, "owner", "api", "drafts:read")
	original := document("Imported")
	original.ImportIssues = []packs.Issue{{Severity: "error", Code: "unknown_import_rule", Path: "package", Message: "Unsupported imported rule"}}
	original.Rounds[0].Themes[0].Questions[0].Right = nil
	asset := Asset{Name: "Images/a.png", Key: strings.Repeat("a", 64), ContentType: "image/png", Size: 10}
	assetIssue := packs.Issue{Severity: "warning", Code: "media_pending", Path: "assets", Message: "Media pending"}
	first, err := s.Create(ctx, owner, Save{Document: original, Assets: []Asset{asset}, OriginalKey: strings.Repeat("b", 64), Issues: []packs.Issue{assetIssue}})
	if err != nil {
		t.Fatal(err)
	}
	edited := document("Edited")
	edited.ImportIssues = []packs.Issue{{Severity: "warning", Code: "forged", Message: "Client supplied"}}
	second, err := s.UpdateDocument(ctx, write, first.Card.ID, 1, edited)
	if err != nil || second.Card.Revision != 2 || second.Document.Name != "Edited" {
		t.Fatal("write-only update", second, err)
	}
	if len(second.Assets) != 1 || second.Assets[0] != asset || second.OriginalKey != strings.Repeat("b", 64) {
		t.Fatal("trusted media changed", second)
	}
	if len(second.AssetIssues) != 1 || second.AssetIssues[0] != assetIssue || len(second.Document.ImportIssues) != 1 || second.Document.ImportIssues[0].Code != "unknown_import_rule" {
		t.Fatal("trusted diagnostics changed", second)
	}
	if len(second.Issues) != 2 || packs.Playable(second.Issues) {
		t.Fatal("derived diagnostics not recomputed or imported error lost", second.Issues)
	}
	if _, err = s.Get(ctx, write, first.Card.ID, 0); !errors.Is(err, ErrForbidden) {
		t.Fatal("write scope granted read", err)
	}
	if _, err = s.ForDownload(ctx, write, first.Card.ID, 0); !errors.Is(err, ErrForbidden) {
		t.Fatal("write scope granted export", err)
	}
	if _, err = s.UpdateDocument(ctx, read, first.Card.ID, 2, document("Read cannot write")); !errors.Is(err, ErrForbidden) {
		t.Fatal("read scope wrote", err)
	}
	if _, err = s.UpdateDocument(ctx, other, first.Card.ID, 2, document("Foreign")); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign update", err)
	}
	if _, err = s.UpdateDocument(ctx, write, first.Card.ID, 1, document("Stale")); !errors.Is(err, ErrConflict) {
		t.Fatal("stale update", err)
	}
	current, err := s.Get(ctx, owner, first.Card.ID, 0)
	if err != nil || current.Card.Revision != 2 || current.Document.Name != "Edited" {
		t.Fatal("failed update changed head", current, err)
	}
	prior, err := s.Get(ctx, owner, first.Card.ID, 1)
	if err != nil || prior.Document.Name != "Imported" || len(prior.Document.Rounds[0].Themes[0].Questions[0].Right) != 0 {
		t.Fatal("original revision changed", prior, err)
	}
}
