-- name: SearchTasks :many
SELECT project_id, id FROM tasks
WHERE search @@ plainto_tsquery('simple', $1) AND project_id = $2;
