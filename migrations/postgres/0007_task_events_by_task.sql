-- +goose Up
-- One task's events, in the feed's order: ListTaskEvents (a ticket's Recent Activity, read on every
-- open ticket after each write and each board frame) and the foreign key's cascade when a task is
-- deleted. Without it both scanned every event of the project, and the read sorted them as well.
CREATE INDEX task_events_task ON task_events(project_id, task_id, ts, id);

-- +goose Down
DROP INDEX task_events_task;
