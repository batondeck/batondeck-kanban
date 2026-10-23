-- name: GetTaskEvent :one
SELECT * FROM task_events WHERE project_id = $1 AND id = $2;

-- name: GetChangeLog :one
SELECT * FROM change_log WHERE id = $1;

-- A task's event, on the task's board, in one statement: no row is a missing task or another board
-- (board_id '' takes the task's), which the caller tells apart.
-- name: AppendTaskEvent :one
INSERT INTO task_events (project_id, id, board_id, task_id, actor, agent, type, data, ts)
SELECT t.project_id, sqlc.arg(id)::text, t.board_id, t.id, sqlc.arg(actor)::text, sqlc.narg(agent)::text,
  sqlc.arg(type)::text, sqlc.arg(data)::text, sqlc.arg(ts)::bigint
FROM tasks t
WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
  AND (sqlc.arg(board_id)::text = '' OR t.board_id = sqlc.arg(board_id)::text)
RETURNING project_id, id, board_id, task_id, actor, agent, type, data, ts;

-- A board's event with no task, in one statement: no row is a board that is not the project's.
-- name: AppendBoardEvent :one
INSERT INTO task_events (project_id, id, board_id, task_id, actor, agent, type, data, ts)
SELECT b.project_id, sqlc.arg(id)::text, b.id, NULL, sqlc.arg(actor)::text, sqlc.narg(agent)::text,
  sqlc.arg(type)::text, sqlc.arg(data)::text, sqlc.arg(ts)::bigint
FROM boards b
WHERE b.project_id = sqlc.arg(project_id) AND b.id = sqlc.arg(board_id)
RETURNING project_id, id, board_id, task_id, actor, agent, type, data, ts;

-- One page of a task's events, newest first ((ts, id) descending), strictly after the cursor's
-- (after_ts, after_id); after_id '' is the first page. Event ids are fixed-length lowercase hex,
-- so they order the same under every collation; the ORDER BY and the cursor share one.
-- name: ListTaskEvents :many
SELECT project_id, id, board_id, task_id, actor, agent, type, data, ts FROM task_events
WHERE project_id = sqlc.arg(project_id) AND task_id = sqlc.arg(task_id)
  AND (sqlc.arg(after_id)::text = '' OR ts < sqlc.arg(after_ts)::bigint
       OR (ts = sqlc.arg(after_ts)::bigint AND id < sqlc.arg(after_id)::text))
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(page_limit)::bigint;

-- One page of the board's feed, its tasks' events and its own, over task_events_feed
-- (board_id, ts, id), newest first, after the cursor as ListTaskEvents.
-- name: ListBoardFeed :many
SELECT project_id, id, board_id, task_id, actor, agent, type, data, ts FROM task_events
WHERE board_id = sqlc.arg(board_id) AND project_id = sqlc.arg(project_id)
  AND (sqlc.arg(after_id)::text = '' OR ts < sqlc.arg(after_ts)::bigint
       OR (ts = sqlc.arg(after_ts)::bigint AND id < sqlc.arg(after_id)::text))
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(page_limit)::bigint;
