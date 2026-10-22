-- name: GetUser :one
SELECT * FROM users WHERE id = ?;

-- name: GetSession :one
SELECT * FROM sessions WHERE id_hash = ?;

-- name: GetToken :one
SELECT * FROM tokens WHERE id = ?;
