package themes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"siplatform/internal/accounts"
	"siplatform/internal/library"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
)

var serviceContext = context.Background()

func serviceFixture(t *testing.T) (*Service, *pgxpool.Pool, accounts.Identity) {
	t.Helper()
	p := testutil.Pool(t)
	if err := postgres.Migrate(serviceContext, p); err != nil {
		t.Fatal(err)
	}
	return New(p), p, serviceActor(t, p, "owner", "author", "session")
}
func serviceActor(t *testing.T, p *pgxpool.Pool, id, role, kind string) accounts.Identity {
	t.Helper()
	if _, err := p.Exec(serviceContext, `INSERT INTO account_users(id,username,display_name,password_hash,role) VALUES($1,$1,$1,'unused',$2) ON CONFLICT DO NOTHING`, id, role); err != nil {
		t.Fatal(err)
	}
	credential := id + "-" + kind
	hash := sha256.Sum256([]byte(credential))
	scopes := []string{}
	if kind == "api" {
		scopes = []string{"drafts:read", "drafts:write", "drafts:export"}
	}
	if _, err := p.Exec(serviceContext, `INSERT INTO account_credentials(id,user_id,token_hash,kind,scopes,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '1 day')`, credential, id, hex.EncodeToString(hash[:]), kind, scopes); err != nil {
		t.Fatal(err)
	}
	return accounts.Identity{User: accounts.User{ID: id, Role: role, DisplayName: id}, SessionID: credential, APIToken: kind == "api", Scopes: scopes}
}
func servicePrepared(t *testing.T, name string) Prepared {
	t.Helper()
	m := Manifest{SchemaVersion: 1, Name: name, Author: "Author", Provenance: "Original", Font: "sans", Palette: Palette{Background: "#FFFFFF", Surface: "#FFFFFF", SurfaceAlt: "#FFFFFF", Border: "#000000", Text: "#000000", Muted: "#000000", Accent: "#000000", AccentText: "#FFFFFF", Danger: "#000000", Success: "#000000", Focus: "#000000"}, Cards: Shape{Radius: 8, BorderWidth: 1}, Buttons: Shape{Radius: 8, BorderWidth: 1}}
	assets := []Asset{}
	raw, err := json.Marshal(struct {
		Manifest Manifest `json:"manifest"`
		Assets   []Asset  `json:"assets"`
	}{m, assets})
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	return Prepared{Manifest: m, Assets: assets, Hash: hex.EncodeToString(hash[:]), OriginalKey: strings.Repeat("a", 64), OriginalSize: 100, SizeBytes: 100}
}
func serviceUpload(t *testing.T, p *pgxpool.Pool, a accounts.Identity, n int, prepared Prepared) string {
	t.Helper()
	id := fmt.Sprintf("%032x", n)
	raw, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(serviceContext, `INSERT INTO uploads(id,owner_id,credential_id,name,size,offset_bytes,status,purpose,result_theme) VALUES($1,$2,$3,'theme.zip',$4,$4,'ready','theme',$5)`, id, a.User.ID, a.SessionID, prepared.OriginalSize, raw); err != nil {
		t.Fatal(err)
	}
	return id
}

