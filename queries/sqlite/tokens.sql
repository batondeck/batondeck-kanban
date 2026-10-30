-- API tokens: the id is public (the lookup key), the secret is stored only as its SHA-256.

-- name: InsertToken :exec
INSERT INTO tokens (id, identity_id, subject, secret_hash, label, permissions, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetToken :one
SELECT id, identity_id, subject, secret_hash, label, permissions, created_at, expires_at, last_used_at, revoked_at
FROM tokens WHERE id = ?;

-- name: ListTokens :many
SELECT id, identity_id, subject, secret_hash, label, permissions, created_at, expires_at, last_used_at, revoked_at
FROM tokens ORDER BY created_at, id;

-- name: ListTokensByIdentity :many
SELECT id, identity_id, subject, secret_hash, label, permissions, created_at, expires_at, last_used_at, revoked_at
FROM tokens WHERE identity_id = ? ORDER BY created_at, id;

-- name: RevokeToken :execrows
UPDATE tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL;

-- name: DeleteLapsedTokens :execrows
-- The retention sweep: tokens revoked or expired before the cutoff, oldest first, a page at a time,
-- except the newest row (created_at, then id) of each (identity_id, label). The table is also the
-- label-ownership record, which reads every row of any status, so each label an identity carried
-- keeps one row.
DELETE FROM tokens WHERE id IN (
  SELECT t.id FROM tokens t
  WHERE (t.revoked_at < CAST(sqlc.arg(before) AS INTEGER) OR t.expires_at < CAST(sqlc.arg(before) AS INTEGER))
    AND EXISTS (SELECT 1 FROM tokens n WHERE n.identity_id = t.identity_id AND n.label = t.label
      AND (n.created_at > t.created_at OR (n.created_at = t.created_at AND n.id > t.id)))
  ORDER BY t.created_at, t.id LIMIT sqlc.arg(page_limit));
