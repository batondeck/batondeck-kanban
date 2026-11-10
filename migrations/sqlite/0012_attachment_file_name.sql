-- +goose Up
-- The name attach_file is given (fileName), kept so list_attachments and the web UI can say which
-- file an attachment is. A row from before keeps the empty name.
ALTER TABLE attachments ADD COLUMN file_name TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE attachments DROP COLUMN file_name;
