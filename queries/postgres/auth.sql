-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetSession :one
SELECT * FROM sessions WHERE id_hash = $1;

-- name: GetToken :one
SELECT * FROM tokens WHERE id = $1;
