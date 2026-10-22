-- name: GetSprint :one
SELECT * FROM sprints WHERE project_id = $1 AND id = $2;
