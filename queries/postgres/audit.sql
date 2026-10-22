-- name: GetAuditLog :one
SELECT * FROM audit_log WHERE id = $1;

-- name: GetToolCall :one
SELECT * FROM tool_calls WHERE id = $1;

-- name: GetIdempotencyKey :one
SELECT * FROM idempotency_keys WHERE project_id = $1 AND identity = $2 AND key = $3;
