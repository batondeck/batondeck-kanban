-- +goose Up
-- A Chronicle body that embeds an attachment's download URL holds that blob (P-244: every read
-- signs the stored form afresh), but the blob GC only knew attachments and memory, so deleting the
-- source task freed the file the record shows. chronicle_blob_refs names each blob a body embeds,
-- written with the body and gone with it (the foreign key cascades from chronicle_blobs, and from
-- there from projects); the GC keeps a blob any row names. The backfill reads the bodies already
-- stored: every `/blobs/<64 hex>?exp=` in them, the shape blob.Signer writes.
CREATE TABLE chronicle_blob_refs (
    project_id TEXT NOT NULL,
    object_key TEXT NOT NULL,
    blob_sha256 TEXT NOT NULL,
    PRIMARY KEY (project_id, object_key, blob_sha256),
    FOREIGN KEY (project_id, object_key) REFERENCES chronicle_blobs(project_id, object_key) ON DELETE CASCADE
);
CREATE INDEX chronicle_blob_refs_blob ON chronicle_blob_refs (blob_sha256);
WITH RECURSIVE scan(project_id, object_key, rest) AS (
    SELECT project_id, object_key, CAST(body AS TEXT) FROM chronicle_blobs WHERE instr(CAST(body AS TEXT), '/blobs/') > 0
    UNION ALL
    SELECT project_id, object_key, substr(rest, instr(rest, '/blobs/') + 7) FROM scan WHERE instr(rest, '/blobs/') > 0
)
INSERT OR IGNORE INTO chronicle_blob_refs (project_id, object_key, blob_sha256)
SELECT project_id, object_key, substr(rest, 1, 64) FROM scan
WHERE substr(rest, 65, 5) = '?exp=' AND length(substr(rest, 1, 64)) = 64 AND substr(rest, 1, 64) NOT GLOB '*[^0-9a-f]*';

-- +goose Down
DROP TABLE chronicle_blob_refs;
