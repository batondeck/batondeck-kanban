-- The attachment area. A row is created pending with no blob, and made ready on its uploaded blob
-- once; the task's own row is the existence check of a list.

-- name: InsertAttachment :one
INSERT INTO attachments (project_id, task_id, id, mime_type, bytes, kind, status, created_by, ts)
VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?)
RETURNING project_id, task_id, id, blob_sha256, mime_type, bytes, kind, status, created_by, ts;

-- name: GetAttachment :one
SELECT project_id, task_id, id, blob_sha256, mime_type, bytes, kind, status, created_by, ts
FROM attachments WHERE project_id = ? AND task_id = ? AND id = ?;

-- name: SetAttachmentReady :one
UPDATE attachments SET blob_sha256 = sqlc.arg(blob_sha256), status = 'ready'
WHERE project_id = sqlc.arg(project_id) AND task_id = sqlc.arg(task_id) AND id = sqlc.arg(id) AND status = 'pending'
RETURNING project_id, task_id, id, blob_sha256, mime_type, bytes, kind, status, created_by, ts;

-- The task's attachments, oldest first, in one statement: no row is a missing task, one row whose
-- attachment columns are NULL a task with none.
-- name: ListAttachments :many
SELECT t.id AS task_id, a.id, a.blob_sha256, a.mime_type, a.bytes, a.kind, a.status, a.created_by, a.ts
FROM tasks t LEFT JOIN attachments a ON a.project_id = t.project_id AND a.task_id = t.id
WHERE t.project_id = ? AND t.id = ?
ORDER BY a.ts, a.id;

-- name: DeleteAttachmentsByTask :execrows
DELETE FROM attachments WHERE project_id = ? AND task_id = ?;
