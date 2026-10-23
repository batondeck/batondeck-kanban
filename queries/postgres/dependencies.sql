-- The dependency area: canonical edges (blocker_id is the contract's FromTaskID, dependent_id its
-- ToTaskID) and the tasks' two caches. Lists are (ts, id) ascending, ids COLLATE "C", as bytes. A
-- list starts FROM its project or task, so one statement answers "missing" (no row) and "none"
-- (one NULL row).

-- name: FindEdge :one
SELECT project_id, id, blocker_id, dependent_id, type, created_by, ts FROM dependencies
WHERE project_id = $1 AND blocker_id = $2 AND dependent_id = $3 AND type = $4
ORDER BY ts, id COLLATE "C" LIMIT 1;

-- What domain.CheckCycle reads: every blocks edge of the project.
-- name: ListBlocksEdges :many
SELECT blocker_id, dependent_id FROM dependencies WHERE project_id = $1 AND type = 'blocks';

-- name: InsertEdge :one
INSERT INTO dependencies (project_id, id, blocker_id, dependent_id, type, created_by, ts) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING project_id, id, blocker_id, dependent_id, type, created_by, ts;

-- name: DeleteEdge :one
DELETE FROM dependencies WHERE project_id = $1 AND id = $2
RETURNING project_id, id, blocker_id, dependent_id, type, created_by, ts;

-- name: ListProjectEdges :many
SELECT p.id AS project_id, d.id, d.blocker_id, d.dependent_id, d.type, d.created_by, d.ts
FROM projects p LEFT JOIN dependencies d ON d.project_id = p.id
WHERE p.id = $1
ORDER BY d.ts, d.id COLLATE "C";

-- What the task waits on: the edges whose dependent is the task.
-- name: ListBlockerEdges :many
SELECT t.id AS task_id, d.id, d.blocker_id, d.dependent_id, d.type, d.created_by, d.ts
FROM tasks t LEFT JOIN dependencies d ON d.project_id = t.project_id AND d.dependent_id = t.id
WHERE t.project_id = $1 AND t.id = $2
ORDER BY d.ts, d.id COLLATE "C";

-- What waits on the task: the edges whose blocker is the task.
-- name: ListDependentEdges :many
SELECT t.id AS task_id, d.id, d.blocker_id, d.dependent_id, d.type, d.created_by, d.ts
FROM tasks t LEFT JOIN dependencies d ON d.project_id = t.project_id AND d.blocker_id = t.id
WHERE t.project_id = $1 AND t.id = $2
ORDER BY d.ts, d.id COLLATE "C";

-- The two caches alone: never the version, never updated_at (the bump is the caller's).
-- name: SetTaskCaches :execrows
UPDATE tasks SET blocked_by = $1, blocks = $2 WHERE project_id = $3 AND id = $4;
