-- name: GetTaskEvent :one
SELECT * FROM task_events WHERE project_id = ? AND id = ?;

-- name: GetChangeLog :one
SELECT * FROM change_log WHERE id = ?;

-- A task's event, on the task's board, in one statement: no row is a missing task or another board
-- (board_id '' takes the task's), which the caller tells apart.
-- name: AppendTaskEvent :one
INSERT INTO task_events (project_id, id, board_id, task_id, actor, agent, type, data, ts)
SELECT t.project_id, CAST(sqlc.arg(id) AS TEXT), t.board_id, t.id, CAST(sqlc.arg(actor) AS TEXT), CAST(sqlc.narg(agent) AS TEXT),
  CAST(sqlc.arg(type) AS TEXT), CAST(sqlc.arg(data) AS TEXT), CAST(sqlc.arg(ts) AS INTEGER)
FROM tasks t
WHERE t.project_id = sqlc.arg(project_id) AND t.id = sqlc.arg(task_id)
  AND (CAST(sqlc.arg(board_id) AS TEXT) = '' OR t.board_id = CAST(sqlc.arg(board_id) AS TEXT))
RETURNING project_id, id, board_id, task_id, actor, agent, type, data, ts;

-- A board's event with no task, in one statement: no row is a board that is not the project's.
-- name: AppendBoardEvent :one
INSERT INTO task_events (project_id, id, board_id, task_id, actor, agent, type, data, ts)
SELECT b.project_id, CAST(sqlc.arg(id) AS TEXT), b.id, NULL, CAST(sqlc.arg(actor) AS TEXT), CAST(sqlc.narg(agent) AS TEXT),
  CAST(sqlc.arg(type) AS TEXT), CAST(sqlc.arg(data) AS TEXT), CAST(sqlc.arg(ts) AS INTEGER)
FROM boards b
WHERE b.project_id = sqlc.arg(project_id) AND b.id = sqlc.arg(board_id)
RETURNING project_id, id, board_id, task_id, actor, agent, type, data, ts;

-- One page of a task's events, newest first ((ts, id) descending), strictly after the cursor's
-- (after_ts, after_id); after_id '' is the first page.
-- name: ListTaskEvents :many
SELECT project_id, id, board_id, task_id, actor, agent, type, data, ts FROM task_events
WHERE project_id = sqlc.arg(project_id) AND task_id = sqlc.arg(task_id)
  AND (CAST(sqlc.arg(after_id) AS TEXT) = '' OR ts < CAST(sqlc.arg(after_ts) AS INTEGER)
       OR (ts = CAST(sqlc.arg(after_ts) AS INTEGER) AND id < CAST(sqlc.arg(after_id) AS TEXT)))
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- One page of the board's feed, its tasks' events and its own, over task_events_feed
-- (board_id, ts, id), newest first, after the cursor as ListTaskEvents.
-- name: ListBoardFeed :many
SELECT project_id, id, board_id, task_id, actor, agent, type, data, ts FROM task_events
WHERE board_id = sqlc.arg(board_id) AND project_id = sqlc.arg(project_id)
  AND (CAST(sqlc.arg(after_id) AS TEXT) = '' OR ts < CAST(sqlc.arg(after_ts) AS INTEGER)
       OR (ts = CAST(sqlc.arg(after_ts) AS INTEGER) AND id < CAST(sqlc.arg(after_id) AS TEXT)))
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(page_limit);
