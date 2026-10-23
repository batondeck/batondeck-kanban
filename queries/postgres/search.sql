-- search_tasks over the generated tsvector tasks.search ('simple': no stemming, as FTS5's default
-- tokenizer does none). match is built by the store from the query's normalised terms (each a
-- single-quoted letters-and-digits lexeme, a prefix one followed by :*, joined by &), never the
-- caller's text, so to_tsquery rather than plainto_tsquery (which has no prefix). score is ts_rank
-- as float8, higher is better; num is the task number, the tie-break. statuses is a JSON array of
-- statuses, '[]' every status; after_id '' is the first page, else the page starts strictly after
-- (after_rank, after_num).
-- name: SearchTasks :many
SELECT h.id, h.score FROM (
  SELECT t.id AS id, ts_rank(t.search, to_tsquery('simple', sqlc.arg(match)::text))::float8 AS score, substr(t.id, 3)::bigint AS num
  FROM tasks t
  WHERE t.search @@ to_tsquery('simple', sqlc.arg(match)::text) AND t.project_id = sqlc.arg(project_id)::text
    AND (sqlc.arg(board_id)::text = '' OR t.board_id = sqlc.arg(board_id)::text)
    AND (sqlc.arg(statuses)::text = '[]' OR t.status IN (SELECT jsonb_array_elements_text(sqlc.arg(statuses)::jsonb)))
) h
WHERE sqlc.arg(after_id)::text = '' OR h.score < sqlc.arg(after_rank)::float8
   OR (h.score = sqlc.arg(after_rank)::float8 AND h.num > sqlc.arg(after_num)::bigint)
ORDER BY h.score DESC, h.num
LIMIT sqlc.arg(page_limit)::bigint;

-- How many tasks SearchTasks pages through for the same match and filter, in one statement.
-- name: CountSearch :one
SELECT COUNT(*) FROM tasks t
WHERE t.search @@ to_tsquery('simple', sqlc.arg(match)::text) AND t.project_id = sqlc.arg(project_id)::text
  AND (sqlc.arg(board_id)::text = '' OR t.board_id = sqlc.arg(board_id)::text)
  AND (sqlc.arg(statuses)::text = '[]' OR t.status IN (SELECT jsonb_array_elements_text(sqlc.arg(statuses)::jsonb)));
