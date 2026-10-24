-- +goose Up
-- P-275 (audit A12), owner-approved 2026-10-24 (zone Z2): a handoff writes the receiver a
-- notification, so a client that pulls (list_notifications each turn) learns of it. The type CHECK
-- gains 'handoff'. SQLite cannot alter a CHECK, so the table is rebuilt as
-- https://sqlite.org/lang_altertable.html section 7 prescribes; nothing references notifications and
-- it has no index or trigger, so goose's transaction holds it all. Down drops the handoff rows, which
-- the old CHECK cannot hold.
CREATE TABLE notifications_new (
    identity_id TEXT NOT NULL,
    id TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    board_id TEXT,
    type TEXT NOT NULL CHECK (type IN ('mention', 'follow_up', 'review_request', 'handoff')),
    task_id TEXT,
    actor TEXT NOT NULL,
    data TEXT NOT NULL DEFAULT '{}',
    read INTEGER NOT NULL DEFAULT 0 CHECK (read IN (0, 1)),
    ts INTEGER NOT NULL,
    PRIMARY KEY (identity_id, id)
);
INSERT INTO notifications_new SELECT identity_id, id, project_id, board_id, type, task_id, actor, data, read, ts FROM notifications;
DROP TABLE notifications;
ALTER TABLE notifications_new RENAME TO notifications;

-- +goose Down
CREATE TABLE notifications_old (
    identity_id TEXT NOT NULL,
    id TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    board_id TEXT,
    type TEXT NOT NULL CHECK (type IN ('mention', 'follow_up', 'review_request')),
    task_id TEXT,
    actor TEXT NOT NULL,
    data TEXT NOT NULL DEFAULT '{}',
    read INTEGER NOT NULL DEFAULT 0 CHECK (read IN (0, 1)),
    ts INTEGER NOT NULL,
    PRIMARY KEY (identity_id, id)
);
INSERT INTO notifications_old SELECT identity_id, id, project_id, board_id, type, task_id, actor, data, read, ts FROM notifications WHERE type <> 'handoff';
DROP TABLE notifications;
ALTER TABLE notifications_old RENAME TO notifications;