// Receipt replay returns the originally attached version even after later edits;
// a global upload receipt cannot be reused for a different target.
func TestThemeAttachImmutableGlobalReceiptAndConflict(t *testing.T) {
	s, p, a := serviceFixture(t)
	u1 := serviceUpload(t, p, a, 1, servicePrepared(t, "First"))
	v1, err := s.Attach(serviceContext, a, "", 0, u1)
	if err != nil {
		t.Fatal(err)
	}
	if v1.Card.Revision != 1 || v1.Card.OwnerID != a.User.ID || v1.Card.Name != "First" {
		t.Fatal(v1)
	}
	u2 := serviceUpload(t, p, a, 2, servicePrepared(t, "Second"))
	v2, err := s.Attach(serviceContext, a, v1.Card.ID, 1, u2)
	if err != nil || v2.Card.Revision != 2 {
		t.Fatal(v2, err)
	}
	for _, target := range []string{"", v1.Card.ID} {
		replayed, err := New(p).Attach(serviceContext, a, target, 0, u1)
		if err != nil || replayed.Card.Revision != 1 || replayed.Prepared.Hash != v1.Prepared.Hash {
			t.Fatal("receipt changed original version", replayed, err)
		}
	}
	u3 := serviceUpload(t, p, a, 3, servicePrepared(t, "Other"))
	other, err := s.Attach(serviceContext, a, "", 0, u3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Attach(serviceContext, a, other.Card.ID, 1, u1); !errors.Is(err, ErrConflict) {
		t.Fatal("receipt retargeted", err)
	}
	u4 := serviceUpload(t, p, a, 4, servicePrepared(t, "Conflict"))
	if _, err = s.Attach(serviceContext, a, v1.Card.ID, 1, u4); !errors.Is(err, ErrConflict) {
		t.Fatal("stale base accepted", err)
	}
	current, err := s.Get(serviceContext, a, v1.Card.ID, 0)
	if err != nil || current.Card.Revision != 2 || current.Card.Name != "Second" {
		t.Fatal(current, err)
	}
	old, err := s.Get(serviceContext, a, v1.Card.ID, 1)
	if err != nil || old.Card.Name != "First" {
		t.Fatal("history mutated", old, err)
	}
}

func TestThemeServiceRequiresLiveOwnerSessionAndExactReadyResult(t *testing.T) {
	s, p, a := serviceFixture(t)
	api := serviceActor(t, p, "owner", "author", "api")
	api.APIToken = false // Caller-supplied credential kind cannot override the database.
	other := serviceActor(t, p, "other", "author", "session")
	u := serviceUpload(t, p, a, 1, servicePrepared(t, "Private"))
	if _, err := s.Attach(serviceContext, api, "", 0, u); !errors.Is(err, ErrForbidden) {
		t.Fatal("API attached", err)
	}
	if _, err := s.Attach(serviceContext, other, "", 0, u); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign upload attached", err)
	}
	v, err := s.Attach(serviceContext, a, "", 0, u)
	if err != nil {
		t.Fatal(err)
	}
	for name, fn := range map[string]func() error{
		"list":   func() error { _, e := s.List(serviceContext, api); return e },
		"get":    func() error { _, e := s.Get(serviceContext, api, v.Card.ID, 1); return e },
		"delete": func() error { return s.Delete(serviceContext, api, v.Card.ID) },
	} {
		if err := fn(); !errors.Is(err, ErrForbidden) {
			t.Fatal(name, err)
		}
	}
	if _, err = s.Get(serviceContext, other, v.Card.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign theme read", err)
	}
	bad := servicePrepared(t, "Forged")
	bad.Hash = strings.Repeat("f", 64)
	badID := serviceUpload(t, p, a, 2, bad)
	if _, err = s.Attach(serviceContext, a, "", 0, badID); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid persisted result attached", err)
	}
	if _, err = p.Exec(serviceContext, "UPDATE account_credentials SET revoked_at=now() WHERE id=$1", a.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Attach(serviceContext, a, "", 0, u); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoked replay", err)
	}
	if _, err = s.List(serviceContext, a); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoked read", err)
	}
}

