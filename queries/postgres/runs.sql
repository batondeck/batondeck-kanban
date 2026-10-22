-- name: GetRun :one
SELECT * FROM runs WHERE project_id = $1 AND run_task_id = $2;
