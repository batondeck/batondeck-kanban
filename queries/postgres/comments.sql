-- name: GetComment :one
SELECT * FROM comments WHERE project_id = $1 AND task_id = $2 AND id = $3;

-- name: GetFollowUp :one
SELECT * FROM follow_ups WHERE project_id = $1 AND task_id = $2 AND id = $3;

-- name: GetNotification :one
SELECT * FROM notifications WHERE identity_id = $1 AND id = $2;
