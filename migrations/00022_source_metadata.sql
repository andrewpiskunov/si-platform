-- +goose Up
CREATE TABLE source_metadata (
 pack_id text PRIMARY KEY REFERENCES library_packs(id) ON DELETE CASCADE,
 external_id text NOT NULL UNIQUE,
 snapshot jsonb NOT NULL,
 summary jsonb NOT NULL,
 search_text text NOT NULL,
 tags jsonb NOT NULL DEFAULT '[]',
 parser_version integer NOT NULL DEFAULT 1,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 cover jsonb,
 cover_source text NOT NULL DEFAULT '',
 cover_upload text NOT NULL DEFAULT '',
 size_bytes bigint NOT NULL DEFAULT 0 CHECK(size_bytes>=0)
);
CREATE TABLE source_cover_receipts (
 upload_id text PRIMARY KEY REFERENCES uploads(id) ON DELETE CASCADE,
 pack_id text NOT NULL REFERENCES library_packs(id) ON DELETE CASCADE
);
ALTER TABLE source_items ADD COLUMN cover_sha256 text NOT NULL DEFAULT '', ADD COLUMN metadata_next_attempt timestamptz NOT NULL DEFAULT '1970-01-01',
 ADD COLUMN metadata_error text NOT NULL DEFAULT '',
 ADD COLUMN cover_next_attempt timestamptz NOT NULL DEFAULT '1970-01-01',
 ADD COLUMN cover_error text NOT NULL DEFAULT '',
 ADD COLUMN cover_upload text NOT NULL DEFAULT '',
 ADD COLUMN metadata_checked_at timestamptz,
 ADD COLUMN cover_checked_at timestamptz;
-- +goose StatementBegin
CREATE FUNCTION retire_source_cover() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO retention_candidates(key)
 SELECT value->>'key' FROM jsonb_each(COALESCE(NULLIF(OLD.cover,'null'::jsonb),'{}'::jsonb)) WHERE value->>'key' ~ '^[0-9a-f]{64}$'
 ON CONFLICT(key) DO UPDATE SET retry_at=LEAST(retention_candidates.retry_at,excluded.retry_at);
 RETURN OLD;
END $$;
-- +goose StatementEnd
CREATE TRIGGER retire_source_cover AFTER UPDATE OF cover OR DELETE ON source_metadata FOR EACH ROW EXECUTE FUNCTION retire_source_cover();
-- +goose Down
DROP TRIGGER retire_source_cover ON source_metadata;
DROP FUNCTION retire_source_cover();
DROP TABLE source_cover_receipts;
DROP TABLE source_metadata;
ALTER TABLE source_items DROP COLUMN cover_sha256,DROP COLUMN metadata_next_attempt,DROP COLUMN metadata_error,DROP COLUMN cover_next_attempt,DROP COLUMN cover_error,DROP COLUMN cover_upload,DROP COLUMN metadata_checked_at,DROP COLUMN cover_checked_at;
