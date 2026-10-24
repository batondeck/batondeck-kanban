-- The notification area: one recipient's in-app feed, cross-project, keyed (identity_id, id).

-- One row, into an existing project: no row back is a missing project.
-- name: InsertNotification :one
INSERT INTO notifications (identity_id, id, project_id, board_id, type, task_id, actor, data, read, ts)
SELECT CAST(sqlc.arg(identity_id) AS TEXT), CAST(sqlc.arg(id) AS TEXT), p.id, CAST(sqlc.narg(board_id) AS TEXT),
  CAST(sqlc.arg(type) AS TEXT), CAST(sqlc.narg(task_id) AS TEXT), CAST(sqlc.arg(actor) AS TEXT), CAST(sqlc.arg(data) AS TEXT),
  0, CAST(sqlc.arg(ts) AS BIGINT)
FROM projects p WHERE p.id = sqlc.arg(project_id)
RETURNING *;

-- One page of the recipient's feed, newest first ((ts, id) descending, ids compared as bytes),
-- strictly after the cursor's (after_ts, after_id); after_id '' is the first page.
-- name: ListNotificationsNewest :many
SELECT * FROM notifications
WHERE identity_id = sqlc.arg(identity_id)
  AND (CAST(sqlc.arg(after_id) AS TEXT) = '' OR ts < CAST(sqlc.arg(after_ts) AS BIGINT)
       OR (ts = CAST(sqlc.arg(after_ts) AS BIGINT) AND id COLLATE "C" < CAST(sqlc.arg(after_id) AS TEXT) COLLATE "C"))
ORDER BY ts DESC, id COLLATE "C" DESC
LIMIT sqlc.arg(page_limit)::bigint;

-- name: CountUnreadNotifications :one
SELECT COUNT(*) FROM notifications WHERE identity_id = $1 AND read = 0;

-- Flips the named unread rows; the count is the rows flipped.
-- name: MarkNotificationsReadByID :execrows
UPDATE notifications SET read = 1
WHERE identity_id = sqlc.arg(identity_id) AND read = 0 AND id = ANY(sqlc.arg(ids)::text[]);

-- name: MarkAllNotificationsRead :execrows
UPDATE notifications SET read = 1 WHERE identity_id = $1 AND read = 0;
