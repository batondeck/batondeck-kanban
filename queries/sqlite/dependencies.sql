-- name: GetDependencie :one
SELECT * FROM dependencies WHERE project_id = ? AND id = ?;
