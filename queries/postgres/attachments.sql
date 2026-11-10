-- The attachment area. A row is created pending with no blob, and made ready on its uploaded blob
-- once; the task's own row is the existence check of a list.

-- name: InsertAttachment :one
INSERT INTO attachments (project_id, task_id, id, mime_type, bytes, kind, status, created_by, ts, file_name)
VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, $8, $9)
RETURNING project_id, task_id, id, blob_sha256, mime_type, bytes, kind, status, created_by, ts, file_name;

-- name: GetAttachment :one
SELECT project_id, task_id, id, blob_sha256, mime_type, bytes, kind, status, created_by, ts, file_name
FROM attachments WHERE project_id = $1 AND task_id = $2 AND id = $3;

-- name: SetAttachmentReady :one
UPDATE attachments SET blob_sha256 = sqlc.arg(blob_sha256), status = 'ready'
WHERE project_id = sqlc.arg(project_id) AND task_id = sqlc.arg(task_id) AND id = sqlc.arg(id) AND status = 'pending'
RETURNING project_id, task_id, id, blob_sha256, mime_type, bytes, kind, status, created_by, ts, file_name;

-- The task's attachments, oldest first, in one statement: no row is a missing task, one row whose
-- attachment columns are NULL a task with none.
-- name: ListAttachments :many
SELECT t.id AS task_id, a.id, a.blob_sha256, a.mime_type, a.bytes, a.kind, a.status, a.created_by, a.ts, a.file_name
FROM tasks t LEFT JOIN attachments a ON a.project_id = t.project_id AND a.task_id = t.id
WHERE t.project_id = $1 AND t.id = $2
ORDER BY a.ts, a.id COLLATE "C";

-- name: DeleteAttachmentsByTask :execrows
DELETE FROM attachments WHERE project_id = $1 AND task_id = $2;

-- The retention sweep: the pending attachments created before the cutoff, oldest first, a page at a
-- time (an upload that never came, whose URL no call can sign again).
-- name: DeleteStalePendingAttachments :execrows
DELETE FROM attachments WHERE (project_id, task_id, id) IN (
  SELECT a.project_id, a.task_id, a.id FROM attachments a WHERE a.status = 'pending' AND a.ts < sqlc.arg(before)::bigint
  ORDER BY a.ts, a.id COLLATE "C" LIMIT sqlc.arg(page_limit)::bigint);
