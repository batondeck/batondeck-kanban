-- name: InsertSprint :one
INSERT INTO sprints (project_id, id, board_id, name, goal, acceptance, status, member_count, version, created_by, created_at, updated_by, updated_at)
VALUES (?, ?, ?, ?, ?, ?, 'PROPOSED', 0, 1, ?, ?, ?, ?)
RETURNING *;

-- name: GetSprint :one
SELECT * FROM sprints WHERE project_id = ? AND id = ?;

-- One page of the project's sprints, oldest first ((created_at, id) ascending, the id as bytes),
-- strictly after the cursor's (after_created, after_id); after_id '' is the first page, an empty
-- board or status every one.
-- name: ListSprints :many
SELECT * FROM sprints s
WHERE s.project_id = sqlc.arg(project_id)
  AND (CAST(sqlc.arg(board_id) AS TEXT) = '' OR s.board_id = CAST(sqlc.arg(board_id) AS TEXT))
  AND (CAST(sqlc.arg(status) AS TEXT) = '' OR s.status = CAST(sqlc.arg(status) AS TEXT))
  AND (CAST(sqlc.arg(after_id) AS TEXT) = '' OR s.created_at > CAST(sqlc.arg(after_created) AS INTEGER)
       OR (s.created_at = CAST(sqlc.arg(after_created) AS INTEGER) AND s.id > CAST(sqlc.arg(after_id) AS TEXT)))
ORDER BY s.created_at, s.id
LIMIT sqlc.arg(page_limit);

-- The whole row as the caller merged it, compare-and-set on version.
-- name: UpdateSprint :one
UPDATE sprints SET name = ?, goal = ?, acceptance = ?, status = ?, proposal_task_id = ?, approved_by = ?, checkpoint = ?, event_cursor = ?, started_at = ?, closed_at = ?, version = version + 1, updated_by = ?, updated_at = ?
WHERE project_id = ? AND id = ? AND version = ?
RETURNING *;

-- The member count moved by delta, never below 0; the version is kept.
-- name: AddSprintMembers :one
UPDATE sprints SET member_count = MAX(0, member_count + CAST(sqlc.arg(delta) AS INTEGER))
WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id)
RETURNING *;

-- The tasks of the sprint's board whose sprint_ids hold the sprint, (order key, id) ascending as bytes.
-- name: ListSprintMembers :many
SELECT t.project_id, t.id, t.board_id, t.column_id, t.title, t.description, t.status, t.blocked_reason, t.priority, t.assignee, t.labels, t.required_capabilities, t.estimate, t.due_at, t.parent_task_id, t.order_key, t.custom_fields, t.blocked_by, t.blocks, t.deliverable, t.reviewer, t.review, t.chars, t.model_hint, t.run_of, t.sprint_ids, t.racing, t.race_opened_at, t.claim_attempts, t.next_eligible_at, t.score, t.search_tokens, t.claim_owner, t.claim_agent, t.claim_agent_id, t.lease_id, t.lease_expires_at, t.version, t.created_by, t.created_by_agent, t.created_at, t.updated_by, t.updated_at
FROM sprints s JOIN tasks t ON t.project_id = s.project_id AND t.board_id = s.board_id
WHERE s.project_id = sqlc.arg(project_id) AND s.id = sqlc.arg(sprint_id)
  AND EXISTS (SELECT 1 FROM json_each(t.sprint_ids) j WHERE j.value = s.id)
ORDER BY t.order_key, t.id
LIMIT sqlc.arg(page_limit);
