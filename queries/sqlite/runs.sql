-- A run's ordinal among its parent's runs; a second insert of one run task changes nothing.
-- name: InsertRun :execrows
INSERT INTO runs (project_id, run_task_id, parent_task_id, ordinal, opened_by, opened_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (project_id, run_task_id) DO NOTHING;

-- The parent's run rows, ordinal ascending, then run task id as bytes.
-- name: ListRuns :many
SELECT * FROM runs WHERE project_id = ? AND parent_task_id = ? ORDER BY ordinal, run_task_id LIMIT 200;
