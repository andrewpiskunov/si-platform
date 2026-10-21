package ai

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"siplatform/internal/accounts"
	"siplatform/internal/aiformat"
	"siplatform/internal/aiprovider"
	"siplatform/internal/storage/postgres"
	"siplatform/internal/testutil"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fake struct {
	calls     atomic.Int32
	block     chan struct{}
	known     bool
	countHook func(context.Context) (int64, error)
}

func (f *fake) Count(ctx context.Context, _ aiformat.Request, _ string, _ int) (int64, error) {
	if f.countHook != nil {
		return f.countHook(ctx)
	}
	return 100, nil
}
func (f *fake) Generate(ctx context.Context, r aiformat.Request, m string, n int) (aiformat.Result, aiprovider.Usage, error) {
	f.calls.Add(1)
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return aiformat.Result{}, aiprovider.Usage{}, ctx.Err()
		}
	}
	return aiformat.Result{}, aiprovider.Usage{InputTokens: 100, OutputTokens: 20, Known: f.known}, errors.New("SECRET")
}
func fixture(t *testing.T) (*Service, *pgxpool.Pool, accounts.Identity, *fake) {
	t.Helper()
	p := testutil.Pool(t)
	if e := postgres.Migrate(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	_, e := p.Exec(context.Background(), `INSERT INTO account_users(id,username,display_name,password_hash) VALUES('a','aaa','A','secret'); INSERT INTO account_credentials(id,user_id,token_hash,kind,expires_at) VALUES('c','a','secret','session',now()+interval '1 day')`)
	if e != nil {
		t.Fatal(e)
	}
	f := &fake{known: true}
	s, e := New(p, Options{Provider: f, Model: "fixture", InputRate: 1000000, OutputRate: 1000000, MonthlyLimit: 10000, MaxOutputTokens: 256})
	if e != nil {
		t.Fatal(e)
	}
	return s, p, accounts.Identity{User: accounts.User{ID: "a"}, SessionID: "c"}, f
}
func request() aiformat.Request {
	return aiformat.Request{Mode: "theme", Topic: "Stars", Audience: "Adults", Difficulty: 2, Themes: 1, QuestionsPerTheme: 1}
}
func TestReservationDuplicateCancelAndFreshAuth(t *testing.T) {
	s, p, a, _ := fixture(t)
	ctx := context.Background()
	q, e := s.Quote(ctx, a, request())
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			j, e := s.Start(ctx, a, q.ID, "same")
			if e != nil {
				t.Error(e)
			}
			ids <- j.ID
		}()
	}
	wg.Wait()
	close(ids)
	var id string
	for v := range ids {
		if id != "" && id != v {
			t.Fatal("duplicate")
		}
		id = v
	}
	st, e := s.Status(ctx, a)
	if e != nil || st.Reserved != 356 {
		t.Fatal(st, e)
	}
	if _, e = s.Cancel(ctx, a, id); e != nil {
		t.Fatal(e)
	}
	st, _ = s.Status(ctx, a)
	if st.Reserved != 0 {
		t.Fatal(st)
	}
	p.Exec(ctx, `UPDATE account_credentials SET revoked_at=now()`)
	if _, e = s.Start(ctx, a, q.ID, "same"); !errors.Is(e, accounts.ErrUnauthorized) {
		t.Fatal(e)
	}
}
func TestWorkerKnownAndUnknownNoReplay(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(map[bool]string{true: "known", false: "unknown"}[known], func(t *testing.T) {
			s, _, a, f := fixture(t)
			f.known = known
			ctx := context.Background()
			q, e := s.Quote(ctx, a, request())
			if e != nil {
				t.Fatal(e)
			}
			j, e := s.Start(ctx, a, q.ID, "key")
			if e != nil {
				t.Fatal(e)
			}
			stop, e := s.StartWorkers(ctx)
			if e != nil {
				t.Fatal(e)
			}
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				j, e = s.Get(ctx, a, j.ID)
				if e != nil {
					t.Fatal(e)
				}
				if j.Status == "failed" || j.Status == "uncertain" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			stop()
			st, _ := s.Status(ctx, a)
			if known && (j.Status != "failed" || st.Spent != 120 || st.Reserved != 0) {
				t.Fatal(j, st)
			}
			if !known && (j.Status != "uncertain" || st.Reserved != 356) {
				t.Fatal(j, st)
			}
			stop, e = s.StartWorkers(ctx)
			if e != nil {
				t.Fatal(e)
			}
			stop()
			if f.calls.Load() != 1 {
				t.Fatal("replayed")
			}
		})
	}
}
func TestCostCeilingAndBounds(t *testing.T) {
	if c, e := cost(1, 1); e != nil || c != 1 {
		t.Fatal(c, e)
	}
	if _, e := cost(1<<62, 1<<62); e == nil {
		t.Fatal("overflow accepted")
	}
}

