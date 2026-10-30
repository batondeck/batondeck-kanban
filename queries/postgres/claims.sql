-- The claim frontier of one board is domain.IsClaimable as a predicate: TODO, nothing in
-- blocked_by, not poisoned, not racing, no live lease (one expiring after now), past its backoff;
-- with assignee set only its tasks, without it the unassigned ones and the caller's (assigned to
-- the caller's id, or to its account email in any case: a handoff stores the login), with caps (a JSON array; JSON null for no filter) only the
-- tasks whose required capabilities are all among caps, with sprint_id set (empty for none) only the members
-- of that sprint (its id in sprint_ids). caps and sprint_id are read once, as the derived table p
-- (the shape SQLite's statement needs, so the two generate one parameter struct), and is always
-- valid JSON, so no plan can fail to cast it. Best first: score descending, then priority (urgent,
-- high, normal, low), then order key and id COLLATE "C", as bytes.

-- ClaimNext in ONE statement: the frontier's first row that no other claim holds (FOR NO KEY
-- UPDATE OF f SKIP LOCKED), locked and claimed, so concurrent callers never win the same task and
-- never wait on each other. NO KEY UPDATE, not UPDATE: the FOR KEY SHARE a concurrent child insert's
-- foreign key check takes on the task (a comment, an event) does not conflict with it, so that task
-- is not skipped (P-96). column_id '' keeps the task's column; a column that is not the board's matches no
-- row, and the caller tells that apart from an empty frontier.
-- name: ClaimNext :one
UPDATE tasks AS t SET status = 'IN_PROGRESS',
  column_id = CASE WHEN sqlc.arg(column_id)::text = '' THEN t.column_id ELSE sqlc.arg(column_id)::text END,
  claim_owner = sqlc.arg(owner)::text, claim_agent = sqlc.narg(agent)::text, claim_agent_id = sqlc.narg(agent_id)::text,
  lease_id = sqlc.arg(lease_id)::text, lease_expires_at = sqlc.arg(expires_at)::bigint, next_eligible_at = NULL,
  version = t.version + 1, updated_by = sqlc.arg(owner)::text, updated_at = sqlc.arg(now)::bigint
WHERE t.id = (
  SELECT f.id FROM tasks f, (SELECT sqlc.arg(caps)::text AS caps, sqlc.arg(sprint_id)::text AS sprint) p
  WHERE f.project_id = sqlc.arg(project_id) AND f.board_id = sqlc.arg(board_id) AND f.status = 'TODO'
    AND json_array_length(f.blocked_by::json) = 0
    AND (f.blocked_reason IS NULL OR f.blocked_reason <> 'poison')
    AND f.racing = 0
    AND (f.lease_expires_at IS NULL OR f.lease_expires_at <= sqlc.arg(now)::bigint)
    AND (f.next_eligible_at IS NULL OR f.next_eligible_at <= sqlc.arg(now)::bigint)
    AND (CASE WHEN sqlc.narg(assignee)::text IS NULL
          THEN f.assignee IS NULL OR f.assignee = sqlc.arg(caller)::text
            OR LOWER(f.assignee) = (SELECT u.email FROM users u WHERE u.id = sqlc.arg(caller)::text)
          ELSE f.assignee = sqlc.narg(assignee)::text END)
    AND CASE WHEN p.caps = 'null' THEN true ELSE f.required_capabilities::jsonb <@ p.caps::jsonb END
    AND (p.sprint = '' OR f.sprint_ids::jsonb @> jsonb_build_array(p.sprint))
    AND (sqlc.arg(column_id)::text = '' OR EXISTS (
      SELECT 1 FROM columns col WHERE col.project_id = f.project_id AND col.board_id = f.board_id AND col.id = sqlc.arg(column_id)::text))
  ORDER BY f.score DESC,
    CASE f.priority WHEN 'urgent' THEN 3 WHEN 'high' THEN 2 WHEN 'normal' THEN 1 ELSE 0 END DESC,
    f.order_key COLLATE "C", f.id COLLATE "C"
  LIMIT 1
  FOR NO KEY UPDATE OF f SKIP LOCKED) AND t.project_id = sqlc.arg(project_id) AND t.status = 'TODO'
RETURNING project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at;

-- ClaimNextWaiting is ClaimNext without SKIP LOCKED: the second look when ClaimNext found nothing.
-- A frontier row a writer holds (a mutation that bumps a task locks it from its load) is waited for,
-- not passed over, and Postgres re-checks it against the frontier once the writer commits, so a
-- task the writer moved out of TODO is not claimed (P-278 B1).
-- name: ClaimNextWaiting :one
UPDATE tasks AS t SET status = 'IN_PROGRESS',
  column_id = CASE WHEN sqlc.arg(column_id)::text = '' THEN t.column_id ELSE sqlc.arg(column_id)::text END,
  claim_owner = sqlc.arg(owner)::text, claim_agent = sqlc.narg(agent)::text, claim_agent_id = sqlc.narg(agent_id)::text,
  lease_id = sqlc.arg(lease_id)::text, lease_expires_at = sqlc.arg(expires_at)::bigint, next_eligible_at = NULL,
  version = t.version + 1, updated_by = sqlc.arg(owner)::text, updated_at = sqlc.arg(now)::bigint
