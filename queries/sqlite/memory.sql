-- The memory area. A read judges the TTL at its own time (now): an entry whose expires_at is at or
-- before it is left out, before any sweep has run.

-- name: InsertMemory :one
INSERT INTO memory (id, project_id, task_id, scope, key, value, blob_sha256, author, ts, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, project_id, task_id, scope, key, value, blob_sha256, author, ts, expires_at;

-- name: BlobExists :one
SELECT CAST(EXISTS (SELECT 1 FROM blobs WHERE sha256 = ?) AS INTEGER) AS found;

-- ReadMemory in one statement, window first: the task's newest `window_limit` task, agent and
-- shared entries ((ts, id) descending, of every author, live or expired), and only then the TTL,
-- the scope and key filters and the agent scope's privacy. No row is a missing task; one row whose
-- entry columns are NULL is a task with nothing left after the filters.
-- name: ReadMemoryWindow :many
SELECT t.id AS task_id, m.id, m.scope, m.key, m.value, m.blob_sha256, m.author, m.ts, m.expires_at
FROM tasks t LEFT JOIN memory m ON m.id IN (
    SELECT w.id FROM memory w
    WHERE w.project_id = t.project_id AND w.task_id = t.id AND w.scope IN ('task', 'agent', 'shared')
    ORDER BY w.ts DESC, w.id DESC
    LIMIT sqlc.arg(window_limit))
  AND (m.expires_at IS NULL OR m.expires_at > CAST(sqlc.arg(now) AS INTEGER))
  AND (CAST(sqlc.arg(scope) AS TEXT) = '' OR m.scope = CAST(sqlc.arg(scope) AS TEXT))
  AND (CAST(sqlc.arg(key) AS TEXT) = '' OR m.key = CAST(sqlc.arg(key) AS TEXT))
  AND (m.scope <> 'agent' OR m.author = CAST(sqlc.arg(identity) AS TEXT))
WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
ORDER BY m.ts DESC, m.id DESC;

-- RecallMemory: the durable entries of one project (scope 'project') or one author (scope
-- 'agent_global'), newest first. With no key it takes the newest `page_limit` entries and then
-- leaves out the expired ones; with a key it takes the live entries of that key, at most
-- `page_limit`. Exact key or none: there is no FTS here (owner ruling 2026-10-23).
-- name: RecallMemory :many
SELECT w.id, w.project_id, w.task_id, w.scope, w.key, w.value, w.blob_sha256, w.author, w.ts, w.expires_at FROM (
  SELECT m.id, m.project_id, m.task_id, m.scope, m.key, m.value, m.blob_sha256, m.author, m.ts, m.expires_at FROM memory m
  WHERE m.scope = CAST(sqlc.arg(scope) AS TEXT)
    AND ((m.scope = 'project' AND m.project_id = CAST(sqlc.arg(project_id) AS TEXT))
         OR (m.scope = 'agent_global' AND m.author = CAST(sqlc.arg(identity) AS TEXT)))
    AND (CAST(sqlc.arg(key) AS TEXT) = ''
         OR (m.key = CAST(sqlc.arg(key) AS TEXT) AND (m.expires_at IS NULL OR m.expires_at > CAST(sqlc.arg(now) AS INTEGER))))
  ORDER BY m.ts DESC, m.id DESC
  LIMIT sqlc.arg(page_limit)
) w
WHERE w.expires_at IS NULL OR w.expires_at > CAST(sqlc.arg(now) AS INTEGER)
ORDER BY w.ts DESC, w.id DESC;

-- The bounded sweep: at most page_limit entries expired at or before now, oldest expiry first.
-- name: DeleteExpiredMemory :execrows
DELETE FROM memory WHERE id IN (
  SELECT m.id FROM memory m WHERE m.expires_at IS NOT NULL AND m.expires_at <= CAST(sqlc.arg(now) AS INTEGER)
  ORDER BY m.expires_at, m.id LIMIT sqlc.arg(page_limit));

-- The task's task, agent and shared entries; project entries naming it as provenance stay.
-- name: DeleteMemoryByTask :execrows
DELETE FROM memory WHERE project_id = ? AND task_id = ? AND scope IN ('task', 'agent', 'shared');
