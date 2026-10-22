-- name: GetArtifact :one
SELECT * FROM artifacts WHERE project_id = $1 AND task_id = $2 AND id = $3;
