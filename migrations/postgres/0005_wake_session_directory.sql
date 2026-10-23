-- +goose Up
-- ADR-0004, owner-approved 2026-10-23 (zone Z2): the device session directory (R0.25). A row is one
-- CLI client process on this device: its pid (the directory's key: one entry per pid), its role, the
-- sessionId and procStart Claude Code's registry (~/.claude/sessions/<pid>.json) gave at register
-- (a doorbell names the session it is for, so a reused pid drops it), and its bearer sealed with
-- the keyring (AAD "wake_sessions.bearer:<id>"), re-resolved at every delivery and every sweep.
-- No row is deleted: one that fails re-validation at start-up is dropped by the directory itself.
ALTER TABLE wake_sessions ADD COLUMN pid bigint;
ALTER TABLE wake_sessions ADD COLUMN role text CHECK (role IN ('master', 'worker'));
ALTER TABLE wake_sessions ADD COLUMN client_session_id text;
ALTER TABLE wake_sessions ADD COLUMN proc_start text;
ALTER TABLE wake_sessions ADD COLUMN bearer_sealed bytea;
CREATE UNIQUE INDEX wake_sessions_pid ON wake_sessions (pid) WHERE pid IS NOT NULL;

-- +goose Down
DROP INDEX wake_sessions_pid;
ALTER TABLE wake_sessions DROP COLUMN bearer_sealed;
ALTER TABLE wake_sessions DROP COLUMN proc_start;
ALTER TABLE wake_sessions DROP COLUMN client_session_id;
ALTER TABLE wake_sessions DROP COLUMN role;
ALTER TABLE wake_sessions DROP COLUMN pid;
