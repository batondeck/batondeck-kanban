-- name: CreateBoard :exec
INSERT INTO boards (project_id, id, name, created_by, created_at) VALUES ($1, $2, $3, $4, $5);

-- name: GetBoard :one
SELECT project_id, id, name, created_by, created_at FROM boards WHERE project_id = $1 AND id = $2;

-- The row a column change reads first: on Postgres it is locked to the transaction, so two column
-- changes on one board take turns.
-- name: LockBoard :one
SELECT project_id, id, name, created_by, created_at FROM boards WHERE project_id = $1 AND id = $2 FOR UPDATE;

-- name: ListBoards :many
SELECT project_id, id, name, created_by, created_at FROM boards WHERE project_id = $1
ORDER BY created_at, id;

-- name: CreateColumn :exec
INSERT INTO columns (project_id, board_id, id, name, position, status, wip_limit)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- The column a write puts a task into, its row held FOR KEY SHARE to the transaction: remove_column
-- (LockColumn, FOR UPDATE) waits for the write and then counts its task, or the write waits for
-- remove_column and then finds no column. Concurrent writes into one column do not wait on each other.
-- name: GetColumn :one
SELECT * FROM columns WHERE project_id = $1 AND board_id = $2 AND id = $3 FOR KEY SHARE;

-- The column remove_column and update_column judge, locked to the transaction before they count its
-- tasks: a write that holds it (GetColumn) commits first, and one that comes later waits.
-- name: LockColumn :one
SELECT * FROM columns WHERE project_id = $1 AND board_id = $2 AND id = $3 FOR UPDATE;

-- name: ListColumns :many
SELECT project_id, board_id, id, name, position, status, wip_limit
FROM columns WHERE project_id = $1 AND board_id = $2
ORDER BY position, id;

-- name: ListProjectColumns :many
SELECT project_id, board_id, id, name, position, status, wip_limit
FROM columns WHERE project_id = $1
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
UPDATE columns SET name = $1, position = $2, status = $3, wip_limit = $4
WHERE project_id = $5 AND board_id = $6 AND id = $7;

-- name: DeleteColumn :exec
DELETE FROM columns WHERE project_id = $1 AND board_id = $2 AND id = $3;

-- name: CountColumnTasks :one
SELECT COUNT(*) FROM tasks WHERE project_id = $1 AND board_id = $2 AND column_id = $3;
