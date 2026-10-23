-- The OAuth 2.1 authorization server: registered clients, codes, refresh families and opaque
-- tokens. Codes and tokens are held by the SHA-256 of their value, never the value.

-- name: InsertOauthClient :exec
INSERT INTO oauth_clients (client_id, client_name, redirect_uris, scope, created_at)
VALUES ($1, $2, $3, $4, $5);

-- name: GetOauthClient :one
SELECT client_id, client_name, redirect_uris, scope, created_at, last_used_at
FROM oauth_clients WHERE client_id = $1;

-- name: TouchOauthClient :execrows
UPDATE oauth_clients SET last_used_at = $1 WHERE client_id = $2;

-- name: DeleteUnusedOauthClients :execrows
DELETE FROM oauth_clients WHERE last_used_at IS NULL AND created_at < $1;

-- name: InsertOauthCode :exec
INSERT INTO oauth_codes (code_hash, client_id, identity_id, redirect_uri, code_challenge, scope, resource, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetOauthCode :one
SELECT code_hash, client_id, identity_id, redirect_uri, code_challenge, scope, resource, created_at, expires_at, used_at
FROM oauth_codes WHERE code_hash = $1;

-- name: UseOauthCode :execrows
UPDATE oauth_codes SET used_at = $1 WHERE code_hash = $2 AND used_at IS NULL;

-- name: InsertOauthFamily :exec
INSERT INTO oauth_refresh_families (id, client_id, identity_id, scope, resource, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetOauthFamily :one
SELECT id, client_id, identity_id, scope, resource, created_at, revoked_at, revoked_reason
FROM oauth_refresh_families WHERE id = $1;

-- name: RevokeOauthFamily :execrows
UPDATE oauth_refresh_families SET revoked_at = $1, revoked_reason = $2 WHERE id = $3 AND revoked_at IS NULL;

-- name: InsertOauthToken :exec
INSERT INTO oauth_tokens (token_hash, kind, family_id, client_id, identity_id, scope, resource, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetOauthToken :one
SELECT t.token_hash, t.kind, t.family_id, t.client_id, t.identity_id, t.scope, t.resource,
       t.created_at, t.expires_at, t.used_at, t.revoked_at, f.revoked_at AS family_revoked_at
FROM oauth_tokens t JOIN oauth_refresh_families f ON f.id = t.family_id
WHERE t.token_hash = $1;

-- name: UseOauthToken :execrows
UPDATE oauth_tokens SET used_at = $1 WHERE token_hash = $2 AND used_at IS NULL;
