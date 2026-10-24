-- The claim frontier of one board is domain.IsClaimable as a predicate: TODO, nothing in
-- blocked_by, not poisoned, not racing, no live lease (one expiring after now), past its backoff;
-- with assignee set only its tasks, with caps (a JSON array; JSON null for no filter) only the
-- tasks whose required capabilities are all among caps. caps is read once, as the derived table p:
-- sqlc does not bind a parameter nested deeper than one subquery under json_each. Best first: score
-- descending, then priority (urgent, high, normal, low), then order key and id as bytes (SQLite's
-- BINARY collation).

-- ClaimNext in ONE statement. The connection is the store's single writer, and the statement's own
-- transaction takes the write lock before it reads the frontier, so two callers cannot pick the same
-- row. column_id '' keeps the task's column; a column that is not the board's matches no row, and
-- the caller tells that apart from an empty frontier.
-- name: ClaimNext :one
UPDATE tasks SET status = 'IN_PROGRESS',
  column_id = CASE WHEN CAST(sqlc.arg(column_id) AS TEXT) = '' THEN column_id ELSE CAST(sqlc.arg(column_id) AS TEXT) END,
  claim_owner = CAST(sqlc.arg(owner) AS TEXT), claim_agent = CAST(sqlc.narg(agent) AS TEXT), claim_agent_id = CAST(sqlc.narg(agent_id) AS TEXT),
  lease_id = CAST(sqlc.arg(lease_id) AS TEXT), lease_expires_at = CAST(sqlc.arg(expires_at) AS INTEGER), next_eligible_at = NULL,
  version = version + 1, updated_by = CAST(sqlc.arg(owner) AS TEXT), updated_at = CAST(sqlc.arg(now) AS INTEGER)
WHERE rid = (
  SELECT f.rid FROM tasks f, (SELECT CAST(sqlc.arg(caps) AS TEXT) AS caps) p
  WHERE f.project_id = sqlc.arg(project_id) AND f.board_id = sqlc.arg(board_id) AND f.status = 'TODO'
    AND json_array_length(f.blocked_by) = 0
    AND (f.blocked_reason IS NULL OR f.blocked_reason <> 'poison')
    AND f.racing = 0
    AND (f.lease_expires_at IS NULL OR f.lease_expires_at <= CAST(sqlc.arg(now) AS INTEGER))
    AND (f.next_eligible_at IS NULL OR f.next_eligible_at <= CAST(sqlc.arg(now) AS INTEGER))
    AND (CAST(sqlc.narg(assignee) AS TEXT) IS NULL OR f.assignee = CAST(sqlc.narg(assignee) AS TEXT))
    AND CASE WHEN p.caps = 'null' THEN 1 ELSE NOT EXISTS (
      SELECT 1 FROM json_each(f.required_capabilities) r
      WHERE r.value NOT IN (SELECT c.value FROM json_each(p.caps) c)) END
    AND (CAST(sqlc.arg(column_id) AS TEXT) = '' OR EXISTS (
      SELECT 1 FROM columns col WHERE col.project_id = f.project_id AND col.board_id = f.board_id AND col.id = CAST(sqlc.arg(column_id) AS TEXT)))
  ORDER BY f.score DESC,
    CASE f.priority WHEN 'urgent' THEN 3 WHEN 'high' THEN 2 WHEN 'normal' THEN 1 ELSE 0 END DESC,
    f.order_key, f.id
  LIMIT 1)
RETURNING project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at;

-- name: PreviewFrontier :many
SELECT project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at
FROM tasks f, (SELECT CAST(sqlc.arg(caps) AS TEXT) AS caps) p
WHERE f.project_id = sqlc.arg(project_id) AND f.board_id = sqlc.arg(board_id) AND f.status = 'TODO'
  AND json_array_length(f.blocked_by) = 0
  AND (f.blocked_reason IS NULL OR f.blocked_reason <> 'poison')
  AND f.racing = 0
  AND (f.lease_expires_at IS NULL OR f.lease_expires_at <= CAST(sqlc.arg(now) AS INTEGER))
  AND (f.next_eligible_at IS NULL OR f.next_eligible_at <= CAST(sqlc.arg(now) AS INTEGER))
  AND (CAST(sqlc.narg(assignee) AS TEXT) IS NULL OR f.assignee = CAST(sqlc.narg(assignee) AS TEXT))
  AND CASE WHEN p.caps = 'null' THEN 1 ELSE NOT EXISTS (
    SELECT 1 FROM json_each(f.required_capabilities) r
    WHERE r.value NOT IN (SELECT c.value FROM json_each(p.caps) c)) END
ORDER BY f.score DESC,
  CASE f.priority WHEN 'urgent' THEN 3 WHEN 'high' THEN 2 WHEN 'normal' THEN 1 ELSE 0 END DESC,
  f.order_key, f.id
LIMIT sqlc.arg(page_limit);

-- The lease moved on, in one statement while it is the caller's and live; no row is a refusal the
-- caller reads the task to name. keep_longer <> 0 keeps an expiry already later than expires_at.
-- Neither the version nor updated_at moves.
-- name: HeartbeatLease :one
UPDATE tasks SET lease_expires_at = CASE WHEN CAST(sqlc.arg(keep_longer) AS INTEGER) <> 0
  THEN MAX(lease_expires_at, CAST(sqlc.arg(expires_at) AS INTEGER)) ELSE CAST(sqlc.arg(expires_at) AS INTEGER) END
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND claim_owner = CAST(sqlc.arg(owner) AS TEXT)
  AND lease_id = CAST(sqlc.arg(lease_id) AS TEXT) AND lease_expires_at > CAST(sqlc.arg(now) AS INTEGER)
RETURNING project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at;

-- The lease dropped, under the same conditions as HeartbeatLease.
-- name: ReleaseTaskLease :one
UPDATE tasks SET claim_owner = NULL, claim_agent = NULL, claim_agent_id = NULL, lease_id = NULL, lease_expires_at = NULL
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND claim_owner = CAST(sqlc.arg(owner) AS TEXT)
  AND lease_id = CAST(sqlc.arg(lease_id) AS TEXT) AND lease_expires_at > CAST(sqlc.arg(now) AS INTEGER)
RETURNING project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at;

-- The project's tasks (of board_id, or every board when it is '') holding a lease expired at now,
-- the oldest expiry first, then id.
-- name: ListExpiredLeases :many
SELECT project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at
FROM tasks
WHERE project_id = sqlc.arg(project_id) AND (CAST(sqlc.arg(board_id) AS TEXT) = '' OR board_id = CAST(sqlc.arg(board_id) AS TEXT))
  AND claim_owner IS NOT NULL AND lease_expires_at <= CAST(sqlc.arg(now) AS INTEGER)
ORDER BY lease_expires_at, id
LIMIT sqlc.arg(page_limit);
