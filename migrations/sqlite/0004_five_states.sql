-- +goose NO TRANSACTION
-- +goose Up
-- ADR-0005, owner-approved 2026-10-23 with plan Amendment E (zone Z2): five states. TODO replaces
-- READY and BACKLOG is gone, so no task is a draft. Every BACKLOG or READY task becomes TODO; on each
-- board the BACKLOG columns fold into its first READY column (lowest position, then id), which
-- keeps its name, unless that is the default "Ready" (then "To do"), and its WIP limit, and takes
-- their tasks (order keys kept) and their integrations; a board with no READY column keeps its
-- BACKLOG columns as TODO columns ("Backlog" renamed "To do"). Both status CHECKs become the five.
--
-- SQLite cannot alter a CHECK, so columns and tasks are rebuilt as
-- https://sqlite.org/lang_altertable.html section 7 prescribes: the new shape under a new name, the
-- rows copied, the old table dropped, the new one renamed, then tasks' index and triggers remade.
-- Unlike 0003's memory, tasks is REFERENCED (artifacts, summaries, runs, task_events, dependencies,
-- context_items, memory's old key, comments, follow_ups, attachments, all ON DELETE CASCADE), and
-- with foreign keys on, DROP TABLE tasks is an implicit DELETE that would cascade through them all.
-- So this file runs outside goose's transaction (PRAGMA foreign_keys is a no-op inside one), turns
-- the keys off, does its work in one transaction of its own, and turns them back on. The writer pool
-- is one connection (internal/store/sqlite.go), so the pragma and the statements share it. rid is
-- copied, so the external-content FTS5 index over tasks stays valid; search_body is generated.
-- internal/store/five_states_migration_test.go pins the rebuilt schema to 0003's, CHECKs aside.
PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;
UPDATE tasks SET column_id = (
    SELECT r.id FROM columns r WHERE r.project_id = tasks.project_id AND r.board_id = tasks.board_id AND r.status = 'READY'
    ORDER BY r.position, r.id LIMIT 1)
WHERE EXISTS (SELECT 1 FROM columns b WHERE b.project_id = tasks.project_id AND b.board_id = tasks.board_id AND b.id = tasks.column_id AND b.status = 'BACKLOG')
  AND EXISTS (SELECT 1 FROM columns r WHERE r.project_id = tasks.project_id AND r.board_id = tasks.board_id AND r.status = 'READY');
UPDATE integrations SET column_id = (
    SELECT r.id FROM columns r WHERE r.project_id = integrations.project_id AND r.board_id = integrations.board_id AND r.status = 'READY'
    ORDER BY r.position, r.id LIMIT 1)
WHERE EXISTS (SELECT 1 FROM columns b WHERE b.project_id = integrations.project_id AND b.board_id = integrations.board_id AND b.id = integrations.column_id AND b.status = 'BACKLOG')
  AND EXISTS (SELECT 1 FROM columns r WHERE r.project_id = integrations.project_id AND r.board_id = integrations.board_id AND r.status = 'READY');

CREATE TABLE columns_new (
    project_id TEXT NOT NULL,
    board_id TEXT NOT NULL,
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    position INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('TODO', 'IN_PROGRESS', 'BLOCKED', 'REVIEW', 'DONE')),
    wip_limit INTEGER,
    PRIMARY KEY (project_id, board_id, id),
    FOREIGN KEY (project_id, board_id) REFERENCES boards(project_id, id) ON DELETE CASCADE
);
INSERT INTO columns_new (project_id, board_id, id, name, position, status, wip_limit)
SELECT c.project_id, c.board_id, c.id,
  CASE WHEN (c.status = 'READY' AND c.name = 'Ready') OR (c.status = 'BACKLOG' AND c.name = 'Backlog') THEN 'To do' ELSE c.name END,
  c.position, CASE WHEN c.status IN ('BACKLOG', 'READY') THEN 'TODO' ELSE c.status END, c.wip_limit
FROM columns c
WHERE c.status <> 'BACKLOG'
  OR NOT EXISTS (SELECT 1 FROM columns r WHERE r.project_id = c.project_id AND r.board_id = c.board_id AND r.status = 'READY');
DROP TABLE columns;
ALTER TABLE columns_new RENAME TO columns;

