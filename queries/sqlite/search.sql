-- search_tasks over the FTS5 index of tasks.search_body. match is built by the store from the
-- query's normalised terms (each a double-quoted letters-and-digits token, a prefix one followed
-- by *), never the caller's text. score is how many tokens of the task's own body the query
-- matched (highlight marks each with char(1)), so it reads no other row: bm25 is not used, its
-- IDF counts every project's tasks. num is the task number, the tie-break. statuses is a JSON
-- array of statuses, '[]' every status; a task matches when its status, quoted, is in the array
-- (sqlc binds no parameter under a json_each nested this deep). after_id '' is the first page,
-- else the page starts strictly after (after_rank, after_num).
-- name: SearchTasks :many
SELECT h.id, h.score FROM (
  SELECT t.id AS id, CAST(length(highlight(tasks_fts, 0, char(1), '')) - length(replace(highlight(tasks_fts, 0, char(1), ''), char(1), '')) AS REAL) AS score, CAST(substr(t.id, 3) AS INTEGER) AS num
  FROM tasks_fts JOIN tasks t ON t.rid = tasks_fts.rowid
  WHERE tasks_fts.search_body MATCH CAST(sqlc.arg(match) AS TEXT) AND t.project_id = CAST(sqlc.arg(project_id) AS TEXT)
    AND (CAST(sqlc.arg(board_id) AS TEXT) = '' OR t.board_id = CAST(sqlc.arg(board_id) AS TEXT))
    AND (CAST(sqlc.arg(statuses) AS TEXT) = '[]' OR instr(CAST(sqlc.arg(statuses) AS TEXT), '"' || t.status || '"') > 0)
) h
WHERE CAST(sqlc.arg(after_id) AS TEXT) = '' OR h.score < CAST(sqlc.arg(after_rank) AS REAL)
   OR (h.score = CAST(sqlc.arg(after_rank) AS REAL) AND h.num > CAST(sqlc.arg(after_num) AS INTEGER))
ORDER BY h.score DESC, h.num
LIMIT CAST(sqlc.arg(page_limit) AS INTEGER);

-- How many tasks SearchTasks pages through for the same match and filter, in one statement.
-- name: CountSearch :one
SELECT COUNT(*) FROM tasks_fts JOIN tasks t ON t.rid = tasks_fts.rowid
WHERE tasks_fts.search_body MATCH CAST(sqlc.arg(match) AS TEXT) AND t.project_id = CAST(sqlc.arg(project_id) AS TEXT)
  AND (CAST(sqlc.arg(board_id) AS TEXT) = '' OR t.board_id = CAST(sqlc.arg(board_id) AS TEXT))
  AND (CAST(sqlc.arg(statuses) AS TEXT) = '[]' OR instr(CAST(sqlc.arg(statuses) AS TEXT), '"' || t.status || '"') > 0);
