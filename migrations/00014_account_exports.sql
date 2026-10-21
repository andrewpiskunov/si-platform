-- +goose Up
-- Future physical cleanup must skip all owner references while this lease is live.
CREATE TABLE account_export_leases (
 owner_id text PRIMARY KEY REFERENCES account_users(id),
 id text NOT NULL CHECK (id ~ '^[0-9a-f]{32}$'),
 expires_at timestamptz NOT NULL
);
CREATE INDEX account_export_leases_expiry ON account_export_leases(expires_at);

-- +goose Down
DROP TABLE account_export_leases;
