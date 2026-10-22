-- name: GetDependencie :one
SELECT * FROM dependencies WHERE project_id = $1 AND id = $2;
