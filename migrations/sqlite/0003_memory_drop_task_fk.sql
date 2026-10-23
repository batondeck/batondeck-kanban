-- +goose Up
-- P-65, owner-approved 2026-10-23 (zone Z2): a memory row keeps its task_id after the task is
-- deleted. A project-scope row's task_id is provenance ("learned on T-3") and outlives the task, as
-- in conductor; the three task scopes are deleted by the service's cascade (DeleteMemoryByTask
-- before DeleteTask, R1.3), no longer by this key. memory's other keys (project, blob) stay.
--
-- SQLite cannot drop a constraint, so the table is rebuilt as https://sqlite.org/lang_altertable.html
-- section 7 prescribes: the new shape under a new name, the rows copied, the old table dropped, the
-- new one renamed (never the old renamed first). Its steps 1 and 12, foreign_keys OFF and back ON,
-- are not here: goose runs this file inside a transaction, where PRAGMA foreign_keys is a no-op, and
-- they guard the tables that REFERENCE the rebuilt one. Nothing references memory, so the DROP's
-- implicit DELETE cascades nowhere, and memory has no index but its primary key's (which the CREATE
-- makes), no trigger, no view and no FTS hook to recreate (internal/store/schema_test.go pins both).
CREATE TABLE memory_new (
    id TEXT PRIMARY KEY,
    project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
    task_id TEXT,
    scope TEXT NOT NULL CHECK (scope IN ('task', 'agent', 'shared', 'project', 'agent_global')),
    key TEXT NOT NULL,
    value TEXT,
    blob_sha256 TEXT REFERENCES blobs(sha256),
    author TEXT NOT NULL,
    ts INTEGER NOT NULL,
    expires_at INTEGER
);
INSERT INTO memory_new (id, project_id, task_id, scope, key, value, blob_sha256, author, ts, expires_at)
SELECT id, project_id, task_id, scope, key, value, blob_sha256, author, ts, expires_at FROM memory;
DROP TABLE memory;
ALTER TABLE memory_new RENAME TO memory;

-- +goose Down
-- The reverse rebuild, with the key back. It cannot hold a row whose task is gone: such a
-- task-scope row is one the old key would have cascaded away, so it is not copied; a project-scope
-- row is copied without its provenance.
CREATE TABLE memory_old (
    id TEXT PRIMARY KEY,
    project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
    task_id TEXT,
    scope TEXT NOT NULL CHECK (scope IN ('task', 'agent', 'shared', 'project', 'agent_global')),
    key TEXT NOT NULL,
    value TEXT,
    blob_sha256 TEXT REFERENCES blobs(sha256),
    author TEXT NOT NULL,
    ts INTEGER NOT NULL,
    expires_at INTEGER,
    FOREIGN KEY (project_id, task_id) REFERENCES tasks(project_id, id) ON DELETE CASCADE
);
INSERT INTO memory_old (id, project_id, task_id, scope, key, value, blob_sha256, author, ts, expires_at)
SELECT m.id, m.project_id,
  CASE WHEN EXISTS (SELECT 1 FROM tasks t WHERE t.project_id = m.project_id AND t.id = m.task_id) THEN m.task_id END,
  m.scope, m.key, m.value, m.blob_sha256, m.author, m.ts, m.expires_at
FROM memory m
WHERE m.task_id IS NULL OR m.scope NOT IN ('task', 'agent', 'shared')
  OR EXISTS (SELECT 1 FROM tasks t WHERE t.project_id = m.project_id AND t.id = m.task_id);
DROP TABLE memory;
ALTER TABLE memory_old RENAME TO memory;
