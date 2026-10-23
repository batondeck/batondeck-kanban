-- The parent's children in task-number order (T-2 before T-10), in one statement: no row is a
-- missing parent, one NULL row a parent with none.
-- name: ListSubtaskIDs :many
SELECT t.id AS parent_id, c.id
FROM tasks t LEFT JOIN tasks c ON c.project_id = t.project_id AND c.parent_task_id = t.id
WHERE t.project_id = ? AND t.id = ?
ORDER BY CAST(substr(c.id, 3) AS INTEGER);
