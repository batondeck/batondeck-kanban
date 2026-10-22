-- name: GetComment :one
SELECT * FROM comments WHERE project_id = ? AND task_id = ? AND id = ?;

-- name: GetFollowUp :one
SELECT * FROM follow_ups WHERE project_id = ? AND task_id = ? AND id = ?;

-- name: GetNotification :one
SELECT * FROM notifications WHERE identity_id = ? AND id = ?;
