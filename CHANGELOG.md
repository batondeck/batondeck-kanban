# Changelog

Notable changes to batondeck-kanban. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project will use
[semantic versioning](https://semver.org/spec/v2.0.0.html) from its first release.

## [Unreleased]

### Added

- The base schema, `migrations/{sqlite,postgres}/0001_base.sql`: 42 tables on both engines for
  every in-scope area (projects, members, grants and the per-project `T-n` counter; boards and
  columns; tasks with the six statuses, `blocked_reason`, `claim_attempts`, `next_eligible_at`,
  `score`, runs and summaries; artifacts, events and the change log; dependencies, context items,
  memory, comments, follow-ups, notifications, sprints, attachments and blobs; identities, agent
  sessions, profiles and skill stats, wake sessions; users, sessions, tokens and the OAuth tables;
  audit log, tool calls and idempotency keys; chronicle; on-call integrations and deliveries), the
  claim-frontier, feed, change-log and dependency indexes, an FTS5 index over tasks on SQLite and
  a generated `tsvector` with a GIN index on Postgres. One scaffold query per table under
  `queries/{sqlite,postgres}/`, the sqlc output in `internal/store/{sqlitedb,pgdb}`, and
  `make sqlc-check` (sqlc v1.30.0) in `make check`. `batondeck-kanban migrate` applies it and a
  second run prints "no change".
- `internal/domain`: the six ticket statuses and the fixed transition table (same-status moves
  legal; `CANCELLED` and `DEAD_LETTER` refused as `VALIDATION`), with allowed-next and shortest-path
  guidance on `INVALID_TRANSITION`; the error model (13 codes, `STALE`, `RATE_LIMITED` and `INTERNAL`
  retryable); the move decision (WIP limit on entering IN_PROGRESS, a promotion with open blockers
  landing on BLOCKED, reorder within a column); order keys ported from fractional-indexing 4.0.0;
  dependency cycle detection (`CYCLE_DETECTED`). Pure functions, not yet called by any tool.
- The skeleton, copied from hdtp-gateway and stripped of HDTP: the Go module
  `github.com/batondeck/batondeck-kanban`; the `batondeck-kanban` binary with `serve`, `healthcheck`
  and `version` (`token` and `user` exit 2, not implemented in this release; `migrate` and `doctor` below);
  `serve` answers `GET /healthz` with the binary and its version and serves the embedded web UI
  placeholder; config from `BATONDECK_*` > a JSON file > defaults, holding `database.url`,
  `database.synchronous`, `listen` and `public_url`; the data-dir lock, admin socket and keyring;
  the SQLite and Postgres engines with goose's migration machinery and no
  area (the base schema is the entry above); the Makefile gate
  (`make check`: fmt vet staticcheck notices-check release-check test), the githooks, the
  distroless Dockerfile, THIRD_PARTY_NOTICES and RELEASING.md.
- The store engine pair, wired: `database.url` (`BATONDECK_DATABASE_URL`) unset or a file path opens
  SQLite (WAL, `_txlock=immediate`, `busy_timeout=5000`, `synchronous` FULL or the NORMAL opt-in) with
  one write connection and a `query_only` read pool of four; a `postgres://` URL opens a pgx pool with
  goose's session lock. `Store` gains `Atomically` and `ServerVersion`, and `Migrate` reports what it
  did; with no migration in the binary it creates goose's version table alone. `batondeck-kanban
  migrate` runs the migrations, `serve` runs them before it listens, and `doctor` reports the engine
  the connection answers as (`sqlite_version()` / `version()`), exiting 1 and naming the engine when
  there is none. The conformance suite holds its first area, the lifecycle, and runs on SQLite and on
  Postgres (`BATONDECK_TEST_POSTGRES_DSN`; skipped when unset, failing when unreachable) as
  `make conformance`, a step of `make check`. THIRD_PARTY_NOTICES now covers the 20 modules the
  binary links.
