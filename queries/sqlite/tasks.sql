-- Every query that returns a task selects exactly these columns, in this order, so each one's row
-- converts to GetTaskRow and is mapped once.
-- name: GetTask :one
SELECT project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at FROM tasks WHERE project_id = ? AND id = ?;

-- The read a versioned write starts from: on Postgres the row is locked to the transaction, so two
-- writers of one task take turns; SQLite's transaction holds the write lock from BEGIN.
-- name: LockTask :one
SELECT project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at FROM tasks WHERE project_id = ? AND id = ?;

-- name: InsertTask :one
INSERT INTO tasks (project_id, id, board_id, column_id, title, description, status, priority, assignee, labels, required_capabilities, parent_task_id, order_key, custom_fields, model_hint, run_of, sprint_ids, version, created_by, created_by_agent, created_at, updated_by, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?)
RETURNING project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at;

-- UpdateTask's write: the patchable fields. The only task write that names title, description or
-- search_tokens, the columns the FTS trigger watches.
-- name: WriteTaskFields :one
UPDATE tasks SET title = ?, description = ?, priority = ?, assignee = ?, labels = ?, required_capabilities = ?, estimate = ?, due_at = ?, custom_fields = ?, deliverable = ?, reviewer = ?, review = ?, chars = ?, model_hint = ?, sprint_ids = ?, racing = ?, race_opened_at = ?, score = ?, search_tokens = ?, version = ?, updated_by = ?, updated_at = ?
WHERE project_id = ? AND id = ?
RETURNING project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at;

-- The write of SetStatus, SetOrderKey and ClaimTask: status, place, retry state, lease, and the
-- assignee, deliverable, review and race flag a SetStatus may carry with them.
-- name: WriteTaskState :one
UPDATE tasks SET status = ?, column_id = ?, order_key = ?, blocked_reason = ?, labels = ?, claim_attempts = ?, next_eligible_at = ?, claim_owner = ?, claim_agent = ?, claim_agent_id = ?, lease_id = ?, lease_expires_at = ?, assignee = ?, deliverable = ?, review = ?, racing = ?, race_opened_at = ?, version = ?, updated_by = ?, updated_at = ?
WHERE project_id = ? AND id = ?
RETURNING project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at;

-- One page of a board's tasks: (order key, id) ascending, as bytes (SQLite's BINARY collation),
-- strictly after the cursor's (after_order, after_id); after_id '' is the first page. A NULL
-- filter matches every task; assignee_mode 0 is any, 1 exactly assignee, 2 unassigned.
-- name: ListTasks :many
SELECT project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at
FROM tasks t
WHERE t.project_id = sqlc.arg(project_id) AND t.board_id = sqlc.arg(board_id)
  AND (CAST(sqlc.narg(status) AS TEXT) IS NULL OR t.status = CAST(sqlc.narg(status) AS TEXT))
  AND (CAST(sqlc.arg(column_id) AS TEXT) = '' OR t.column_id = CAST(sqlc.arg(column_id) AS TEXT))
  AND (CAST(sqlc.arg(assignee_mode) AS INTEGER) = 0
       OR (CAST(sqlc.arg(assignee_mode) AS INTEGER) = 1 AND t.assignee = sqlc.arg(assignee))
       OR (CAST(sqlc.arg(assignee_mode) AS INTEGER) = 2 AND t.assignee IS NULL))
  AND (CAST(sqlc.arg(label) AS TEXT) = '' OR EXISTS (SELECT 1 FROM json_each(t.labels) l WHERE l.value = CAST(sqlc.arg(label) AS TEXT)))
  AND (CAST(sqlc.narg(run_of) AS TEXT) IS NULL OR t.run_of = CAST(sqlc.narg(run_of) AS TEXT))
  AND (CAST(sqlc.arg(after_id) AS TEXT) = '' OR t.order_key > CAST(sqlc.arg(after_order) AS TEXT) OR (t.order_key = CAST(sqlc.arg(after_order) AS TEXT) AND t.id > CAST(sqlc.arg(after_id) AS TEXT)))
ORDER BY t.order_key, t.id
LIMIT sqlc.arg(page_limit);

-- How many of the board's tasks match, and whether the board is the project's, in one statement.
-- name: CountTasks :one
SELECT
  CAST(EXISTS (SELECT 1 FROM boards b WHERE b.project_id = sqlc.arg(project_id) AND b.id = sqlc.arg(board_id)) AS INTEGER) AS board_found,
  CAST((SELECT COUNT(*) FROM tasks t
   WHERE t.project_id = sqlc.arg(project_id) AND t.board_id = sqlc.arg(board_id)
     AND (CAST(sqlc.narg(status) AS TEXT) IS NULL OR t.status = CAST(sqlc.narg(status) AS TEXT))
     AND (CAST(sqlc.arg(column_id) AS TEXT) = '' OR t.column_id = CAST(sqlc.arg(column_id) AS TEXT))
     AND (CAST(sqlc.arg(assignee_mode) AS INTEGER) = 0
          OR (CAST(sqlc.arg(assignee_mode) AS INTEGER) = 1 AND t.assignee = sqlc.arg(assignee))
          OR (CAST(sqlc.arg(assignee_mode) AS INTEGER) = 2 AND t.assignee IS NULL))
     AND (CAST(sqlc.arg(label) AS TEXT) = '' OR EXISTS (SELECT 1 FROM json_each(t.labels) l WHERE l.value = CAST(sqlc.arg(label) AS TEXT)))
     AND (CAST(sqlc.narg(run_of) AS TEXT) IS NULL OR t.run_of = CAST(sqlc.narg(run_of) AS TEXT))) AS INTEGER) AS task_count;

-- name: ListTasksByIDs :many
SELECT project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at
FROM tasks WHERE project_id = sqlc.arg(project_id) AND id IN (sqlc.slice(ids));

-- The delete at a version: no row is a missing task or another version, which the caller tells apart.
-- name: DeleteTask :execrows
DELETE FROM tasks WHERE project_id = ? AND id = ? AND version = ?;
