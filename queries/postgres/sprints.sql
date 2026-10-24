-- name: InsertSprint :one
INSERT INTO sprints (project_id, id, board_id, name, goal, acceptance, status, member_count, version, created_by, created_at, updated_by, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, 'PROPOSED', 0, 1, $7, $8, $9, $10)
RETURNING *;

-- name: GetSprint :one
SELECT * FROM sprints WHERE project_id = $1 AND id = $2;

-- One page of the project's sprints, oldest first ((created_at, id) ascending, the id as bytes),
-- strictly after the cursor's (after_created, after_id); after_id '' is the first page, an empty
-- board or status every one.
-- name: ListSprints :many
SELECT * FROM sprints s
WHERE s.project_id = sqlc.arg(project_id)
  AND (sqlc.arg(board_id)::text = '' OR s.board_id = sqlc.arg(board_id)::text)
  AND (sqlc.arg(status)::text = '' OR s.status = sqlc.arg(status)::text)
  AND (sqlc.arg(after_id)::text = '' OR s.created_at > sqlc.arg(after_created)::bigint
       OR (s.created_at = sqlc.arg(after_created)::bigint AND s.id COLLATE "C" > sqlc.arg(after_id)::text))
ORDER BY s.created_at, s.id COLLATE "C"
LIMIT sqlc.arg(page_limit)::bigint;

-- The whole row as the caller merged it, compare-and-set on version.
-- name: UpdateSprint :one
UPDATE sprints SET name = $1, goal = $2, acceptance = $3, status = $4, proposal_task_id = $5, approved_by = $6, checkpoint = $7, event_cursor = $8, started_at = $9, closed_at = $10, version = version + 1, updated_by = $11, updated_at = $12
WHERE project_id = $13 AND id = $14 AND version = $15
RETURNING *;

-- The member count moved by delta, never below 0; the version is kept.
-- name: AddSprintMembers :one
UPDATE sprints SET member_count = GREATEST(0, member_count + sqlc.arg(delta)::bigint)
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id)
RETURNING *;

-- The tasks of the sprint's board whose sprint_ids hold the sprint, (order key, id) ascending as bytes.
-- name: ListSprintMembers :many
SELECT t.project_id, t.id, t.board_id, t.column_id, t.title, t.description, t.status, t.blocked_reason, t.priority, t.assignee, t.labels, t.required_capabilities, t.estimate, t.due_at, t.parent_task_id, t.order_key, t.custom_fields, t.blocked_by, t.blocks, t.deliverable, t.reviewer, t.review, t.chars, t.model_hint, t.run_of, t.sprint_ids, t.racing, t.race_opened_at, t.claim_attempts, t.next_eligible_at, t.score, t.search_tokens, t.claim_owner, t.claim_agent, t.claim_agent_id, t.lease_id, t.lease_expires_at, t.version, t.created_by, t.created_by_agent, t.created_at, t.updated_by, t.updated_at
FROM sprints s JOIN tasks t ON t.project_id = s.project_id AND t.board_id = s.board_id
WHERE s.project_id = sqlc.arg(project_id) AND s.id = sqlc.arg(sprint_id)
  AND t.sprint_ids::jsonb @> jsonb_build_array(s.id)
ORDER BY t.order_key COLLATE "C", t.id COLLATE "C"
LIMIT sqlc.arg(page_limit)::bigint;
