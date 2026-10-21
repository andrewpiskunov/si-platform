package ingest

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"siplatform/internal/blobrefs"
)

// BlobGCLockSQL is the transaction-scoped guard for object reservations and
// failed-import object cleanup. Future media/AI writers must acquire this guard
// before recording durable hash reservations. Writers without a reservation
// ledger must keep it through Put and committing a library revision reference.
// The lock key is hashtextextended(database || ':' || schema || ':si-ingest-blob-gc',0).
const BlobGCLockSQL = blobrefs.GuardSQL

func (s *Service) reserveObjects(ctx context.Context, id string, epoch int64, keys []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, BlobGCLockSQL); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE uploads SET stored_keys=ARRAY(SELECT DISTINCT unnest(stored_keys||$3::text[])) WHERE id=$1 AND lease_epoch=$2 AND status='processing'
 AND EXISTS(SELECT 1 FROM ingest_owner WHERE singleton=true AND epoch=$2)`, id, epoch, keys)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrUnavailable
	}
	return tx.Commit(ctx)
}

func (s *Service) cleanupOrphans(ctx context.Context) {
	dir, err := s.root.Open(".")
	if err != nil {
		return
	}
	defer dir.Close()
	cutoff := time.Now().Add(-24 * time.Hour)
	for ctx.Err() == nil {
		entries, err := dir.ReadDir(100)
		for _, entry := range entries {
			if !validID(entry.Name()) || !entry.Type().IsRegular() {
				continue
			}
			info, infoErr := entry.Info()
			if infoErr != nil || !info.ModTime().Before(cutoff) {
				continue
			}
			var known bool
			if checkErr := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM uploads WHERE id=$1)`, entry.Name()).Scan(&known); checkErr != nil {
				return
			}
			if !known {
				_ = s.root.Remove(entry.Name())
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			return
		}
	}
}

type failedObject struct{ id, key string }

func (s *Service) cleanupObjects(ctx context.Context, epoch int64) {
	// Fixed work budget bounds database memory and remote Delete calls per pass.
	rows, err := s.pool.Query(ctx, `SELECT id,k FROM uploads CROSS JOIN LATERAL unnest(stored_keys) AS k
 WHERE status='failed' AND updated_at<clock_timestamp()-interval '1 hour' ORDER BY updated_at,id LIMIT 100`)
	if err != nil {
		return
	}
	pending := make([]failedObject, 0, 100)
	for rows.Next() {
		var object failedObject
		if err = rows.Scan(&object.id, &object.key); err != nil {
			break
		}
		pending = append(pending, object)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		return
	}
	for _, object := range pending {
		if ctx.Err() != nil {
			return
		}
		if err = s.cleanupObject(ctx, epoch, object); err != nil {
			return
		}
	}
}
func (s *Service) cleanupObject(ctx context.Context, epoch int64, object failedObject) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, BlobGCLockSQL); err != nil {
		return err
	}
	var current int64
	if err = tx.QueryRow(ctx, `SELECT epoch FROM ingest_owner WHERE singleton=true FOR SHARE`).Scan(&current); err != nil {
		return err
	}
	if current != epoch {
		return ErrUnavailable
	}
	var owner string
	if err = tx.QueryRow(ctx, `SELECT owner_id FROM uploads WHERE id=$1`, object.id).Scan(&owner); errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM account_users WHERE id=$1 FOR UPDATE`, owner); err != nil {
		return err
	}
	var exporting bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM account_export_leases WHERE owner_id=$1 AND expires_at>clock_timestamp())`, owner).Scan(&exporting); err != nil {
		return err
	}
	if exporting {
		return nil
	}
	var keys []string
	err = tx.QueryRow(ctx, `SELECT stored_keys FROM uploads WHERE id=$1 AND status='failed' AND updated_at<clock_timestamp()-interval '1 hour' FOR UPDATE`, object.id).Scan(&keys)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	found := false
	for _, key := range keys {
		if key == object.key {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	refs, err := blobrefs.Check(ctx, tx, object.key)
	if err != nil {
		return err
	}
	if refs.Active {
		return nil
	} // Keep the failed ledger entry until active work resolves.
	// Transfer the reservation to a durable GC candidate before removing it.
	// The collector rechecks all owners under the same guard; no remote Delete
	// runs while this transaction holds the account lock.
	if _, err = tx.Exec(ctx, `INSERT INTO retention_candidates(key) VALUES($1) ON CONFLICT(key) DO NOTHING`, object.key); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE uploads SET stored_keys=array_remove(stored_keys,$2) WHERE id=$1`, object.id, object.key); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
