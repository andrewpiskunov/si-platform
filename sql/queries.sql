-- name: LockCreate :exec
SELECT pg_advisory_xact_lock(1902026001);

-- name: CountRooms :one
SELECT count(*) FROM rooms;

-- name: CreateRoom :exec
INSERT INTO rooms (id, snapshot, seq, invite_hash) VALUES ($1, $2, $3, $4);

-- name: GetRoom :one
SELECT * FROM rooms WHERE id = $1;

-- name: LockRoom :one
SELECT * FROM rooms WHERE id = $1 FOR UPDATE;

-- name: UpdateRoom :exec
UPDATE rooms SET snapshot = $2, seq = $3 WHERE id = $1;

-- name: ListRooms :many
SELECT id FROM rooms ORDER BY id;

-- name: AddSession :exec
INSERT INTO sessions (token_hash, room_id, participant_id) VALUES ($1, $2, $3);

-- name: GetSession :one
SELECT participant_id FROM sessions WHERE token_hash = $1 AND room_id = $2;

-- name: FindCommand :one
SELECT * FROM command_results WHERE room_id = $1 AND actor_id = $2 AND command_id = $3;

-- name: SaveCommand :exec
INSERT INTO command_results (room_id, actor_id, command_id, request_hash, code, seq)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: AddEvent :exec
INSERT INTO game_events (room_id, seq, kind, actor_id, payload) VALUES ($1, $2, $3, $4, $5);
