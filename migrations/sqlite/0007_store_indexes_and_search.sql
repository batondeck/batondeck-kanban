-- +goose Up
-- A column's tasks in order (order key, then id, as bytes): the head read every write that moves a
-- task into another column makes to land it before the column's first card, and list_tasks by column.
CREATE INDEX tasks_column_order ON tasks(project_id, board_id, column_id, order_key, id);

-- One task's events, in the feed's order: the foreign key's cascade when a task is deleted (without
-- it each delete walked every event of the project, inside the write transaction) and ListTaskEvents.
CREATE INDEX task_events_task ON task_events(project_id, task_id, ts, id);

-- The blob references the GC and a blob's delete look up by hash (without them each blob was checked
-- against a scan of both tables).
CREATE INDEX attachments_blob ON attachments(blob_sha256) WHERE blob_sha256 IS NOT NULL;
CREATE INDEX memory_blob ON memory(blob_sha256) WHERE blob_sha256 IS NOT NULL;

-- A task's memory window (get_task_context, the delete cascade) and the expiry sweep.
CREATE INDEX memory_task ON memory(project_id, task_id, ts, id);
CREATE INDEX memory_expiry ON memory(expires_at, id) WHERE expires_at IS NOT NULL;

-- The idempotency keys past their window, for the retention sweep.
CREATE INDEX idempotency_keys_expiry ON idempotency_keys(expires_at, project_id, identity, key);

-- +goose Down
DROP INDEX idempotency_keys_expiry;
DROP INDEX memory_expiry;
DROP INDEX memory_task;
DROP INDEX memory_blob;
DROP INDEX attachments_blob;
DROP INDEX task_events_task;
DROP INDEX tasks_column_order;
