-- The follow-up area. Every page starts FROM the task, so one statement answers both "no such task"
-- (no row) and "no follow-ups" (one row whose follow-up columns are NULL). Ids order and compare
-- COLLATE "C", as bytes, as SQLite's BINARY does.

-- name: GetFollowUp :one
SELECT * FROM follow_ups WHERE project_id = $1 AND task_id = $2 AND id = $3;

-- A follow-up on the task, in one statement: no row is a missing task.
-- name: InsertFollowUp :one
INSERT INTO follow_ups (project_id, task_id, id, body, author, target, target_identity, status, reopened, ts)
SELECT t.project_id, t.id, sqlc.arg(id)::text, sqlc.arg(body)::text, sqlc.arg(author)::text,
  sqlc.narg(target)::text, sqlc.narg(target_identity)::text, 'open',
  sqlc.arg(reopened)::bigint, sqlc.arg(ts)::bigint
FROM tasks t WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
RETURNING *;

-- One page of the task's follow-ups, oldest first ((ts, id) ascending, as bytes), strictly after the
-- cursor's (after_ts, after_id); open_only 1 keeps the open ones.
-- name: ListFollowUpsOldest :many
SELECT t.id AS task_id, f.id, f.body, f.author, f.target, f.target_identity, f.status, f.reopened, f.ts
FROM tasks t LEFT JOIN follow_ups f ON f.project_id = t.project_id AND f.task_id = t.id
  AND (sqlc.arg(open_only)::bigint = 0 OR f.status = 'open')
  AND (sqlc.arg(after_id)::text = '' OR f.ts > sqlc.arg(after_ts)::bigint
       OR (f.ts = sqlc.arg(after_ts)::bigint AND f.id COLLATE "C" > sqlc.arg(after_id)::text))
WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
ORDER BY f.ts, f.id COLLATE "C"
LIMIT sqlc.arg(page_limit)::bigint;

-- The follow-up acknowledged: no row is a missing one. Acking an acked one is a no-op that answers it.
-- name: AckFollowUp :one
UPDATE follow_ups SET status = 'acked' WHERE project_id = $1 AND task_id = $2 AND id = $3
RETURNING *;
