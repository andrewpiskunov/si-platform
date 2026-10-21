package ai

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"siplatform/internal/accounts"
	"siplatform/internal/aiformat"
	"siplatform/internal/aiprovider"
	"sync"
	"time"
)

type claimed struct {
	job                                Job
	owner, credential                  string
	inputRate, outputRate, inputTokens int64
	maxTokens                          int
	ctx                                context.Context
	cancel                             context.CancelFunc
}

// StartWorkers must be called only after exclusive engine ownership is acquired.
func (s *Service) StartWorkers(ctx context.Context) (func(), error) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil, ErrConflict
	}
	s.started = true
	s.mu.Unlock()
	_, e := s.pool.Exec(ctx, `UPDATE ai_jobs SET status='uncertain',error='interrupted',completed_at=clock_timestamp() WHERE status='running'`)
	if e != nil {
		s.mu.Lock()
		s.started = false
		s.mu.Unlock()
		return nil, e
	}
	run, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-run.Done():
					return
				default:
				}
				if s.options.Provider != nil {
					c, e := s.claim(run)
					if e == nil && c != nil {
						s.dispatch(c)
						continue
					}
				}
				select {
				case <-run.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			s.cleanup(run)
			select {
			case <-run.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { cancel(); wg.Wait(); s.mu.Lock(); s.started = false; s.mu.Unlock() }) }, nil
}
func (s *Service) claim(ctx context.Context) (*claimed, error) {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT id FROM ai_admission WHERE id=1 FOR UPDATE`); e != nil {
		return nil, e
	}
	c := &claimed{}
	var id string
	e = tx.QueryRow(ctx, `SELECT id,owner_id,credential_id FROM ai_jobs WHERE status='queued' ORDER BY created_at,id LIMIT 1`).Scan(&id, &c.owner, &c.credential)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, `SELECT id FROM account_users WHERE id=$1 FOR UPDATE`, c.owner); e != nil {
		return nil, e
	}
	a := accounts.Identity{User: accounts.User{ID: c.owner}, SessionID: c.credential}
	authErr := auth(ctx, tx, a)
	if authErr != nil && !errors.Is(authErr, accounts.ErrUnauthorized) && !errors.Is(authErr, accounts.ErrForbidden) {
		return nil, authErr
	}
	var status string
	e = tx.QueryRow(ctx, `SELECT status FROM ai_jobs WHERE id=$1 FOR UPDATE`, id).Scan(&status)
	if e != nil {
		return nil, e
	}
	if status != "queued" {
		return nil, nil
	}
	if authErr != nil {
		if e = refund(ctx, tx, id); e != nil {
			return nil, e
		}
		_, e = tx.Exec(ctx, `UPDATE ai_jobs SET status='cancelled',actual_cost=0,error='authorization_revoked',completed_at=clock_timestamp() WHERE id=$1`, id)
		if e != nil {
			return nil, e
		}
		return nil, tx.Commit(ctx)
	}
	_, e = tx.Exec(ctx, `UPDATE ai_jobs SET status='running',started_at=clock_timestamp() WHERE id=$1`, id)
	if e != nil {
		return nil, e
	}
	c.job, e = scanJob(tx.QueryRow(ctx, `SELECT `+columns+` FROM ai_jobs WHERE id=$1`, id))
	if e != nil {
		return nil, e
	}
	e = tx.QueryRow(ctx, `SELECT input_rate,output_rate,input_tokens,max_output_tokens FROM ai_jobs WHERE id=$1`, id).Scan(&c.inputRate, &c.outputRate, &c.inputTokens, &c.maxTokens)
	if e != nil {
		return nil, e
	}
	c.ctx, c.cancel = context.WithTimeout(ctx, 180*time.Second)
	s.mu.Lock()
	s.running[id] = c.cancel
	s.mu.Unlock()
	if e = tx.Commit(ctx); e != nil {
		c.cancel()
		s.mu.Lock()
		delete(s.running, id)
		s.mu.Unlock()
		return nil, e
	}
	return c, nil
}
func (s *Service) dispatch(c *claimed) {
	defer func() { c.cancel(); s.mu.Lock(); delete(s.running, c.job.ID); s.mu.Unlock() }()
	result, usage, providerErr := s.options.Provider.Generate(c.ctx, c.job.Request, c.job.Model, c.maxTokens)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Retry only the local transaction. A lost COMMIT acknowledgement is safe:
	// settle locks the job and returns immediately if it is already terminal.
	for attempt := 0; attempt < 3; attempt++ {
		if s.settle(ctx, c, result, usage, providerErr) == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	// A persistent local failure cannot authorize another remote call. If the
	// database is reachable, free the active slot while holding its reservation.
	_ = s.settle(ctx, c, aiformat.Result{}, aiprovider.Usage{}, ErrUnavailable)
}
func (s *Service) settle(ctx context.Context, c *claimed, result aiformat.Result, u aiprovider.Usage, providerErr error) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT id FROM account_users WHERE id=$1 FOR UPDATE`, c.owner); e != nil {
		return e
	}
	var status string
	var cancelled bool
	e = tx.QueryRow(ctx, `SELECT status,cancel_requested FROM ai_jobs WHERE id=$1 FOR UPDATE`, c.job.ID).Scan(&status, &cancelled)
	if e != nil {
		return e
	}
	if status != "running" {
		return nil
	}
	var actual *int64
	state, code := "uncertain", "usage_unknown"
	var body []byte
	if u.Known && u.InputTokens >= 0 && u.InputTokens <= c.inputTokens && u.OutputTokens >= 0 && u.OutputTokens <= int64(c.maxTokens) {
		ic, ie := cost(u.InputTokens, c.inputRate)
		oc, oe := cost(u.OutputTokens, c.outputRate)
		v := ic + oc
		if ie == nil && oe == nil && v <= c.job.MaximumCost {
			actual = &v
			state, code = "failed", "generation_failed"
			if cancelled {
				state, code = "cancelled", ""
			} else if providerErr == nil && aiformat.ValidateResult(c.job.Request, result) == nil {
				state, code = "ready", ""
				body, _ = json.Marshal(result)
			}
			_, e = tx.Exec(ctx, `UPDATE ai_monthly_usage u SET reserved=u.reserved-j.maximum_cost,spent=u.spent+$2 FROM ai_jobs j WHERE j.id=$1 AND u.owner_id=j.owner_id AND u.month=j.month`, c.job.ID, v)
			if e != nil {
				return e
			}
		}
	}
	_, e = tx.Exec(ctx, `UPDATE ai_jobs SET status=$2,error=$3,result=$4,actual_cost=$5,completed_at=clock_timestamp() WHERE id=$1`, c.job.ID, state, code, body, actual)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// Each category is bounded, and account locks synchronize with export lease creation.
