-- The OAuth 2.1 authorization server: registered clients, codes, refresh families and opaque
-- tokens. Codes and tokens are held by the SHA-256 of their value, never the value.

-- name: InsertOauthClient :exec
INSERT INTO oauth_clients (client_id, client_name, redirect_uris, scope, created_at)
VALUES (?, ?, ?, ?, ?);

-- name: GetOauthClient :one
SELECT client_id, client_name, redirect_uris, scope, created_at, last_used_at
FROM oauth_clients WHERE client_id = ?;

-- name: TouchOauthClient :execrows
UPDATE oauth_clients SET last_used_at = ? WHERE client_id = ?;

-- name: DeleteUnusedOauthClients :execrows
DELETE FROM oauth_clients WHERE last_used_at IS NULL AND created_at < ?;

-- name: InsertOauthCode :exec
INSERT INTO oauth_codes (code_hash, client_id, identity_id, redirect_uri, code_challenge, scope, resource, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetOauthCode :one
SELECT code_hash, client_id, identity_id, redirect_uri, code_challenge, scope, resource, created_at, expires_at, used_at
FROM oauth_codes WHERE code_hash = ?;

-- name: UseOauthCode :execrows
UPDATE oauth_codes SET used_at = ? WHERE code_hash = ? AND used_at IS NULL;

-- name: InsertOauthFamily :exec
INSERT INTO oauth_refresh_families (id, client_id, identity_id, scope, resource, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetOauthFamily :one
SELECT id, client_id, identity_id, scope, resource, created_at, revoked_at, revoked_reason
FROM oauth_refresh_families WHERE id = ?;

-- name: RevokeOauthFamily :execrows
UPDATE oauth_refresh_families SET revoked_at = ?, revoked_reason = ? WHERE id = ? AND revoked_at IS NULL;

-- name: InsertOauthToken :exec
INSERT INTO oauth_tokens (token_hash, kind, family_id, client_id, identity_id, scope, resource, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetOauthToken :one
SELECT t.token_hash, t.kind, t.family_id, t.client_id, t.identity_id, t.scope, t.resource,
       t.created_at, t.expires_at, t.used_at, t.revoked_at, f.revoked_at AS family_revoked_at
FROM oauth_tokens t JOIN oauth_refresh_families f ON f.id = t.family_id
WHERE t.token_hash = ?;

-- name: UseOauthToken :execrows
UPDATE oauth_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL;

-- name: DeleteExpiredOauthCodes :execrows
-- The retention sweep: codes that expired before the cutoff, used or not, oldest expiry first, a
-- page at a time. The client and the family the code started stay.
DELETE FROM oauth_codes WHERE code_hash IN (
  SELECT c.code_hash FROM oauth_codes c WHERE c.expires_at < CAST(sqlc.arg(before) AS INTEGER)
  ORDER BY c.expires_at, c.code_hash LIMIT sqlc.arg(page_limit));

-- name: DeleteExpiredOauthTokens :execrows
-- The retention sweep: access and refresh tokens that expired before the cutoff, whether used or
-- revoked and whatever their family's state, oldest expiry first, a page at a time. The family stays.
DELETE FROM oauth_tokens WHERE token_hash IN (
  SELECT t.token_hash FROM oauth_tokens t WHERE t.expires_at < CAST(sqlc.arg(before) AS INTEGER)
  ORDER BY t.expires_at, t.token_hash LIMIT sqlc.arg(page_limit));
