-- name: InsertIntegration :one
INSERT INTO integrations (project_id, id, name, source, enabled, board_id, column_id, auth, mapping, routing, lifecycle, policy, limits, version, created_by, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 1, $14, $15, $16)
RETURNING *;

-- A live integration: a tombstoned one (deleted_at set) is not found.
-- name: GetIntegration :one
SELECT * FROM integrations WHERE project_id = $1 AND id = $2 AND deleted_at IS NULL;

-- The project's integrations, newest first ((created_at, id) descending); tombstones only when
-- include_deleted is 1.
-- name: ListIntegrations :many
SELECT * FROM integrations
WHERE project_id = sqlc.arg(project_id)::text
  AND (sqlc.arg(include_deleted)::bigint = 1 OR deleted_at IS NULL)
ORDER BY created_at DESC, id COLLATE "C" DESC
LIMIT sqlc.arg(page_limit)::bigint;

-- The whole row as the caller merged it, compare-and-set on version; a tombstone is never updated.
-- name: UpdateIntegration :one
UPDATE integrations SET name = $1, enabled = $2, board_id = $3, column_id = $4, auth = $5, mapping = $6, routing = $7, lifecycle = $8, policy = $9, limits = $10, deleted_at = $11, version = version + 1, updated_at = $12
WHERE project_id = $13 AND id = $14 AND version = $15 AND deleted_at IS NULL
RETURNING *;

-- name: GetAlertState :one
SELECT * FROM alert_states WHERE project_id = $1 AND integration_id = $2 AND fingerprint_hash = $3;

-- A delivery is recorded once: a second insert of its id changes nothing and returns no row.
-- name: InsertDelivery :one
INSERT INTO deliveries (project_id, integration_id, id, delivery_id, ts, kind, result, task_id, error, normalized_event, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (project_id, integration_id, id) DO NOTHING
RETURNING *;

-- name: GetDelivery :one
SELECT * FROM deliveries WHERE project_id = $1 AND integration_id = $2 AND id = $3;

-- One page of an integration's deliveries, newest first ((ts, id) descending), strictly after the
-- cursor's (after_ts, after_id); kind '' is every kind.
-- name: ListDeliveries :many
SELECT * FROM deliveries
WHERE project_id = sqlc.arg(project_id)::text AND integration_id = sqlc.arg(integration_id)::text
  AND (sqlc.arg(kind)::text = '' OR kind = sqlc.arg(kind)::text)
  AND (ts < sqlc.arg(after_ts)::bigint
       OR (ts = sqlc.arg(after_ts)::bigint AND id COLLATE "C" < sqlc.arg(after_id)::text))
ORDER BY ts DESC, id COLLATE "C" DESC
LIMIT sqlc.arg(page_limit)::bigint;

-- name: PutDeliveryRawBody :exec
INSERT INTO delivery_raw_bodies (project_id, integration_id, delivery_id, body, expires_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (project_id, integration_id, delivery_id) DO UPDATE SET body = excluded.body, expires_at = excluded.expires_at;

-- name: GetDeliveryRawBody :one
SELECT * FROM delivery_raw_bodies WHERE project_id = $1 AND integration_id = $2 AND delivery_id = $3;

-- The bounded sweeps: at most page_limit rows expired at or before now, oldest expiry first. A
-- delivery's raw body goes with it (ON DELETE CASCADE).
-- name: DeleteExpiredDeliveries :execrows
DELETE FROM deliveries WHERE (project_id, integration_id, id) IN (
  SELECT d.project_id, d.integration_id, d.id FROM deliveries d WHERE d.expires_at <= sqlc.arg(now)::bigint
  ORDER BY d.expires_at, d.id COLLATE "C" LIMIT sqlc.arg(page_limit)::bigint);

-- name: DeleteExpiredDeliveryRawBodies :execrows
DELETE FROM delivery_raw_bodies WHERE (project_id, integration_id, delivery_id) IN (
  SELECT r.project_id, r.integration_id, r.delivery_id FROM delivery_raw_bodies r WHERE r.expires_at <= sqlc.arg(now)::bigint
  ORDER BY r.expires_at, r.delivery_id COLLATE "C" LIMIT sqlc.arg(page_limit)::bigint);
