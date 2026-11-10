-- name: CreateBoard :exec
INSERT INTO boards (project_id, id, name, created_by, created_at) VALUES (?, ?, ?, ?, ?);

-- name: GetBoard :one
SELECT project_id, id, name, created_by, created_at FROM boards WHERE project_id = ? AND id = ?;

-- The row a column change reads first: on Postgres it is locked to the transaction, so two column
-- changes on one board take turns.
-- name: LockBoard :one
SELECT project_id, id, name, created_by, created_at FROM boards WHERE project_id = ? AND id = ?;

-- name: ListBoards :many
SELECT project_id, id, name, created_by, created_at FROM boards WHERE project_id = ?
ORDER BY created_at, id;

-- name: CreateColumn :exec
INSERT INTO columns (project_id, board_id, id, name, position, status, wip_limit)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetColumn :one
SELECT * FROM columns WHERE project_id = ? AND board_id = ? AND id = ?;

-- The column remove_column and update_column judge (on Postgres locked FOR UPDATE; SQLite's
-- transaction holds the write lock from BEGIN).
-- name: LockColumn :one
SELECT * FROM columns WHERE project_id = ? AND board_id = ? AND id = ?;

-- name: ListColumns :many
SELECT project_id, board_id, id, name, position, status, wip_limit
FROM columns WHERE project_id = ? AND board_id = ?
ORDER BY position, id;

-- name: ListProjectColumns :many
SELECT project_id, board_id, id, name, position, status, wip_limit
FROM columns WHERE project_id = ?
ORDER BY board_id, position, id;

-- A board's columns with the number of tasks in each, from ONE grouped count over the board's
-- tasks: a column holding none is a 0, never absent.
-- name: ListColumnsWithTaskCounts :many
SELECT c.project_id, c.board_id, c.id, c.name, c.position, c.status, c.wip_limit,
       CAST(COALESCE(n.task_count, 0) AS INTEGER) AS task_count
FROM columns c
LEFT JOIN (
    SELECT t.column_id, COUNT(*) AS task_count
    FROM tasks t
    WHERE t.project_id = sqlc.arg(project_id) AND t.board_id = sqlc.arg(board_id)
    GROUP BY t.column_id
) n ON n.column_id = c.id
WHERE c.project_id = sqlc.arg(project_id) AND c.board_id = sqlc.arg(board_id)
ORDER BY c.position, c.id;

-- name: UpdateColumn :exec
UPDATE columns SET name = ?, position = ?, status = ?, wip_limit = ?
WHERE project_id = ? AND board_id = ? AND id = ?;

-- name: DeleteColumn :exec
DELETE FROM columns WHERE project_id = ? AND board_id = ? AND id = ?;

-- name: CountColumnTasks :one
SELECT COUNT(*) FROM tasks WHERE project_id = ? AND board_id = ? AND column_id = ?;
