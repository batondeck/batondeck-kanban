-- The agent-session area: one row per (identity_id, agent_id). An empty client field is NULL, and
-- NULL leaves the stored value.

-- name: UpsertAgentSession :one
INSERT INTO agent_sessions (identity_id, agent_id, agent_name, kind, client_name, client_version, oauth_client_id, first_seen_at, last_seen_at, session_count)
VALUES (sqlc.arg(identity_id), sqlc.arg(agent_id), sqlc.arg(agent_name), sqlc.arg(kind), sqlc.narg(client_name), sqlc.narg(client_version),
  sqlc.narg(oauth_client_id), sqlc.arg(at), sqlc.arg(at), 1)
ON CONFLICT (identity_id, agent_id) DO UPDATE SET agent_name = excluded.agent_name, kind = excluded.kind,
  client_name = COALESCE(excluded.client_name, agent_sessions.client_name),
  client_version = COALESCE(excluded.client_version, agent_sessions.client_version),
  oauth_client_id = COALESCE(excluded.oauth_client_id, agent_sessions.oauth_client_id),
  last_seen_at = excluded.last_seen_at, session_count = agent_sessions.session_count + 1
RETURNING identity_id, agent_id, agent_name, kind, client_name, client_version, oauth_client_id, first_seen_at, last_seen_at, session_count, request_count, tool_counts, last_tool, last_project_id;

-- Activity in one statement: a missing row is made (session_count 0); an existing one grows by n,
-- its tool's count in tool_counts too (jsonb_build_object takes the tool name as a key, ->> reads
-- that key exactly, || merges the one key).
-- name: TouchAgentSession :exec
INSERT INTO agent_sessions (identity_id, agent_id, agent_name, kind, first_seen_at, last_seen_at, session_count, request_count, tool_counts, last_tool, last_project_id)
VALUES (sqlc.arg(identity_id), sqlc.arg(agent_id), sqlc.arg(agent_name), sqlc.arg(kind), sqlc.arg(at), sqlc.arg(at), 0, sqlc.arg(n),
  jsonb_build_object(sqlc.arg(tool)::text, sqlc.arg(n)::bigint)::text, sqlc.arg(tool)::text, sqlc.narg(project_id))
ON CONFLICT (identity_id, agent_id) DO UPDATE SET agent_name = excluded.agent_name, kind = excluded.kind,
  request_count = agent_sessions.request_count + excluded.request_count,
  tool_counts = (agent_sessions.tool_counts::jsonb || jsonb_build_object(excluded.last_tool,
    COALESCE((agent_sessions.tool_counts::jsonb ->> excluded.last_tool)::bigint, 0) + excluded.request_count))::text,
  last_tool = excluded.last_tool,
  last_project_id = COALESCE(excluded.last_project_id, agent_sessions.last_project_id),
  last_seen_at = excluded.last_seen_at;

-- name: DeleteAgentSession :execrows
DELETE FROM agent_sessions WHERE identity_id = $1 AND agent_id = $2;

-- Newest activity first, then agent id and identity as bytes; identity_id '' is every row.
-- name: ListAgentSessions :many
SELECT identity_id, agent_id, agent_name, kind, client_name, client_version, oauth_client_id, first_seen_at, last_seen_at, session_count, request_count, tool_counts, last_tool, last_project_id
FROM agent_sessions
WHERE sqlc.arg(identity_id)::text = '' OR identity_id = sqlc.arg(identity_id)::text
ORDER BY last_seen_at DESC, agent_id COLLATE "C", identity_id COLLATE "C"
LIMIT sqlc.arg(page_limit)::bigint;

-- At most page_limit rows last seen before before, the longest idle first, except the rows in
-- keep, a JSON array of [identity_id, agent_id] pairs ('[]' for none): the agents with a live MCP
-- session (P-279).
-- name: DeleteIdleAgentSessions :execrows
DELETE FROM agent_sessions WHERE (identity_id, agent_id) IN (
  SELECT a.identity_id, a.agent_id FROM agent_sessions a, (SELECT sqlc.arg(keep)::text AS keep) p
  WHERE a.last_seen_at < sqlc.arg(before)
    AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(p.keep::jsonb) k
      WHERE k->>0 = a.identity_id AND k->>1 = a.agent_id)
  ORDER BY a.last_seen_at, a.identity_id, a.agent_id LIMIT sqlc.arg(page_limit)::bigint);
