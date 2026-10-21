package library

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"siplatform/internal/accounts"
	"siplatform/internal/aiformat"
)

func aiResult() aiformat.Result {
	return aiformat.Result{Title: "Reviewed quiz", Themes: []aiformat.Theme{{Name: "History", Questions: []aiformat.Question{{Text: "Who?", Answer: "Author", Comment: "Edited by author", Sources: []string{"https://example.test/source"}}}}}}
}
func aiJob(t *testing.T, p *pgxpool.Pool, a accounts.Identity, status string) string {
	t.Helper()
	job, err := id()
	if err != nil {
		t.Fatal(err)
	}
	req, _ := json.Marshal(aiformat.Request{Mode: "pack", Topic: "History", Audience: "Adults", Difficulty: 3, Themes: 1, QuestionsPerTheme: 1})
	res, _ := json.Marshal(aiResult())
	_, err = p.Exec(ctx, `INSERT INTO ai_jobs(id,owner_id,status,request,result,model,completed_at,idempotency_key,request_hash,month,input_rate,output_rate,input_tokens,max_output_tokens,maximum_cost) VALUES($1,$2,$3,$4,$5,'fixture',now(),$1,'h',current_date,1,1,1,256,1)`, job, a.User.ID, status, req, res)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

// Missing authorization or validation must never create a draft or receipt.
func TestAIApplyGuards(t *testing.T) {
	for _, tc := range []struct {
		name string
		want error
	}{
		{"ack", ErrInvalid}, {"other", ErrNotFound}, {"ai_scope", ErrForbidden}, {"write_scope", ErrForbidden}, {"not_ready", ErrConflict}, {"invalid_shape", ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, p := setup(t)
			a := identity(t, p, "owner", "author", "session")
			job := aiJob(t, p, a, "ready")
			reviewed := true
			result := aiResult()
			caller := a
			switch tc.name {
			case "ack":
				reviewed = false
			case "other":
				caller = identity(t, p, "other", "admin", "session")
			case "ai_scope":
				caller = credential(t, p, "owner", "api", "drafts:write")
			case "write_scope":
				caller = credential(t, p, "owner", "api", "ai:generate")
			case "not_ready":
				_, err := p.Exec(ctx, "UPDATE ai_jobs SET status='cancelled' WHERE id=$1", job)
				if err != nil {
					t.Fatal(err)
				}
			case "invalid_shape":
				result.Themes = nil
			}
			if _, err := s.CreateAIDraft(ctx, caller, job, reviewed, result); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			var n int
			if err := p.QueryRow(ctx, "SELECT count(*) FROM library_packs").Scan(&n); err != nil || n != 0 {
				t.Fatal(n, err)
			}
		})
	}
}

// A lost response or simultaneous retry must create exactly one private pack;
// later retries must return current edits and reject a changed accepted payload.
func TestAIApplyConcurrentReceipt(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	a = credential(t, p, "owner", "api", "ai:generate", "drafts:write")
	job := aiJob(t, p, a, "ready")
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan Draft, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			d, err := s.CreateAIDraft(ctx, a, job, true, aiResult())
			results <- d
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var packID string
	for d := range results {
		if packID == "" {
			packID = d.Card.ID
		}
		if d.Card.ID != packID || d.Card.Status != "draft" || d.Card.PublishedRevision != 0 || d.Card.PendingRevision != 0 {
			t.Fatalf("unexpected draft: %+v", d.Card)
		}
		if !strings.Contains(d.Document.Rounds[0].Themes[0].Questions[0].Info.Comments, job) {
			t.Fatal("missing provenance")
		}
	}
	var n int
	var receipt, hash string
	if err := p.QueryRow(ctx, "SELECT count(*) FROM library_packs WHERE owner_id=$1", a.User.ID).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := p.QueryRow(ctx, "SELECT applied_pack_id,applied_hash FROM ai_jobs WHERE id=$1", job).Scan(&receipt, &hash); err != nil || receipt != packID || len(hash) != 64 {
		t.Fatal(receipt, hash, err)
	}
	if _, err := s.Update(ctx, a, packID, 1, Save{Document: document("Later edit")}); err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateAIDraft(ctx, a, job, true, aiResult())
	if err != nil || d.Card.Revision != 2 || d.Document.Name != "Later edit" {
		t.Fatal(d, err)
	}
	changed := aiResult()
	changed.Title = "Changed"
	if _, err = s.CreateAIDraft(ctx, a, job, true, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("hash conflict", err)
	}
}