func TestRevokedQueuedAndRecovery(t *testing.T) {
	s, p, a, f := fixture(t)
	ctx := context.Background()
	q, e := s.Quote(ctx, a, request())
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.Start(ctx, a, q.ID, "key")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE account_credentials SET revoked_at=now()`); e != nil {
		t.Fatal(e)
	}
	stop, e := s.StartWorkers(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer stop()
	deadline := time.Now().Add(3 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		p.QueryRow(ctx, `SELECT status FROM ai_jobs WHERE id=$1`, j.ID).Scan(&status)
		if status == "cancelled" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != "cancelled" || f.calls.Load() != 0 {
		t.Fatal(status, f.calls.Load())
	}
	var reserved int64
	p.QueryRow(ctx, `SELECT reserved FROM ai_monthly_usage`).Scan(&reserved)
	if reserved != 0 {
		t.Fatal(reserved)
	}
}
func TestRunningRecoveryAndCancel(t *testing.T) {
	s, p, a, f := fixture(t)
	ctx := context.Background()
	q, e := s.Quote(ctx, a, request())
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.Start(ctx, a, q.ID, "key")
	if e != nil {
		t.Fatal(e)
	}
	f.block = make(chan struct{})
	stop, e := s.StartWorkers(ctx)
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for f.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if _, e = s.Cancel(ctx, a, j.ID); e != nil {
		t.Fatal(e)
	}
	stop()
	j, e = s.Get(ctx, a, j.ID)
	if e != nil || j.Status != "uncertain" || !j.CancelRequested {
		t.Fatal(j, e)
	}
	if _, e = p.Exec(ctx, `UPDATE ai_jobs SET status='running',completed_at=NULL`); e != nil {
		t.Fatal(e)
	}
	stop, e = s.StartWorkers(ctx)
	if e != nil {
		t.Fatal(e)
	}
	stop()
	j, e = s.Get(ctx, a, j.ID)
	if e != nil || j.Status != "uncertain" || f.calls.Load() != 1 {
		t.Fatal(j, e, f.calls.Load())
	}
}
func TestCooldownBudgetOwnershipAndRetention(t *testing.T) {
	s, p, a, _ := fixture(t)
	ctx := context.Background()
	q, e := s.Quote(ctx, a, request())
	if e != nil {
		t.Fatal(e)
	}
	s2, e := New(p, s.options)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s2.Quote(ctx, a, request()); !errors.Is(e, ErrLimit) {
		t.Fatal("durable cooldown", e)
	}
	s.options.MonthlyLimit = 355
	if _, e = s.Start(ctx, a, q.ID, "key"); !errors.Is(e, ErrLimit) {
		t.Fatal("budget", e)
	}
	s.options.MonthlyLimit = 10000
	j, e := s.Start(ctx, a, q.ID, "key")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Start(ctx, a, "another", "key"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = s.Start(ctx, a, q.ID, "other"); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash) VALUES('b','bbb','B','secret'); INSERT INTO account_credentials(id,user_id,token_hash,kind,expires_at) VALUES('d','b','different','session',now()+interval '1 day')`); e != nil {
		t.Fatal(e)
	}
	other := accounts.Identity{User: accounts.User{ID: "b"}, SessionID: "d"}
	if _, e = s.Get(ctx, other, j.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = s.Cancel(ctx, other, j.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = s.Cancel(ctx, a, j.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE ai_jobs SET completed_at=now()-interval '91 days'; UPDATE ai_monthly_usage SET spent=99; INSERT INTO account_export_leases(owner_id,id,expires_at) VALUES('a','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',now()+interval '1 hour')`); e != nil {
		t.Fatal(e)
	}
	s.cleanup(ctx)
	if _, e = s.Get(ctx, a, j.ID); e != nil {
		t.Fatal("lease", e)
	}
	p.Exec(ctx, `DELETE FROM account_export_leases`)
	s.cleanup(ctx)
	if _, e = s.Get(ctx, a, j.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("retention", e)
	}
	st, e := s.Status(ctx, a)
	if e != nil || st.Spent != 99 {
		t.Fatal("aggregate lost", st, e)
	}
}

func TestPreviousMonthSettlementAndBoundedCleanup(t *testing.T) {
	s, p, a, _ := fixture(t)
	ctx := context.Background()
	q, e := s.Quote(ctx, a, request())
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.Start(ctx, a, q.ID, "key")
	if e != nil {
		t.Fatal(e)
	}
	_, e = p.Exec(ctx, `UPDATE ai_monthly_usage SET month=(date_trunc('month',now() AT TIME ZONE 'UTC')-interval '1 month')::date; UPDATE ai_jobs SET month=(date_trunc('month',now() AT TIME ZONE 'UTC')-interval '1 month')::date`)
	if e != nil {
		t.Fatal(e)
	}
	stop, e := s.StartWorkers(ctx)
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, e = s.Get(ctx, a, j.ID)
		if e != nil {
			t.Fatal(e)
		}
		if j.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	st, e := s.Status(ctx, a)
	if e != nil || st.Reserved != 0 || st.Spent != 0 {
		t.Fatal(st, e)
	}
	var spent, reserved int64
	if e = p.QueryRow(ctx, `SELECT spent,reserved FROM ai_monthly_usage`).Scan(&spent, &reserved); e != nil || spent != 120 || reserved != 0 {
		t.Fatal(spent, reserved, e)
	}
	_, e = p.Exec(ctx, `INSERT INTO ai_jobs(id,owner_id,status,request,model,idempotency_key,request_hash,month,input_rate,output_rate,input_tokens,max_output_tokens,maximum_cost,completed_at) SELECT 'old'||g,'a','failed','{}','m','old'||g,'h',current_date,1,1,1,256,1,now()-interval '91 days' FROM generate_series(1,60) g`)
	if e != nil {
		t.Fatal(e)
	}
	pageBefore, e := s.List(ctx, a, "")
	if e != nil || len(pageBefore.Jobs) != 50 || pageBefore.Next == "" {
		t.Fatal("history cap", pageBefore, e)
	}
	rest, e := s.List(ctx, a, pageBefore.Next)
	if e != nil || len(rest.Jobs) != 11 {
		t.Fatal("history cursor", rest, e)
	}
	s.cleanup(ctx)
	var n int
	p.QueryRow(ctx, `SELECT count(*) FROM ai_jobs WHERE id LIKE 'old%'`).Scan(&n)
	if n != 35 {
		t.Fatal("unbounded cleanup", n)
	}
	page, e := s.List(ctx, a, "")
	if e != nil || len(page.Jobs) != 36 {
		t.Fatal(len(page.Jobs), e)
	}
}
func TestDisabledAndLivePATScopes(t *testing.T) {
	s, p, a, _ := fixture(t)
	ctx := context.Background()
	disabled, e := New(p, Options{})
	if e != nil {
		t.Fatal(e)
	}
	st, e := disabled.Status(ctx, a)
	if e != nil || st.Enabled {
		t.Fatal(st, e)
	}
	page, e := disabled.List(ctx, a, "")
	if e != nil || len(page.Jobs) != 0 {
		t.Fatal(page, e)
	}
	if _, e = disabled.Quote(ctx, a, request()); !errors.Is(e, ErrDisabled) {
		t.Fatal(e)
	}
	_, e = p.Exec(ctx, `UPDATE account_credentials SET kind='api',scopes=ARRAY['drafts:read'] WHERE id='c'`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Status(ctx, a); !errors.Is(e, accounts.ErrForbidden) {
		t.Fatal(e)
	}
	_, e = p.Exec(ctx, `UPDATE account_credentials SET scopes=ARRAY['ai:generate'] WHERE id='c'`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Status(ctx, a); e != nil {
		t.Fatal(e)
	}
}
func TestCountSlotsGlobalAndNoAccountLock(t *testing.T) {
	s, p, a, f := fixture(t)
	ctx := context.Background()
	_, e := p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash) VALUES('b','bbb','B','secret'),('d','ddd','D','secret'); INSERT INTO account_credentials(id,user_id,token_hash,kind,expires_at) VALUES('bc','b','bc','session',now()+interval '1 day'),('dc','d','dc','session',now()+interval '1 day')`)
	if e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	f.countHook = func(ctx context.Context) (int64, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return 100, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	defer close(release)
	for _, actor := range []accounts.Identity{a, {User: accounts.User{ID: "b"}, SessionID: "bc"}} {
		wg.Add(1)
		go func(actor accounts.Identity) { defer wg.Done(); _, _ = s.Quote(ctx, actor, request()) }(actor)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("count never entered")
		}
	}
	lockCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if _, e = p.Exec(lockCtx, `UPDATE account_users SET display_name='Unlocked' WHERE id='a'`); e != nil {
		t.Fatal("provider under account lock", e)
	}
	s2, e := New(p, s.options)
	if e != nil {
		t.Fatal(e)
	}
	thirdCtx, thirdCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer thirdCancel()
	if _, e = s2.Quote(thirdCtx, accounts.Identity{User: accounts.User{ID: "d"}, SessionID: "dc"}, request()); !errors.Is(e, ErrLimit) {
		t.Fatal("global count slots", e)
	}
}
func TestQueueTwentyAndTwoWorkers(t *testing.T) {
	s, p, _, f := fixture(t)
	ctx := context.Background()
	_, e := p.Exec(ctx, `INSERT INTO account_users(id,username,display_name,password_hash) SELECT 'u'||n,'user'||n,'User','secret' FROM generate_series(1,21)n; INSERT INTO account_credentials(id,user_id,token_hash,kind,expires_at) SELECT 'cred'||n,'u'||n,'hash'||n,'session',now()+interval '1 day' FROM generate_series(1,21)n; INSERT INTO ai_quotes(owner_id,id,credential_id,request,model,input_rate,output_rate,max_output_tokens,input_tokens,maximum_cost) SELECT 'u'||n,'quote'||n,'cred'||n,'{"mode":"theme","topic":"Stars","audience":"Adults","difficulty":2,"themes":1,"questions_per_theme":1}','fixture',1000000,1000000,256,100,356 FROM generate_series(1,21)n`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE ai_quotes SET format_version=$1`, aiprovider.ContractFingerprint()); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var admitted atomic.Int32
	for i := 1; i <= 21; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			n := fmt.Sprint(i)
			_, e := s.Start(ctx, accounts.Identity{User: accounts.User{ID: "u" + n}, SessionID: "cred" + n}, "quote"+n, "key")
			if e == nil {
				admitted.Add(1)
			} else if !errors.Is(e, ErrLimit) {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	if admitted.Load() != 20 {
		t.Fatal(admitted.Load())
	}
	f.block = make(chan struct{})
	stop, e := s.StartWorkers(ctx)
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for f.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	var running int
	if e = p.QueryRow(ctx, `SELECT count(*) FROM ai_jobs WHERE status='running'`).Scan(&running); e != nil {
		t.Fatal(e)
	}
	stop()
	if running != 2 || f.calls.Load() != 2 {
		t.Fatal(running, f.calls.Load())
	}
}
func TestCurrentQuoteClaimCannotOverwriteNewQuote(t *testing.T) {
	s, p, a, f := fixture(t)
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	var counts atomic.Int32
	f.countHook = func(ctx context.Context) (int64, error) {
		if counts.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		return 100, nil
	}
	finished := make(chan error, 1)
	go func() { _, e := s.Quote(ctx, a, request()); finished <- e }()
	<-entered
	if _, e := p.Exec(ctx, `UPDATE ai_quotes SET created_at=now()-interval '11 seconds'`); e != nil {
		t.Fatal(e)
	}
	q, e := s.Quote(ctx, a, request())
	close(release)
	if e != nil {
		t.Fatal(e)
	}
	if e = <-finished; !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	var current string
	if e = p.QueryRow(ctx, `SELECT id FROM ai_quotes`).Scan(&current); e != nil || current != q.ID {
		t.Fatal(current, q.ID, e)
	}
}
func TestSettlementRetriesDatabaseOnly(t *testing.T) {
	s, p, a, f := fixture(t)
	ctx := context.Background()
	_, e := p.Exec(ctx, `CREATE SEQUENCE settlement_failure; CREATE FUNCTION fail_settlement_once() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='failed' AND nextval('settlement_failure')=1 THEN RAISE EXCEPTION 'transient database failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER transient_settlement BEFORE UPDATE ON ai_jobs FOR EACH ROW EXECUTE FUNCTION fail_settlement_once()`)
	if e != nil {
		t.Fatal(e)
	}
	q, e := s.Quote(ctx, a, request())
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.Start(ctx, a, q.ID, "key")
	if e != nil {
		t.Fatal(e)
	}
	stop, e := s.StartWorkers(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer stop()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, e = s.Get(ctx, a, j.ID)
		if e != nil {
			t.Fatal(e)
		}
		if j.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	st, e := s.Status(ctx, a)
	if e != nil || j.Status != "failed" || st.Spent != 120 || st.Reserved != 0 || f.calls.Load() != 1 {
		t.Fatal(j, st, e, f.calls.Load())
	}
}
func TestStaleRunningRecoveryHoldsReservation(t *testing.T) {
	s, p, a, f := fixture(t)
	ctx := context.Background()
	q, e := s.Quote(ctx, a, request())
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.Start(ctx, a, q.ID, "key")
	if e != nil {
		t.Fatal(e)
	}
	_, e = p.Exec(ctx, `UPDATE ai_jobs SET status='running',started_at=now()-interval '5 minutes' WHERE id=$1`, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	s.cleanup(ctx)
	j, e = s.Get(ctx, a, j.ID)
	if e != nil || j.Status != "uncertain" {
		t.Fatal(j, e)
	}
	st, e := s.Status(ctx, a)
	if e != nil || st.Reserved != 356 || f.calls.Load() != 0 {
		t.Fatal(st, e, f.calls.Load())
	}
}
