# Changelog

Notable changes to batondeck-kanban. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project will use
[semantic versioning](https://semver.org/spec/v2.0.0.html) from its first release.

## [Unreleased]

### Added

- The web UI in `web/`: the hosted BatonDeck's React SPA, relicensed by its owner (NOTICE), copied
  and trimmed. Kept: projects, board, task page and drawer, agents, chronicle (index, new, page,
  history), analytics, reports, activity, notifications, settings, on-call, sign-in. Removed with
  their routes, nav entries and tests: the plugin studio (7 routes), billing, admin, onboarding, the
  organization (tenant) switcher, and the transition-graph editor with `getTransitions` /
  `setTransitions`. `api.ts` is kept whole. The brand is hdtp-web-kit 0.2.0's brand set, vendored by
  `web/tools/kit.mjs` and pinned in `web/kit/kit.lock`, in place of the generated logo artwork. The
  build is committed in `web/dist`, embedded in the binary and served at `/`. `make check` gains
  `web-public-check` (refuses `web/public/pitch/` and `web/ds-bundle/` anywhere under `web/`, and
  holds `web/public/` to `scripts/web-public-allowlist.txt`) and `kit-check` (every kit file
  against its sha256 in the lock).
- The base schema, `migrations/{sqlite,postgres}/0001_base.sql`: 43 tables on both engines for
  every in-scope area (projects, members, grants and the per-project `T-n` counter; boards and
  columns; tasks with the six statuses, `blocked_reason`, `claim_attempts`, `next_eligible_at`,
  `score`, runs and summaries; artifacts, events and the change log; dependencies, context items,
  memory, comments, follow-ups, notifications, sprints, attachments and blobs; identities, agent
  sessions, profiles and skill stats, wake sessions; users, sessions, tokens and the OAuth tables;
  audit log, tool calls and idempotency keys; chronicle; on-call integrations, alert states and deliveries), the
  claim-frontier, feed, change-log and dependency indexes, an FTS5 index over one generated
  `search_body` column (title, description, search tokens) on SQLite and a generated `tsvector` with a GIN index on Postgres. One scaffold query per table under
  `queries/{sqlite,postgres}/`, the sqlc output in `internal/store/{sqlitedb,pgdb}`, and
  `make sqlc-check` (sqlc v1.30.0) in `make check`. `batondeck-kanban migrate` applies it and a
  second run prints "no change".
- `internal/domain`, the rest of the pure rules: one claimability predicate (READY, unblocked, not
  poisoned, not racing, no live lease with expiry lazy on read, past the retry backoff) and the
  open-frontier variant; the retry policy (`poisonAfterAttempts`, default 30 s doubling to 1 h, ±20 %
  jitter, poison after 5) and `ApplyFailure`, which returns READY with a backoff or BLOCKED with
  `blockedReason: "poison"` and the system label `poisoned` (on `retryable:false` at once), and
  un-poison, which resets `claimAttempts` and removes the label; the auto-unblock guard, which never
  readies a poisoned task; the five roles and the tier ladder; the posture of the 93 target tools
  with the capability grant check (anything not exempt is governed) and the least-privilege worker
  grant; the audit descriptors of the 14 sensitive tools; the 59 event types, `task.poisoned` in
  place of `task.dead_lettered`; and the reserved labels `cancelled` and `poisoned`, which a caller
  cannot add, remove or respell (ADR-0001). Pure functions, not yet called by any tool.
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
- The MCP contract, before any tool exists: `contract/tools.json` (conductor's published
  `docs-site/mcp.json` at `5e2baafa`, 130 tools with input and output schemas, its 4 prompts and 10
  resource templates) and `contract/admin-tools.json` (`list_audit_log`, from an admin `tools/list`
  of the same build), both written by `scripts/contract-import.mjs`; `contract/canonical.go`, which
  reduces a JSON Schema to its typeSet, enum, items, properties, required and
  additionalProperties; `contract/deltas.json`, every departure from conductor (40 removed tools,
  6 removed properties, 116 removed `CANCELLED`/`DEAD_LETTER` enum values, `delete_task` and
  `cancel_task` added, `deadLetter` renamed `poisoned` twice, `settings.transitionTable` dropped
  from the `get_project` and `update_settings` outputs, `get_sprint`'s `attention.why` value
  `dead_letter` renamed `poisoned`: ADR-0001); `contract/expected.json`, the derived 93-tool
  surface; `contract/errors.json`, conductor's 13 error codes and the error payload. `make check`
  runs the new `contract` step, which refuses an `expected.json` that is not the exact derivation,
  a surface carrying `get_transitions`, `set_transitions` or `requeue_task`, and one without
  `delete_task` or `cancel_task`.
- The gate: `pre-commit` scans the INDEX for credentials (`scripts/check-secrets.py`; a documented
  fake carries `secret-scan-ok: <reason>`, and a marker with no reason fails); `pre-push` starts
  `postgres:16-alpine` in Docker, exports `BATONDECK_TEST_POSTGRES_DSN` and runs `make check`,
  refusing the push when Docker is absent; `make check` gains `sqlc-check` and `web-dist-check`
  (each prints a skip line until `queries/*.sql` and `web/package.json` exist); a GitHub Actions
  `check` workflow runs `make check` on pull requests against a `postgres:16` service.

### Removed

- The GitHub Actions `check` workflow (`.github/workflows/check.yml`), by the owner's ruling
  (ADR-0002): the local hooks are the gate. `pre-commit` runs gofmt, the 5 MiB cap and the INDEX
  secret scan; `pre-push` runs `make check` on both engines and refuses the push without Docker. A
  pull request now reports no status check; the reviewer re-runs `make check` on its head.

### Fixed

- Two processes migrating one SQLite file at once no longer race in goose's Up (`SQLITE_BUSY`,
  `table projects already exists`): `migrate` holds the data-dir lock alone, waiting up to a
  minute for another holder, and `serve` takes it through `core.AcquireServeLock`, migrates only
  when alone, then shares it for its lifetime, so a serve beside another does not migrate.
  Postgres keeps goose's session lock. `TestFirstMigrationsOnOneFileBothSucceed` moved to
  `internal/cli` and runs the real migrations through `migrate`, 30 rounds.
