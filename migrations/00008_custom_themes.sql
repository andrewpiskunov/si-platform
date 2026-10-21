-- +goose Up
ALTER TABLE uploads DROP CONSTRAINT uploads_purpose_check;
ALTER TABLE uploads DROP CONSTRAINT uploads_media_purpose;
ALTER TABLE uploads ADD COLUMN result_theme jsonb;
ALTER TABLE uploads ADD COLUMN diagnostic_issues jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(diagnostic_issues)='array');
ALTER TABLE uploads ADD CONSTRAINT uploads_purpose_check CHECK (purpose IN ('siq','media','theme'));
ALTER TABLE uploads ADD CONSTRAINT uploads_media_purpose CHECK (
    (purpose='siq' AND media_kind='' AND result_asset IS NULL AND result_theme IS NULL)
    OR (purpose='media' AND media_kind<>'' AND size<=104857600 AND result_theme IS NULL)
    OR (purpose='theme' AND media_kind='' AND size<=5242880 AND result_asset IS NULL)
);

CREATE TABLE themes (
    id text PRIMARY KEY CHECK (id ~ '^[0-9a-f]{32}$'),
    owner_id text NOT NULL REFERENCES account_users(id),
    current_revision integer NOT NULL CHECK (current_revision BETWEEN 1 AND 100),
    blocked boolean NOT NULL DEFAULT false,
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX themes_owner ON themes(owner_id,updated_at);
CREATE TABLE theme_versions (
    theme_id text NOT NULL REFERENCES themes(id),
    revision integer NOT NULL CHECK (revision BETWEEN 1 AND 100),
    prepared jsonb NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes>0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(theme_id,revision)
);
CREATE TABLE theme_receipts (
    upload_id text PRIMARY KEY REFERENCES uploads(id),
    theme_id text NOT NULL,
    revision integer NOT NULL,
    FOREIGN KEY(theme_id,revision) REFERENCES theme_versions(theme_id,revision)
);
CREATE TABLE theme_moderation (
    id bigserial PRIMARY KEY,
    theme_id text NOT NULL REFERENCES themes(id),
    actor_id text NOT NULL REFERENCES account_users(id),
    blocked boolean NOT NULL,
    reason text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE room_theme_pins (
    room_id text PRIMARY KEY REFERENCES rooms(id),
    theme_id text NOT NULL,
    revision integer NOT NULL,
    hash text NOT NULL CHECK (hash ~ '^[0-9a-f]{64}$'),
    FOREIGN KEY(theme_id,revision) REFERENCES theme_versions(theme_id,revision)
);

-- +goose Down
DROP TABLE room_theme_pins;
DROP TABLE theme_moderation;
DROP TABLE theme_receipts;
DROP TABLE theme_versions;
DROP TABLE themes;
ALTER TABLE uploads DROP CONSTRAINT uploads_media_purpose;
ALTER TABLE uploads DROP CONSTRAINT uploads_purpose_check;
DELETE FROM uploads WHERE purpose='theme';
ALTER TABLE uploads DROP COLUMN diagnostic_issues;
ALTER TABLE uploads DROP COLUMN result_theme;
ALTER TABLE uploads ADD CONSTRAINT uploads_purpose_check CHECK (purpose IN ('siq','media'));
ALTER TABLE uploads ADD CONSTRAINT uploads_media_purpose CHECK (
    (purpose='siq' AND media_kind='' AND result_asset IS NULL)
    OR (purpose='media' AND media_kind<>'' AND size<=104857600)
);
