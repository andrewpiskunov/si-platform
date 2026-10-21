-- +goose Up
CREATE TABLE retention_candidates (
 key text PRIMARY KEY CHECK (key ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 retry_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 attempts bigint NOT NULL DEFAULT 0,
 last_error text NOT NULL DEFAULT '' CHECK (last_error IN ('','delete_failed','delete_timeout'))
);
CREATE INDEX retention_candidates_retry ON retention_candidates(retry_at,key);
CREATE INDEX library_packs_retention ON library_packs(deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX themes_retention ON themes(deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX uploads_retention ON uploads(updated_at) WHERE staged_deleted AND status IN ('ready','failed');

-- +goose Down
DROP INDEX uploads_retention;
DROP INDEX themes_retention;
DROP INDEX library_packs_retention;
DROP TABLE retention_candidates;
