-- +goose Up
-- P-167 (core-cli-ops#4): serves on several devices may share one database (Postgres), and a
-- directory row is valid only on the device whose serve registered it, where its pid names a
-- process. A row names that device (device_id, the serve's device id; '' for a row written before
-- this column, which any serve may restore or drop, as every row was before), a serve starts from
-- its own device's rows alone, and a pid is unique per device, not per database. The index keeps its
-- name, which the store reads in the engine's refusal.
ALTER TABLE wake_sessions ADD COLUMN device_id text NOT NULL DEFAULT '';
DROP INDEX wake_sessions_pid;
CREATE UNIQUE INDEX wake_sessions_pid ON wake_sessions (device_id, pid) WHERE pid IS NOT NULL;

-- +goose Down
-- The old index holds one row per pid in the database: of the rows that share a pid, all but the
-- first by id are dropped (a directory row is re-validated at every start-up anyway).
DROP INDEX wake_sessions_pid;
DELETE FROM wake_sessions WHERE pid IS NOT NULL AND id NOT IN (SELECT MIN(id) FROM wake_sessions WHERE pid IS NOT NULL GROUP BY pid);
CREATE UNIQUE INDEX wake_sessions_pid ON wake_sessions (pid) WHERE pid IS NOT NULL;
ALTER TABLE wake_sessions DROP COLUMN device_id;
