-- +goose Up
-- A Chronicle body that embeds an attachment's download URL holds that blob (P-244: every read
-- signs the stored form afresh), but the blob GC only knew attachments and memory, so deleting the
-- source task freed the file the record shows. chronicle_blob_refs names each blob a body embeds,
-- written with the body and gone with it (the foreign key cascades from chronicle_blobs, and from
-- there from projects); the GC keeps a blob any row names. The backfill reads the bodies already
-- stored: every `/blobs/<64 hex>?exp=` in them, the shape blob.Signer writes.
CREATE TABLE chronicle_blob_refs (
    project_id text NOT NULL,
    object_key text NOT NULL,
    blob_sha256 text NOT NULL,
    PRIMARY KEY (project_id, object_key, blob_sha256),
    FOREIGN KEY (project_id, object_key) REFERENCES chronicle_blobs(project_id, object_key) ON DELETE CASCADE
);
CREATE INDEX chronicle_blob_refs_blob ON chronicle_blob_refs (blob_sha256);
INSERT INTO chronicle_blob_refs (project_id, object_key, blob_sha256)
SELECT DISTINCT c.project_id, c.object_key, m[1]
FROM chronicle_blobs c, LATERAL regexp_matches(convert_from(c.body, 'UTF8'), '/blobs/([0-9a-f]{64})\?exp=', 'g') AS m
ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE chronicle_blob_refs;
