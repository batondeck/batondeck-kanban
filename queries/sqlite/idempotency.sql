-- Claims (project_id, identity, key) for this call: inserts it, or takes over an entry whose window
-- has ended. Returns no row while another call's entry is live; on Postgres a concurrent claim of
-- the same key waits here until the first transaction ends.
-- name: ClaimIdempotencyKey :one
INSERT INTO idempotency_keys (project_id, identity, key, result, created_at, expires_at)
VALUES (?, ?, ?, '', ?, ?)
ON CONFLICT (project_id, identity, key) DO UPDATE
SET result = '', created_at = excluded.created_at, expires_at = excluded.expires_at
WHERE idempotency_keys.expires_at <= excluded.created_at
RETURNING key;

-- name: GetIdempotencyKey :one
SELECT project_id, identity, key, result, created_at, expires_at
FROM idempotency_keys WHERE project_id = ? AND identity = ? AND key = ?;

-- name: SetIdempotencyResult :exec
UPDATE idempotency_keys SET result = ? WHERE project_id = ? AND identity = ? AND key = ?;

-- name: DeleteProjectIdempotencyKeys :exec
DELETE FROM idempotency_keys WHERE project_id = ?;

-- The retention sweep: the keys whose window ended at or before now (a replay can no longer find
-- them, and each holds a mutation's whole result), the oldest expiry first, a page at a time, through
-- idempotency_keys_expiry. A global key (create_project's) goes too.
-- name: DeleteExpiredIdempotencyKeys :execrows
DELETE FROM idempotency_keys WHERE (project_id, identity, key) IN (
  SELECT i.project_id, i.identity, i.key FROM idempotency_keys i
  WHERE i.expires_at <= CAST(sqlc.arg(now) AS INTEGER)
  ORDER BY i.expires_at, i.project_id, i.identity, i.key LIMIT sqlc.arg(page_limit));
