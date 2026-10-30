-- API tokens: the id is public (the lookup key), the secret is stored only as its SHA-256.

-- name: InsertToken :exec
INSERT INTO tokens (id, identity_id, subject, secret_hash, label, permissions, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: GetToken :one
SELECT id, identity_id, subject, secret_hash, label, permissions, created_at, expires_at, last_used_at, revoked_at
FROM tokens WHERE id = $1;

-- name: ListTokens :many
SELECT id, identity_id, subject, secret_hash, label, permissions, created_at, expires_at, last_used_at, revoked_at
FROM tokens ORDER BY created_at, id;

-- name: ListTokensByIdentity :many
SELECT id, identity_id, subject, secret_hash, label, permissions, created_at, expires_at, last_used_at, revoked_at
FROM tokens WHERE identity_id = $1 ORDER BY created_at, id;

-- name: RevokeToken :execrows
UPDATE tokens SET revoked_at = $1 WHERE id = $2 AND revoked_at IS NULL;

-- name: DeleteLapsedTokens :execrows
-- The retention sweep: tokens revoked or expired before the cutoff, oldest first, a page at a time,
-- except the oldest and the newest row (created_at, then id) of each (identity_id, label). The
-- table is also the label-ownership record, which reads every row of any status: each label an
-- identity carried keeps a row, and its earliest row stays, so who minted a label first does not
-- change (train clash T1). Ids compare COLLATE "C", as bytes, as SQLite's BINARY does.
DELETE FROM tokens WHERE id IN (
  SELECT t.id FROM tokens t
  WHERE (t.revoked_at < sqlc.arg(before)::bigint OR t.expires_at < sqlc.arg(before)::bigint)
    AND EXISTS (SELECT 1 FROM tokens n WHERE n.identity_id = t.identity_id AND n.label = t.label
      AND (n.created_at > t.created_at OR (n.created_at = t.created_at AND n.id COLLATE "C" > t.id)))
    AND EXISTS (SELECT 1 FROM tokens o WHERE o.identity_id = t.identity_id AND o.label = t.label
      AND (o.created_at < t.created_at OR (o.created_at = t.created_at AND o.id COLLATE "C" < t.id)))
  ORDER BY t.created_at, t.id COLLATE "C" LIMIT sqlc.arg(page_limit)::bigint);
