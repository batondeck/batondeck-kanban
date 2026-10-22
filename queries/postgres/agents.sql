-- name: GetIdentity :one
SELECT * FROM identities WHERE id = $1;

-- name: GetAgentSession :one
SELECT * FROM agent_sessions WHERE identity_id = $1 AND agent_id = $2;

-- name: GetAgentProfile :one
SELECT * FROM agent_profiles WHERE project_id = $1 AND agent_key = $2;

-- name: GetSkillStat :one
SELECT * FROM skill_stats WHERE project_id = $1 AND agent_key = $2 AND capability = $3;
