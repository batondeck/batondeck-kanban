-- name: GetOauthClient :one
SELECT * FROM oauth_clients WHERE client_id = $1;

-- name: GetOauthCode :one
SELECT * FROM oauth_codes WHERE code_hash = $1;

-- name: GetOauthRefreshFamily :one
SELECT * FROM oauth_refresh_families WHERE id = $1;

-- name: GetOauthToken :one
SELECT * FROM oauth_tokens WHERE token_hash = $1;
