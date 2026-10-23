-- +goose Up
-- P-65, owner-approved 2026-10-23 (zone Z2): a memory row keeps its task_id after the task is
-- deleted. A project-scope row's task_id is provenance ("learned on T-3") and outlives the task, as
-- in conductor; the three task scopes are deleted by the service's cascade (DeleteMemoryByTask
-- before DeleteTask, R1.3), no longer by this key. memory's other keys (project, blob) stay.
ALTER TABLE memory DROP CONSTRAINT memory_project_id_task_id_fkey;

-- +goose Down
-- The key cannot come back over a row whose task is gone: such a task-scope row is one the old
-- key would have cascaded away, so it goes; a project-scope row keeps itself and loses its
-- provenance.
DELETE FROM memory m WHERE m.task_id IS NOT NULL AND m.scope IN ('task', 'agent', 'shared')
  AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.project_id = m.project_id AND t.id = m.task_id);
UPDATE memory m SET task_id = NULL WHERE m.task_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.project_id = m.project_id AND t.id = m.task_id);
ALTER TABLE memory ADD CONSTRAINT memory_project_id_task_id_fkey
  FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE;
