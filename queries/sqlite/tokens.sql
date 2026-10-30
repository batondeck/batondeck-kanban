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
-- except the oldest and the newest row (created_at, then id) of each (identity_id, label). The
-- table is also the label-ownership record, which reads every row of any status: each label an
-- identity carried keeps a row, and its earliest row stays, so who minted a label first does not
-- change (train clash T1). A row's ranks within its (identity_id, label), oldest and newest first,
-- are read in one pass: there is no (identity_id, label) index, so a correlated "a newer row
-- exists" scanned the table once per row (review Advisory 1).
DELETE FROM tokens WHERE id IN (
  SELECT r.id FROM (
    SELECT t.id, t.created_at, t.revoked_at, t.expires_at,
      ROW_NUMBER() OVER (PARTITION BY t.identity_id, t.label ORDER BY t.created_at, t.id) AS oldest,
      ROW_NUMBER() OVER (PARTITION BY t.identity_id, t.label ORDER BY t.created_at DESC, t.id DESC) AS newest
    FROM tokens t) r
  WHERE r.oldest > 1 AND r.newest > 1
    AND (r.revoked_at < CAST(sqlc.arg(before) AS INTEGER) OR r.expires_at < CAST(sqlc.arg(before) AS INTEGER))
  ORDER BY r.created_at, r.id LIMIT sqlc.arg(page_limit));
