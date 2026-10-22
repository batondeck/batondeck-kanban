-- name: GetBlob :one
SELECT * FROM blobs WHERE sha256 = $1;

-- name: GetAttachment :one
SELECT * FROM attachments WHERE project_id = $1 AND task_id = $2 AND id = $3;
