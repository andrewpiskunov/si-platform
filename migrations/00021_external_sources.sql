-- +goose Up
CREATE TABLE external_sources (
 source text PRIMARY KEY CHECK(source='sibrowser'),
 owner_id text NOT NULL UNIQUE REFERENCES account_users(id),
 pack_limit integer NOT NULL CHECK(pack_limit BETWEEN 1 AND 100000),
 storage_limit bigint NOT NULL CHECK(storage_limit>0 AND storage_limit<=1125899906842624),
 next_page integer NOT NULL DEFAULT 1 CHECK(next_page>0),
 bootstrap_complete boolean NOT NULL DEFAULT false,
 epoch bigint NOT NULL DEFAULT 0
);
ALTER TABLE catalogue_imports ADD COLUMN origin text NOT NULL DEFAULT '' CHECK(origin IN ('','external_import'));
ALTER TABLE catalogue_imports ADD COLUMN published_once boolean NOT NULL DEFAULT false;
CREATE UNIQUE INDEX catalogue_external_identity ON catalogue_imports(namespace,external_id) WHERE origin='external_import';
CREATE TABLE source_items (
 source text NOT NULL REFERENCES external_sources(source),
 external_id text NOT NULL,
 metadata jsonb NOT NULL,
 input jsonb,
 state text NOT NULL DEFAULT 'discovered' CHECK(state IN ('discovered','downloaded','importing','imported','published','retryable','rejected','suppressed')),
 attempts integer NOT NULL DEFAULT 0,
 next_attempt timestamptz NOT NULL DEFAULT now(),
 error text NOT NULL DEFAULT '',
 pack_id text NOT NULL DEFAULT '',
 discovered_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(source,external_id)
);
CREATE INDEX source_items_pending ON source_items(source,next_attempt) WHERE state IN ('discovered','downloaded','importing','imported','retryable');
-- +goose Down
DROP TABLE source_items;
DROP INDEX catalogue_external_identity;
ALTER TABLE catalogue_imports DROP COLUMN published_once, DROP COLUMN origin;
DROP TABLE external_sources;
