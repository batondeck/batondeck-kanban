# Changelog

Notable changes to batondeck-kanban. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project will use
[semantic versioning](https://semver.org/spec/v2.0.0.html) from its first release.

## [Unreleased]

### Added

- The MCP mount (`internal/mcp`, R0.14): a go-sdk v1.8.0 server behind the stateful Streamable HTTP
  handler at `/mcp` (GET, POST, DELETE), declaring tools, prompts and resources with `listChanged`
  and resource subscribe; all three lists are empty in R0 and `resources/subscribe` refuses every URI
  until resources exist. Every request's bearer resolves through `auth.Resolver`: none, or one the
  resolver refuses, is 401 with `WWW-Authenticate: Bearer`; a resolver failure is 500; a session is
  bound to its caller's identity (another identity's bearer on it is 403). The caller reaches every
  handler through `mcp.CallerFrom(ctx)`. A failed `tools/call` is a result with `isError` carrying
  the `contract/errors.json` object (`{code, message, details?, retryable}`) as structured content
  and as text; a go-sdk argument refusal is VALIDATION; a panic is INTERNAL (retryable) and its log
  line carries the stack. Sessions are collected after 30 min idle (`Options.SessionIdle`) and
  answer 404 "session not found" afterwards; at most 1000 live (`Options.MaxSessions`), an
  initialize past it refused. Each initialised session is mirrored through `service.Sessions` as the
  caller's agent session (key `<identity>::<agent id>`): written at initialize with the client's name
  and version, touched by every tool call, removed when the last MCP session of that agent ends
  (idle, DELETE or close). Every `tools/call` writes one `tool_calls` row through
  `service.ToolCalls` (`ok`/`OK`, else `error` and the code returned); a handler error that is no
  BatonDeckError is logged with a stack and answered INTERNAL. A protocol error from `tools/call`
  (an unknown tool, a handler's `*jsonrpc.Error`) reaches the client as a JSON-RPC error and is
  recorded; the bookkeeping never aborts a call or the process. `serve` stops in order: the MCP
  sessions are closed first (their agent sessions removed), then the HTTP server drains, then the
  buffered tool calls are flushed on their own 5 s budget. A stop with an idle client attached
  returns at once; a tool call still running gets the 10 s shutdown budget to finish, then its
  context is cancelled and `serve` exits 1 naming the step that ran out. A second `initialize` on a
  live session is refused and leaves the session, and the session bound, as they were.
  `tools/list` is filtered per audience (the workspace-admin
  flag) and memoised per audience; `mcp.AddTool` registers a tool with its visibility. `serve` mounts
  `/mcp` over the store's API tokens and the service core's Sessions and ToolCalls, flushed on
  shutdown. New modules: `github.com/modelcontextprotocol/go-sdk` v1.8.0 and
  its linked dependencies (THIRD_PARTY_NOTICES: 21 to 28 linked modules).
- Store contract II in SQL on both engines for seven areas (R0.10): `internal/store/{context,memory,
  summaries,dependencies,subtasks,agentsessions,wakesessions}.go` over
  `queries/{sqlite,postgres}/{context,memory,summaries,dependencies,subtasks,agentsessions,wakesessions}.sql`
  (`wake_sessions.sql` renamed, the unused placeholder queries replaced) replace their INTERNAL stubs,
  and the seven batteries pass on SQLite and Postgres; memstore carries context, memory, summaries,
  dependencies and subtasks and passes those five. `conformance.WithSeeder` is the seeder the batteries
  stand on, over a store's own Projects, Boards and Tasks. `RecallMemory` is an exact key, or the newest
  `k` with no key and then the expired dropped, no FTS (owner ruling 2026-10-23; plan §Clarifications 1
  corrected). An edge write and its two caches land in one transaction and move no task's version
  (`SetTaskCaches` writes the caches alone). The primitives `get_task_context` composes, `GetTask`,
  `GetSummary`, `ListContextItems`, `ReadMemory`, `ListBlockers` and `ListDependents`, are one
  statement each, six together on both engines, on a task holding rows and on a bare one
  (`TestTaskContextStatements{SQLite,Postgres}`).
- Migration `0003_memory_drop_task_fk` on both engines (P-65, owner-approved 2026-10-23): memory loses
  its foreign key to tasks, so a project entry keeps its task as provenance after the task is deleted;
  the task scopes are the service's cascade (`DeleteMemoryByTask`). SQLite rebuilds the table (it has no
  index, trigger or view beyond its primary key's); the down migration restores the key, deleting the
  task, agent and shared rows whose task is gone and clearing the task_id of the other orphaned rows.
  `TestMemoryKeepsNoKeyToTasks{SQLite,Postgres}` pin the rebuilt table's columns, scope CHECK and the
  remaining keys' ON DELETE actions.
- The projects, boards and feed services (R1.16, ADR-0003 B3): `Core.Projects()`, `Core.Boards()`
  and `Core.Feed()` in `internal/service/{projects,members,boards,feed}.go` implement R0.20's
  contracts, every mutation through `mutate` and every check through `authorize`. `create_project`
  and `create_board` replay on `idempotencyKey` (kept by `mutate` alone; the store write carries no
  key); a board created with no columns gets the store's six default columns, none Cancelled;
  `remove_column` is refused while the column holds a task; `get_board` and `list_boards` answer
  the fixed transition table; `delete_project` is the project admin's (or a workspace admin's) and
  recursive; the last admin cannot be removed or demoted; `add_member` keys a membership by the
  account id a login names. `add_member`, `remove_member` and `delete_project` call
  `Core.InvalidateAccess` after their commit, so a removed or demoted member is refused on its next
  call. `update_settings` takes no `transitionTable` (the request has no such field) and refuses
  `quotaEnabled`/`txnCap` (metering is dropped). The board snapshot cache: `Boards.Get` and
  `Feed.BoardSnapshot` serve a board from it until a change row names that board, which the bus
  delivers through `Core.ObserveChange` (the invalidation hook) and which this process's own column
  mutations drop directly after commit; a miss costs one `store.GetBoard` (two statements). Board
  mutations ring a `changed` row naming their board. `Feed.BoardFeed` pages `ListBoardFeed`. Tests
  over memstore: one event, audit row and change row per mutation as each contract names them, 0
  then 1 `GetBoard` around a change row, and 21 mutations each killed by its test. The cache
  serves nothing until the bus has delivered a change: before that every read composes.
