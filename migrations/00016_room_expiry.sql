-- +goose Up
ALTER TABLE rooms ADD COLUMN last_activity_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE rooms ADD COLUMN finished_at timestamptz;
-- Historic snapshots do not reliably record the first finish. Grant a fresh
-- idle/finished window at migration; the original creation hard limit remains.
UPDATE rooms SET finished_at=now() WHERE snapshot->>'phase'='finished';
CREATE INDEX rooms_expiry_idx ON rooms (created_at, last_activity_at);

-- +goose Down
DROP INDEX rooms_expiry_idx;
ALTER TABLE rooms DROP COLUMN finished_at;
ALTER TABLE rooms DROP COLUMN last_activity_at;
