-- +goose Up
-- A column's tasks in order (order key, then id, as bytes): the head read every write that moves a
-- task into another column makes to land it before the column's first card, and list_tasks by column.
CREATE INDEX tasks_column_order ON tasks(project_id, board_id, column_id, order_key, id);

-- The idempotency keys past their window, for the retention sweep.
CREATE INDEX idempotency_keys_expiry ON idempotency_keys(expires_at, project_id, identity, key);

-- +goose Down
DROP INDEX idempotency_keys_expiry;
DROP INDEX tasks_column_order;
