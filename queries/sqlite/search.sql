-- name: SearchTasks :many
SELECT t.project_id, t.id FROM tasks_fts f JOIN tasks t ON t.rid = f.rid
WHERE f.search_body MATCH ? AND t.project_id = ?;