func TestThemeDeleteKeepsPinnedVersionAndBlockRevokesWithAudit(t *testing.T) {
	s, p, a := serviceFixture(t)
	moderator := serviceActor(t, p, "moderator", "moderator", "session")
	u := serviceUpload(t, p, a, 1, servicePrepared(t, "Retained"))
	v, err := s.Attach(serviceContext, a, "", 0, u)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Block(serviceContext, a, v.Card.ID, true, "No permission"); !errors.Is(err, ErrForbidden) {
		t.Fatal("author blocked", err)
	}
	if err = s.Block(serviceContext, moderator, v.Card.ID, true, ""); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing reason", err)
	}
	if err = s.Block(serviceContext, moderator, v.Card.ID, true, "Verified infringement"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pinned(serviceContext, v.Card.ID, 1, v.Prepared.Hash); !errors.Is(err, ErrBlocked) {
		t.Fatal("blocked pin served", err)
	}
	if got, err := s.Get(serviceContext, a, v.Card.ID, 1); err != nil || !got.Card.Blocked {
		t.Fatal("owner inspection lost", got, err)
	}
	if err = s.Block(serviceContext, moderator, v.Card.ID, false, "Resolved"); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(serviceContext, a, v.Card.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(serviceContext, a, v.Card.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted owner view", err)
	}
	if list, err := s.List(serviceContext, a); err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	if _, err = s.Pinned(serviceContext, v.Card.ID, 1, v.Prepared.Hash); err != nil {
		t.Fatal("ordinary deletion broke existing pin", err)
	}
	if _, err = s.Pinned(serviceContext, v.Card.ID, 1, strings.Repeat("f", 64)); !errors.Is(err, ErrNotFound) {
		t.Fatal("wrong pin hash", err)
	}
	if err = s.Block(serviceContext, moderator, v.Card.ID, true, "Revoked after ordinary deletion"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pinned(serviceContext, v.Card.ID, 1, v.Prepared.Hash); !errors.Is(err, ErrBlocked) {
		t.Fatal("deleted blocked pin served", err)
	}
	var audits int
	if err = p.QueryRow(serviceContext, "SELECT count(*) FROM theme_moderation WHERE theme_id=$1 AND actor_id=$2", v.Card.ID, moderator.User.ID).Scan(&audits); err != nil || audits != 3 {
		t.Fatal(audits, err)
	}
	if _, err = p.Exec(serviceContext, "UPDATE account_users SET deleted_at=now(),username=NULL,password_hash=NULL WHERE id=$1", a.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Pinned(serviceContext, v.Card.ID, 1, v.Prepared.Hash); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted account pin served", err)
	}
}

func TestThemeConcurrentAttachHasSingleReceiptAndRevision(t *testing.T) {
	s, p, a := serviceFixture(t)
	u := serviceUpload(t, p, a, 1, servicePrepared(t, "Concurrent"))
	results := make(chan Version, 2)
	errs := make(chan error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			v, err := s.Attach(serviceContext, a, "", 0, u)
			results <- v
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(results)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id string
	for v := range results {
		if v.Card.Revision != 1 || id != "" && id != v.Card.ID {
			t.Fatal(v)
		}
		id = v.Card.ID
	}
	var count int
	if err := p.QueryRow(serviceContext, "SELECT count(*) FROM theme_versions").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestThemeQuotaCountsReadyAndRetainedVersionsWithoutDoubleCharge(t *testing.T) {
	s, p, a := serviceFixture(t)
	u := serviceUpload(t, p, a, 1, servicePrepared(t, "One"))
	usage := func() int64 {
		t.Helper()
		tx, err := p.Begin(serviceContext)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(serviceContext)
		if _, err = tx.Exec(serviceContext, "SELECT id FROM account_users WHERE id=$1 FOR UPDATE", a.User.ID); err != nil {
			t.Fatal(err)
		}
		n, err := library.StorageUsage(serviceContext, tx, a.User.ID)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := usage(); n != 100 {
		t.Fatal("ready theme omitted", n)
	}
	v, err := s.Attach(serviceContext, a, "", 0, u)
	if err != nil {
		t.Fatal(err)
	}
	if n := usage(); n != 100 {
		t.Fatal("attachment charged twice", n)
	}
	if _, err = s.Attach(serviceContext, a, "", 0, u); err != nil {
		t.Fatal(err)
	}
	if n := usage(); n != 100 {
		t.Fatal("replay changed usage", n)
	}
	if err = s.Delete(serviceContext, a, v.Card.ID); err != nil {
		t.Fatal(err)
	}
	if n := usage(); n != 100 {
		t.Fatal("retained deleted version released quota", n)
	}
}

func TestThemeCountAndVersionLimits(t *testing.T) {
	for _, mode := range []string{"themes", "versions"} {
		t.Run(mode, func(t *testing.T) {
			s, p, a := serviceFixture(t)
			prepared := servicePrepared(t, "Limit")
			u := serviceUpload(t, p, a, 1, prepared)
			id := ""
			base := 0
			if mode == "themes" {
				if _, err := p.Exec(serviceContext, `INSERT INTO themes(id,owner_id,current_revision,deleted_at) SELECT lpad(to_hex(n),32,'0'),$1,1,now() FROM generate_series(1,100) n`, a.User.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				id = strings.Repeat("f", 32)
				base = 100
				raw, err := json.Marshal(prepared)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = p.Exec(serviceContext, `INSERT INTO themes(id,owner_id,current_revision) VALUES($1,$2,100)`, id, a.User.ID); err != nil {
					t.Fatal(err)
				}
				if _, err = p.Exec(serviceContext, `INSERT INTO theme_versions(theme_id,revision,prepared,size_bytes) SELECT $1,n,$2,100 FROM generate_series(1,100) n`, id, raw); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Attach(serviceContext, a, id, base, u); !errors.Is(err, ErrQuota) {
				t.Fatal("retained limit exceeded", err)
			}
		})
	}
}

func TestThemeConcurrentVersionUpdatesCannotOverwrite(t *testing.T) {
	s, p, a := serviceFixture(t)
	u := serviceUpload(t, p, a, 1, servicePrepared(t, "Initial"))
	v, err := s.Attach(serviceContext, a, "", 0, u)
	if err != nil {
		t.Fatal(err)
	}
	uploads := []string{serviceUpload(t, p, a, 2, servicePrepared(t, "A")), serviceUpload(t, p, a, 3, servicePrepared(t, "B"))}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, upload := range uploads {
		wg.Add(1)
		go func(upload string) {
			defer wg.Done()
			<-start
			_, e := s.Attach(serviceContext, a, v.Card.ID, 1, upload)
			errs <- e
		}(upload)
	}
	close(start)
	wg.Wait()
	close(errs)
	good, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			good++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if good != 1 || conflicts != 1 {
		t.Fatal(good, conflicts)
	}
	var count int
	if err = p.QueryRow(serviceContext, "SELECT count(*) FROM theme_versions WHERE theme_id=$1", v.Card.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}

func TestThemeAttachTransfersQuotaAtExactLimit(t *testing.T) {
	s, p, a := serviceFixture(t)
	u1 := serviceUpload(t, p, a, 1, servicePrepared(t, "Retained"))
	v, err := s.Attach(serviceContext, a, "", 0, u1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(serviceContext, "UPDATE theme_versions SET size_bytes=$2 WHERE theme_id=$1", v.Card.ID, int64(10<<30-100)); err != nil {
		t.Fatal(err)
	}
	u2 := serviceUpload(t, p, a, 2, servicePrepared(t, "Exact limit"))
	if _, err = s.Attach(serviceContext, a, v.Card.ID, 1, u2); err != nil {
		t.Fatal("ready charge counted twice", err)
	}
	u3 := serviceUpload(t, p, a, 3, servicePrepared(t, "Too much"))
	if _, err = s.Attach(serviceContext, a, v.Card.ID, 2, u3); !errors.Is(err, ErrQuota) {
		t.Fatal("shared quota exceeded", err)
	}
}

func TestThemeModerationUsesLiveRoleAndCredential(t *testing.T) {
	s, p, a := serviceFixture(t)
	u := serviceUpload(t, p, a, 1, servicePrepared(t, "Moderation"))
	v, err := s.Attach(serviceContext, a, "", 0, u)
	if err != nil {
		t.Fatal(err)
	}
	moderator := serviceActor(t, p, "moderator", "moderator", "session")
	api := serviceActor(t, p, "moderator", "moderator", "api")
	if err = s.Block(serviceContext, api, v.Card.ID, true, "API denied"); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	a.User.Role = "admin"
	if err = s.Block(serviceContext, a, v.Card.ID, true, "Forged role"); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	if _, err = p.Exec(serviceContext, "UPDATE account_credentials SET expires_at=now()-interval '1 second' WHERE id=$1", moderator.SessionID); err != nil {
		t.Fatal(err)
	}
	if err = s.Block(serviceContext, moderator, v.Card.ID, true, "Expired moderator"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	var audits int
	if err = p.QueryRow(serviceContext, "SELECT count(*) FROM theme_moderation").Scan(&audits); err != nil || audits != 0 {
		t.Fatal("unauthorized moderation audited as applied", audits, err)
	}
}
