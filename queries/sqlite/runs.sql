-- name: GetRun :one
SELECT * FROM runs WHERE project_id = ? AND run_task_id = ?;
