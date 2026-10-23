-- The comment area. Every read starts FROM the task, so one statement answers both "no such task"
-- (no row) and "no comments" (one row whose comment columns are NULL). Ids order and compare
-- COLLATE "C", as bytes, as SQLite's BINARY does.

-- name: GetComment :one
SELECT * FROM comments WHERE project_id = $1 AND task_id = $2 AND id = $3;

-- A comment on the task, in one statement: no row is a missing task. mentions is a JSON array.
-- name: InsertComment :one
INSERT INTO comments (project_id, task_id, id, author, body, parent_comment_id, mentions, ts)
SELECT t.project_id, t.id, sqlc.arg(id)::text, sqlc.arg(author)::text, sqlc.arg(body)::text,
  sqlc.narg(parent_comment_id)::text, sqlc.arg(mentions)::text, sqlc.arg(ts)::bigint
FROM tasks t WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
RETURNING *;

-- One page of the task's comments, oldest first ((ts, id) ascending, as bytes), strictly after the
-- cursor's (after_ts, after_id); after_id '' is the first page.
-- name: ListCommentsOldest :many
SELECT t.id AS task_id, c.id, c.author, c.body, c.parent_comment_id, c.mentions, c.ts
FROM tasks t LEFT JOIN comments c ON c.project_id = t.project_id AND c.task_id = t.id
  AND (sqlc.arg(after_id)::text = '' OR c.ts > sqlc.arg(after_ts)::bigint
       OR (c.ts = sqlc.arg(after_ts)::bigint AND c.id COLLATE "C" > sqlc.arg(after_id)::text))
WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
ORDER BY c.ts, c.id COLLATE "C"
LIMIT sqlc.arg(page_limit)::bigint;

-- name: GetNotification :one
SELECT * FROM notifications WHERE identity_id = $1 AND id = $2;
