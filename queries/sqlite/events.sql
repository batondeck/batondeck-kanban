-- name: GetTaskEvent :one
SELECT * FROM task_events WHERE project_id = ? AND id = ?;

-- name: GetChangeLog :one
SELECT * FROM change_log WHERE id = ?;
