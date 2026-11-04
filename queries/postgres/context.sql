-- The context-item area. Every read starts FROM the task, so one statement answers both "no such
-- task" (no row) and "no items" (one row whose item columns are NULL). Ids order and compare
-- COLLATE "C", as bytes, as SQLite's BINARY does.

-- An item on the task, in one statement: no row is a missing task.
-- ts is the given time, or one past the task's newest item's when that is not earlier, so a task's
-- items are strictly increasing in ts in the order they were added (store.Context.AddContextItem).
-- name: InsertContextItem :one
INSERT INTO context_items (project_id, task_id, id, kind, body, author, ts)
SELECT t.project_id, t.id, sqlc.arg(id)::text, sqlc.arg(kind)::text, sqlc.arg(body)::text,
  sqlc.arg(author)::text,
  GREATEST(sqlc.arg(ts)::bigint,
           COALESCE((SELECT MAX(c.ts) + 1 FROM context_items c WHERE c.project_id = t.project_id AND c.task_id = t.id), 0))
FROM tasks t WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
RETURNING project_id, task_id, id, kind, body, author, ts;

-- One page of the task's items, newest first ((ts, id) descending, as bytes), strictly after the
-- cursor's (after_ts, after_id); after_id '' is the first page.
-- name: ListContextItemsNewest :many
SELECT t.id AS task_id, c.id, c.kind, c.body, c.author, c.ts
FROM tasks t LEFT JOIN context_items c ON c.project_id = t.project_id AND c.task_id = t.id
  AND (sqlc.arg(after_id)::text = '' OR c.ts < sqlc.arg(after_ts)::bigint
       OR (c.ts = sqlc.arg(after_ts)::bigint AND c.id COLLATE "C" < sqlc.arg(after_id)::text))
WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
ORDER BY c.ts DESC, c.id COLLATE "C" DESC
LIMIT sqlc.arg(page_limit)::bigint;

-- The same page oldest first.
-- name: ListContextItemsOldest :many
SELECT t.id AS task_id, c.id, c.kind, c.body, c.author, c.ts
FROM tasks t LEFT JOIN context_items c ON c.project_id = t.project_id AND c.task_id = t.id
  AND (sqlc.arg(after_id)::text = '' OR c.ts > sqlc.arg(after_ts)::bigint
       OR (c.ts = sqlc.arg(after_ts)::bigint AND c.id COLLATE "C" > sqlc.arg(after_id)::text))
WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
ORDER BY c.ts, c.id COLLATE "C"
LIMIT sqlc.arg(page_limit)::bigint;

-- name: DeleteContextItemsByTask :execrows
DELETE FROM context_items WHERE project_id = $1 AND task_id = $2;
