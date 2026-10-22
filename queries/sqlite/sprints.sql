-- name: GetSprint :one
SELECT * FROM sprints WHERE project_id = ? AND id = ?;
