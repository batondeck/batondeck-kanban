-- name: GetContextItem :one
SELECT * FROM context_items WHERE project_id = $1 AND task_id = $2 AND id = $3;

-- name: GetMemory :one
SELECT * FROM memory WHERE id = $1;
