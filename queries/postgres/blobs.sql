-- The blob table: content-addressed rows, written once. A blob is referenced while an attachment or
-- a memory entry names it.

-- name: InsertBlob :exec
INSERT INTO blobs (sha256, bytes, created_at) VALUES ($1, $2, $3) ON CONFLICT (sha256) DO NOTHING;

-- name: GetBlob :one
SELECT sha256, bytes, created_at FROM blobs WHERE sha256 = $1;

-- The delete and its reference check in one statement, so a reference written beside it is never
-- left pointing at a deleted row.
-- name: DeleteUnreferencedBlob :execrows
DELETE FROM blobs WHERE sha256 = $1
  AND NOT EXISTS (SELECT 1 FROM attachments a WHERE a.blob_sha256 = blobs.sha256)
  AND NOT EXISTS (SELECT 1 FROM memory m WHERE m.blob_sha256 = blobs.sha256);

-- name: ListUnreferencedBlobs :many
SELECT b.sha256, b.bytes, b.created_at FROM blobs b
WHERE b.created_at < sqlc.arg(older_than)::bigint
  AND NOT EXISTS (SELECT 1 FROM attachments a WHERE a.blob_sha256 = b.sha256)
  AND NOT EXISTS (SELECT 1 FROM memory m WHERE m.blob_sha256 = b.sha256)
ORDER BY b.created_at, b.sha256 COLLATE "C" LIMIT sqlc.arg(page_limit)::bigint;
