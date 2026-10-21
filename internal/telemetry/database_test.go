package telemetry

import (
	"context"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
	"strings"
	"testing"
	"time"
)

func TestDatabaseSamplePublishesOnlyCompleteSnapshotAndStops(t *testing.T) {
	p := testutil.Pool(t)
	ctx := context.Background()
	if e := postgres.Migrate(ctx, p); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash) VALUES('owner','owner','private-owner','secret'); INSERT INTO ai_monthly_usage(owner_id,month,spent,reserved) VALUES('owner',date_trunc('month',now())::date,1230000,450000)`); e != nil {
		t.Fatal(e)
	}
	m := New()
	if e := m.RefreshDatabase(ctx, p); e != nil {
		t.Fatal(e)
	}
	before := scrape(t, m)
	if !strings.Contains(before, `si_ai_spent_usd 1.23`) || !strings.Contains(before, `si_ai_reserved_usd 0.45`) || strings.Contains(before, "private-owner") {
		t.Fatal(before)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if e := m.RefreshDatabase(cancelled, p); e == nil {
		t.Fatal("cancel ignored")
	}
	after := scrape(t, m)
	if !strings.Contains(after, `si_ai_spent_usd 1.23`) || !strings.Contains(after, `si_database_sample_errors_total 1`) {
		t.Fatal(after)
	}
	stop := m.WatchDatabase(cancelled, p)
	done := make(chan struct{})
	go func() { stop(); stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked")
	}
}

func TestDatabaseSamplingTimeoutKeepsPreviousValues(t *testing.T) {
	p := testutil.Pool(t)
	ctx := context.Background()
	if e := postgres.Migrate(ctx, p); e != nil {
		t.Fatal(e)
	}
	m := New()
	if e := m.RefreshDatabase(ctx, p); e != nil {
		t.Fatal(e)
	}
	m.database.mu.RLock()
	before := m.database.snapshot.success
	m.database.mu.RUnlock()
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `LOCK TABLE uploads IN ACCESS EXCLUSIVE MODE`); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	if e = m.RefreshDatabase(ctx, p); e == nil {
		t.Fatal("blocked DB sample succeeded")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatal("sampling exceeded bound", elapsed)
	}
	m.database.mu.RLock()
	after := m.database.snapshot.success
	m.database.mu.RUnlock()
	if after != before {
		t.Fatal("partial snapshot published")
	}
	text := scrape(t, m)
	if !strings.Contains(text, `si_database_sample_errors_total 1`) {
		t.Fatal("timeout missing")
	}
}
