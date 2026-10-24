-- The settings area: an identity's own preferences on its identities row. NULL is unset.

-- name: GetIdentitySettings :one
SELECT id, max_ticket_tokens, token_policy, starred_assets FROM identities WHERE id = $1;

-- name: PutIdentitySettings :exec
INSERT INTO identities (id, kind, display, max_ticket_tokens, token_policy, starred_assets)
VALUES (sqlc.arg(id), sqlc.arg(kind), sqlc.arg(display), sqlc.narg(max_ticket_tokens), sqlc.narg(token_policy), sqlc.arg(starred_assets))
ON CONFLICT (id) DO UPDATE SET max_ticket_tokens = excluded.max_ticket_tokens, token_policy = excluded.token_policy,
  starred_assets = excluded.starred_assets;
