-- name: GetSummary :one
SELECT * FROM summaries WHERE project_id = ? AND task_id = ?;
