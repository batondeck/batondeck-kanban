-- name: GetTaskEvent :one
SELECT * FROM task_events WHERE project_id = $1 AND id = $2;

-- name: GetChangeLog :one
SELECT * FROM change_log WHERE id = $1;
