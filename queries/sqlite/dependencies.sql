-- The dependency area: canonical edges (blocker_id is the contract's FromTaskID, dependent_id its
-- ToTaskID) and the tasks' two caches. Lists are (ts, id) ascending, as bytes. A list starts FROM
-- its project or task, so one statement answers "missing" (no row) and "none" (one NULL row).

-- name: FindEdge :one
SELECT project_id, id, blocker_id, dependent_id, type, created_by, ts FROM dependencies
WHERE project_id = ? AND blocker_id = ? AND dependent_id = ? AND type = ?
ORDER BY ts, id LIMIT 1;

-- What domain.CheckCycle reads: the project's live gates, the blocks edges whose blocker is still in
-- the dependent's blockedBy. An edge whose blocker reached DONE stays (upstream context) but gates
-- nothing, so it closes no cycle.
-- name: ListGatingEdges :many
SELECT d.blocker_id, d.dependent_id FROM dependencies d
JOIN tasks t ON t.project_id = d.project_id AND t.id = d.dependent_id
WHERE d.project_id = ? AND d.type = 'blocks'
  AND EXISTS (SELECT 1 FROM json_each(t.blocked_by) j WHERE j.value = d.blocker_id);

-- name: InsertEdge :one
INSERT INTO dependencies (project_id, id, blocker_id, dependent_id, type, created_by, ts) VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING project_id, id, blocker_id, dependent_id, type, created_by, ts;

-- name: DeleteEdge :one
DELETE FROM dependencies WHERE project_id = ? AND id = ?
RETURNING project_id, id, blocker_id, dependent_id, type, created_by, ts;

-- name: ListProjectEdges :many
SELECT p.id AS project_id, d.id, d.blocker_id, d.dependent_id, d.type, d.created_by, d.ts
FROM projects p LEFT JOIN dependencies d ON d.project_id = p.id
WHERE p.id = ?
ORDER BY d.ts, d.id;

-- What the task waits on: the edges whose dependent is the task.
-- name: ListBlockerEdges :many
SELECT t.id AS task_id, d.id, d.blocker_id, d.dependent_id, d.type, d.created_by, d.ts
FROM tasks t LEFT JOIN dependencies d ON d.project_id = t.project_id AND d.dependent_id = t.id
WHERE t.project_id = ? AND t.id = ?
ORDER BY d.ts, d.id;

-- What waits on the task: the edges whose blocker is the task.
-- name: ListDependentEdges :many
SELECT t.id AS task_id, d.id, d.blocker_id, d.dependent_id, d.type, d.created_by, d.ts
FROM tasks t LEFT JOIN dependencies d ON d.project_id = t.project_id AND d.blocker_id = t.id
WHERE t.project_id = ? AND t.id = ?
ORDER BY d.ts, d.id;

-- The two caches alone: never the version, never updated_at (the bump is the caller's).
-- name: SetTaskCaches :execrows
UPDATE tasks SET blocked_by = ?, blocks = ? WHERE project_id = ? AND id = ?;
