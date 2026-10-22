-- name: GetAuditLog :one
SELECT * FROM audit_log WHERE id = $1;

-- name: GetToolCall :one
SELECT * FROM tool_calls WHERE id = $1;
