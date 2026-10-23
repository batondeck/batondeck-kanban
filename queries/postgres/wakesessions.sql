-- The wake-session area. A put replaces every field of an existing id but created_at.
-- name: PutWakeSession :exec
INSERT INTO wake_sessions (id, identity_id, agent_id, socket_path, socket_token_sealed, last_cursor, created_at, last_seen_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET identity_id = excluded.identity_id, agent_id = excluded.agent_id,
  socket_path = excluded.socket_path, socket_token_sealed = excluded.socket_token_sealed,
  last_cursor = excluded.last_cursor, last_seen_at = excluded.last_seen_at, expires_at = excluded.expires_at;

-- The live session: one whose expires_at is after now.
-- name: GetWakeSession :one
SELECT id, identity_id, agent_id, socket_path, socket_token_sealed, last_cursor, created_at, last_seen_at, expires_at
FROM wake_sessions WHERE id = $1 AND expires_at > $2;

-- name: DeleteWakeSession :execrows
DELETE FROM wake_sessions WHERE id = $1;
