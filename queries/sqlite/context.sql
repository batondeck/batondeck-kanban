-- The context-item area. Every read starts FROM the task, so one statement answers both "no such
-- task" (no row) and "no items" (one row whose item columns are NULL).

-- An item on the task, in one statement: no row is a missing task.
-- name: InsertContextItem :one
INSERT INTO context_items (project_id, task_id, id, kind, body, author, ts)
SELECT t.project_id, t.id, CAST(sqlc.arg(id) AS TEXT), CAST(sqlc.arg(kind) AS TEXT), CAST(sqlc.arg(body) AS TEXT),
  CAST(sqlc.arg(author) AS TEXT), CAST(sqlc.arg(ts) AS INTEGER)
FROM tasks t WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
RETURNING project_id, task_id, id, kind, body, author, ts;

-- One page of the task's items, newest first ((ts, id) descending, as bytes), strictly after the
-- cursor's (after_ts, after_id); after_id '' is the first page.
-- name: ListContextItemsNewest :many
SELECT t.id AS task_id, c.id, c.kind, c.body, c.author, c.ts
FROM tasks t LEFT JOIN context_items c ON c.project_id = t.project_id AND c.task_id = t.id
  AND (CAST(sqlc.arg(after_id) AS TEXT) = '' OR c.ts < CAST(sqlc.arg(after_ts) AS INTEGER)
       OR (c.ts = CAST(sqlc.arg(after_ts) AS INTEGER) AND c.id < CAST(sqlc.arg(after_id) AS TEXT)))
WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
ORDER BY c.ts DESC, c.id DESC
LIMIT sqlc.arg(page_limit);

-- The same page oldest first.
-- name: ListContextItemsOldest :many
SELECT t.id AS task_id, c.id, c.kind, c.body, c.author, c.ts
FROM tasks t LEFT JOIN context_items c ON c.project_id = t.project_id AND c.task_id = t.id
  AND (CAST(sqlc.arg(after_id) AS TEXT) = '' OR c.ts > CAST(sqlc.arg(after_ts) AS INTEGER)
       OR (c.ts = CAST(sqlc.arg(after_ts) AS INTEGER) AND c.id > CAST(sqlc.arg(after_id) AS TEXT)))
WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
ORDER BY c.ts, c.id
LIMIT sqlc.arg(page_limit);

-- name: DeleteContextItemsByTask :execrows
DELETE FROM context_items WHERE project_id = ? AND task_id = ?;
