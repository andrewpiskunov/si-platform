-- +goose Up
CREATE TABLE ingest_owner (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    epoch bigint NOT NULL DEFAULT 0
);
INSERT INTO ingest_owner(singleton) VALUES(true);

CREATE TABLE uploads (
    id text PRIMARY KEY CHECK (id ~ '^[0-9a-f]{32}$'),
    owner_id text NOT NULL REFERENCES account_users(id),
    credential_id text NOT NULL REFERENCES account_credentials(id),
    name text NOT NULL,
    size bigint NOT NULL CHECK (size > 0 AND size <= 262144000),
    offset_bytes bigint NOT NULL DEFAULT 0 CHECK (offset_bytes >= 0 AND offset_bytes <= size),
    status text NOT NULL DEFAULT 'uploading' CHECK (status IN ('uploading','queued','processing','ready','failed')),
    draft_id text NOT NULL DEFAULT '',
    error text NOT NULL DEFAULT '',
    lease_epoch bigint NOT NULL DEFAULT 0,
    staged_deleted boolean NOT NULL DEFAULT false,
    stored_keys text[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT now()+interval '24 hours'
);
CREATE INDEX uploads_active ON uploads(status,created_at) WHERE status IN ('uploading','queued','processing');
CREATE INDEX uploads_owner ON uploads(owner_id,created_at);
CREATE INDEX uploads_cleanup ON uploads(updated_at) WHERE NOT staged_deleted;
CREATE INDEX uploads_stored_keys ON uploads USING gin(stored_keys);
CREATE INDEX uploads_failed_objects ON uploads(updated_at) WHERE status='failed' AND cardinality(stored_keys)>0;

-- +goose Down
DROP TABLE uploads;
DROP TABLE ingest_owner;
