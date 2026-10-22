-- name: GetContextItem :one
SELECT * FROM context_items WHERE project_id = ? AND task_id = ? AND id = ?;

-- name: GetMemory :one
SELECT * FROM memory WHERE id = ?;
