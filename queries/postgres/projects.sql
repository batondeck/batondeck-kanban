-- name: GetProject :one
SELECT * FROM projects WHERE id = $1;

-- name: GetProjectMember :one
SELECT * FROM project_members WHERE project_id = $1 AND identity_id = $2;

-- name: GetCapabilityGrant :one
SELECT * FROM capability_grants WHERE project_id = $1 AND identity_id = $2;

-- name: NextTaskNumber :one
INSERT INTO project_counters (project_id, last_task) VALUES ($1, 1)
ON CONFLICT (project_id) DO UPDATE SET last_task = project_counters.last_task + 1
RETURNING last_task;
