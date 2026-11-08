-- name: AppendAudit :one
INSERT INTO audit_log (ts, action, actor, agent, subject, project_id, result, error_code, data)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id;

-- One page of the audit log, newest first ((ts, id) descending), strictly after the cursor's
-- (after_ts, after_id). An empty string filter matches every row; since and until bound ts,
-- inclusive (the caller passes the extremes for "unbounded", and for "no cursor"). A non-empty
-- subject also matches subject_alias (the caller passes the subject itself when it has no alias).
-- name: ListAuditLog :many
SELECT id, ts, action, actor, agent, subject, project_id, result, error_code, data FROM audit_log
WHERE (sqlc.arg(project_id)::text = '' OR project_id = sqlc.arg(project_id)::text)
  AND (sqlc.arg(actor)::text = '' OR actor = sqlc.arg(actor)::text)
  AND (sqlc.arg(action)::text = '' OR action = sqlc.arg(action)::text)
  AND (sqlc.arg(subject)::text = '' OR subject = sqlc.arg(subject)::text
       OR subject = sqlc.arg(subject_alias)::text)
  AND ts >= sqlc.arg(since)::bigint AND ts <= sqlc.arg(until)::bigint
  AND (ts < sqlc.arg(after_ts)::bigint
       OR (ts = sqlc.arg(after_ts)::bigint AND id < sqlc.arg(after_id)::bigint))
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(page_limit)::bigint;
