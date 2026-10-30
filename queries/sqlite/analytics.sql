-- The operations summary's one statement (conductor's opsQuery, bq.ts:112): a project's tool calls
-- since since_ms, grouped by tool, outcome, agent, identity and UTC day, with each group's latency
-- sum: one agent name under two identities is two groups (P-335).
-- name: SummarizeToolCalls :many
SELECT tool, outcome, agent, identity,
    CAST(date(ts / 1000, 'unixepoch') AS TEXT) AS day,
    CAST(count(*) AS INTEGER) AS calls,
    CAST(sum(latency_ms) AS INTEGER) AS latency_sum
FROM tool_calls
WHERE project_id = sqlc.arg(project_id) AND ts >= sqlc.arg(since_ms)
GROUP BY tool, outcome, agent, identity, day
ORDER BY tool, outcome, agent, identity, day;
