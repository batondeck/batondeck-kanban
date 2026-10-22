-- name: CreateProject :exec
INSERT INTO projects (id, name, settings, review_enabled, retry_policy, created_by, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetProject :one
SELECT id, name, settings, review_enabled, retry_policy, created_by, created_at
FROM projects WHERE id = ?;

-- The row a settings change reads and then writes: on Postgres it is locked to the transaction.
-- name: LockProject :one
SELECT id, name, settings, review_enabled, retry_policy, created_by, created_at
FROM projects WHERE id = ?;

-- name: ListProjectsForIdentity :many
SELECT p.id, p.name, p.settings, p.review_enabled, p.retry_policy, p.created_by, p.created_at, m.role
FROM projects p JOIN project_members m ON m.project_id = p.id
WHERE m.identity_id = ?
ORDER BY p.created_at, p.id;

-- name: UpdateProjectSettings :exec
UPDATE projects SET settings = ?, review_enabled = ?, retry_policy = ? WHERE id = ?;

-- Everything the project owns goes with it by ON DELETE CASCADE, except the rows the caller deletes
-- itself (idempotency keys) and those kept as the trace (audit_log, tool_calls, change_log).
-- name: DeleteProject :execrows
DELETE FROM projects WHERE id = ?;

-- name: GetCapabilityGrant :one
SELECT * FROM capability_grants WHERE project_id = ? AND identity_id = ?;