WHERE t.id = (
  SELECT f.id FROM tasks f, (SELECT sqlc.arg(caps)::text AS caps, sqlc.arg(sprint_id)::text AS sprint) p
  WHERE f.project_id = sqlc.arg(project_id) AND f.board_id = sqlc.arg(board_id) AND f.status = 'TODO'
    AND json_array_length(f.blocked_by::json) = 0
    AND (f.blocked_reason IS NULL OR f.blocked_reason <> 'poison')
    AND f.racing = 0
    AND (f.lease_expires_at IS NULL OR f.lease_expires_at <= sqlc.arg(now)::bigint)
    AND (f.next_eligible_at IS NULL OR f.next_eligible_at <= sqlc.arg(now)::bigint)
    AND (CASE WHEN sqlc.narg(assignee)::text IS NULL
          THEN f.assignee IS NULL OR f.assignee = sqlc.arg(caller)::text
            OR LOWER(f.assignee) = (SELECT u.email FROM users u WHERE u.id = sqlc.arg(caller)::text)
          ELSE f.assignee = sqlc.narg(assignee)::text END)
    AND CASE WHEN p.caps = 'null' THEN true ELSE f.required_capabilities::jsonb <@ p.caps::jsonb END
    AND (p.sprint = '' OR f.sprint_ids::jsonb @> jsonb_build_array(p.sprint))
    AND (sqlc.arg(column_id)::text = '' OR EXISTS (
      SELECT 1 FROM columns col WHERE col.project_id = f.project_id AND col.board_id = f.board_id AND col.id = sqlc.arg(column_id)::text))
  ORDER BY f.score DESC,
    CASE f.priority WHEN 'urgent' THEN 3 WHEN 'high' THEN 2 WHEN 'normal' THEN 1 ELSE 0 END DESC,
    f.order_key COLLATE "C", f.id COLLATE "C"
  LIMIT 1
  FOR NO KEY UPDATE OF f) AND t.project_id = sqlc.arg(project_id) AND t.status = 'TODO'
RETURNING project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at;

-- name: PreviewFrontier :many
SELECT project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at
FROM tasks f, (SELECT sqlc.arg(caps)::text AS caps, sqlc.arg(sprint_id)::text AS sprint) p
WHERE f.project_id = sqlc.arg(project_id) AND f.board_id = sqlc.arg(board_id) AND f.status = 'TODO'
  AND json_array_length(f.blocked_by::json) = 0
  AND (f.blocked_reason IS NULL OR f.blocked_reason <> 'poison')
  AND f.racing = 0
  AND (f.lease_expires_at IS NULL OR f.lease_expires_at <= sqlc.arg(now)::bigint)
  AND (f.next_eligible_at IS NULL OR f.next_eligible_at <= sqlc.arg(now)::bigint)
  AND (CASE WHEN sqlc.narg(assignee)::text IS NULL
        THEN f.assignee IS NULL OR f.assignee = sqlc.arg(caller)::text
          OR LOWER(f.assignee) = (SELECT u.email FROM users u WHERE u.id = sqlc.arg(caller)::text)
        ELSE f.assignee = sqlc.narg(assignee)::text END)
  AND CASE WHEN p.caps = 'null' THEN true ELSE f.required_capabilities::jsonb <@ p.caps::jsonb END
  AND (p.sprint = '' OR f.sprint_ids::jsonb @> jsonb_build_array(p.sprint))
ORDER BY f.score DESC,
  CASE f.priority WHEN 'urgent' THEN 3 WHEN 'high' THEN 2 WHEN 'normal' THEN 1 ELSE 0 END DESC,
  f.order_key COLLATE "C", f.id COLLATE "C"
LIMIT sqlc.arg(page_limit)::bigint;

-- The lease moved on, in one statement while it is the caller's and live; no row is a refusal the
-- caller reads the task to name. keep_longer <> 0 keeps an expiry already later than expires_at.
-- Neither the version nor updated_at moves.
-- name: HeartbeatLease :one
UPDATE tasks SET lease_expires_at = CASE WHEN sqlc.arg(keep_longer)::bigint <> 0
  THEN GREATEST(lease_expires_at, sqlc.arg(expires_at)::bigint) ELSE sqlc.arg(expires_at)::bigint END
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND claim_owner = sqlc.arg(owner)::text
  AND lease_id = sqlc.arg(lease_id)::text AND lease_expires_at > sqlc.arg(now)::bigint
RETURNING project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at;

-- The lease dropped, under the same conditions as HeartbeatLease.
-- name: ReleaseTaskLease :one
UPDATE tasks SET claim_owner = NULL, claim_agent = NULL, claim_agent_id = NULL, lease_id = NULL, lease_expires_at = NULL
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id) AND claim_owner = sqlc.arg(owner)::text
  AND lease_id = sqlc.arg(lease_id)::text AND lease_expires_at > sqlc.arg(now)::bigint
RETURNING project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at;

-- The project's tasks (of board_id, or every board when it is '') holding a lease expired at now,
-- the oldest expiry first, then id as bytes.
-- name: ListExpiredLeases :many
SELECT project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at
FROM tasks
WHERE project_id = sqlc.arg(project_id) AND (sqlc.arg(board_id)::text = '' OR board_id = sqlc.arg(board_id)::text)
  AND claim_owner IS NOT NULL AND lease_expires_at <= sqlc.arg(now)::bigint
ORDER BY lease_expires_at, id COLLATE "C"
LIMIT sqlc.arg(page_limit)::bigint;
