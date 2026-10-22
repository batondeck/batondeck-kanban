-- Local accounts. email holds the sign-in name; its UNIQUE constraint decides a duplicate.

-- name: InsertUser :execrows
INSERT INTO users (id, email, display_name, password_hash, created_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (email) DO NOTHING;

-- name: GetUser :one
SELECT id, email, display_name, password_hash, created_at, disabled_at
FROM users WHERE id = ?;

-- name: GetUserByEmail :one
SELECT id, email, display_name, password_hash, created_at, disabled_at
FROM users WHERE email = ?;

-- name: ListUsers :many
SELECT id, email, display_name, password_hash, created_at, disabled_at
FROM users ORDER BY created_at, id;

-- name: DisableUser :execrows
UPDATE users SET disabled_at = ? WHERE id = ? AND disabled_at IS NULL;

-- name: SetUserPassword :execrows
UPDATE users SET password_hash = ? WHERE id = ?;
