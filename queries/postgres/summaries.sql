-- name: GetSummary :one
SELECT * FROM summaries WHERE project_id = $1 AND task_id = $2;
