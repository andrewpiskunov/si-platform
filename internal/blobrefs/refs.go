// Package blobrefs defines the single global reference check used by object GC.
package blobrefs

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// GuardSQL must also be held by writers before reserving or publishing objects.
const GuardSQL = `SELECT pg_advisory_xact_lock(hashtextextended(current_database()||':'||current_schema()||':si-ingest-blob-gc',0))`

type ReferenceState struct{ Content, Active, Ready bool }

// Check includes direct source keys and every prepared variant, including upload
// result references even when an older writer omitted them from stored_keys.
func Check(ctx context.Context, tx pgx.Tx, key string) (s ReferenceState, err error) {
	err = tx.QueryRow(ctx, `SELECT
 EXISTS(SELECT 1 FROM library_revisions WHERE original_key=$1 OR jsonb_path_exists(assets,'$.** ? (@.key == $key || @.original_key == $key)',jsonb_build_object('key',$1::text)))
 OR EXISTS(SELECT 1 FROM theme_versions WHERE jsonb_path_exists(prepared,'$.** ? (@.key == $key || @.original_key == $key)',jsonb_build_object('key',$1::text))),
 EXISTS(SELECT 1 FROM uploads WHERE (status IN ('uploading','queued','processing') OR EXISTS(SELECT 1 FROM account_export_leases l WHERE l.owner_id=uploads.owner_id AND l.expires_at>clock_timestamp())) AND (stored_keys @> ARRAY[$1]::text[] OR jsonb_path_exists(result_asset,'$.** ? (@.key == $key || @.original_key == $key)',jsonb_build_object('key',$1::text)) OR jsonb_path_exists(result_theme,'$.** ? (@.key == $key || @.original_key == $key)',jsonb_build_object('key',$1::text)))),
 EXISTS(SELECT 1 FROM uploads WHERE status='ready' AND (stored_keys @> ARRAY[$1]::text[] OR jsonb_path_exists(result_asset,'$.** ? (@.key == $key || @.original_key == $key)',jsonb_build_object('key',$1::text)) OR jsonb_path_exists(result_theme,'$.** ? (@.key == $key || @.original_key == $key)',jsonb_build_object('key',$1::text))))`, key).Scan(&s.Content, &s.Active, &s.Ready)
	return
}
