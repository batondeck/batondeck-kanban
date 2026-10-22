-- name: GetArtifact :one
SELECT * FROM artifacts WHERE project_id = ? AND task_id = ? AND id = ?;
