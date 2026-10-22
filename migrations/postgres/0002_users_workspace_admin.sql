-- +goose Up
-- The workspace-admin flag: a property of the account, not a project role (`user add --admin`).
ALTER TABLE users ADD COLUMN workspace_admin boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users DROP COLUMN workspace_admin;
