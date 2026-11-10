-- +goose Up
-- A column's tasks in order (order key, then id, COLLATE "C" as the queries read them, as bytes): the
-- head read every write that moves a task into another column makes to land it before the column's
-- first card, and list_tasks by column.
CREATE INDEX tasks_column_order ON tasks(project_id, board_id, column_id, order_key COLLATE "C", id COLLATE "C");

-- +goose Down
DROP INDEX tasks_column_order;
