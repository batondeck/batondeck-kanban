-- The operations summary's one statement (conductor's opsQuery, bq.ts:112): a project's tool calls
-- since since_ms, grouped by tool, outcome, agent and UTC day, with each group's latency sum.
-- name: SummarizeToolCalls :many
SELECT tool, outcome, agent,
    to_char(to_timestamp(ts / 1000) AT TIME ZONE 'UTC', 'YYYY-MM-DD')::text AS day,
    count(*)::bigint AS calls,
    sum(latency_ms)::bigint AS latency_sum
FROM tool_calls
WHERE project_id = sqlc.arg(project_id)::text AND ts >= sqlc.arg(since_ms)::bigint
GROUP BY tool, outcome, agent, day
ORDER BY tool COLLATE "C", outcome, agent COLLATE "C", day;
