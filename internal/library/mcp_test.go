package library

import (
	"errors"
	"reflect"
	"siplatform/internal/packs"
	"strings"
	"sync"
	"testing"
)

func TestMCPCreateAndUpsertReceipts(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "api", "drafts:read", "drafts:write", "drafts:export")
	doc := document("MCP private")
	var wg sync.WaitGroup
	cards := make(chan Card, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, e := s.MCPCreate(ctx, a, "create-request-0001", doc)
			if e != nil {
				t.Error(e)
			}
			cards <- c
		}()
	}
	wg.Wait()
	close(cards)
	var first Card
	for c := range cards {
		if first.ID == "" {
			first = c
		}
		if c.ID != first.ID || c.Revision != 1 || c.Status != "draft" {
			t.Fatal(c)
		}
	}
	doc.Name = "changed"
	if _, e := s.MCPCreate(ctx, a, "create-request-0001", doc); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	d, e := s.MCPOwn(ctx, a, first.ID, 0, "drafts:read")
	if e != nil {
		t.Fatal(e)
	}
	q := d.Document.Rounds[0].Themes[0].Questions[0]
	q.Right = []string{"edited answer"}
	extra := q
	extra.ID = "q2"
	c, e := s.MCPUpsert(ctx, a, "upsert-request-001", first.ID, 1, "r1", "t1", []packs.Question{q, extra})
	if e != nil || c.Revision != 2 {
		t.Fatal(c, e)
	}
	repeated, e := s.MCPUpsert(ctx, a, "upsert-request-001", first.ID, 1, "r1", "t1", []packs.Question{q, extra})
	if e != nil || repeated.Revision != 2 {
		t.Fatal(repeated, e)
	}
	after, e := s.MCPOwn(ctx, a, first.ID, 0, "drafts:read")
	if e != nil {
		t.Fatal(e)
	}
	if len(after.Document.Rounds[0].Themes[0].Questions) != 2 || !reflect.DeepEqual(after.Document.Rounds[0].Themes[0].Questions[0].Right, q.Right) {
		t.Fatal(after.Document)
	}
	edited := after.Document
	edited.Name = "Browser edit"
	if _, e = s.UpdateDocument(ctx, a, first.ID, 2, edited); e != nil {
		t.Fatal(e)
	}
	repeated, e = s.MCPUpsert(ctx, a, "upsert-request-001", first.ID, 1, "r1", "t1", []packs.Question{q, extra})
	if e != nil || repeated.Revision != 2 {
		t.Fatal("receipt drift", repeated, e)
	}
	if _, e = s.MCPUpsert(ctx, a, "upsert-request-002", first.ID, 1, "r1", "t1", []packs.Question{q}); !errors.Is(e, ErrConflict) {
		t.Fatal("stale write", e)
	}
	var count int
	if e = p.QueryRow(ctx, `SELECT count(*) FROM mcp_receipts`).Scan(&count); e != nil || count != 2 {
		t.Fatal(count, e)
	}
}
func TestMCPAccessAndDeletedReceipts(t *testing.T) {
	for _, mode := range []string{"foreign", "scope", "revoked", "blocked", "deleted", "purged", "session"} {
		t.Run(mode, func(t *testing.T) {
			s, p := setup(t)
			a := identity(t, p, "owner", "author", "api", "drafts:read", "drafts:write", "drafts:export")
			doc := document("private")
			c, e := s.MCPCreate(ctx, a, "create-request-0001", doc)
			if e != nil {
				t.Fatal(e)
			}
			caller := a
			want := ErrNotFound
			switch mode {
			case "foreign":
				caller = identity(t, p, "other", "admin", "api", "drafts:read", "drafts:write")
			case "scope":
				caller = credential(t, p, "owner", "api", "drafts:read")
				want = ErrForbidden
			case "revoked":
				_, e = p.Exec(ctx, `UPDATE account_credentials SET revoked_at=now() WHERE id=$1`, a.SessionID)
				want = ErrUnauthorized
			case "blocked":
				_, e = p.Exec(ctx, `UPDATE library_packs SET blocked=true WHERE id=$1`, c.ID)
				want = ErrBlocked
			case "deleted":
				_, e = p.Exec(ctx, `UPDATE library_packs SET deleted_at=now() WHERE id=$1`, c.ID)
			case "purged":
				tx, err := p.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				_, e = tx.Exec(ctx, `DELETE FROM library_revisions WHERE pack_id=$1`, c.ID)
				if e == nil {
					_, e = tx.Exec(ctx, `DELETE FROM library_packs WHERE id=$1`, c.ID)
				}
				if e == nil {
					e = tx.Commit(ctx)
				} else {
					_ = tx.Rollback(ctx)
				}

			case "session":
				caller = credential(t, p, "owner", "session")
				want = ErrForbidden
			}
			if e != nil {
				t.Fatal(e)
			}
			if mode == "foreign" {
				_, e = s.MCPOwn(ctx, caller, c.ID, 0, "drafts:read")
			} else {
				_, e = s.MCPCreate(ctx, caller, "create-request-0001", doc)
			}
			if !errors.Is(e, want) {
				t.Fatalf("want %v got %v", want, e)
			}
		})
	}
}
func TestMCPUpsertPreservesOtherContentAndRejectsDuplicates(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "api", "drafts:read", "drafts:write")
	doc := document("private")
	other := doc.Rounds[0].Themes[0]
	other.ID = "t2"
	other.Questions = []packs.Question{{ID: "q2", Price: 200, Right: []string{"private"}}}
	doc.Rounds[0].Themes = append(doc.Rounds[0].Themes, other)
	c, e := s.MCPCreate(ctx, a, "create-request-0001", doc)
	if e != nil {
		t.Fatal(e)
	}
	q := doc.Rounds[0].Themes[0].Questions[0]
	q.Right = []string{"new"}
	for _, questions := range [][]packs.Question{{q, q}, {other.Questions[0]}, {}} {
		if _, e = s.MCPUpsert(ctx, a, "invalid-request-01", c.ID, 1, "r1", "t1", questions); !errors.Is(e, ErrInvalid) {
			t.Fatal("invalid patch accepted", e)
		}
	}
	if _, e = s.MCPUpsert(ctx, a, "valid-request-0001", c.ID, 1, "r1", "t1", []packs.Question{q}); e != nil {
		t.Fatal(e)
	}
	d, e := s.MCPOwn(ctx, a, c.ID, 0, "drafts:read")
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(d.Document.Rounds[0].Themes[1], other) || d.Document.Name != doc.Name {
		t.Fatal("unselected content changed")
	}
}

