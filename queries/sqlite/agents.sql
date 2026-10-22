-- name: GetIdentity :one
SELECT * FROM identities WHERE id = ?;

-- name: GetAgentSession :one
SELECT * FROM agent_sessions WHERE identity_id = ? AND agent_id = ?;

-- name: GetAgentProfile :one
SELECT * FROM agent_profiles WHERE project_id = ? AND agent_key = ?;

-- name: GetSkillStat :one
SELECT * FROM skill_stats WHERE project_id = ? AND agent_key = ? AND capability = ?;

-- name: GetWakeSession :one
SELECT * FROM wake_sessions WHERE id = ?;