- Store contract I in SQL on both engines (R0.9): `internal/store/{tasks,claims,events,changelog}.go`
  over `queries/{sqlite,postgres}/{tasks,claims,events,changelog}.sql` replace the INTERNAL stubs
  (`stubs_contract_i.go` is gone), and the tasks, claims, events and changelog batteries pass on
  SQLite and Postgres. `ClaimNext` is one statement: on Postgres `UPDATE … WHERE id = (SELECT …
  FOR UPDATE OF f SKIP LOCKED LIMIT 1)`, on SQLite the same UPDATE over the frontier, whose own
  write lock serialises callers. A versioned write reads the task inside `Atomically` (`FOR UPDATE`
  on Postgres) and answers `STALE {expected, actual}` from that read; a heartbeat, a release, a
  delete and an event append are one statement each and read again only to name a refusal. Order
  keys and ids sort and compare `COLLATE "C"` on Postgres. Measured per call: `GetTask` 1 statement,
  a `ListTasks` page holding tasks 1 (an empty page 2), a winning `ClaimNext` 1
  (`TestStatementsPerCall`). `TestClaimRace` (50 callers, 50 READY tasks, 50 distinct winners, 0
  errors; SQLite over five stores on one file) and `TestListTasksPagesThreeBySeven` run on both
  engines.
