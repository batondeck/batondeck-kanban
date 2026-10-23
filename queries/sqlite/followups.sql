-- The follow-up area. Every page starts FROM the task, so one statement answers both "no such task"
-- (no row) and "no follow-ups" (one row whose follow-up columns are NULL).

-- name: GetFollowUp :one
SELECT * FROM follow_ups WHERE project_id = ? AND task_id = ? AND id = ?;

-- A follow-up on the task, in one statement: no row is a missing task.
-- name: InsertFollowUp :one
INSERT INTO follow_ups (project_id, task_id, id, body, author, target, target_identity, status, reopened, ts)
SELECT t.project_id, t.id, CAST(sqlc.arg(id) AS TEXT), CAST(sqlc.arg(body) AS TEXT), CAST(sqlc.arg(author) AS TEXT),
  CAST(sqlc.narg(target) AS TEXT), CAST(sqlc.narg(target_identity) AS TEXT), 'open',
  CAST(sqlc.arg(reopened) AS INTEGER), CAST(sqlc.arg(ts) AS INTEGER)
FROM tasks t WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
RETURNING *;

-- One page of the task's follow-ups, oldest first ((ts, id) ascending, as bytes), strictly after the
-- cursor's (after_ts, after_id); open_only 1 keeps the open ones.
-- name: ListFollowUpsOldest :many
SELECT t.id AS task_id, f.id, f.body, f.author, f.target, f.target_identity, f.status, f.reopened, f.ts
FROM tasks t LEFT JOIN follow_ups f ON f.project_id = t.project_id AND f.task_id = t.id
  AND (CAST(sqlc.arg(open_only) AS INTEGER) = 0 OR f.status = 'open')
  AND (CAST(sqlc.arg(after_id) AS TEXT) = '' OR f.ts > CAST(sqlc.arg(after_ts) AS INTEGER)
       OR (f.ts = CAST(sqlc.arg(after_ts) AS INTEGER) AND f.id > CAST(sqlc.arg(after_id) AS TEXT)))
WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
ORDER BY f.ts, f.id
LIMIT sqlc.arg(page_limit);

-- The follow-up acknowledged: no row is a missing one. Acking an acked one is a no-op that answers it.
-- name: AckFollowUp :one
UPDATE follow_ups SET status = 'acked' WHERE project_id = ? AND task_id = ? AND id = ?
RETURNING *;
