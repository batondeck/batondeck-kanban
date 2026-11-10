-- The blob table: content-addressed rows, written once. A blob is referenced while an attachment, a
-- memory entry or a Chronicle body (chronicle_blob_refs) names it.

-- name: InsertBlob :exec
INSERT INTO blobs (sha256, bytes, created_at) VALUES (?, ?, ?) ON CONFLICT (sha256) DO NOTHING;

-- name: GetBlob :one
SELECT sha256, bytes, created_at FROM blobs WHERE sha256 = ?;

-- The delete and its reference check in one statement, so a reference written beside it is never
-- left pointing at a deleted row.
-- name: DeleteUnreferencedBlob :execrows
DELETE FROM blobs WHERE sha256 = ?
  AND NOT EXISTS (SELECT 1 FROM attachments a WHERE a.blob_sha256 = blobs.sha256)
  AND NOT EXISTS (SELECT 1 FROM memory m WHERE m.blob_sha256 = blobs.sha256)
  AND NOT EXISTS (SELECT 1 FROM chronicle_blob_refs r WHERE r.blob_sha256 = blobs.sha256);

-- name: ListUnreferencedBlobs :many
SELECT b.sha256, b.bytes, b.created_at FROM blobs b
WHERE b.created_at < CAST(sqlc.arg(older_than) AS INTEGER)
  AND NOT EXISTS (SELECT 1 FROM attachments a WHERE a.blob_sha256 = b.sha256)
  AND NOT EXISTS (SELECT 1 FROM memory m WHERE m.blob_sha256 = b.sha256)
  AND NOT EXISTS (SELECT 1 FROM chronicle_blob_refs r WHERE r.blob_sha256 = b.sha256)
ORDER BY b.created_at, b.sha256 LIMIT sqlc.arg(page_limit);
