-- Every query that returns a task selects exactly these columns, in this order, so each one's row
-- converts to GetTaskRow and is mapped once. Order keys and ids are ordered and compared COLLATE
-- "C", as bytes, whatever the database's collation: lexorank needs "Zz" before "a0".
-- name: GetTask :one
SELECT project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at FROM tasks WHERE project_id = $1 AND id = $2;

-- The read a versioned write starts from: the row is locked to the transaction, so two writers of
-- one task take turns.
-- name: LockTask :one
SELECT project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at FROM tasks WHERE project_id = $1 AND id = $2 FOR UPDATE;

-- name: InsertTask :one
INSERT INTO tasks (project_id, id, board_id, column_id, title, description, status, priority, assignee, labels, required_capabilities, parent_task_id, order_key, custom_fields, model_hint, run_of, sprint_ids, version, created_by, created_by_agent, created_at, updated_by, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, 1, $18, $19, $20, $21, $22)
RETURNING project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at;

-- UpdateTask's write: the patchable fields. The only task write that names title, description or
-- search_tokens, the columns the search vector is generated from.
-- name: WriteTaskFields :one
UPDATE tasks SET title = $1, description = $2, priority = $3, assignee = $4, labels = $5, required_capabilities = $6, estimate = $7, due_at = $8, custom_fields = $9, deliverable = $10, reviewer = $11, review = $12, chars = $13, model_hint = $14, sprint_ids = $15, racing = $16, race_opened_at = $17, score = $18, search_tokens = $19, version = $20, updated_by = $21, updated_at = $22
WHERE project_id = $23 AND id = $24
RETURNING project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at;

-- The write of SetStatus, SetOrderKey and ClaimTask: status, place, retry state and lease.
-- name: WriteTaskState :one
UPDATE tasks SET status = $1, column_id = $2, order_key = $3, blocked_reason = $4, labels = $5, claim_attempts = $6, next_eligible_at = $7, claim_owner = $8, claim_agent = $9, claim_agent_id = $10, lease_id = $11, lease_expires_at = $12, version = $13, updated_by = $14, updated_at = $15
WHERE project_id = $16 AND id = $17
RETURNING project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at;

-- One page of a board's tasks: (order key, id) ascending, as bytes, strictly after the cursor's
-- (after_order, after_id); after_id '' is the first page. A NULL filter matches every task;
-- assignee_mode 0 is any, 1 exactly assignee, 2 unassigned.
-- name: ListTasks :many
SELECT project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at
FROM tasks t
WHERE t.project_id = sqlc.arg(project_id) AND t.board_id = sqlc.arg(board_id)
  AND (sqlc.narg(status)::text IS NULL OR t.status = sqlc.narg(status)::text)
  AND (sqlc.arg(column_id)::text = '' OR t.column_id = sqlc.arg(column_id)::text)
  AND (sqlc.arg(assignee_mode)::bigint = 0
       OR (sqlc.arg(assignee_mode)::bigint = 1 AND t.assignee = sqlc.narg(assignee)::text)
       OR (sqlc.arg(assignee_mode)::bigint = 2 AND t.assignee IS NULL))
  AND (sqlc.arg(label)::text = '' OR t.labels::jsonb @> jsonb_build_array(sqlc.arg(label)::text))
  AND (sqlc.narg(run_of)::text IS NULL OR t.run_of = sqlc.narg(run_of)::text)
  AND (sqlc.arg(after_id)::text = '' OR t.order_key COLLATE "C" > sqlc.arg(after_order)::text
       OR (t.order_key = sqlc.arg(after_order)::text AND t.id COLLATE "C" > sqlc.arg(after_id)::text))
ORDER BY t.order_key COLLATE "C", t.id COLLATE "C"
LIMIT sqlc.arg(page_limit)::bigint;

-- How many of the board's tasks match, and whether the board is the project's, in one statement.
-- name: CountTasks :one
SELECT
  (CASE WHEN EXISTS (SELECT 1 FROM boards b WHERE b.project_id = sqlc.arg(project_id) AND b.id = sqlc.arg(board_id)) THEN 1 ELSE 0 END)::bigint AS board_found,
  (SELECT COUNT(*) FROM tasks t
   WHERE t.project_id = sqlc.arg(project_id) AND t.board_id = sqlc.arg(board_id)
     AND (sqlc.narg(status)::text IS NULL OR t.status = sqlc.narg(status)::text)
     AND (sqlc.arg(column_id)::text = '' OR t.column_id = sqlc.arg(column_id)::text)
     AND (sqlc.arg(assignee_mode)::bigint = 0
          OR (sqlc.arg(assignee_mode)::bigint = 1 AND t.assignee = sqlc.narg(assignee)::text)
          OR (sqlc.arg(assignee_mode)::bigint = 2 AND t.assignee IS NULL))
     AND (sqlc.arg(label)::text = '' OR t.labels::jsonb @> jsonb_build_array(sqlc.arg(label)::text))
     AND (sqlc.narg(run_of)::text IS NULL OR t.run_of = sqlc.narg(run_of)::text))::bigint AS task_count;

-- name: ListTasksByIDs :many
SELECT project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at
FROM tasks WHERE project_id = sqlc.arg(project_id) AND id = ANY(sqlc.arg(ids)::text[]);

-- The delete at a version: no row is a missing task or another version, which the caller tells apart.
-- name: DeleteTask :execrows
DELETE FROM tasks WHERE project_id = $1 AND id = $2 AND version = $3;
