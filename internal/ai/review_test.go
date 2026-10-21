package ai

import (
	"context"
	"errors"
	"siplatform/internal/accounts"
	"siplatform/internal/aiformat"
	"siplatform/internal/aiprovider"
	"testing"
	"time"
)

func TestProviderChangeRejectsQuoteAndQueuedDispatch(t *testing.T) {
	s, p, a, f := fixture(t)
	ctx := context.Background()
	q, e := s.Quote(ctx, a, request())
	if e != nil {
		t.Fatal(e)
	}
	changed := s.options
	changed.ProviderLabel = "different-provider"
	next, e := New(p, changed)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = next.Start(ctx, a, q.ID, "changed"); !errors.Is(e, ErrConflict) {
		t.Errorf("incompatible quote accepted: %v", e)
	}
	// Remove only the unexpectedly created job in the failing implementation.
	if _, e = p.Exec(ctx, `DELETE FROM ai_jobs; UPDATE ai_monthly_usage SET reserved=0`); e != nil {
		t.Fatal(e)
	}
	j, e := s.Start(ctx, a, q.ID, "original")
	if e != nil {
		t.Fatal(e)
	}
	c, e := next.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if c != nil {
		c.cancel()
		t.Fatal("queued request crossed provider boundary")
	}
	j, e = next.Get(ctx, a, j.ID)
	if e != nil || j.Status != "cancelled" {
		t.Fatal(j, e)
	}
	st, e := next.Status(ctx, a)
	if e != nil || st.Reserved != 0 || f.calls.Load() != 0 {
		t.Fatal(st, e)
	}
}
func TestDisabledRevokedQueueCleanup(t *testing.T) {
	s, p, a, _ := fixture(t)
	ctx := context.Background()
	q, e := s.Quote(ctx, a, request())
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.Start(ctx, a, q.ID, "disabled")
	if e != nil {
		t.Fatal(e)
	}
	disabled, e := New(p, Options{})
	if e != nil {
		t.Fatal(e)
	}
	stop, e := disabled.StartWorkers(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer stop()
	if _, e = p.Exec(ctx, `UPDATE account_users SET username=NULL,password_hash=NULL,deleted_at=now()-interval '31 days' WHERE id='a'`); e != nil {
		t.Fatal(e)
	}
	end := time.Now().Add(time.Second)
	var status string
	for time.Now().Before(end) {
		p.QueryRow(ctx, `SELECT status FROM ai_jobs WHERE id=$1`, j.ID).Scan(&status)
		if status == "cancelled" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != "cancelled" {
		t.Fatal("disabled provider retained deleted account queue", status)
	}
	stop()
	disabled.cleanup(ctx)
	var n int
	p.QueryRow(ctx, `SELECT count(*) FROM ai_jobs WHERE id=$1`, j.ID).Scan(&n)
	if n != 0 {
		t.Fatal("deleted prompt survived retention")
	}
}
func TestClaimSkipsLockedOwner(t *testing.T) {
	s, p, a, _ := fixture(t)
	ctx := context.Background()
	q, e := s.Quote(ctx, a, request())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Start(ctx, a, q.ID, "a"); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash) VALUES('b','bbb','B','secret'); INSERT INTO account_credentials(id,user_id,token_hash,kind,expires_at) VALUES('d','b','different','session',now()+interval '1 day')`); e != nil {
		t.Fatal(e)
	}
	b := accounts.Identity{User: accounts.User{ID: "b"}, SessionID: "d"}
	q, e = s.Quote(ctx, b, request())
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.Start(ctx, b, q.ID, "b")
	if e != nil {
		t.Fatal(e)
	}
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT id FROM account_users WHERE id='a' FOR UPDATE`); e != nil {
		t.Fatal(e)
	}
	bounded, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	c, e := s.claim(bounded)
	if e != nil || c == nil {
		t.Fatalf("locked A stalled runnable B: %v", e)
	}
	defer c.cancel()
	if c.job.ID != j.ID {
		t.Fatal("wrong job")
	}
}
func TestOldActiveMonthSurvivesCleanupAndSettlement(t *testing.T) {
	s, p, a, _ := fixture(t)
	ctx := context.Background()
	q, e := s.Quote(ctx, a, request())
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.Start(ctx, a, q.ID, "old")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE ai_monthly_usage SET month=(date_trunc('month',now() AT TIME ZONE 'UTC')-interval '5 months')::date; UPDATE ai_jobs SET month=(date_trunc('month',now() AT TIME ZONE 'UTC')-interval '5 months')::date`); e != nil {
		t.Fatal(e)
	}
	s.cleanup(ctx)
	var n int
	p.QueryRow(ctx, `SELECT count(*) FROM ai_monthly_usage`).Scan(&n)
	if n != 1 {
		t.Fatal("active month ledger deleted")
	}
	c, e := s.claim(ctx)
	if e != nil || c == nil {
		t.Fatal(e)
	}
	defer c.cancel()
	if e = s.settle(ctx, c, aiformat.Result{}, aiprovider.Usage{InputTokens: 100, OutputTokens: 20, Known: true}, errors.New("test")); e != nil {
		t.Fatal(e)
	}
	var spent, reserved int64
	p.QueryRow(ctx, `SELECT spent,reserved FROM ai_monthly_usage`).Scan(&spent, &reserved)
	if spent != 120 || reserved != 0 {
		t.Fatal(j.ID, spent, reserved)
	}
}
func TestMissingLedgerCannotSettleOrRefund(t *testing.T) {
	for _, mode := range []string{"settle", "refund"} {
		t.Run(mode, func(t *testing.T) {
			s, p, a, _ := fixture(t)
			ctx := context.Background()
			q, e := s.Quote(ctx, a, request())
			if e != nil {
				t.Fatal(e)
			}
			j, e := s.Start(ctx, a, q.ID, "missing")
			if e != nil {
				t.Fatal(e)
			}
			var c *claimed
			if mode == "settle" {
				c, e = s.claim(ctx)
				if e != nil {
					t.Fatal(e)
				}
				defer c.cancel()
			}
			if _, e = p.Exec(ctx, `DELETE FROM ai_monthly_usage`); e != nil {
				t.Fatal(e)
			}
			if mode == "settle" {
				e = s.settle(ctx, c, aiformat.Result{}, aiprovider.Usage{InputTokens: 100, OutputTokens: 20, Known: true}, errors.New("test"))
			} else {
				_, e = s.Cancel(ctx, a, j.ID)
			}
			if e == nil {
				t.Fatal("missing ledger silently ignored")
			}
		})
	}
}

func TestStaleContractRejectedAtStartAndClaim(t *testing.T) {
	s, p, a, _ := fixture(t)
	ctx := context.Background()
	q, e := s.Quote(ctx, a, request())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE ai_quotes SET format_version='old-prompt'`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Start(ctx, a, q.ID, "stale"); !errors.Is(e, ErrConflict) {
		t.Fatal("stale quote accepted", e)
	}
	if _, e = p.Exec(ctx, `UPDATE ai_quotes SET format_version=$1`, aiprovider.ContractFingerprint()); e != nil {
		t.Fatal(e)
	}
	j, e := s.Start(ctx, a, q.ID, "current")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE ai_jobs SET format_version='old-schema'`); e != nil {
		t.Fatal(e)
	}
	c, e := s.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if c != nil {
		c.cancel()
		t.Fatal("stale queued contract dispatched")
	}
	j, e = s.Get(ctx, a, j.ID)
	if e != nil || j.Status != "cancelled" || j.Error != "generation_contract_changed" {
		t.Fatal(j, e)
	}
}
func TestBlockedStartDoesNotHoldAdmission(t *testing.T) {
	s, p, a, _ := fixture(t)
	ctx := context.Background()
	qa, e := s.Quote(ctx, a, request())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash) VALUES('b','bbb','B','secret'); INSERT INTO account_credentials(id,user_id,token_hash,kind,expires_at) VALUES('d','b','different','session',now()+interval '1 day')`); e != nil {
		t.Fatal(e)
	}
	b := accounts.Identity{User: accounts.User{ID: "b"}, SessionID: "d"}
	qb, e := s.Quote(ctx, b, request())
	if e != nil {
		t.Fatal(e)
	}
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT id FROM account_users WHERE id='a' FOR UPDATE`); e != nil {
		t.Fatal(e)
	}
	blocked, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.Start(blocked, a, qa.ID, "locked") }()
	defer func() { cancel(); <-done }()
	// Wait for the first Start to be blocked in PostgreSQL, not an assumed scheduler delay.
	end := time.Now().Add(time.Second)
	for {
		var n int
		e = p.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%account_users%'`).Scan(&n)
		if e != nil {
			t.Fatal(e)
		}
		if n > 0 {
			break
		}
		if time.Now().After(end) {
			t.Fatal("first Start never waited")
		}
		time.Sleep(time.Millisecond)
	}
	bounded, stop := context.WithTimeout(ctx, 500*time.Millisecond)
	defer stop()
	if _, e = s.Start(bounded, b, qb.ID, "other"); e != nil {
		t.Fatal("unrelated account admission stalled", e)
	}
}
