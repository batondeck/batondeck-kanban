-- Local accounts. email holds the sign-in name; its UNIQUE constraint decides a duplicate.

-- name: InsertUser :execrows
INSERT INTO users (id, email, display_name, password_hash, created_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (email) DO NOTHING;

-- name: GetUser :one
SELECT id, email, display_name, password_hash, created_at, disabled_at
FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT id, email, display_name, password_hash, created_at, disabled_at
FROM users WHERE email = $1;

-- name: ListUsers :many
SELECT id, email, display_name, password_hash, created_at, disabled_at
FROM users ORDER BY created_at, id;

-- name: DisableUser :execrows
UPDATE users SET disabled_at = $1 WHERE id = $2 AND disabled_at IS NULL;

-- name: SetUserPassword :execrows
UPDATE users SET password_hash = $1 WHERE id = $2;
