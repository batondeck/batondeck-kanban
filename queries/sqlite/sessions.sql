-- Browser sessions, keyed by the SHA-256 of the cookie's secret.

-- name: InsertSession :exec
INSERT INTO sessions (id_hash, user_id, csrf_token, created_at, last_seen_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetSession :one
SELECT id_hash, user_id, csrf_token, created_at, last_seen_at, expires_at
FROM sessions WHERE id_hash = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id_hash = ?;

-- name: DeleteUserSessions :execrows
DELETE FROM sessions WHERE user_id = ?;

-- name: DeleteExpiredSessions :execrows
-- The retention sweep: sessions that expired before the cutoff, oldest expiry first, a page at a time.
DELETE FROM sessions WHERE id_hash IN (
  SELECT s.id_hash FROM sessions s WHERE s.expires_at < CAST(sqlc.arg(before) AS INTEGER)
  ORDER BY s.expires_at, s.id_hash LIMIT sqlc.arg(page_limit));
