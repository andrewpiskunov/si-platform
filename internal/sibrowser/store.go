package sibrowser

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"siplatform/internal/ingest"
	"sync"
	"time"
)

var ErrBusy = errors.New("source_busy")
var ErrIncomplete = errors.New("sync_incomplete")
var ErrConfig = errors.New("invalid_sync_configuration")

// Configure is an operator-only database operation. Ownership is immutable.
func Configure(ctx context.Context, p *pgxpool.Pool, owner string, count int, bytes int64) error {
	if owner == "" || count < 1 || count > 100000 || bytes <= 0 || bytes > 1<<50 {
		return ErrConfig
	}
	result, e := p.Exec(ctx, `INSERT INTO external_sources(source,owner_id,pack_limit,storage_limit)
 SELECT 'sibrowser',id,$2,$3 FROM account_users WHERE id=$1 AND deleted_at IS NULL AND role='admin'
 ON CONFLICT(source) DO UPDATE SET pack_limit=EXCLUDED.pack_limit,storage_limit=EXCLUDED.storage_limit WHERE external_sources.owner_id=EXCLUDED.owner_id`, owner, count, bytes)
	if e != nil {
		return errors.New("source_configuration_failed")
	}
	if result.RowsAffected() != 1 {
		return ErrConfig
	}
	return nil
}

type lease struct {
	conn      *pgx.Conn
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	epoch     int64
	owner     string
	next      int
	bootstrap bool
}

func acquire(parent context.Context, p *pgxpool.Pool) (*lease, error) {
	ctx, cancel := context.WithCancel(parent)
	conn, e := pgx.ConnectConfig(ctx, p.Config().ConnConfig.Copy())
	if e != nil {
		cancel()
		return nil, errors.New("database_unavailable")
	}
	l := &lease{conn: conn, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	ok := false
	defer func() {
		if !ok {
			cancel()
			closeCtx, c := context.WithTimeout(context.Background(), time.Second)
			defer c()
			conn.Close(closeCtx)
		}
	}()
	var locked bool
	if e = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended(current_database()||':'||current_schema()||':sibrowser-sync',0))`).Scan(&locked); e != nil {
		return nil, errors.New("database_unavailable")
	}
	if !locked {
		return nil, ErrBusy
	}
	e = conn.QueryRow(ctx, `UPDATE external_sources SET epoch=epoch+1 WHERE source='sibrowser' AND EXISTS(SELECT 1 FROM account_users WHERE id=owner_id AND deleted_at IS NULL AND role='admin') RETURNING epoch,owner_id,next_page,bootstrap_complete`).Scan(&l.epoch, &l.owner, &l.next, &l.bootstrap)
	if e != nil {
		return nil, ErrConfig
	}
	ok = true
	go func() {
		defer close(l.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				l.mu.Lock()
				check, c := context.WithTimeout(ctx, 2*time.Second)
				e := conn.Ping(check)
				c()
				l.mu.Unlock()
				if e != nil {
					cancel()
					return
				}
			}
		}
	}()
	return l, nil
}
func (l *lease) close() {
	l.cancel()
	<-l.done
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx, c := context.WithTimeout(context.Background(), time.Second)
	defer c()
	l.conn.Close(ctx)
}
func (l *lease) transaction(fn func(pgx.Tx) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	tx, e := l.conn.Begin(l.ctx)
	if e != nil {
		return errors.New("database_unavailable")
	}
	defer tx.Rollback(context.Background())
	var epoch int64
	if e = tx.QueryRow(l.ctx, `SELECT epoch FROM external_sources WHERE source='sibrowser' FOR UPDATE`).Scan(&epoch); e != nil {
		return e
	}
	if epoch != l.epoch {
		return errors.New("source_lease_lost")
	}
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit(l.ctx)
}
func (l *lease) discover(p Page, checkpoint bool) (int, error) {
	added := 0
	err := l.transaction(func(tx pgx.Tx) error {
		for _, item := range p.Items {
			raw, _ := json.Marshal(item)
			r, e := tx.Exec(l.ctx, `INSERT INTO source_items(source,external_id,metadata) VALUES('sibrowser',$1,$2) ON CONFLICT DO NOTHING`, item.ID, raw)
			if e != nil {
				return e
			}
			added += int(r.RowsAffected())
		}
		if checkpoint {
			next := p.Next
			if next == 0 {
				next = 1
			}
			_, e := tx.Exec(l.ctx, `UPDATE external_sources SET next_page=$1 WHERE source='sibrowser'`, next)
			return e
		}
		return nil
	})
	return added, err
}
func (l *lease) complete() error {
	return l.transaction(func(tx pgx.Tx) error {
		_, e := tx.Exec(l.ctx, `UPDATE external_sources SET next_page=1,bootstrap_complete=true WHERE source='sibrowser'`)
		return e
	})
}

type record struct {
	Item     Item
	Input    *ingest.ExternalInput
	State    string
	Attempts int
}

func (l *lease) pending(limit int) ([]record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	rows, e := l.conn.Query(l.ctx, `SELECT metadata,input,state,attempts FROM source_items WHERE source='sibrowser' AND state IN ('discovered','downloaded','importing','imported','retryable') AND next_attempt<=clock_timestamp() ORDER BY discovered_at,external_id LIMIT $1`, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []record{}
	for rows.Next() {
		var r record
		var meta, in []byte
		if e = rows.Scan(&meta, &in, &r.State, &r.Attempts); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(meta, &r.Item); e != nil {
			return nil, e
		}
		if len(in) > 0 {
			if e = json.Unmarshal(in, &r.Input); e != nil {
				return nil, e
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (l *lease) save(r record, state, code, pack string, delay time.Duration) error {
	return l.transaction(func(tx pgx.Tx) error {
		var raw []byte
		if r.Input != nil {
			raw, _ = json.Marshal(r.Input)
		}
		_, e := tx.Exec(l.ctx, `UPDATE source_items SET input=COALESCE($2,input),state=$3,error=$4,pack_id=CASE WHEN $5='' THEN pack_id ELSE $5 END,attempts=$6,next_attempt=clock_timestamp()+($7*interval '1 second'),updated_at=clock_timestamp() WHERE source='sibrowser' AND external_id=$1`, r.Item.ID, raw, state, code, pack, r.Attempts, delay.Seconds())
		return e
	})
}
func (l *lease) retryRejected() error {
	return l.transaction(func(tx pgx.Tx) error {
		_, e := tx.Exec(l.ctx, `UPDATE source_items SET state='retryable',next_attempt=now(),error='' WHERE source='sibrowser' AND state='rejected'`)
		return e
	})
}
