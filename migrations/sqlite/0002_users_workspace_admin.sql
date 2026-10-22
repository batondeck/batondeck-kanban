-- +goose Up
-- The workspace-admin flag: a property of the account, not a project role (`user add --admin`).
ALTER TABLE users ADD COLUMN workspace_admin INTEGER NOT NULL DEFAULT 0 CHECK (workspace_admin IN (0, 1));

-- +goose Down
ALTER TABLE users DROP COLUMN workspace_admin;
