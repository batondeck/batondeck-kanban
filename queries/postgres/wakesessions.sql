-- The wake-session area: the device session directory's rows (R0.25). A put replaces every field of
-- an existing id but created_at.
-- name: PutWakeSession :exec
INSERT INTO wake_sessions (id, identity_id, agent_id, socket_path, socket_token_sealed, last_cursor, created_at, last_seen_at, expires_at, pid, role, client_session_id, proc_start, bearer_sealed)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (id) DO UPDATE SET identity_id = excluded.identity_id, agent_id = excluded.agent_id,
  socket_path = excluded.socket_path, socket_token_sealed = excluded.socket_token_sealed,
  last_cursor = excluded.last_cursor, last_seen_at = excluded.last_seen_at, expires_at = excluded.expires_at,
  pid = excluded.pid, role = excluded.role, client_session_id = excluded.client_session_id,
  proc_start = excluded.proc_start, bearer_sealed = excluded.bearer_sealed;

-- The live session: one whose expires_at is after now.
-- name: GetWakeSession :one
SELECT id, identity_id, agent_id, socket_path, socket_token_sealed, last_cursor, created_at, last_seen_at, expires_at, pid, role, client_session_id, proc_start, bearer_sealed
FROM wake_sessions WHERE id = $1 AND expires_at > $2;

-- Every row, expired or not, oldest first: the directory re-validates each at start-up.
-- name: ListWakeSessions :many
SELECT id, identity_id, agent_id, socket_path, socket_token_sealed, last_cursor, created_at, last_seen_at, expires_at, pid, role, client_session_id, proc_start, bearer_sealed
FROM wake_sessions ORDER BY created_at, id;

-- The last change-log id delivered to the session, and when.
-- name: SetWakeSessionCursor :execrows
UPDATE wake_sessions SET last_cursor = $1, last_seen_at = $2 WHERE id = $3;

-- name: DeleteWakeSession :execrows
DELETE FROM wake_sessions WHERE id = $1;
