-- The task's summary in one statement: no row is a missing task, NULL columns a task with none.
-- name: GetSummary :one
SELECT t.id AS task_id, s.body, s.updated_by, s.updated_at
FROM tasks t LEFT JOIN summaries s ON s.project_id = t.project_id AND s.task_id = t.id
WHERE t.project_id = ? AND t.id = ?;

-- name: UpsertSummary :one
INSERT INTO summaries (project_id, task_id, body, updated_by, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (project_id, task_id) DO UPDATE SET body = excluded.body, updated_by = excluded.updated_by, updated_at = excluded.updated_at
RETURNING project_id, task_id, body, updated_by, updated_at;
