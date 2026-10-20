-- +goose Up
CREATE TABLE library_packs (
    id text PRIMARY KEY,
    owner_id text NOT NULL REFERENCES account_users(id),
    import_id text,
    current_revision integer NOT NULL CHECK (current_revision BETWEEN 1 AND 500),
    published_revision integer,
    pending_revision integer,
    blocked boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    UNIQUE(owner_id, import_id),
    CHECK (import_id IS NULL OR import_id ~ '^[0-9a-f]{32}$')
);
CREATE INDEX library_packs_owner ON library_packs(owner_id) WHERE deleted_at IS NULL;
CREATE TABLE library_revisions (
    pack_id text NOT NULL REFERENCES library_packs(id),
    revision integer NOT NULL CHECK (revision BETWEEN 1 AND 500),
    card jsonb NOT NULL,
    document jsonb NOT NULL,
    assets jsonb NOT NULL,
    original_key text NOT NULL DEFAULT '',
    issues jsonb NOT NULL,
    asset_issues jsonb NOT NULL DEFAULT '[]',
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','pending','published','rejected','withdrawn','blocked')),
    rights jsonb NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (pack_id, revision)
);
ALTER TABLE library_packs ADD FOREIGN KEY (id,current_revision) REFERENCES library_revisions(pack_id,revision) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE library_packs ADD FOREIGN KEY (id,published_revision) REFERENCES library_revisions(pack_id,revision) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE library_packs ADD FOREIGN KEY (id,pending_revision) REFERENCES library_revisions(pack_id,revision) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE library_decisions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    pack_id text NOT NULL,
    revision integer NOT NULL,
    moderator_id text NOT NULL REFERENCES account_users(id),
    decision text NOT NULL CHECK (decision IN ('approve','reject','block')),
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (pack_id,revision) REFERENCES library_revisions(pack_id,revision)
);
CREATE TABLE library_reports (
    id text PRIMARY KEY,
    pack_id text NOT NULL,
    revision integer NOT NULL,
    reporter_id text NOT NULL REFERENCES account_users(id),
    reason text NOT NULL,
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','resolved')),
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (pack_id,revision) REFERENCES library_revisions(pack_id,revision),
    UNIQUE (pack_id,revision,reporter_id)
);
CREATE INDEX library_reports_status ON library_reports(status,created_at);

-- +goose Down
DROP TABLE library_reports;
DROP TABLE library_decisions;
ALTER TABLE library_packs DROP CONSTRAINT library_packs_id_current_revision_fkey;
ALTER TABLE library_packs DROP CONSTRAINT library_packs_id_published_revision_fkey;
ALTER TABLE library_packs DROP CONSTRAINT library_packs_id_pending_revision_fkey;
DROP TABLE library_revisions;
DROP TABLE library_packs;
