-- name: InsertIntegration :one
INSERT INTO integrations (project_id, id, name, source, enabled, board_id, column_id, auth, mapping, routing, lifecycle, policy, limits, version, created_by, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?)
RETURNING *;

-- A live integration: a tombstoned one (deleted_at set) is not found.
-- name: GetIntegration :one
SELECT * FROM integrations WHERE project_id = ? AND id = ? AND deleted_at IS NULL;

-- The one id-only lookup: POST /hooks/{id} resolves its project from the integration id, which is
-- unguessable and UNIQUE on its own. A tombstone is not found.
-- name: GetIntegrationByID :one
SELECT * FROM integrations WHERE id = ? AND deleted_at IS NULL;

-- The project's integrations, newest first ((created_at, id) descending); tombstones only when
-- include_deleted is 1.
-- name: ListIntegrations :many
SELECT * FROM integrations
WHERE project_id = CAST(sqlc.arg(project_id) AS TEXT)
  AND (CAST(sqlc.arg(include_deleted) AS INTEGER) = 1 OR deleted_at IS NULL)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- The whole row as the caller merged it, compare-and-set on version; a tombstone is never updated.
-- name: UpdateIntegration :one
UPDATE integrations SET name = ?, enabled = ?, board_id = ?, column_id = ?, auth = ?, mapping = ?, routing = ?, lifecycle = ?, policy = ?, limits = ?, deleted_at = ?, version = version + 1, updated_at = ?
WHERE project_id = ? AND id = ? AND version = ? AND deleted_at IS NULL
RETURNING *;

-- name: GetAlertState :one
SELECT * FROM alert_states WHERE project_id = ? AND integration_id = ? AND fingerprint_hash = ?;

-- A fingerprint's correlation row, written whole: inserted on the first event, replaced after.
-- name: PutAlertState :exec
INSERT INTO alert_states (project_id, integration_id, fingerprint_hash, fingerprint, task_id, occurrences, first_at, last_at, alert_status, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (project_id, integration_id, fingerprint_hash) DO UPDATE SET fingerprint = excluded.fingerprint, task_id = excluded.task_id,
  occurrences = excluded.occurrences, first_at = excluded.first_at, last_at = excluded.last_at, alert_status = excluded.alert_status, expires_at = excluded.expires_at;

-- A delivery is recorded once: a second insert of its id changes nothing and returns no row.
-- name: InsertDelivery :one
INSERT INTO deliveries (project_id, integration_id, id, delivery_id, ts, kind, result, task_id, error, normalized_event, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (project_id, integration_id, id) DO NOTHING
RETURNING *;

-- name: GetDelivery :one
SELECT * FROM deliveries WHERE project_id = ? AND integration_id = ? AND id = ?;

-- One page of an integration's deliveries, newest first ((ts, id) descending), strictly after the
-- cursor's (after_ts, after_id); kind '' is every kind.
-- name: ListDeliveries :many
SELECT * FROM deliveries
WHERE project_id = CAST(sqlc.arg(project_id) AS TEXT) AND integration_id = CAST(sqlc.arg(integration_id) AS TEXT)
  AND (CAST(sqlc.arg(kind) AS TEXT) = '' OR kind = CAST(sqlc.arg(kind) AS TEXT))
  AND (ts < CAST(sqlc.arg(after_ts) AS INTEGER)
       OR (ts = CAST(sqlc.arg(after_ts) AS INTEGER) AND id < CAST(sqlc.arg(after_id) AS TEXT)))
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: PutDeliveryRawBody :exec
INSERT INTO delivery_raw_bodies (project_id, integration_id, delivery_id, body, expires_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (project_id, integration_id, delivery_id) DO UPDATE SET body = excluded.body, expires_at = excluded.expires_at;

-- name: GetDeliveryRawBody :one
SELECT * FROM delivery_raw_bodies WHERE project_id = ? AND integration_id = ? AND delivery_id = ?;

-- The bounded sweeps: at most page_limit rows expired at or before now, oldest expiry first. A
-- delivery's raw body goes with it (ON DELETE CASCADE).
-- name: DeleteExpiredDeliveries :execrows
DELETE FROM deliveries WHERE (project_id, integration_id, id) IN (
  SELECT d.project_id, d.integration_id, d.id FROM deliveries d WHERE d.expires_at <= CAST(sqlc.arg(now) AS INTEGER)
  ORDER BY d.expires_at, d.id LIMIT sqlc.arg(page_limit));

-- name: DeleteExpiredDeliveryRawBodies :execrows
DELETE FROM delivery_raw_bodies WHERE (project_id, integration_id, delivery_id) IN (
  SELECT r.project_id, r.integration_id, r.delivery_id FROM delivery_raw_bodies r WHERE r.expires_at <= CAST(sqlc.arg(now) AS INTEGER)
  ORDER BY r.expires_at, r.delivery_id LIMIT sqlc.arg(page_limit));
