-- name: GetProject :one
SELECT * FROM projects WHERE id = ?;

-- name: GetProjectMember :one
SELECT * FROM project_members WHERE project_id = ? AND identity_id = ?;

-- name: GetCapabilityGrant :one
SELECT * FROM capability_grants WHERE project_id = ? AND identity_id = ?;

-- name: NextTaskNumber :one
INSERT INTO project_counters (project_id, last_task) VALUES (?, 1)
ON CONFLICT (project_id) DO UPDATE SET last_task = project_counters.last_task + 1
RETURNING last_task;
