-- name: GetBoard :one
SELECT * FROM boards WHERE project_id = ? AND id = ?;

-- name: GetColumn :one
SELECT * FROM columns WHERE project_id = ? AND board_id = ? AND id = ?;
