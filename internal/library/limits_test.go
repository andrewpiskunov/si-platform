package library

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"siplatform/internal/packs"
)

// Quotas apply to active packs and all retained revisions, including simultaneous creations.
func TestPackRevisionAndStorageQuotas(t *testing.T) {
	t.Run("packs", func(t *testing.T) {
		s, p := setup(t)
		a := identity(t, p, "owner", "author", "session")
		seed := create(t, s, a, "Seed")
		tx, err := p.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		_, err = tx.Exec(ctx, `INSERT INTO library_packs(id,owner_id,current_revision) SELECT 'seed-'||n,'owner',1 FROM generate_series(1,198) n`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO library_revisions(pack_id,revision,card,document,assets,issues,size_bytes) SELECT p.id,1,r.card,r.document,r.assets,r.issues,r.size_bytes FROM library_packs p CROSS JOIN library_revisions r WHERE p.id LIKE 'seed-%' AND r.pack_id=$1`, seed.Card.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func() { <-start; _, err := s.Create(ctx, a, Save{Document: document("Last")}); results <- err }()
		}
		close(start)
		successes, quota := 0, 0
		for i := 0; i < 2; i++ {
			err := <-results
			if err == nil {
				successes++
			} else if errors.Is(err, ErrQuota) {
				quota++
			} else {
				t.Fatal(err)
			}
		}
		if successes != 1 || quota != 1 {
			t.Fatalf("successes=%d quota=%d", successes, quota)
		}
		if err = s.Delete(ctx, a, seed.Card.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Create(ctx, a, Save{Document: document("After delete")}); err != nil {
			t.Fatal("deleted pack counted", err)
		}
	})
	t.Run("revisions", func(t *testing.T) {
		s, p := setup(t)
		a := identity(t, p, "owner", "author", "session")
		d := create(t, s, a, "Full history")
		_, err := p.Exec(ctx, `INSERT INTO library_revisions(pack_id,revision,card,document,assets,issues,size_bytes) SELECT r.pack_id,n,r.card,r.document,r.assets,r.issues,r.size_bytes FROM library_revisions r CROSS JOIN generate_series(2,500)n WHERE r.pack_id=$1`, d.Card.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = p.Exec(ctx, "UPDATE library_packs SET current_revision=500 WHERE id=$1", d.Card.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Update(ctx, a, d.Card.ID, 500, Save{Document: document("Too many")}); !errors.Is(err, ErrQuota) {
			t.Fatal("revision cap", err)
		}
	})
	t.Run("storage", func(t *testing.T) {
		s, p := setup(t)
		a := identity(t, p, "owner", "author", "session")
		in := Save{Document: document("Large"), Assets: []Asset{{Name: "Images/large.png", Key: strings.Repeat("a", 64), Size: 1 << 30}}}
		d, err := s.Create(ctx, a, in)
		if err != nil {
			t.Fatal(err)
		}
		for revision := 1; revision < 9; revision++ {
			d, err = s.Update(ctx, a, d.Card.ID, revision, in)
			if err != nil {
				t.Fatal(revision, err)
			}
		}
		if _, err = s.Update(ctx, a, d.Card.ID, 9, in); !errors.Is(err, ErrQuota) {
			t.Fatal("logical revision storage cap", err)
		}
		history, err := s.History(ctx, a, d.Card.ID)
		if err != nil || len(history) != 9 {
			t.Fatal("quota mutated history", history, err)
		}
	})
}

// Structural storage bounds reject hostile payloads before committing any revision.
func TestInputSizeBounds(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	huge := document("Huge")
	huge.Info.Comments = strings.Repeat("a", 10<<20)
	for name, in := range map[string]Save{
		"document":       {Document: huge},
		"asset count":    {Document: document("Many"), Assets: make([]Asset, 5001)},
		"asset bytes":    {Document: document("Big"), Assets: []Asset{{Name: "Images/a.png", Key: strings.Repeat("a", 64), Size: 1<<30 + 1}}},
		"asset overflow": {Document: document("Big"), Assets: []Asset{{Name: "Images/a.png", Key: strings.Repeat("a", 64), Size: 1 << 62}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Create(ctx, a, in); !errors.Is(err, ErrQuota) {
				t.Fatal(err)
			}
		})
	}
	mine, err := s.Mine(ctx, a)
	if err != nil || len(mine) != 0 {
		t.Fatal("rejected payload saved", mine, err)
	}
}

// Assets outside the exporter's bounded profile can be retained but must not become playable/public.
func TestOversizedExportAssetIsDiagnosed(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	d, err := s.Create(ctx, a, Save{Document: document("Recoverable"), Assets: []Asset{{Name: "Images/a.png", Key: strings.Repeat("a", 64), Size: 100<<20 + 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if packs.Playable(d.Issues) {
		t.Fatal("oversized exporter asset had no blocking diagnostic")
	}
	if _, err = s.ForDownload(ctx, a, d.Card.ID, 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid export", err)
	}
}

// Any failure after writing a revision or changing moderation state must roll back the entire transaction.
func TestFailedRevisionAndDecisionRollback(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	m := identity(t, p, "moderator", "moderator", "session")
	_, err := p.Exec(ctx, `CREATE FUNCTION fail_library_write() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'test storage failure'; END$$`)
	if err != nil {
		t.Fatal(err)
	}
	install := func(table string) {
		t.Helper()
		if _, err := p.Exec(ctx, fmt.Sprintf("CREATE TRIGGER fail_write BEFORE INSERT ON %s FOR EACH ROW EXECUTE FUNCTION fail_library_write()", table)); err != nil {
			t.Fatal(err)
		}
	}
	remove := func(table string) {
		t.Helper()
		if _, err := p.Exec(ctx, "DROP TRIGGER fail_write ON "+table); err != nil {
			t.Fatal(err)
		}
	}
	install("library_revisions")
	if _, err = s.Create(ctx, a, Save{Document: document("Fail")}); err == nil {
		t.Fatal("injected storage failure ignored")
	}
	var count int
	if err = p.QueryRow(ctx, "SELECT count(*) FROM library_packs").Scan(&count); err != nil || count != 0 {
		t.Fatal("orphan pack", count, err)
	}
	remove("library_revisions")
	d := create(t, s, a, "Retained")
	install("library_revisions")
	if _, err = s.Update(ctx, a, d.Card.ID, 1, Save{Document: document("Lost")}); err == nil {
		t.Fatal("failed update accepted")
	}
	remove("library_revisions")
	current, err := s.Get(ctx, a, d.Card.ID, 0)
	if err != nil || current.Card.Revision != 1 || current.Document.Name != "Retained" {
		t.Fatal("failed update advanced head", current, err)
	}
	if _, err = s.Submit(ctx, a, d.Card.ID, 1, rights(true)); err != nil {
		t.Fatal(err)
	}
	install("library_decisions")
	if _, err = s.Decide(ctx, m, d.Card.ID, 1, "approve", ""); err == nil {
		t.Fatal("audit failure ignored")
	}
	if _, err = s.Public(ctx, d.Card.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("failed approval public", err)
	}
	queue, err := s.Queue(ctx, m)
	if err != nil || len(queue) != 1 || queue[0].Status != "pending" {
		t.Fatal("failed approval lost queue", queue, err)
	}
}

// Revocation while a request waits for the user lock must defeat a previously authenticated identity.
func TestRevocationWinsOverWaitingUpdate(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	d := create(t, s, a, "Retained")
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT id FROM account_users WHERE id=$1 FOR UPDATE", a.User.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := s.Update(ctx, a, d.Card.ID, 1, Save{Document: document("Revoked")}); result <- err }()
	deadline := time.Now().Add(5 * time.Second)
	waiting := false
	for time.Now().Before(deadline) {
		var count int
		err = p.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'SELECT id,display_name,role FROM account_users%'`).Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			waiting = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("request did not wait on account lock")
	}
	if _, err = tx.Exec(ctx, "UPDATE account_credentials SET revoked_at=now() WHERE id=$1", a.SessionID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatal("revoked waiter wrote", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("update did not finish")
	}
}
