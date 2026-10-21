-- +goose Up
CREATE TABLE mcp_receipts (
 owner_id text NOT NULL REFERENCES account_users(id),
 idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 16 AND 128),
 operation text NOT NULL CHECK(operation IN ('create_draft','upsert_questions')),
 request_hash text NOT NULL,
 -- No pack FK: a material purge must not make an old create retry recreate it.
 pack_id text NOT NULL,
 result_revision integer NOT NULL CHECK(result_revision>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(owner_id,idempotency_key)
);
CREATE INDEX mcp_receipts_pack ON mcp_receipts(owner_id,pack_id);
-- +goose Down
DROP TABLE mcp_receipts;
