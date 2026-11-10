-- +goose Up
-- A column's tasks in order (order key, then id, COLLATE "C" as the queries read them, as bytes): the
-- head read every write that moves a task into another column makes to land it before the column's
-- first card, and list_tasks by column.
CREATE INDEX tasks_column_order ON tasks(project_id, board_id, column_id, order_key COLLATE "C", id COLLATE "C");

-- One task's events, in the feed's order: the foreign key's cascade when a task is deleted (without
-- it each delete walked every event of the project, inside the write transaction) and ListTaskEvents.
CREATE INDEX task_events_task ON task_events(project_id, task_id, ts, id);

-- The blob references the GC, a blob's delete and its foreign-key checks look up by hash (without
-- them each blob was checked against a scan of both tables).
CREATE INDEX attachments_blob ON attachments(blob_sha256) WHERE blob_sha256 IS NOT NULL;
CREATE INDEX memory_blob ON memory(blob_sha256) WHERE blob_sha256 IS NOT NULL;

-- A task's memory window (get_task_context, the delete cascade) and the expiry sweep.
CREATE INDEX memory_task ON memory(project_id, task_id, ts, id COLLATE "C");
CREATE INDEX memory_expiry ON memory(expires_at, id COLLATE "C") WHERE expires_at IS NOT NULL;

-- The idempotency keys past their window, for the retention sweep.
CREATE INDEX idempotency_keys_expiry ON idempotency_keys(expires_at, project_id COLLATE "C", identity COLLATE "C", key COLLATE "C");

-- search_tasks splits a body where its query splits (every character that is not a letter or a
-- digit, store.SearchTerms) and where SQLite's FTS5 splits: the default text parser kept a path, a
-- host, an e-mail address, a version or a signed number as ONE lexeme ("internal/store/tasks.go",
-- "-123"), so no query term ever matched a word inside one. Every ASCII punctuation and symbol
-- character becomes a space before the parser runs (code-point ranges, whatever the collation).
-- Postgres 16 cannot change a generated column's expression, so the column (and its GIN index) is
-- dropped and added again; every row's vector is computed afresh.
DROP INDEX tasks_search;
ALTER TABLE tasks DROP COLUMN search;
ALTER TABLE tasks ADD COLUMN search tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', regexp_replace(title || ' ' || description || ' ' || search_tokens, '[!-/:-@[-`{-~]+', ' ', 'g'))) STORED;
CREATE INDEX tasks_search ON tasks USING gin (search);

-- Each sprint's member count, recounted from the tasks that name it as ListSprintMembers reads them
-- (the sprint's board): deleting a member did not lower it before the delete cascade did, so a
-- sprint could refuse new members with SPRINT_MAX_TASKS while holding fewer. The down keeps the
-- true counts.
UPDATE sprints SET member_count = (
    SELECT COUNT(*) FROM tasks t
    WHERE t.project_id = sprints.project_id AND t.board_id = sprints.board_id
      AND t.sprint_ids::jsonb @> jsonb_build_array(sprints.id));

-- +goose Down
DROP INDEX tasks_search;
ALTER TABLE tasks DROP COLUMN search;
ALTER TABLE tasks ADD COLUMN search tsvector GENERATED ALWAYS AS (to_tsvector('simple', title || ' ' || description || ' ' || search_tokens)) STORED;
CREATE INDEX tasks_search ON tasks USING gin (search);
DROP INDEX idempotency_keys_expiry;
DROP INDEX memory_expiry;
DROP INDEX memory_task;
DROP INDEX memory_blob;
DROP INDEX attachments_blob;
DROP INDEX task_events_task;
DROP INDEX tasks_column_order;
