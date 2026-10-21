-- +goose Up
ALTER TABLE sessions ADD COLUMN guest_hash text CHECK (guest_hash ~ '^[a-f0-9]{64}$');
CREATE INDEX sessions_guest_idx ON sessions(room_id, guest_hash) WHERE guest_hash IS NOT NULL;
CREATE TABLE room_guest_bans (
 room_id text NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
 guest_hash text NOT NULL CHECK (guest_hash ~ '^[a-f0-9]{64}$'),
 PRIMARY KEY(room_id,guest_hash)
);
CREATE TABLE room_participants (
 room_id text NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
 participant_id text NOT NULL,
 name text NOT NULL,
 role text NOT NULL CHECK (role IN ('host','player','spectator')),
 score bigint NOT NULL,
 removed boolean NOT NULL DEFAULT false,
 PRIMARY KEY(room_id,participant_id)
);
INSERT INTO room_participants(room_id,participant_id,name,role,score,removed)
 SELECT r.id,p->>'id',p->>'name',p->>'role',COALESCE((p->>'score')::bigint,0),COALESCE((p->>'removed')::boolean,false)
 FROM rooms r CROSS JOIN LATERAL jsonb_array_elements(r.snapshot->'participants') p;
-- +goose Down
DROP TABLE room_participants;
DROP TABLE room_guest_bans;
DROP INDEX sessions_guest_idx;
ALTER TABLE sessions DROP COLUMN guest_hash;
