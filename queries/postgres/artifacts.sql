-- The artifact area. Every read of a task's artifacts starts FROM the task, so one statement
-- answers both "no such task" (no row) and "no artifacts" (one row whose artifact columns are NULL).
-- Ids order COLLATE "C", as bytes, as SQLite's BINARY does.

-- One row by its key.
-- name: GetArtifact :one
SELECT * FROM artifacts WHERE project_id = $1 AND task_id = $2 AND id = $3;

-- An artifact on the task, in one statement: no row is a missing task.
-- name: InsertArtifact :one
INSERT INTO artifacts (project_id, task_id, id, kind, url, ref, repo, title, added_by, added_by_agent, ts)
SELECT t.project_id, t.id, sqlc.arg(id)::text, sqlc.arg(kind)::text, sqlc.narg(url)::text,
  sqlc.narg(ref)::text, sqlc.narg(repo)::text, sqlc.narg(title)::text,
  sqlc.arg(added_by)::text, sqlc.narg(added_by_agent)::text, sqlc.arg(ts)::bigint
FROM tasks t WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
RETURNING *;

-- Every artifact of the task, tombstones included, oldest first ((ts, id), as bytes). A task holds at
-- most 50 (the service's cap); the limit is a backstop.
-- name: ListArtifacts :many
SELECT t.id AS task_id, a.id, a.kind, a.url, a.ref, a.repo, a.title, a.added_by, a.added_by_agent, a.ts,
  a.retracted_by, a.retracted_by_agent, a.retracted_at, a.retracted_reason
FROM tasks t LEFT JOIN artifacts a ON a.project_id = t.project_id AND a.task_id = t.id
WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
ORDER BY a.ts, a.id COLLATE "C"
LIMIT 200;

-- The tombstone on a live artifact: no row is a missing or an already retracted one.
-- name: RetractArtifact :one
UPDATE artifacts SET retracted_by = sqlc.arg(retracted_by)::text, retracted_by_agent = sqlc.narg(retracted_by_agent)::text,
  retracted_at = sqlc.arg(retracted_at)::bigint, retracted_reason = sqlc.arg(retracted_reason)::text
WHERE project_id = sqlc.arg(project_id) AND task_id = sqlc.arg(task_id) AND id = sqlc.arg(id) AND retracted_at IS NULL
RETURNING *;

-- The tasks among task_ids that carry at least one live artifact (list_tasks missingArtifacts).
-- name: ListLiveArtifactTaskIDs :many
SELECT DISTINCT task_id FROM artifacts
WHERE project_id = sqlc.arg(project_id) AND retracted_at IS NULL AND task_id = ANY(sqlc.arg(task_ids)::text[]);
