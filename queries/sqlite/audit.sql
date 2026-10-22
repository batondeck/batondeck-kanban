-- name: GetAuditLog :one
SELECT * FROM audit_log WHERE id = ?;

-- name: GetToolCall :one
SELECT * FROM tool_calls WHERE id = ?;

-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys WHERE project_id = ? AND identity = ? AND key = ?;
