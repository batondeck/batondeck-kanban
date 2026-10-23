-- The Chronicle: pages, their immutable versions, and the bodies those point at. A body is a
-- chronicle_blobs row keyed "<pageId>/<sha256 of the body>": content-addressed within its page, so a
-- page's bodies are one key range and leave with the page. Every key carries project_id first: a page
-- id is a hash of the slug, so two projects recording one slug share it.

-- name: GetChroniclePage :one
SELECT * FROM chronicle_pages WHERE project_id = $1 AND id = $2;

-- The page id is the slug's hash, so a second writer of one slug inserts nothing: the caller reads
-- the row it lost to.
-- name: InsertChroniclePage :execrows
INSERT INTO chronicle_pages (project_id, id, kind, slug, title, head_version_id, source_path, last_derived_object,
  conflict, topics, coverage, superseded_by, updated_by, deleting, created_at, updated_at)
VALUES (sqlc.arg(project_id), sqlc.arg(id), sqlc.arg(kind), sqlc.arg(slug), sqlc.arg(title), sqlc.arg(head_version_id),
  sqlc.narg(source_path), sqlc.narg(last_derived_object), NULL, sqlc.arg(topics), sqlc.narg(coverage),
  sqlc.arg(superseded_by), sqlc.narg(updated_by), 0, sqlc.arg(created_at), sqlc.arg(created_at))
ON CONFLICT (project_id, id) DO NOTHING;

-- name: RefreshChroniclePage :execrows
UPDATE chronicle_pages SET kind = sqlc.arg(kind), title = sqlc.arg(title), source_path = sqlc.narg(source_path),
  topics = sqlc.arg(topics), coverage = sqlc.narg(coverage), superseded_by = sqlc.arg(superseded_by), updated_at = sqlc.arg(updated_at)
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- The head moves only from the version the writer read: no row is a stale or missing page.
-- name: MoveChronicleHead :execrows
UPDATE chronicle_pages SET head_version_id = sqlc.arg(head_version_id), last_derived_object = sqlc.narg(last_derived_object),
  conflict = sqlc.narg(conflict), updated_by = sqlc.narg(updated_by), updated_at = sqlc.arg(updated_at)
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND head_version_id = sqlc.arg(expected_head_version_id);

-- name: SetChronicleConflict :execrows
UPDATE chronicle_pages SET conflict = sqlc.narg(conflict), updated_at = sqlc.arg(updated_at)
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND head_version_id = sqlc.arg(expected_head_version_id);

-- expected_head_version_id '' deletes whatever the head; else only from that head.
-- name: DeleteChroniclePage :execrows
DELETE FROM chronicle_pages WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id)
  AND (sqlc.arg(expected_head_version_id)::text = '' OR head_version_id = sqlc.arg(expected_head_version_id)::text);

-- One page of a project's pages, by slug, after the cursor's slug; kind '' is every kind.
-- name: ListChroniclePages :many
SELECT * FROM chronicle_pages
WHERE project_id = sqlc.arg(project_id)
  AND (sqlc.arg(kind)::text = '' OR kind = sqlc.arg(kind)::text)
  AND slug COLLATE "C" > sqlc.arg(after_slug)::text
ORDER BY slug COLLATE "C"
LIMIT sqlc.arg(page_limit)::bigint;

-- name: InsertChronicleVersion :exec
INSERT INTO chronicle_versions (project_id, page_id, id, parent_version_id, source, author, agent, ts, blocks_object, block_count)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: GetChronicleVersion :one
SELECT * FROM chronicle_versions WHERE project_id = $1 AND page_id = $2 AND id = $3;

-- One page of a page's versions, newest first ((ts, id) descending), strictly after the cursor's
-- (after_ts, after_id); after_id '' is the first page.
-- name: ListChronicleVersions :many
SELECT * FROM chronicle_versions
WHERE project_id = sqlc.arg(project_id) AND page_id = sqlc.arg(page_id)
  AND (sqlc.arg(after_id)::text = '' OR ts < sqlc.arg(after_ts)::bigint
       OR (ts = sqlc.arg(after_ts)::bigint AND id COLLATE "C" < sqlc.arg(after_id)::text))
ORDER BY ts DESC, id COLLATE "C" DESC
LIMIT sqlc.arg(page_limit)::bigint;

-- name: InsertChronicleBlob :exec
INSERT INTO chronicle_blobs (project_id, object_key, body, created_at) VALUES ($1, $2, $3, $4)
ON CONFLICT (project_id, object_key) DO NOTHING;

-- name: GetChronicleBlob :one
SELECT * FROM chronicle_blobs WHERE project_id = $1 AND object_key = $2;

-- A page's bodies are the key range ["<pageId>/", "<pageId>0"): '0' is the byte after '/'.
-- name: DeleteChronicleBlobsByPage :execrows
DELETE FROM chronicle_blobs
WHERE project_id = sqlc.arg(project_id) AND object_key COLLATE "C" >= sqlc.arg(key_from)::text AND object_key COLLATE "C" < sqlc.arg(key_to)::text;

-- A body a pointer stopped naming (an old merge base or conflict copy) goes when no version names
-- it either: the count, then the delete, both under the page row's lock the pointer move took.
-- name: CountChronicleVersionsNaming :one
SELECT COUNT(*) FROM chronicle_versions WHERE project_id = $1 AND page_id = $2 AND blocks_object = $3;

-- name: DeleteChronicleBlob :execrows
DELETE FROM chronicle_blobs WHERE project_id = $1 AND object_key = $2;

-- name: ListChronicleObjects :many
SELECT object_key FROM chronicle_blobs WHERE project_id = $1 ORDER BY object_key COLLATE "C";

-- A chronicle event names no board and no task.
-- name: AppendChronicleEvent :exec
INSERT INTO task_events (project_id, id, board_id, task_id, actor, agent, type, data, ts)
VALUES (sqlc.arg(project_id), sqlc.arg(id), NULL, NULL, sqlc.arg(actor), sqlc.narg(agent), sqlc.arg(type), sqlc.arg(data), sqlc.arg(ts));
