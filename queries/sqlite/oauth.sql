-- name: GetOauthClient :one
SELECT * FROM oauth_clients WHERE client_id = ?;

-- name: GetOauthCode :one
SELECT * FROM oauth_codes WHERE code_hash = ?;

-- name: GetOauthRefreshFamily :one
SELECT * FROM oauth_refresh_families WHERE id = ?;

-- name: GetOauthToken :one
SELECT * FROM oauth_tokens WHERE token_hash = ?;