func TestMCPConcurrentUpsertPreservesTrustedFields(t *testing.T) {
	s, p := setup(t)
	owner := identity(t, p, "owner", "author", "session")
	a := credential(t, p, "owner", "api", "drafts:write")
	reader := credential(t, p, "owner", "api", "drafts:read")
	doc := document("Imported")
	doc.ImportIssues = []packs.Issue{{Severity: "warning", Code: "retained", Message: "Import metadata"}}
	original, e := s.Create(ctx, owner, Save{Document: doc, Assets: []Asset{{Name: "Images/a.png", Key: strings.Repeat("a", 64), Size: 10, ContentType: "image/png"}}, OriginalKey: strings.Repeat("b", 64)})
	if e != nil {
		t.Fatal(e)
	}
	q := doc.Rounds[0].Themes[0].Questions[0]
	q.Right = []string{"new answer"}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, e := s.MCPUpsert(ctx, a, "concurrent-update-1", original.Card.ID, 1, "r1", "t1", []packs.Question{q})
			if e != nil || c.Revision != 2 {
				t.Error(c, e)
			}
		}()
	}
	wg.Wait()
	d, e := s.MCPOwn(ctx, reader, original.Card.ID, 0, "drafts:read")
	if e != nil {
		t.Fatal(e)
	}
	if d.Card.Revision != 2 || !reflect.DeepEqual(d.Assets, original.Assets) || d.OriginalKey != original.OriginalKey || !reflect.DeepEqual(d.Document.ImportIssues, original.Document.ImportIssues) {
		t.Fatal("trusted data changed", d)
	}
}
func TestMCPReceiptCapacityRollsBackCreation(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "api", "drafts:write")
	if _, e := p.Exec(ctx, `INSERT INTO mcp_receipts(owner_id,idempotency_key,operation,request_hash,pack_id,result_revision) SELECT 'owner','receipt-'||lpad(n::text,10,'0'),'create_draft','hash','purged',1 FROM generate_series(1,$1)n`, MCPReceiptLimit); e != nil {
		t.Fatal(e)
	}
	if _, e := s.MCPCreate(ctx, a, "new-request-key-01", document("Too many")); !errors.Is(e, ErrQuota) {
		t.Fatal(e)
	}
	var n int
	if e := p.QueryRow(ctx, `SELECT count(*) FROM library_packs`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}
