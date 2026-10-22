-- name: GetChroniclePage :one
SELECT * FROM chronicle_pages WHERE project_id = ? AND id = ?;

-- name: GetChronicleVersion :one
SELECT * FROM chronicle_versions WHERE project_id = ? AND page_id = ? AND id = ?;

-- name: GetChronicleBlob :one
SELECT * FROM chronicle_blobs WHERE project_id = ? AND object_key = ?;
