-- The comment area. Every read starts FROM the task, so one statement answers both "no such task"
-- (no row) and "no comments" (one row whose comment columns are NULL).

-- One row by its key.
-- name: GetComment :one
SELECT * FROM comments WHERE project_id = ? AND task_id = ? AND id = ?;

-- A comment on the task, in one statement: no row is a missing task. mentions is a JSON array.
-- name: InsertComment :one
INSERT INTO comments (project_id, task_id, id, author, body, parent_comment_id, mentions, ts)
SELECT t.project_id, t.id, CAST(sqlc.arg(id) AS TEXT), CAST(sqlc.arg(author) AS TEXT), CAST(sqlc.arg(body) AS TEXT),
  CAST(sqlc.narg(parent_comment_id) AS TEXT), CAST(sqlc.arg(mentions) AS TEXT), CAST(sqlc.arg(ts) AS INTEGER)
FROM tasks t WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
RETURNING *;

-- One page of the task's comments, oldest first ((ts, id) ascending, as bytes), strictly after the
-- cursor's (after_ts, after_id); after_id '' is the first page.
-- name: ListCommentsOldest :many
SELECT t.id AS task_id, c.id, c.author, c.body, c.parent_comment_id, c.mentions, c.ts
FROM tasks t LEFT JOIN comments c ON c.project_id = t.project_id AND c.task_id = t.id
  AND (CAST(sqlc.arg(after_id) AS TEXT) = '' OR c.ts > CAST(sqlc.arg(after_ts) AS INTEGER)
       OR (c.ts = CAST(sqlc.arg(after_ts) AS INTEGER) AND c.id > CAST(sqlc.arg(after_id) AS TEXT)))
WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
ORDER BY c.ts, c.id
LIMIT sqlc.arg(page_limit);

-- name: GetNotification :one
SELECT * FROM notifications WHERE identity_id = ? AND id = ?;
