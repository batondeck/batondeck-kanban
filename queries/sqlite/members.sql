-- Add or update: a second add of the same identity changes its role and keeps who added it first.
-- name: UpsertProjectMember :one
INSERT INTO project_members (project_id, identity_id, role, added_by, added_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (project_id, identity_id) DO UPDATE SET role = excluded.role
RETURNING project_id, identity_id, role, added_by, added_at;

-- name: GetProjectMember :one
SELECT project_id, identity_id, role, added_by, added_at
FROM project_members WHERE project_id = ? AND identity_id = ?;

-- name: ListProjectMembers :many
SELECT project_id, identity_id, role, added_by, added_at
FROM project_members WHERE project_id = ?
ORDER BY added_at, identity_id;

-- name: DeleteProjectMember :execrows
DELETE FROM project_members WHERE project_id = ? AND identity_id = ?;
