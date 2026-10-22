-- name: GetWakeSession :one
SELECT * FROM wake_sessions WHERE id = $1;
