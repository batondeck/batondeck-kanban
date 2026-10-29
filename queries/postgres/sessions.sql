-- Browser sessions, keyed by the SHA-256 of the cookie's secret.

-- name: InsertSession :exec
INSERT INTO sessions (id_hash, user_id, csrf_token, created_at, last_seen_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetSession :one
SELECT id_hash, user_id, csrf_token, created_at, last_seen_at, expires_at
FROM sessions WHERE id_hash = $1;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id_hash = $1;

-- name: DeleteUserSessions :execrows
DELETE FROM sessions WHERE user_id = $1;