CREATE TABLE tasks_new (
    rid INTEGER PRIMARY KEY,
    project_id TEXT NOT NULL,
    id TEXT NOT NULL,
    board_id TEXT NOT NULL,
    column_id TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('TODO', 'IN_PROGRESS', 'BLOCKED', 'REVIEW', 'DONE')),
    -- 'poison' marks a quarantined task; auto-unblock skips it.
    blocked_reason TEXT,
    priority TEXT NOT NULL DEFAULT 'normal' CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    assignee TEXT,
    labels TEXT NOT NULL DEFAULT '[]',
    required_capabilities TEXT NOT NULL DEFAULT '[]',
    estimate REAL,
    due_at INTEGER,
    parent_task_id TEXT,
    order_key TEXT NOT NULL,
    custom_fields TEXT NOT NULL DEFAULT '{}',
    -- Caches of the canonical dependencies edges, kept in the same transaction.
    blocked_by TEXT NOT NULL DEFAULT '[]',
    blocks TEXT NOT NULL DEFAULT '[]',
    deliverable TEXT,
    reviewer TEXT,
    review TEXT,
    chars TEXT,
    model_hint TEXT,
    run_of TEXT,
    sprint_ids TEXT NOT NULL DEFAULT '[]',
    racing INTEGER NOT NULL DEFAULT 0 CHECK (racing IN (0, 1)),
    race_opened_at INTEGER,
    claim_attempts INTEGER NOT NULL DEFAULT 0,
    next_eligible_at INTEGER,
    score REAL NOT NULL DEFAULT 0,
    search_tokens TEXT NOT NULL DEFAULT '',
    claim_owner TEXT,
    claim_agent TEXT,
    claim_agent_id TEXT,
    lease_id TEXT,
    lease_expires_at INTEGER,
    version INTEGER NOT NULL DEFAULT 1,
    created_by TEXT NOT NULL,
    created_by_agent TEXT,
    created_at INTEGER NOT NULL,
    updated_by TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    -- search_tasks reads this one column: the title, description and search tokens together, so a
    -- single-column MATCH searches all three.
    search_body TEXT GENERATED ALWAYS AS (title || ' ' || description || ' ' || search_tokens) VIRTUAL,
    UNIQUE (project_id, id),
    FOREIGN KEY (project_id, board_id) REFERENCES boards(project_id, id) ON DELETE CASCADE
);
INSERT INTO tasks_new (rid, project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at)
SELECT rid, project_id, id, board_id, column_id, title, description, CASE WHEN status IN ('BACKLOG', 'READY') THEN 'TODO' ELSE status END, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at
FROM tasks;
DROP TABLE tasks;
ALTER TABLE tasks_new RENAME TO tasks;
-- The claim frontier: claim_next and next_task read the eligible tasks of one board and status,
-- best score first.
CREATE INDEX tasks_claim_frontier ON tasks(project_id, board_id, status, next_eligible_at, score DESC);

-- The three triggers keep the index in step with tasks.
-- +goose StatementBegin
CREATE TRIGGER tasks_fts_ai AFTER INSERT ON tasks BEGIN
    INSERT INTO tasks_fts(rowid, search_body, rid) VALUES (new.rid, new.search_body, new.rid);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER tasks_fts_ad AFTER DELETE ON tasks BEGIN
    INSERT INTO tasks_fts(tasks_fts, rowid, search_body, rid) VALUES ('delete', old.rid, old.search_body, old.rid);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER tasks_fts_au AFTER UPDATE OF title, description, search_tokens ON tasks BEGIN
    INSERT INTO tasks_fts(tasks_fts, rowid, search_body, rid) VALUES ('delete', old.rid, old.search_body, old.rid);
    INSERT INTO tasks_fts(rowid, search_body, rid) VALUES (new.rid, new.search_body, new.rid);
END;
-- +goose StatementEnd
COMMIT;
PRAGMA foreign_keys = ON;

-- +goose Down
-- TODO maps back to READY and both CHECKs back to the six, by the same rebuild. It is lossy: no
-- Backlog column comes back (its tasks stay in To do, as READY), and a TODO column named "To do" is
-- named "Ready" again.
PRAGMA foreign_keys = OFF;
BEGIN IMMEDIATE;
CREATE TABLE columns_old (
    project_id TEXT NOT NULL,
    board_id TEXT NOT NULL,
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    position INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('BACKLOG', 'READY', 'IN_PROGRESS', 'BLOCKED', 'REVIEW', 'DONE')),
    wip_limit INTEGER,
    PRIMARY KEY (project_id, board_id, id),
    FOREIGN KEY (project_id, board_id) REFERENCES boards(project_id, id) ON DELETE CASCADE
);
INSERT INTO columns_old (project_id, board_id, id, name, position, status, wip_limit)
SELECT project_id, board_id, id, CASE WHEN status = 'TODO' AND name = 'To do' THEN 'Ready' ELSE name END,
  position, CASE WHEN status = 'TODO' THEN 'READY' ELSE status END, wip_limit
