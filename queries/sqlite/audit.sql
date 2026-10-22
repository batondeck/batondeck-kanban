-- name: GetAuditLog :one
SELECT * FROM audit_log WHERE id = ?;

-- name: GetToolCall :one
SELECT * FROM tool_calls WHERE id = ?;
