-- name: AppendAudit :one
INSERT INTO audit_log (ts, action, actor, agent, subject, project_id, result, error_code, data)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- One page of the audit log, newest first ((ts, id) descending), strictly after the cursor's
-- (after_ts, after_id). An empty string filter matches every row; since and until bound ts,
-- inclusive (the caller passes the extremes for "unbounded", and for "no cursor").
-- name: ListAuditLog :many
SELECT id, ts, action, actor, agent, subject, project_id, result, error_code, data FROM audit_log
WHERE (CAST(sqlc.arg(project_id) AS TEXT) = '' OR project_id = CAST(sqlc.arg(project_id) AS TEXT))
  AND (CAST(sqlc.arg(actor) AS TEXT) = '' OR actor = CAST(sqlc.arg(actor) AS TEXT))
  AND (CAST(sqlc.arg(action) AS TEXT) = '' OR action = CAST(sqlc.arg(action) AS TEXT))
  AND (CAST(sqlc.arg(subject) AS TEXT) = '' OR subject = CAST(sqlc.arg(subject) AS TEXT))
  AND ts >= CAST(sqlc.arg(since) AS INTEGER) AND ts <= CAST(sqlc.arg(until) AS INTEGER)
  AND (ts < CAST(sqlc.arg(after_ts) AS INTEGER)
       OR (ts = CAST(sqlc.arg(after_ts) AS INTEGER) AND id < CAST(sqlc.arg(after_id) AS INTEGER)))
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(page_limit);
