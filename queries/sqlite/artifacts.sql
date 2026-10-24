-- The artifact area. Every read of a task's artifacts starts FROM the task, so one statement
-- answers both "no such task" (no row) and "no artifacts" (one row whose artifact columns are NULL).

-- One row by its key.
-- name: GetArtifact :one
SELECT * FROM artifacts WHERE project_id = ? AND task_id = ? AND id = ?;

-- An artifact on the task, in one statement: no row is a missing task.
-- name: InsertArtifact :one
INSERT INTO artifacts (project_id, task_id, id, kind, url, ref, repo, title, added_by, added_by_agent, ts)
SELECT t.project_id, t.id, CAST(sqlc.arg(id) AS TEXT), CAST(sqlc.arg(kind) AS TEXT), CAST(sqlc.narg(url) AS TEXT),
  CAST(sqlc.narg(ref) AS TEXT), CAST(sqlc.narg(repo) AS TEXT), CAST(sqlc.narg(title) AS TEXT),
  CAST(sqlc.arg(added_by) AS TEXT), CAST(sqlc.narg(added_by_agent) AS TEXT), CAST(sqlc.arg(ts) AS INTEGER)
FROM tasks t WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
RETURNING *;

-- Every artifact of the task, tombstones included, oldest first ((ts, id), as bytes). A task holds at
-- most 50 (the service's cap); the limit is a backstop.
-- name: ListArtifacts :many
SELECT t.id AS task_id, a.id, a.kind, a.url, a.ref, a.repo, a.title, a.added_by, a.added_by_agent, a.ts,
  a.retracted_by, a.retracted_by_agent, a.retracted_at, a.retracted_reason
FROM tasks t LEFT JOIN artifacts a ON a.project_id = t.project_id AND a.task_id = t.id
WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
ORDER BY a.ts, a.id
LIMIT 200;

-- The tombstone on a live artifact: no row is a missing or an already retracted one.
-- name: RetractArtifact :one
UPDATE artifacts SET retracted_by = CAST(sqlc.arg(retracted_by) AS TEXT), retracted_by_agent = CAST(sqlc.narg(retracted_by_agent) AS TEXT),
  retracted_at = CAST(sqlc.arg(retracted_at) AS INTEGER), retracted_reason = CAST(sqlc.arg(retracted_reason) AS TEXT)
WHERE project_id = sqlc.arg(project_id) AND task_id = sqlc.arg(task_id) AND id = sqlc.arg(id) AND retracted_at IS NULL
RETURNING *;

-- The tasks among task_ids that carry at least one live artifact (list_tasks missingArtifacts).
-- name: ListLiveArtifactTaskIDs :many
SELECT DISTINCT task_id FROM artifacts
WHERE project_id = sqlc.arg(project_id) AND retracted_at IS NULL AND task_id IN (sqlc.slice(task_ids));
