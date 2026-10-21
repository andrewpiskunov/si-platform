-- +goose Up
CREATE TABLE catalogue_imports (
 id text PRIMARY KEY,
 owner_id text NOT NULL REFERENCES account_users(id),
 namespace text NOT NULL,
 external_id text NOT NULL,
 request_hash text NOT NULL,
 sha256 text NOT NULL,
 name text NOT NULL,
 size bigint NOT NULL CHECK(size>0),
 source_url text NOT NULL,
 license text NOT NULL,
 attribution text NOT NULL,
 permission_basis text NOT NULL,
 -- Independent receipts survive upload and pack retention.
 upload_id text NOT NULL UNIQUE,
 pack_id text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(owner_id,namespace,external_id)
);
CREATE INDEX catalogue_imports_owner_pack ON catalogue_imports(owner_id,pack_id);
-- +goose Down
DROP TABLE catalogue_imports;
