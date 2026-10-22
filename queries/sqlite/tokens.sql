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
