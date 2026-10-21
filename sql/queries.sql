-- name: LockCreate :exec
SELECT pg_advisory_xact_lock(1902026001);

-- name: LockOwner :one
SELECT epoch FROM engine_owner WHERE singleton = true FOR SHARE;

-- name: CountRooms :one
SELECT count(*) FROM rooms;

-- name: CreateRoom :exec
INSERT INTO rooms (id, snapshot, seq, invite_hash) VALUES ($1, $2, $3, $4);

-- name: GetRoom :one
SELECT * FROM rooms WHERE id = $1;

-- name: LockRoom :one
SELECT * FROM rooms WHERE id = $1 FOR UPDATE;

-- name: UpdateRoom :exec
UPDATE rooms SET snapshot = $2, seq = $3,
 last_activity_at = CASE WHEN sqlc.arg(human_activity)::boolean THEN statement_timestamp() ELSE last_activity_at END,
 finished_at = CASE WHEN sqlc.arg(finished)::boolean THEN COALESCE(finished_at,statement_timestamp()) ELSE finished_at END
 WHERE id = $1;

-- name: ListRooms :many
SELECT id FROM rooms ORDER BY id;

-- name: AddSession :exec
INSERT INTO sessions (token_hash, room_id, participant_id) VALUES ($1, $2, $3);

-- name: GetSession :one
SELECT s.participant_id FROM sessions s JOIN rooms r ON r.id=s.room_id
WHERE s.token_hash=$1 AND s.room_id=$2 AND EXISTS (
 SELECT 1 FROM jsonb_array_elements(r.snapshot->'participants') p
 WHERE p->>'id'=s.participant_id AND NOT COALESCE((p->>'removed')::boolean,false)
);

-- name: FindCommand :one
SELECT * FROM command_results WHERE room_id = $1 AND actor_id = $2 AND command_id = $3;

-- name: SaveCommand :exec
INSERT INTO command_results (room_id, actor_id, command_id, request_hash, code, seq)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: AddEvent :exec
INSERT INTO game_events (room_id, seq, kind, actor_id, payload) VALUES ($1, $2, $3, $4, $5);

-- name: SetInvite :exec
UPDATE rooms SET invite_hash = $2 WHERE id = $1;

-- name: RoomEvents :many
SELECT seq,kind,actor_id,payload,created_at FROM game_events
WHERE room_id=$1 AND seq>$2 AND seq<=$3 ORDER BY seq LIMIT $4;

-- name: BindRoomGuest :execrows
UPDATE sessions SET guest_hash = $3 WHERE room_id = $1 AND token_hash = $2 AND (guest_hash IS NULL OR guest_hash = $3);

-- name: RoomGuestHash :one
SELECT guest_hash FROM sessions WHERE room_id = $1 AND participant_id = $2;

-- name: RoomGuestParticipants :many
SELECT participant_id FROM sessions WHERE room_id = $1 AND guest_hash = $2 ORDER BY participant_id;

-- name: RoomGuestBanned :one
SELECT EXISTS(SELECT 1 FROM room_guest_bans WHERE room_id = $1 AND guest_hash = $2)::boolean;

-- name: BanRoomGuest :exec
INSERT INTO room_guest_bans(room_id,guest_hash) VALUES($1,$2) ON CONFLICT DO NOTHING;

-- name: RevokeRoomParticipants :exec
DELETE FROM sessions WHERE room_id = $1 AND participant_id = ANY(sqlc.arg(ids)::text[]);

-- name: ArchiveRoomParticipants :exec
INSERT INTO room_participants(room_id, participant_id, name, role, score, removed)
SELECT sqlc.arg(room_id)::text, p.id, p.name, p.role, p.score, COALESCE(p.removed,false)
FROM jsonb_to_recordset(sqlc.arg(participants)::jsonb) AS p(id text,name text,role text,score bigint,removed boolean)
ON CONFLICT(room_id,participant_id) DO UPDATE
SET name=EXCLUDED.name,role=EXCLUDED.role,score=EXCLUDED.score,removed=EXCLUDED.removed
WHERE (room_participants.name,room_participants.role,room_participants.score,room_participants.removed)
 IS DISTINCT FROM (EXCLUDED.name,EXCLUDED.role,EXCLUDED.score,EXCLUDED.removed);

-- name: RoomRoster :many
SELECT participant_id,name,role,score,removed FROM room_participants
WHERE room_id = $1 AND participant_id > sqlc.arg(after_id)::text
ORDER BY participant_id LIMIT $2;

-- name: RoomRosterNames :many
SELECT participant_id,name,role,score,removed FROM room_participants
WHERE room_id = $1 AND (NOT removed OR participant_id = ANY(sqlc.arg(ids)::text[]))
ORDER BY participant_id;
