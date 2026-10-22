-- T-n is last_task after the increment: one statement, so two callers can never read the same n.
-- name: NextTaskNumber :one
INSERT INTO project_counters (project_id, last_task) VALUES (?, 1)
ON CONFLICT (project_id) DO UPDATE SET last_task = project_counters.last_task + 1
RETURNING last_task;