func (s *Service) cleanup(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s.recoverStale(ctx)
	categories := []struct{ table, predicate string }{{"ai_quotes", `expires_at<clock_timestamp()`}, {"ai_jobs", `status NOT IN ('queued','running') AND (completed_at<clock_timestamp()-interval '90 days' OR EXISTS(SELECT 1 FROM account_users u WHERE u.id=t.owner_id AND u.deleted_at<clock_timestamp()-interval '30 days'))`}, {"ai_monthly_usage", `month<date_trunc('month',clock_timestamp() AT TIME ZONE 'UTC')::date AND month<((clock_timestamp() AT TIME ZONE 'UTC')::date-90)`}}
	for _, cat := range categories {
		rows, e := s.pool.Query(ctx, `SELECT DISTINCT owner_id FROM `+cat.table+` t WHERE `+cat.predicate+` LIMIT 25`)
		if e != nil {
			return
		}
		var owners []string
		for rows.Next() {
			var owner string
			if rows.Scan(&owner) == nil {
				owners = append(owners, owner)
			}
		}
		rows.Close()
		remaining := 25
		for _, owner := range owners {
			if remaining <= 0 {
				break
			}
			tx, e := s.pool.Begin(ctx)
			if e != nil {
				return
			}
			_, e = tx.Exec(ctx, `SELECT id FROM account_users WHERE id=$1 FOR UPDATE`, owner)
			var lease bool
			if e == nil {
				e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM account_export_leases WHERE owner_id=$1 AND expires_at>clock_timestamp())`, owner).Scan(&lease)
			}
			if e == nil && !lease {
				tag, err := tx.Exec(ctx, `DELETE FROM `+cat.table+` WHERE ctid IN (SELECT ctid FROM `+cat.table+` t WHERE owner_id=$1 AND `+cat.predicate+` LIMIT $2)`, owner, remaining)
				e = err
				remaining -= int(tag.RowsAffected())
			}
			if e == nil {
				_ = tx.Commit(ctx)
			} else {
				_ = tx.Rollback(ctx)
			}
		}
	}
}

// Dispatch has a 180s deadline and settlement a 10s deadline. A four-minute
// stale marker also resolves reservations after a temporary database outage.
func (s *Service) recoverStale(ctx context.Context) {
	rows, e := s.pool.Query(ctx, `SELECT id,owner_id FROM ai_jobs WHERE status='running' AND COALESCE(started_at,created_at)<clock_timestamp()-interval '4 minutes' ORDER BY id LIMIT 25`)
	if e != nil {
		return
	}
	type item struct{ id, owner string }
	var items []item
	for rows.Next() {
		var v item
		if rows.Scan(&v.id, &v.owner) == nil {
			items = append(items, v)
		}
	}
	rows.Close()
	for _, v := range items {
		tx, e := s.pool.Begin(ctx)
		if e != nil {
			return
		}
		_, e = tx.Exec(ctx, `SELECT id FROM account_users WHERE id=$1 FOR UPDATE`, v.owner)
		if e == nil {
			_, e = tx.Exec(ctx, `UPDATE ai_jobs SET status='uncertain',error='interrupted',completed_at=clock_timestamp() WHERE id=$1 AND status='running' AND COALESCE(started_at,created_at)<clock_timestamp()-interval '4 minutes'`, v.id)
		}
		if e == nil {
			_ = tx.Commit(ctx)
		} else {
			_ = tx.Rollback(ctx)
		}
	}
}