// Receipt lookup cannot bypass revoked/expired credentials, deleted users or narrowed PAT scopes.
func TestAIApplyReceiptFreshAuthorization(t *testing.T) {
	for _, mutation := range []string{"revoked", "expired", "deleted", "scopes"} {
		t.Run(mutation, func(t *testing.T) {
			s, p := setup(t)
			a := identity(t, p, "owner", "author", "api", "ai:generate", "drafts:write")
			job := aiJob(t, p, a, "ready")
			if _, err := s.CreateAIDraft(ctx, a, job, true, aiResult()); err != nil {
				t.Fatal(err)
			}
			sql := "UPDATE account_credentials SET revoked_at=now() WHERE id=$1"
			arg := a.SessionID
			want := ErrUnauthorized
			switch mutation {
			case "expired":
				sql = "UPDATE account_credentials SET expires_at=now()-interval '1 second' WHERE id=$1"
			case "deleted":
				sql = "UPDATE account_users SET deleted_at=now(),username=NULL,password_hash=NULL WHERE id=$1"
				arg = a.User.ID
			case "scopes":
				sql = "UPDATE account_credentials SET scopes=ARRAY['drafts:write'] WHERE id=$1"
				want = ErrForbidden
			}
			if _, err := p.Exec(ctx, sql, arg); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateAIDraft(ctx, a, job, true, aiResult()); !errors.Is(err, want) {
				t.Fatal(err)
			}
		})
	}
}

// Retrying cannot revive a deleted or blocked receipt target.
func TestAIApplyReceiptTarget(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "deleted", true: "blocked"}[blocked], func(t *testing.T) {
			s, p := setup(t)
			a := identity(t, p, "owner", "author", "session")
			job := aiJob(t, p, a, "ready")
			d, err := s.CreateAIDraft(ctx, a, job, true, aiResult())
			if err != nil {
				t.Fatal(err)
			}
			want := ErrNotFound
			if blocked {
				_, err = p.Exec(ctx, "UPDATE library_packs SET blocked=true WHERE id=$1", d.Card.ID)
				want = ErrBlocked
			} else {
				err = s.Delete(ctx, a, d.Card.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.CreateAIDraft(ctx, a, job, true, aiResult()); !errors.Is(err, want) {
				t.Fatal(err)
			}
			var n int
			if err = p.QueryRow(ctx, "SELECT count(*) FROM library_packs").Scan(&n); err != nil || n != 1 {
				t.Fatal(n, err)
			}
		})
	}
}

// Quota failure must roll back both creation and receipt, allowing a later retry.
func TestAIApplyQuotaRollback(t *testing.T) {
	s, p := setup(t)
	a := identity(t, p, "owner", "author", "session")
	job := aiJob(t, p, a, "ready")
	var first string
	for i := 0; i < PackCountLimit; i++ {
		d := create(t, s, a, "Quota")
		if i == 0 {
			first = d.Card.ID
		}
	}
	var err error
	if _, err = s.CreateAIDraft(ctx, a, job, true, aiResult()); !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
	var empty bool
	if err = p.QueryRow(ctx, "SELECT applied_pack_id IS NULL AND applied_hash='' FROM ai_jobs WHERE id=$1", job).Scan(&empty); err != nil || !empty {
		t.Fatal(empty, err)
	}
	if err = s.Delete(ctx, a, first); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateAIDraft(ctx, a, job, true, aiResult()); err != nil {
		t.Fatal(err)
	}
}
