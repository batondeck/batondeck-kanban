-- name: GetBlob :one
SELECT * FROM blobs WHERE sha256 = ?;

-- name: GetAttachment :one
SELECT * FROM attachments WHERE project_id = ? AND task_id = ? AND id = ?;
