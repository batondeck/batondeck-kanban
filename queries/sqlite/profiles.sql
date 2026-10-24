-- The agent capability registry and skill stats (conductor T-3), per project.

-- Create or replace an agent's profile in an existing project, keeping created_by and created_at:
-- no row back is a missing project.
-- name: UpsertAgentProfile :one
INSERT INTO agent_profiles (project_id, agent_key, capabilities, tags, max_concurrency, cost_class, models, max_effort,
  created_by, created_at, updated_at)
SELECT p.id, CAST(sqlc.arg(agent_key) AS TEXT), CAST(sqlc.arg(capabilities) AS TEXT), CAST(sqlc.arg(tags) AS TEXT),
  CAST(sqlc.narg(max_concurrency) AS INTEGER), CAST(sqlc.narg(cost_class) AS TEXT), CAST(sqlc.arg(models) AS TEXT),
  CAST(sqlc.narg(max_effort) AS TEXT), CAST(sqlc.arg(created_by) AS TEXT), CAST(sqlc.arg(now) AS INTEGER), CAST(sqlc.arg(now) AS INTEGER)
FROM projects p WHERE p.id = sqlc.arg(project_id)
ON CONFLICT (project_id, agent_key) DO UPDATE SET capabilities = excluded.capabilities, tags = excluded.tags,
  max_concurrency = excluded.max_concurrency, cost_class = excluded.cost_class, models = excluded.models,
  max_effort = excluded.max_effort, updated_at = excluded.updated_at
RETURNING *;

-- One (agent, capability) outcome merged into its rollup.
-- name: RecordSkillOutcome :exec
INSERT INTO skill_stats (project_id, agent_key, capability, completed, blocked, total_ms, samples, updated_at)
VALUES (sqlc.arg(project_id), sqlc.arg(agent_key), sqlc.arg(capability), sqlc.arg(completed), sqlc.arg(blocked),
  sqlc.arg(total_ms), 1, sqlc.arg(updated_at))
ON CONFLICT (project_id, agent_key, capability) DO UPDATE SET completed = skill_stats.completed + excluded.completed,
  blocked = skill_stats.blocked + excluded.blocked, total_ms = skill_stats.total_ms + excluded.total_ms,
  samples = skill_stats.samples + 1, updated_at = excluded.updated_at;

-- The agent's stats, by capability.
-- name: ListSkillStats :many
SELECT * FROM skill_stats WHERE project_id = sqlc.arg(project_id) AND agent_key = sqlc.arg(agent_key)
ORDER BY capability LIMIT 200;