- Store contract I (ADR-0003 B2): the `store.Tasks`, `Claims`, `Events` and `ChangeLog` roles in
  `internal/store/contract_{tasks,claims,events,changelog}.go`, embedded in `Store`, with doc
  comments pinning keys `(project_id, task_id)`, `domain.Stale(expected, actual)` on a versioned
  write, the NOT_FOUND, CONFLICT_LOCKED and LEASE_EXPIRED shapes (`store.LeaseExpired`), and the
  base64url `{"order","id"}` page cursor (`store.EncodeCursor`/`DecodeCursor`). Their batteries,
  `conformance_{tasks,claims,events,changelog}.go`, cover every method's happy path and declared
  error, 50 concurrent `ClaimNext` callers winning 50 distinct tasks and one winner of two
  `ClaimTask` callers. `conformance.Run` now lists all 24 areas, each a named subtest; an area file
  registers its battery from `init`, and an area with no file yet, or whose engine methods are still
  unbuilt (`store.UnbuiltTask`), skips naming the task that builds it. On SQLite and Postgres the
  four areas are stubs answering INTERNAL until R0.9 writes their statements. `internal/store/memstore`,
  a test-only in-memory `Store` (maps under one mutex, `Atomically` as snapshot and rollback), passes
  the suite for these four areas and for projects, members, boards, counters, idempotency, users,
  sessions and tokens (`TestMemstoreConformance`), and refuses a call through a transaction's Store
  after its `Atomically` has returned, as the engines do. Order keys and ids compare as bytes. The members battery is split out of the projects
  battery unchanged. `queries/{sqlite,postgres}/` gains `artifacts.sql`, `runs.sql`, `search.sql`,
  `summaries.sql` and `wake_sessions.sql`, moved byte for byte out of `tasks.sql` and `agents.sql`;
  the generated declarations are identical.
- The service contract (`internal/service`, ADR-0003 B3): the nine interfaces the transports call
  (Projects, Boards, Tasks, Lifecycle with cancel, Context, Media, Feed, Sessions, ToolCalls; 50
  methods, each `(ctx, domain.Caller, Req) (Resp, error)`), their request and response DTOs, and
  the `service.Services` bundle; each method's comment names its tier, event, audit descriptor and
  doorbell kind. `domain.Caller`. `internal/service/servicefake`: per-method results, call
  recording, and `servicefake.Trigger(code)` returning each of the 13 codes from any method.
  `contract/dto_test.go` (`TestDTOCanonical`) infers each tool's DTO pair with jsonschema-go v0.4.3
  (a test-only dependency) and holds it to `contract/expected.json` exactly, for 44 tools: the 42
  CORE tools, `cancel_task` and `list_agent_sessions`. No implementation yet.
- The service core (`internal/service`, ADR-0003 layer 2): `service.New(store, Options)` returns
  a `*service.Core` holding the one transaction shape and the one access check every service
  method runs through. `mutate` (unexported, for the service lanes) runs inside `store.Atomically`
  (or `WithIdempotency` under a key): the replay, load, STALE with `expected`/`actual` before any
  write, the pending grant check, fn, exactly one version bump, the `task_events` rows, the audit
  row when `domain.DescribeAudit` names the tool, and the change-log rows; a panic is INTERNAL with
  its stack logged, a domain code passes unchanged, any other error is INTERNAL. Every mutation
  writes at least one change-log row: one per doorbell (`assigned`, scoped to the assignee's
  identity when it is a project member, else to `service.AgentScope(label)` = `agent:<label>`, since
  an assignee may be a free-text agent label; `broadcast`, scoped to
  `service.BroadcastScope(projectID)` = `project:<id>`), else one `changed` row in the project's
  scope naming the task it wrote. An idempotency key is kept per tool (`<tool>:<key>`), and a
  supplied expected version 0 is STALE; only the lease-guarded paths pass no version. `authorize` checks the tool's posture (an unclassified
  tool is FORBIDDEN), workspace admin for AdminOnly "only", membership (a non-member or a missing
  project is FORBIDDEN), the tier against the member's role capped by the token's, and the
  capability grant through `domain.CheckGrant`, finishing a task-attribute grant inside `mutate`
  against the loaded task; membership and grant are cached per (project, identity) for 60 s
  (`Options.AccessTTL`), dropped at once by `Core.InvalidateAccess`. The grant reader is
  `Options.Grants` until the store has one. `Core.ToolCalls()` buffers `tool_calls` rows and writes
  them in batches (100 rows or 1 s), never failing the call it records; `Core.FlushToolCalls` and
  `Core.Close` drain it. `Core.Sessions()` is `service.Sessions` over the agent- and wake-session
  areas, with `service.SessionKey` and `service.AgentID`. memstore now implements `store.Audit`,
  `ToolCalls`, `AgentSessions` and `WakeSessions` and passes their batteries
  (`TestMemstoreConformance`).
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
- The layering guard, `internal/arch/layering_test.go`, run by `make arch` before `test` in
  `make check`. One `go list -e -deps -json` over every Go directory of the module (the walk skips
  `node_modules`, `testdata`, dot and underscore directories and nested modules) judges the eight
  rules of ADR-0003 §C, held as the `layeringRules` table: domain and core reach no internal package;
  outside the store, migrations and the composition root no import chain reaches `database/sql`,
  SQLite, pgx or goose except one through `internal/store/...` or `migrations` (rule 2, amended by
  the orchestrator: through-store reach allowed); the store, bus/retention/auth/blob, service and
  transport rows forbid importing a layer above; transports import no store package and not each
  other; only `internal/mcp` and `internal/cli` import the MCP SDK; `contract/` non-test code reaches
  no internal package. Test imports are judged directly under the looser `testRules` table, which
  allows `memstore`, `servicefake` and `authfake`. Subjects match by path prefix, so a package such
  as `internal/service` is judged as soon as it exists.
