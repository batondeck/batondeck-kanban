-- name: GetBoard :one
SELECT * FROM boards WHERE project_id = $1 AND id = $2;

-- name: GetColumn :one
SELECT * FROM columns WHERE project_id = $1 AND board_id = $2 AND id = $3;
