-- name: InsertToolCall :exec
INSERT INTO tool_calls (ts, tool, identity, agent, outcome, code, latency_ms, project_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- At most page_limit rows older than before, oldest first.
-- name: DeleteToolCallsOlderThan :execrows
DELETE FROM tool_calls WHERE id IN (
  SELECT t.id FROM tool_calls t WHERE t.ts < sqlc.arg(before)::bigint ORDER BY t.ts, t.id LIMIT sqlc.arg(page_limit)::bigint);
