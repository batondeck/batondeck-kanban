-- name: AppendChange :one
INSERT INTO change_log (token_scope, project_id, kind, payload, ts) VALUES ($1, $2, $3, $4, $5)
RETURNING id;

-- The changes after since, oldest first.
-- name: ReadChanges :many
SELECT id, token_scope, project_id, kind, payload, ts FROM change_log
WHERE id > sqlc.arg(since)::bigint ORDER BY id LIMIT sqlc.arg(page_limit)::bigint;

-- One scope's changes after since, over change_log_scope (token_scope, id).
-- name: ReadChangesForScope :many
SELECT id, token_scope, project_id, kind, payload, ts FROM change_log
WHERE token_scope = sqlc.arg(token_scope) AND id > sqlc.arg(since)::bigint ORDER BY id LIMIT sqlc.arg(page_limit)::bigint;

-- At most page_limit changes older than before, the lowest ids first.
-- name: DeleteChangesOlderThan :execrows
DELETE FROM change_log WHERE id IN (
  SELECT c.id FROM change_log c WHERE c.ts < sqlc.arg(before)::bigint ORDER BY c.id LIMIT sqlc.arg(page_limit)::bigint);

-- The wake the bus's LISTEN (ListenChanges) hears. Issued by AppendChange in the change's own
-- transaction, so Postgres delivers it at commit, once per transaction however many rows it wrote.
-- name: NotifyChanges :exec
SELECT pg_notify('batondeck_changes', '');

-- name: ListenChanges :exec
LISTEN batondeck_changes;