- Local accounts and credentials (`internal/auth`): users with argon2id password hashes (t=3,
  64 MiB, p=4) made on the host with `batondeck-kanban user add [--admin] [--name N] LOGIN` (the
  password is read from piped stdin; there is no sign-up page), and `user disable|passwd|list`;
  the workspace-admin flag in `migrations/{sqlite,postgres}/0002_users_workspace_admin.sql`. API
  tokens through the TokenService, `bd_<16 hex>_<secret>`, with only the secret's SHA-256 stored:
  `batondeck-kanban token create --user LOGIN [--label L] [--ttl D]` prints the token once (default
  30 days, at most 365), and `token list` shows the `bd_<id>` prefix and status and never the
  plaintext. `token revoke` makes it fail on the next call. `ResolveCaller` (the `auth.Resolver`
  contract, with `authfake` as its double) turns a token into a `domain.Caller`, the identity and
  the workspace-admin flag. Cookie sessions (`bk_session`, HttpOnly, SameSite=Lax, Secure over TLS,
  7 days) opened by a login POST. A CSRF check ported from hdtp-gateway refuses with 403 and audits
  a cross-site state change, judging browsers by Fetch Metadata or Origin and a session by its CSRF
  token, and a bearer by the token alone. A disabled user's tokens and sessions stop within the
  60 s account cache. The users, sessions and tokens store areas on both engines, with their
  conformance batteries; they replace the scaffold `queries/*/auth.sql`. The login and middleware
  handlers are not yet mounted by `serve`.
- The first Store areas, on both engines, each an exported role (`store.Projects`, `Members`,
  `Boards`, `Counters`, `Idempotency`, in `internal/store/contract_*.go`) embedded in `Store`:
  projects (create with the creator as admin, get, list by identity with its role, settings without
  `transitionTable`, recursive delete keeping the audit log, tool calls and change log), members (add
  or update, remove, list, look up), boards and columns (create with the default columns Backlog,
  Ready, In progress, Blocked, Review, Done, Review left out when `reviewEnabled` is off; get with
  per-column task counts in two statements; list; add, update and remove a column, refusing one that
  holds a task or is the last), the per-project `T-n` counter (`NextTaskID`) and idempotency keys
  (`WithIdempotency`, replay within the caller's window per project, identity and key, one run under
  concurrency). Every lookup is keyed by project. These areas are versionless, as conductor's are;
  they write no event or audit row, which the service layer composes. The conformance suite judges
  all five on SQLite and Postgres.
- Store contract II: the interfaces of twelve areas in `internal/store/contract_{context,memory,
  summaries,dependencies,subtasks,audit,toolcalls,agentsessions,wakesessions,attachments,blobs,search}.go`,
  embedded in `store.Store`, with doc comments pinning keys `(project_id, task_id)`, error shapes
  (`domain.NotFound`, `domain.Stale(expected, actual)`, `domain.CycleDetected`, `domain.Validation`),
  limits and base64url `{order, id}` cursors. Both engines implement them as stubs returning INTERNAL
  that name the task building each area (R0.10, R0.11, R1.8, R1.9; `store.UnbuiltTask`), and the
  memstore does too (R0.10, R0.21, P-67; `store.Unbuilt`). The conformance suite gains one battery per
  area (`conformance_<area>.go`, 60 subtests, registered with `Run`), which skips an area on a store
  while it is unbuilt, and `TestContractIIBatteriesOnAReference` runs all of them
  against a map-backed reference: the memory TTL absent on read after expiry before any sweep, a
  refused dependency cycle leaving no partial edge or cache entry, and ten search queries with their
  ordered ids.
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
