-- +goose Up
-- A single snapshot remains authoritative. Every writer, including legacy
-- recovery and invite rotation, updates discovery atomically with that snapshot.
ALTER TABLE rooms ADD COLUMN visibility text
    GENERATED ALWAYS AS (COALESCE(snapshot->'settings'->>'visibility','private')) STORED
    CHECK (visibility IN ('private','public'));
CREATE INDEX rooms_public_unfinished_idx ON rooms(id)
    WHERE visibility='public' AND snapshot->>'phase'<>'finished';

-- +goose Down
DROP INDEX rooms_public_unfinished_idx;
ALTER TABLE rooms DROP COLUMN visibility;
