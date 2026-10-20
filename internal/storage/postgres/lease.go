package postgres

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"siplatform/internal/game"
	"siplatform/internal/storage/db"
)

type Lease struct {
	done   chan struct{}
	cancel context.CancelFunc
	once   sync.Once
}

func (l *Lease) Done() <-chan struct{} { return l.done }
func (l *Lease) Close()                { l.once.Do(l.cancel); <-l.done }

// Own excludes another game process in this database/schema. Epoch fencing
// also rejects a former owner's writes before its heartbeat detects a lost lock.
func (s *Store) Own(ctx context.Context) (*Lease, error) {
	conn, err := pgx.ConnectConfig(ctx, s.pool.Config().ConnConfig.Copy())
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Lease, error) {
		c, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = conn.Close(c)
		return nil, err
	}
	var acquired bool
	err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended(current_database() || ':' || current_schema() || ':si-platform-owner',0))`).Scan(&acquired)
	if err != nil {
		return fail(err)
	}
	if !acquired {
		return fail(game.Fault("already_running"))
	}
	var epoch int64
	if err = conn.QueryRow(ctx, "UPDATE engine_owner SET epoch=epoch+1 WHERE singleton=true RETURNING epoch").Scan(&epoch); err != nil {
		return fail(err)
	}
	s.epoch.Store(epoch)
	lctx, cancel := context.WithCancel(ctx)
	l := &Lease{done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(l.done)
		defer func() {
			c, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = conn.Close(c)
		}()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-lctx.Done():
				return
			case <-tick.C:
				pctx, cancel := context.WithTimeout(lctx, 2*time.Second)
				err := conn.Ping(pctx)
				cancel()
				if err != nil {
					return
				}
			}
		}
	}()
	return l, nil
}
func (s *Store) checkOwner(ctx context.Context, q *db.Queries) error {
	epoch, err := q.LockOwner(ctx)
	if err != nil {
		return err
	}
	if epoch != s.epoch.Load() {
		return game.Fault("ownership_lost")
	}
	return nil
}