FROM columns;
DROP TABLE columns;
ALTER TABLE columns_old RENAME TO columns;

CREATE TABLE tasks_old (
    rid INTEGER PRIMARY KEY,
    project_id TEXT NOT NULL,
    id TEXT NOT NULL,
    board_id TEXT NOT NULL,
    column_id TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('BACKLOG', 'READY', 'IN_PROGRESS', 'BLOCKED', 'REVIEW', 'DONE')),
    -- 'poison' marks a quarantined task; auto-unblock skips it.
    blocked_reason TEXT,
    priority TEXT NOT NULL DEFAULT 'normal' CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    assignee TEXT,
    labels TEXT NOT NULL DEFAULT '[]',
    required_capabilities TEXT NOT NULL DEFAULT '[]',
    estimate REAL,
    due_at INTEGER,
    parent_task_id TEXT,
    order_key TEXT NOT NULL,
    custom_fields TEXT NOT NULL DEFAULT '{}',
    -- Caches of the canonical dependencies edges, kept in the same transaction.
    blocked_by TEXT NOT NULL DEFAULT '[]',
    blocks TEXT NOT NULL DEFAULT '[]',
    deliverable TEXT,
    reviewer TEXT,
    review TEXT,
    chars TEXT,
    model_hint TEXT,
    run_of TEXT,
    sprint_ids TEXT NOT NULL DEFAULT '[]',
    racing INTEGER NOT NULL DEFAULT 0 CHECK (racing IN (0, 1)),
    race_opened_at INTEGER,
    claim_attempts INTEGER NOT NULL DEFAULT 0,
    next_eligible_at INTEGER,
    score REAL NOT NULL DEFAULT 0,
    search_tokens TEXT NOT NULL DEFAULT '',
    claim_owner TEXT,
    claim_agent TEXT,
    claim_agent_id TEXT,
    lease_id TEXT,
    lease_expires_at INTEGER,
    version INTEGER NOT NULL DEFAULT 1,
    created_by TEXT NOT NULL,
    created_by_agent TEXT,
    created_at INTEGER NOT NULL,
    updated_by TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    -- search_tasks reads this one column: the title, description and search tokens together, so a
    -- single-column MATCH searches all three.
    search_body TEXT GENERATED ALWAYS AS (title || ' ' || description || ' ' || search_tokens) VIRTUAL,
    UNIQUE (project_id, id),
    FOREIGN KEY (project_id, board_id) REFERENCES boards(project_id, id) ON DELETE CASCADE
);
INSERT INTO tasks_old (rid, project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at)
SELECT rid, project_id, id, board_id, column_id, title, description, CASE WHEN status = 'TODO' THEN 'READY' ELSE status END, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at
FROM tasks;
DROP TABLE tasks;
ALTER TABLE tasks_old RENAME TO tasks;
-- The claim frontier: claim_next and next_task read the eligible tasks of one board and status,
-- best score first.
CREATE INDEX tasks_claim_frontier ON tasks(project_id, board_id, status, next_eligible_at, score DESC);

-- The three triggers keep the index in step with tasks.
-- +goose StatementBegin
CREATE TRIGGER tasks_fts_ai AFTER INSERT ON tasks BEGIN
    INSERT INTO tasks_fts(rowid, search_body, rid) VALUES (new.rid, new.search_body, new.rid);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER tasks_fts_ad AFTER DELETE ON tasks BEGIN
    INSERT INTO tasks_fts(tasks_fts, rowid, search_body, rid) VALUES ('delete', old.rid, old.search_body, old.rid);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER tasks_fts_au AFTER UPDATE OF title, description, search_tokens ON tasks BEGIN
    INSERT INTO tasks_fts(tasks_fts, rowid, search_body, rid) VALUES ('delete', old.rid, old.search_body, old.rid);
    INSERT INTO tasks_fts(rowid, search_body, rid) VALUES (new.rid, new.search_body, new.rid);
END;
-- +goose StatementEnd
COMMIT;
PRAGMA foreign_keys = ON;
