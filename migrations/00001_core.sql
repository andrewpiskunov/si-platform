-- +goose Up
CREATE TABLE rooms (
    id text PRIMARY KEY,
    snapshot jsonb NOT NULL,
    seq bigint NOT NULL CHECK (seq > 0),
    invite_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE sessions (
    token_hash text PRIMARY KEY,
    room_id text NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    participant_id text NOT NULL,
    UNIQUE (room_id, participant_id)
);
CREATE TABLE command_results (
    room_id text NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    actor_id text NOT NULL,
    command_id text NOT NULL,
    request_hash text NOT NULL,
    code text NOT NULL,
    seq bigint NOT NULL,
    PRIMARY KEY (room_id, actor_id, command_id)
);
CREATE TABLE game_events (
    room_id text NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    seq bigint NOT NULL,
    kind text NOT NULL,
    actor_id text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, seq)
);

-- +goose Down
DROP TABLE game_events;
DROP TABLE command_results;
DROP TABLE sessions;
DROP TABLE rooms;
