-- name: GetCapabilityGrant :one
SELECT * FROM capability_grants WHERE project_id = ? AND identity_id = ?;

-- A second grant to the same member replaces the first.
-- name: UpsertCapabilityGrant :one
INSERT INTO capability_grants (project_id, identity_id, grant_json, updated_by, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (project_id, identity_id) DO UPDATE
SET grant_json = excluded.grant_json, updated_by = excluded.updated_by, updated_at = excluded.updated_at
RETURNING *;

-- name: DeleteCapabilityGrant :execrows
DELETE FROM capability_grants WHERE project_id = ? AND identity_id = ?;

-- Every membership of one identity with its grant, if any: the tools/list narrowing reads it.
-- name: ListMemberGrants :many
SELECT m.project_id, m.role, g.grant_json
FROM project_members m
LEFT JOIN capability_grants g ON g.project_id = m.project_id AND g.identity_id = m.identity_id
WHERE m.identity_id = ?
ORDER BY m.project_id;
