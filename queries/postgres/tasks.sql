-- name: GetTask :one
SELECT project_id, id, board_id, column_id, title, description, status, blocked_reason, priority, assignee, labels, required_capabilities, estimate, due_at, parent_task_id, order_key, custom_fields, blocked_by, blocks, deliverable, reviewer, review, chars, model_hint, run_of, sprint_ids, racing, race_opened_at, claim_attempts, next_eligible_at, score, search_tokens, claim_owner, claim_agent, claim_agent_id, lease_id, lease_expires_at, version, created_by, created_by_agent, created_at, updated_by, updated_at FROM tasks WHERE project_id = $1 AND id = $2;

-- name: SearchTasks :many
SELECT project_id, id FROM tasks
WHERE search @@ plainto_tsquery('simple', $1) AND project_id = $2;

-- name: GetArtifact :one
SELECT * FROM artifacts WHERE project_id = $1 AND task_id = $2 AND id = $3;

-- name: GetSummary :one
SELECT * FROM summaries WHERE project_id = $1 AND task_id = $2;

-- name: GetRun :one
SELECT * FROM runs WHERE project_id = $1 AND run_task_id = $2;
