-- name: GetChroniclePage :one
SELECT * FROM chronicle_pages WHERE project_id = $1 AND id = $2;

-- name: GetChronicleVersion :one
SELECT * FROM chronicle_versions WHERE project_id = $1 AND page_id = $2 AND id = $3;

-- name: GetChronicleBlob :one
SELECT * FROM chronicle_blobs WHERE project_id = $1 AND object_key = $2;
