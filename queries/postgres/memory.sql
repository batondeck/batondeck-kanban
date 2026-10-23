-- The memory area. A read judges the TTL at its own time (now): an entry whose expires_at is at or
-- before it is left out, before any sweep has run. Ids order COLLATE "C", as bytes.

-- name: InsertMemory :one
INSERT INTO memory (id, project_id, task_id, scope, key, value, blob_sha256, author, ts, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id, project_id, task_id, scope, key, value, blob_sha256, author, ts, expires_at;

-- name: BlobExists :one
SELECT EXISTS (SELECT 1 FROM blobs WHERE sha256 = $1)::int8 AS found;

-- ReadMemory in one statement, window first: the task's newest `window_limit` task, agent and
-- shared entries ((ts, id) descending, of every author, live or expired), and only then the TTL,
-- the scope and key filters and the agent scope's privacy. No row is a missing task; one row whose
-- entry columns are NULL is a task with nothing left after the filters.
-- name: ReadMemoryWindow :many
SELECT t.id AS task_id, m.id, m.scope, m.key, m.value, m.blob_sha256, m.author, m.ts, m.expires_at
FROM tasks t LEFT JOIN memory m ON m.id IN (
    SELECT w.id FROM memory w
    WHERE w.project_id = t.project_id AND w.task_id = t.id AND w.scope IN ('task', 'agent', 'shared')
    ORDER BY w.ts DESC, w.id COLLATE "C" DESC
    LIMIT sqlc.arg(window_limit)::bigint)
  AND (m.expires_at IS NULL OR m.expires_at > sqlc.arg(now)::bigint)
  AND (sqlc.arg(scope)::text = '' OR m.scope = sqlc.arg(scope)::text)
  AND (sqlc.arg(key)::text = '' OR m.key = sqlc.arg(key)::text)
  AND (m.scope <> 'agent' OR m.author = sqlc.arg(identity)::text)
WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
ORDER BY m.ts DESC, m.id COLLATE "C" DESC;

-- RecallMemory: the durable entries of one project (scope 'project') or one author (scope
-- 'agent_global'), newest first. With no key it takes the newest `page_limit` entries and then
-- leaves out the expired ones; with a key it takes the live entries of that key, at most
-- `page_limit`. Exact key or none: there is no FTS here (owner ruling 2026-10-23).
-- name: RecallMemory :many
SELECT w.id, w.project_id, w.task_id, w.scope, w.key, w.value, w.blob_sha256, w.author, w.ts, w.expires_at FROM (
  SELECT m.id, m.project_id, m.task_id, m.scope, m.key, m.value, m.blob_sha256, m.author, m.ts, m.expires_at FROM memory m
  WHERE m.scope = sqlc.arg(scope)::text
    AND ((m.scope = 'project' AND m.project_id = sqlc.arg(project_id)::text)
         OR (m.scope = 'agent_global' AND m.author = sqlc.arg(identity)::text))
    AND (sqlc.arg(key)::text = ''
         OR (m.key = sqlc.arg(key)::text AND (m.expires_at IS NULL OR m.expires_at > sqlc.arg(now)::bigint)))
  ORDER BY m.ts DESC, m.id COLLATE "C" DESC
  LIMIT sqlc.arg(page_limit)::bigint
) w
WHERE w.expires_at IS NULL OR w.expires_at > sqlc.arg(now)::bigint
ORDER BY w.ts DESC, w.id COLLATE "C" DESC;

-- The bounded sweep: at most page_limit entries expired at or before now, oldest expiry first.
-- name: DeleteExpiredMemory :execrows
DELETE FROM memory WHERE id IN (
  SELECT m.id FROM memory m WHERE m.expires_at IS NOT NULL AND m.expires_at <= sqlc.arg(now)::bigint
  ORDER BY m.expires_at, m.id COLLATE "C" LIMIT sqlc.arg(page_limit)::bigint);

-- The task's task, agent and shared entries; project entries naming it as provenance stay.
-- name: DeleteMemoryByTask :execrows
DELETE FROM memory WHERE project_id = $1 AND task_id = $2 AND scope IN ('task', 'agent', 'shared');
