-- +goose Up
-- ADR-0005, owner-approved 2026-10-23 (zone Z2): five states. TODO replaces
-- READY and BACKLOG is gone, so no task is a draft. Every BACKLOG or READY task becomes TODO; on each
-- board the BACKLOG columns fold into its first READY column (lowest position, then id), which
-- keeps its name, unless that is the default "Ready" (then "To do"), and its WIP limit, and takes
-- their tasks (order keys kept) and their integrations; a board with no READY column keeps its
-- BACKLOG columns as TODO columns ("Backlog" renamed "To do"). Both status CHECKs become the five;
-- the old ones go first, since they refuse 'TODO'. Constraint names are Postgres's defaults for
-- 0001's inline CHECKs (internal/store/five_states_migration_test.go reads them back).
UPDATE tasks t SET column_id = (
    SELECT r.id FROM columns r WHERE r.project_id = t.project_id AND r.board_id = t.board_id AND r.status = 'READY'
    ORDER BY r.position, r.id LIMIT 1)
WHERE EXISTS (SELECT 1 FROM columns b WHERE b.project_id = t.project_id AND b.board_id = t.board_id AND b.id = t.column_id AND b.status = 'BACKLOG')
  AND EXISTS (SELECT 1 FROM columns r WHERE r.project_id = t.project_id AND r.board_id = t.board_id AND r.status = 'READY');
UPDATE integrations i SET column_id = (
    SELECT r.id FROM columns r WHERE r.project_id = i.project_id AND r.board_id = i.board_id AND r.status = 'READY'
    ORDER BY r.position, r.id LIMIT 1)
WHERE EXISTS (SELECT 1 FROM columns b WHERE b.project_id = i.project_id AND b.board_id = i.board_id AND b.id = i.column_id AND b.status = 'BACKLOG')
  AND EXISTS (SELECT 1 FROM columns r WHERE r.project_id = i.project_id AND r.board_id = i.board_id AND r.status = 'READY');
DELETE FROM columns c WHERE c.status = 'BACKLOG'
  AND EXISTS (SELECT 1 FROM columns r WHERE r.project_id = c.project_id AND r.board_id = c.board_id AND r.status = 'READY');
ALTER TABLE tasks DROP CONSTRAINT tasks_status_check;
ALTER TABLE columns DROP CONSTRAINT columns_status_check;
UPDATE tasks SET status = 'TODO' WHERE status IN ('BACKLOG', 'READY');
UPDATE columns SET name = 'To do' WHERE (status = 'READY' AND name = 'Ready') OR (status = 'BACKLOG' AND name = 'Backlog');
UPDATE columns SET status = 'TODO' WHERE status IN ('BACKLOG', 'READY');
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check CHECK (status IN ('TODO', 'IN_PROGRESS', 'BLOCKED', 'REVIEW', 'DONE'));
ALTER TABLE columns ADD CONSTRAINT columns_status_check CHECK (status IN ('TODO', 'IN_PROGRESS', 'BLOCKED', 'REVIEW', 'DONE'));

-- +goose Down
-- TODO maps back to READY and both CHECKs back to the six. It is lossy: no Backlog column comes
-- back (its tasks stay in To do, as READY), and a TODO column named "To do" is named "Ready" again.
ALTER TABLE tasks DROP CONSTRAINT tasks_status_check;
ALTER TABLE columns DROP CONSTRAINT columns_status_check;
UPDATE tasks SET status = 'READY' WHERE status = 'TODO';
UPDATE columns SET name = 'Ready' WHERE status = 'TODO' AND name = 'To do';
UPDATE columns SET status = 'READY' WHERE status = 'TODO';
ALTER TABLE tasks ADD CONSTRAINT tasks_status_check CHECK (status IN ('BACKLOG', 'READY', 'IN_PROGRESS', 'BLOCKED', 'REVIEW', 'DONE'));
ALTER TABLE columns ADD CONSTRAINT columns_status_check CHECK (status IN ('BACKLOG', 'READY', 'IN_PROGRESS', 'BLOCKED', 'REVIEW', 'DONE'));
