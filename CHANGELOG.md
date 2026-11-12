# Changelog

Notable changes to batondeck-kanban. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project uses
[semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0]

The first release, published 2026-11-09 as `v0.1.0`. The entries recorded while it was built, under
this section's headings in CHANGELOG.md, are all part of it.

- **One binary, two engines.** `batondeck-kanban serve` runs the MCP server at `/mcp` (Streamable HTTP,
  API tokens or OAuth), the REST API behind the web UI, the embedded web UI and `/healthz`, on a
  SQLite file or, with a `postgres://` `database.url`, on Postgres. `migrate`, `user`, `token`,
  `session`, `doctor`, `healthcheck` and `version` are its other subcommands. The image
  `ghcr.io/batondeck/batondeck-kanban:0.1.0` is built for linux/amd64 and linux/arm64 by `make image`,
  which cross-compiles and so needs no arm64 emulation, and `compose.yaml` runs it on Postgres 16.
- **86 tools, 4 prompts and 11 resource templates**, by area:
  projects and members (`create_project`, `get_project`, `list_projects`, `update_settings`,
  `delete_project`, `add_member`, `remove_member`);
  boards and columns (`create_board`, `get_board`, `list_boards`, `add_column`, `update_column`,
  `remove_column`);
  tasks (`create_task`, `get_task`, `list_tasks`, `update_task`, `move_task`, `search_tasks`,
  `rank_tasks`, `cancel_task`, `delete_task`);
  the lifecycle (`claim_task`, `claim_next`, `next_task`, `heartbeat_task`, `release_task`,
  `complete_task`, `block_task`, `fail_task`, `handoff_task`, `reap_stale_leases`);
  context and memory (`get_task_context`, `add_context_item`, `set_summary`, `write_memory`,
  `read_memory`, `recall_memory`);
  dependencies and subtasks (`add_dependency`, `remove_dependency`, `add_subtask`, `list_subtasks`,
  `orchestrate_subtasks`);
  attachments and artifacts (`attach_file`, `list_attachments`, `add_artifact`, `retract_artifact`);
  comments and follow-ups (`add_comment`, `list_comments`, `add_follow_up`, `list_follow_ups`,
  `ack_follow_up`);
  runs (`start_runs`, `open_run`, `list_runs`, `pick_run`, `cancel_runs`);
  sprints (`create_sprint`, `get_sprint`, `list_sprints`, `update_sprint`, `set_sprint`);
  notifications (`list_notifications`, `mark_notifications_read`);
  agents (`list_agent_sessions`, `disconnect_agent`, `register_agent_profile`, `get_skill_stats`,
  `grant_capabilities`, `detect_anomalies`);
  me and audit (`get_my_settings`, `set_my_settings`, `list_audit_log`);
  chronicle (`create_chronicle_page`, `edit_chronicle_page`, `delete_chronicle_page`,
  `ingest_chronicle_page`);
  on-call (`create_integration`, `update_integration`, `delete_integration`, `list_integrations`,
  `rotate_integration_secret`, `send_test_event`, `list_deliveries`, `read_delivery_raw`,
  `replay_delivery`), with signed ingest at `/hooks/{id}` and eight connectors in the binary.
  Analytics is the operations summary over the local `tool_calls` table
  (`GET /api/analytics/operations`). `contract/expected.json` holds the exact surface.
- **Five states and a fixed transition table** (TODO, IN_PROGRESS, BLOCKED, REVIEW, DONE; ADR-0005).
  The table is not editable: `get_transitions`, `set_transitions` and `settings.transitionTable` are
  gone.
- **Two folds, named deviations from conductor:**
  1. `DEAD_LETTER` is folded into `BLOCKED`: a poisoned task is BLOCKED with
     `blockedReason: "poison"` and the system label `poisoned`; auto-unblock never un-poisons it, and
     only a master's `move_task` to TODO does, resetting `claimAttempts`. `requeue_task` is not ported.
  2. `CANCELLED` is folded away: `delete_task` (new; master tier) removes a task and what hangs off
     it, `cancel_task` (new, ADR-0001) moves it to DONE with the system label `cancelled`, and
     `pick_run` and `cancel_runs` delete the losing runs instead of marking them cancelled.
- **Dropped:** the media function (thumbnails, OCR, captions; attachments still upload and
  download), semantic search (keyword search over FTS5 and tsvector stays), notification delivery to
  SendGrid and Slack (notifications are in-app), Pub/Sub (replaced by the device-local socket
  doorbell for Claude Code and SSE for the web UI, ADR-0004) and BigQuery (replaced by the local
  `tool_calls` table). Also dropped, by the owner's scope: billing, WorkOS, admin, the plugin maker,
  the router facade, the CLI-token tools and `prune_agent_sessions` (ADR-0004).
- **The Claude Code plugin, 0.1.0** (`plugin/`; its pre-release numbers 0.1.2 and 0.1.3 in the entries
  below reset to the release's 0.1.0): the board's MCP tools, SessionStart and SessionEnd
  hooks that register the session in the server's device directory so it is rung over its own
  messaging socket when work arrives, the `batondeck-worker` and `batondeck-chronicle` skills and six
  commands. There is no channel and no Stop hook; clients off the device pull each turn.
- **The MCP OAuth 2.1 authorization server** for remote connectors, on when `public_url` is set:
  dynamic client registration, PKCE S256, opaque tokens. It was built for the claude.ai and ChatGPT
  connectors and exercised end to end with `mcp-remote`; neither connector has been tried against it.
- **Release machinery:** `make dist` builds the four platform binaries reproducibly with
  `SHA256SUMS`, `make image` builds the two-architecture image and refuses a version `compose.yaml`
  does not pull, and `scripts/release-check.sh 0.1.0` refuses a candidate whose `compose.yaml`,
  `plugin.json`, this file and binary disagree on the version (RELEASING.md).

### Changed

- A lease that recorded an agent is that agent's for `cancel_task`, `move_task`, `delete_task` and
  `start_runs` too (P-399). Another agent of the same identity no longer cancels, moves, deletes or
  races a task that agent holds: `cancel_task` answers `FORBIDDEN` below master and
  `CONFLICT_LOCKED` at master, and the other three answer `CONFLICT_LOCKED`. `start_runs` now
  refuses another identity's live lease the same way; it used to drop any lease. `delete_task` also
  refuses when a subtask or run it would delete with the task is under such a lease, naming that
  task (P-416). A refused call writes nothing. At master, the owning identity's caller that names
  no agent still passes, so a self-hoster can free a task its own agent is stuck on. Its browser and
  its unlabelled tokens over REST reach only `move_task` and `start_runs` (there is no REST cancel
  or delete route). An MCP session that names no agent (no token label or OAuth `client_name`, and an
  empty `clientInfo.name`) reaches all four. A lease that recorded no agent is the
  owning identity's on any of its callers, and a lapsed lease blocks no one, as before. Not covered:
  `cancel_runs` and `pick_run` still delete a run that another agent holds.
- `POST /hooks/{id}` over its rate budget, or over the pre-check's burst, answers
  `503 Service Unavailable` with `Retry-After` and `{"code":"RATE_LIMITED","retryable":true,…}`,
  not 429 (P-353; owner call 2026-10-30, a change to R2.9's "the 11th is 429"). Alertmanager's
  webhook retries only a 5xx, so a 429 dropped the alert that tripped the limit; PagerDuty retries
  both. `Retry-After` is unchanged: `ceil(60/ratePerMin)` seconds, 1 for the pre-check. The
  integration form's hint says so instead of promising a retry of a 429.
- **An agent session outlives its MCP connection (P-264, P-324).** The Agents screen and
  `list_agent_sessions` keep an agent after it disconnects and across a serve restart, with its
  connection and request counts; its next connection updates the same row. A row goes when
  `disconnect_agent` deletes it or 30 minutes after its last request (plan Clarification 7).
- A token label of the form `id:<x>` is refused at mint (P-352). `token create` and
  `POST /api/me/cli-tokens` answer `VALIDATION` for any label whose agent (trimmed, control
  characters dropped) starts with `id:`, the caller's own id included. That form is the agent id the
  server derives for a caller that names no agent, so such a token presented its caller's MCP agent,
  claims and events as that identity's agentless self. A token minted with such a label before this
  release now names no agent, as the default label `cli` does. `ID:x` and other labels mint as
  before, under the first-minter rule.
- An agent name equal to another identity's id no longer reaches that identity's agentless profile
  or stats (P-351). `register_agent_profile` and `useProfile` on `rank_tasks`, `next_task` and
  `claim_next` answer `FORBIDDEN` (`details.agentKey`) for a key that is another account's id or
  another project member's id, and the outcomes `complete_task` and `block_task` record go under
  the caller's own identity instead.
  A caller naming its own id, or naming no agent, keeps its own profile as before.
- `claim_task {taskId}` applies `claim_next`'s assignee rule (P-334, P-297). Below master, a task
  assigned to another account's id or login (email, any case), to another project member's id, to
  a label another identity's token has ever carried (live, revoked or expired), or to a live label
  the caller is not on that label's own token for, answers `FORBIDDEN` and is not claimed, from
  MCP, REST and the task drawer's "Claim to work" alike; a re-claim of a task reassigned to another
  is refused the same way. An unassigned task, the caller's own id or login, its own label on that
  label's token, and a string naming no identity and no token's label are claimed as before, and a
  master or admin claims any task. A browser session names no agent, so its claim of a task
  assigned to the member's own agent label is refused, as `claim_next` is.
- A lease that recorded an agent is that agent's for every lease-guarded outcome (P-320).
  `heartbeat_task`, `release_task`, `complete_task`, `block_task`, `fail_task`, `handoff_task` and
  `orchestrate_subtasks` answer `LEASE_EXPIRED` when the claim named an agent and the caller is not
  that agent: another agent of the same identity holding the lease id, or that identity with no
  agent named, as a second `claim_task` already was. A lease that recorded no agent (a browser's
  claim, or one from before agents were recorded) is acted on by any caller of its identity, as
  before.
- `make image` builds linux/arm64 on a host without arm64 emulation (P-336). The `Dockerfile`'s
  build stage ran the arm64 golang image for the arm64 build, so on an amd64 host with no QEMU (a CI
  runner, a Linux machine without binfmt) it stopped at `go mod download: exec /bin/sh: exec format
  error`. It now runs on the builder's platform (`FROM --platform=$BUILDPLATFORM`) and cross-compiles
  with `GOOS`/`GOARCH` from `TARGETOS`/`TARGETARCH`; the final stage, which only copies, is
  unchanged. `make release-check` holds the `Dockerfile` to both.
- The web UI bundles dompurify 3.4.17, not 3.4.14 (P-57). mermaid, which renders a chronicle's
  diagrams, pulls dompurify in, and `npm audit` rated 3.4.14 critical (DOM XSS: GHSA-p98j-92pf-mc4p,
  GHSA-6688-9rhm-gjv2, GHSA-cg2g-xh64-v4q7, GHSA-mrv2-3pqr-j5jr, GHSA-c6gr-qvv4-5v3f). The fix is a
  patch inside mermaid's own `^3.3.3`: `web/package-lock.json` moves that one entry, no other
  package changes, and `web/dist` is rebuilt. The remaining advisories are dev-only (vite,
  vitest, undici, source-map-js) or need the breaking mermaid 12 (katex, low), and stay open.
- `user passwd LOGIN` ends every browser session of that user (P-51). A reset after a leaked
  password left the old sessions signed in for the rest of their 7 days, so the reset did not lock
  out whoever held them. Their requests are 401 from then on, and an open `/api/stream` closes at
  its next ping (20 s by default). Other users' sessions stay. API and OAuth tokens are separate
  credentials and also stay: `token revoke ID` ends an API token, and `user disable` suspends both
  (a disabled user's OAuth grants are not refreshed). The command now says how many sessions it ended
  and points at `token list --user`.
- A comment mention wakes the mentioned agent (P-310, owner decision 5, the one exception to
  ADR-0004's "comments ring nobody"). `add_comment` already recorded mentions and wrote each
  mentioned member a `mention` notification, but rang nobody, so a worker named in a comment slept
  until something else woke it. A mention of a member with a live worker session on this device, by
  login or by that worker's registered agent label (`@claude-rev`), now rings that member's workers
  `assigned` on the task, once per comment. A self-mention, a member with no worker (a human at the
  web UI), a name that is no member and a non-member ring nobody and do not error, and masters stay
  silent. A comment without a mention still rings nobody.

- A wake entry takes only an agent its caller may claim for, and a label mention reaches only the
  label's owner (P-322, P-343, Fable B1 on #78). `session register --agent` stored any label, so a
  member could register under another agent's token label and be rung, and mention-notified, for
  work and comments addressed to it. The register now applies `claim_next`'s own label rule
  (REVIEW-257 R2-Q1: the first minter owns a label, token rows of any status count, and there is no
  admin pass): another account's label, or a live label of the caller's own that the calling token
  does not carry, is `FORBIDDEN` and writes no entry; `serve`'s start-up rebuild drops, and logs, a
  stored entry the rule refuses. A comment's `@label` aliases a worker entry only when that
  worker's identity owns the label, so a case or local-part variant registered by another member is
  not read as the owner's. No migration.

- A wake entry's `id:` agent is only its caller's own (P-345). `CheckWakeAgent` passed any agent
  starting `id:` before the label rule, so `session register --agent id:<another identity>` (or a
  token label or OAuth `client_name` of that shape) wrote an entry under the id `AgentID` derives
  for that other member. `id:` followed by anything but the caller's identity is now `FORBIDDEN`
  and writes no entry, and `serve`'s start-up rebuild drops such a stored entry with the log
  reason `names another identity`. `id:<own identity>` and every name the label rule allows still
  register. No migration.

- An agent profile and its skill stats are written and used only by their label's owner (P-340,
  the profile half of P-335). They are keyed by the agent name alone, so an OAuth `client_name` or
  an MCP `clientInfo.name` equal to another member's token label wrote over that member's profile
  and stats, which `rank_tasks {useProfile: true}` reads. `register_agent_profile` and
  `rank_tasks {useProfile: true}` under another agent's label (the wake register's rule: token rows
  of any status count, no admin pass) are now `FORBIDDEN` with `details.label`;
  `complete_task` and `block_task` still transition, and record the outcome under the caller's
  identity instead of the label. `get_skill_stats` is unchanged. No migration.

- Every MCP call carries the agent that made it (P-257, audit B1; P-276; P-277; drifts D6 and D9).
  `Caller.Agent` was set nowhere on the served path, so `tool_calls.agent`, `tasks.claim_agent`,
  `created_by_agent` and every event's agent stayed empty and `agent_sessions` held one row per
  user. The agent is now the API token's label (`token create --label`), else the OAuth client's
  registered `client_name`, else the MCP initialize `clientInfo.name`, with control characters
  dropped, trimmed and cut to 64 characters. The default label `cli` names no agent, so two clients
  on one default token are two `agent_sessions` rows by their client names; two on one labelled
  token are that label's one row. The row's `kind` says `api_token` or `oauth_user` (a new enum
  value, `contract/deltas.json`) instead of the hard-coded `service_account`. No migration:
  `agent_sessions` was keyed by (identity, agent) already. A claim belongs to its agent: the same
  identity's other agent claiming a held task is `CONFLICT_LOCKED`, and the same agent's second
  session still shares the lease (P-278); a lease taken before the upgrade has no agent, so its
  agent's re-claim is `CONFLICT_LOCKED` until it expires. A follow-up on a held task now targets the
  holding agent (the claim's `agentId`, conductor's resolveTarget) rather than its account; the
  account's own browser session still acks it, another agent of the account does not. A label is
  one user's: `token create` and `POST /api/me/cli-tokens` refuse with `VALIDATION` a label another
  user's token has ever carried, revoked or expired included, so the first user to mint a label keeps
  it. Below master, an explicit `assignee` on `claim_next` or `next_task`, or a `handoff_task`
  `toAgent`, naming a label a live token carries, or one another user's token has ever carried
  (so a lapsed owner's queued work stays hers, P-338), is `FORBIDDEN` unless the caller is on a
  token carrying it and no other user's token has ever carried it (P-277); an OAuth `client_name`
  or a `clientInfo.name` never holds a label. Two users minting one new label at the same instant
  can both land (the check is a read before the insert); the label then stays `FORBIDDEN` to both
  below master, and revoking either token does not free it, so each picks another label. A master
  may name any label, and `claim_task {taskId}` does not check the assignee. A label-registered
  worker's assigned doorbell line names
  `claim_next {projectId, boardId, assignee: "<label>"}` (P-276). The Agents screen counts a session
  online when the device directory lists it or its last request is recent, not by
  `wakeAttachedAt`, which nothing writes, and its Auth column says `api token` or `oauth`.
- Every tool carries MCP annotations (P-286, audit B2). All 86 advertised `annotations: {}`, and
  Codex headless (`approval_policy=never`) cancelled even `list_tasks`. Each `domain.Posture` row
  now has an `Effect` (read, write or destructive) and `Idempotent`, and `tools/list` gives every
  tool `readOnlyHint`, `destructiveHint`, `idempotentHint` and `openWorldHint: false` from them:
  27 reads, 13 destructive (every `delete_*` and `remove_*`, `pick_run`, `cancel_runs`,
  `retract_artifact`, `disconnect_agent`, `grant_capabilities`, `rotate_integration_secret`).
  `detect_anomalies` is a write: it records first sightings as events. A row without an `Effect`
  fails `TestPostureEffect` and panics at registration, and `TestReadEffectWritesNothing` calls
  every read on both engines and fails if the board feed or change log moves. Annotations sit
  outside the contract's canonical form, so no delta.
- `claim_next` and `next_task` take only your own or unassigned work (P-275, audit A1, owner
  2026-10-24). With no `assignee` the frontier admitted every TODO task, so a client following the
  served instructions (`claim_next {projectId, boardId}`) claimed tasks handed to another agent,
  across identities and vendors. Without `assignee` the frontier is now the unassigned tasks plus
  those assigned to the caller's id or, in any case, its account email (a handoff stores the login
  as given), matched inside the one claim statement on SQLite and Postgres, so the statement pins
  are unchanged. A task assigned to another identity or to an agent label is in no one's default
  frontier. An explicit `assignee` naming another account (by email or id) or another member of the
  project is `FORBIDDEN` below master; an agent label names no identity and still passes, since no
  caller carries its label yet. Conductor filters nothing without `assignee`; this is a deliberate
  departure. The served MCP instructions give non-Claude clients the pull recipe
  (`list_notifications`, then `claim_next {projectId, boardId}`, no assignee needed), and the worker
  skill and the worker, work and work-assigned commands say the same and pass `assignee` only for an
  agent label; the plugin is `0.1.3`.
- A handoff notifies its receiver (P-275, audit A12; migration 0006 owner-approved 2026-10-24, zone
  Z2). `handoff_task` wrote a task memory and rang the socket doorbell, but no notification row, so a
  client that pulls (every non-Claude client, `list_notifications` each turn) never learned of it.
  It now writes one `handoff` notification (`data.toAgent`) for the member `toAgent` names, by member
  key or by login in any case, and the receiver's `/api/stream` gets its notifications frame; the
  sender, an agent label and a handoff to oneself notify nobody. Migration 0006 adds `handoff` to the
  notifications type CHECK on both engines (SQLite rebuilds the table; down drops the handoff rows).
  `list_notifications` declares the new type (`contract/deltas.json` gains `addedEnumValues`), and
  the Notifications screen reads "X handed you T-n". The doorbell is unchanged.
- A reviewer's reopen hands the rework back to its completer (P-275, review B72-1). `add_follow_up
  {reopen: true}` kept the assignee, which `complete_task` had made the reviewer, so under the new
  routing the reopened task was in no worker's `claim_next` and nobody was rung. The reopen now
  assigns the task to `review.completedBy`, who is rung and gets the follow-up; with no completer on
  record it clears an assignee that is the reopener or the reviewer (a broadcast) and keeps any
  other. The master command says so, and that `next_task`'s head is unassigned or your own work.
- The newcomer web path points at this server (P-287). **Connect MCP** builds every snippet from the
  page's origin + `/mcp` with `Authorization: Bearer $BATONDECK_TOKEN`, links "create a token" to
  Settings → CLI tokens, has one tab each for Claude Code, Codex CLI, Gemini CLI and Cursor (each in
  the shape its own `mcp add --help` or docs give), says only Claude Code with the plugin gets the
  device wake, and shows the OAuth connector path only when the OAuth server runs. The hosted URL,
  the conductor marketplace, `mcp-remote`, the dead `/docs` link and the agent-name field (this server
  reads no `X-BatonDeck-Agent` header) are gone. **Sign-in** says agents use API tokens, drops the
  "What is BatonDeck?" link that looped to itself, and on a server with no account shows
  `batondeck-kanban user add --admin <login>`. **Projects**, empty, lists three next steps (create a
  project, create a token, connect an agent). **Settings** leads with Columns, Members and CLI tokens.
  `GET /api/public-config` gains `oauth` (the OAuth server runs) and `noUsers` (no account exists).

- The default data dir is absolute and per-user (P-288): with no `database.url`, the SQLite file and
  the blobs live in `$XDG_DATA_HOME/batondeck-kanban` (when that is absolute), else
  `~/.local/share/batondeck-kanban`, made 0700 by `serve` or `migrate`. It was `./data`, relative to
  the working directory, so `user add` run from another directory found no database (or `migrate`
  made a second one) and the admin it made could not sign in. **An existing install keeps its data by
  setting `BATONDECK_DATABASE_URL=/abs/path/to/data/batondeck-kanban.db`** (blobs and `master.key`
  stay beside it), or by moving `./data` to the new dir. On Postgres with no `blob.dir`, blobs moved
  from `./data/blobs` to `blobs` in that dir likewise. The Docker image is unchanged: it sets
  `BATONDECK_DATABASE_URL=/data/batondeck-kanban.db` and now `BATONDECK_BLOB_DIR=/data/blobs` too.
  `serve`'s first line names the SQLite file (or the Postgres host and database, never the URL) and
  the blob dir; `doctor` reports a SQLite file not created yet as `not created yet; serve creates it`
  and exits 0 instead of FAIL.
- `serve -h` and `doctor -h` list every `BATONDECK_*` variable with its default, and the config-file
  rule; a test fails when a source reads a variable the list does not name. `-h` exits 0.
  `batondeck-kanban help`, `-h` and `--help` print the usage to stdout and exit 0 (they said
  "unknown command" and exited 2).
- `user add` and `user passwd` refuse a password under 12 characters, saying how long it was.
- A commit wake reads the change log at once, and `/api/stream` sends a board's first change at once
  (P-193, P-295). The bus read at most once per 250 ms poll, so a wake within a poll of the last read
  waited for the next tick, and the stream held every first change for its 100 ms coalesce: a board
  frame arrived 239-344 ms after a write that followed another. A wake now reads at once unless the
  last woken read is under `bus.WakeGap` (25 ms) old, when it reads at the gap's end with every wake
  since: a write storm of about 200 commits a second costs about 36 reads a second (pinned at 45),
  not one per commit, and an idle tail still issues none. The stream sends a change a coalesce after
  its last board frame at once and gathers the changes within a coalesce of it into one frame at
  that coalesce's end, so frames stay at most one per 100 ms. The join's stream test bounds five
  back-to-back moves and the quiet one at 200 ms from `move_task`'s return (commit to frame), at
  serve's real poll, on SQLite and Postgres.

- `serve` runs the retention sweep (P-109): R0.11's `retention.Sweeper` starts beside the server,
  sweeps once at boot and then every `retention.sweep_interval_seconds` (new config, environment
  `BATONDECK_RETENTION_SWEEP_INTERVAL_SECONDS`, default 3600), and stops before the store closes.
  It deletes, in bounded batches, memory past its expiry, change-log rows past
  `retention.change_log_days`, tool_calls rows past `retention.tool_calls_days`, agent sessions idle
  for 30 minutes (ADR-0004's GC in place of `prune_agent_sessions`; `Store.DeleteIdleAgentSessions`
  now takes a limit, longest idle first), deliveries and raw webhook bodies past the expiry stamped
  on each, and (with a blob store) the blobs unreferenced for an hour. Before this, nothing ran it,
  so tool_calls and the change log grew without bound. The delivery and raw-body windows are config
  too: `retention.deliveries_days` (`BATONDECK_RETENTION_DELIVERIES_DAYS`, default 30, also an alert
  state's expiry) and `retention.raw_bodies_days` (`BATONDECK_RETENTION_RAW_BODIES_DAYS`, default 7),
  stamped on each row when written, so a change applies to rows written after it. Every window is
  1 to 36500 days and the interval 1 to 86400 seconds; past a bound the config is refused, since a
  window that overflows `time.Duration` would put the cutoff in the future and sweep a whole table.
  Boot logs the interval and every window.
- The race test run's per-package timeout is 20 minutes (was Go's 10-minute default): `internal/cli`
  reached it under load with no hung test, failing three gates (owner 2026-10-25). P-289 shortens
  the package instead of leaning on the bound.
- The device-directory tests run in parallel with shorter coalescing waits (P-289). They waited
  ADR-0004's real 5 s broadcast and changed window one after another, and set package variables,
  so they could not run together. `wake.Options.Windows` now takes the windows (a zero field is the
  production value: assigned 300 ms quiet within 1 s, broadcast and changed 5 s), and serve's
  composition passes none; a test hands its registry, sweep and windows to `composeWith`, and the
  tests that wait the slow window out hold it at 2 s. `TestProductionWindowsAreADR0004s` and
  `TestServeDirectoryHoldsTheProductionWindows` pin the production numbers. Every bound and every
  expected silence is unchanged, and so is every expected ring but one: before its silent step,
  `TestDirectoryHandoffToSelfRingsNobody` now drains the creates' burst, so it no longer fails when
  that burst rings more than once. Four other tests still catch a broadcast window that does not
  coalesce (`NoSelfRing`, `RebuildsAndCatchesUp`, `RefusesAnotherIdentityTakingAPid`,
  `RingsAWorkerWithTheExactFrame`), and `MasterCoalescesAt5s` pins the slow window. The three tests that time or count against a bound
  (`RingsAWorkerWithTheExactFrame`, `DropsAKilledProcessWithin1s`, `SweepDropsADisabledUser`) stay
  serial, and the no-ring tests count from the writes their clients have read, since the directory
  counts a doorbell just after writing it (P-319's "wrote 1 doorbells, want 0"). On 4 vCPUs with
  Postgres, `go test -race -count=1 ./internal/cli/` went from 584 s to 451 s (the directory tests
  from 197 s run serially to a 51 s parallel phase); the rest of the package is serial and
  untouched, so the 20-minute timeout stays.
- Sign-in is bounded (P-220, audit G4). Each password check allocates argon2id's 64 MiB, and 64
  concurrent bad logins took `serve` from 124 MB to 2.47 GB RSS. Now at most 2 checks (128 MiB) run
  at once process-wide, whatever the core count, and a caller that waits 3 s for a slot is refused.
  Every attempt debits a token bucket of its client address (10 a minute; an IPv6 address counts
  by its /64) before the password is checked; a success refunds it. There is no bucket per login,
  so failures from other addresses never lock an account. Past the limit, `POST /api/login` and the
  OAuth sign-in's `POST /login` answer 429 `{"code":"RATE_LIMITED"}` with `Retry-After` (the OAuth
  page's form with `next` gets its `login=failed` redirect). The address is the TCP peer: behind a
  proxy every client shares its budget (P-291).
- The doorbell names its board (P-191, owner 2026-10-24). The socket frame was `{kind, cursor,
  projectId}` and its line said "call claim_next", which needs a `boardId` the frame never carried:
  two live haiku receivers passed the project id as the board id. The frame now adds `boardId` when
  every row coalesced into it is on one board, and `boardIds` (sorted, distinct) when they are on
  several, up to 20. A frame naming no board means every board: a project-level row, the frame rung
  after the dispatcher dropped rows, or more than 20 boards (never a partial list); it absorbs a
  board frame it coalesces with rather than being narrowed to it. A frame of another project brings
  only its own boards, so no board is paired with a project it is not in. The line names the exact
  call, `claim_next {projectId: P-…, boardId: B-…}` (a master's `changed`: `next_task {…,
  includeInbox: true}`; each id of `boardIds`; `list_boards` first when no board is named). Still no
  task id and no title. Coalescing windows are unchanged; the start-up catch-up merges the same way.
  The served MCP instructions, the worker skill and the worker and master commands say to take
  `boardId` from the doorbell; the plugin is `0.1.2`.
- Local sign-in, sign-out and Settings → Users (R1.15). The sign-in screen is a login form posting
  R0.12's form fields to `POST /api/login`, which now answers a refusal as `{code, message}` (one
  message for an unknown login, a wrong password and a disabled account) and the form shows it; on
  success the route asked for renders, with no navigation. The WorkOS button, the tenant and access
  copy and the dead demo button are gone. New `POST /api/logout` (behind the session and CSRF)
  deletes the session row (`Store.DeleteSession`, both engines) and expires both cookies: sign-out
  called the unmounted `/auth/logout` before (P-198). New `GET` and `POST /api/users`, the workspace
  admin's only (anyone else is 403 `FORBIDDEN`): list the local accounts (login, name, role,
  disabled) and add a member, whose generated password the answer carries once; disable and
  password reset stay with `batondeck-kanban user disable|passwd`, and nothing changes a role yet.
  Settings shows the Users panel to the workspace admin in place of the WorkOS invitation panel, and
  `web/src/api/tenants.ts` is gone. A disabled user's open session is 401 within the 60 s
  account-cache window.
- The SPA on five states (R1.14, ADR-0005): `Status` is `TODO · IN_PROGRESS · BLOCKED · REVIEW ·
  DONE`, derived from the exported `STATUSES`; every status map, filter, chart order, the move-button
  fallback table and the ticket-search aliases follow it (no `CANCELLED`, `BACKLOG`, `READY`, and the
  `cancelled`/`canceled`/`backlog` aliases are gone), and the move targets read the board's fixed
  `transitions` instead of the dropped `settings.transitionTable`. `make web-states-check`, a `make
  check` step, refuses `CANCELLED`, `DEAD_LETTER`, `BACKLOG`, `READY`, `getTransitions` or
  `setTransitions` anywhere in `web/src`, tests included.
- The poison badge (plan §Fold 1, ADR-0001): a BLOCKED task carrying the `poisoned` system label shows
  `Poisoned · N attempts` (its `claimAttempts`) on its card in the Blocked lane and in the drawer,
  where a master (master, member or admin of the project) gets "Requeue to To do" (`move_task` →
  TODO) and a worker sees it disabled. A task blocked by a dependency keeps the plain `Blocked` badge.
- `web/src/api.ts` is gone: every client function lives in one `web/src/api/<area>.ts`, one per
  `internal/api/<area>.go` (new: `session`, `projects`, `boards`, `tasks`, `lifecycle`, `context`,
  `media`, `feed` and `stream`; the interim `tenants` file went with the WorkOS invitation panel),
  `http.ts` holds `req`, `ApiError` and the local-edit marks, and every screen imports its areas
  directly. The studio, admin, billing, tenant-switch and onboarding functions, which no kept screen
  called, are dropped with the routes.
- Live board from `/api/stream`: presence frames (the device session directory's entries) become
  `device:<pid>` session rows that each frame replaces, and a `resync` frame or a rebuilt socket
  refetches the board, its tasks and its feed, since no board frame comes until the next change.

- The board snapshot cache serves (P-100, ADR-0004): `serve` subscribes it to the bus with a second
  `bus.Tap` that feeds `Core.ObserveChange`, so a second `get_board`, `get_task_context`, `claim_next`
  or `/api/stream` board frame on an unchanged board reads no board (2 statements saved), and any
  change-log row naming the board (any project-wide row: every board of it) drops it. The tap adds no
  statement and no poll; a change it drops clears every snapshot, and the cache turns off when the
  bus closes. `service.mutate` drops the snapshot of every board its change rows name, and of its
  task's board, once the transaction commits, so a writer reads its own write before the bus delivers
  the row; this replaces the four hand-placed drops in `add_column`, `update_column`,
  `remove_column` and `delete_project`. New: `Core.ResetSnapshots(live bool)`.
- `move_task` to DONE writes a `changed` row naming its own task's board before it unblocks the
  dependents, as `complete_task` does (P-187): a move whose only dependent sat on another board wrote
  one row naming only the dependent's board, so every other process served the moved task's board
  from a stale snapshot.
- The pre-push gate removes its Postgres container with `docker rm -f -v`, so a gate run no longer
  leaves an anonymous volume behind (P-130: about 57 GB had piled up and filled the disk twice).
- `TestPostgresDeliversAnInFlightIDAfterAnEarlierHoleFills` runs with a 3 s gap timeout instead of 1 s, so a
  loaded gate no longer passes hole 2 before its commit (P-141, P-147). It still fails on the per-tail clock bug.
- `TestAttachUploadListOverTheEngines` compares the attachment events as a set: two events in one millisecond tie
  on a random id, so their order is not fixed.
- Five states (R0.26, ADR-0005; the owner, 2026-10-23: "rename Ready to TODO and remove backlog. if
  task is added, it can't be draft"). `domain.Todo` (`"TODO"`) replaces the claimable state and the
  parking state is gone: `domain.AllStatuses()` is `TODO, IN_PROGRESS, BLOCKED, REVIEW, DONE`, and the
  fixed table is TODO→IN_PROGRESS; IN_PROGRESS→REVIEW, BLOCKED, TODO; BLOCKED→IN_PROGRESS, TODO;
  REVIEW→DONE, IN_PROGRESS; DONE→IN_PROGRESS, 14 legal pairs of 25 with the same-status moves.
  Unblock, retry backoff, release, lease expiry, handoff and un-poison land on TODO; poison stays
  BLOCKED with `blockedReason: "poison"`. A new board's default columns are To do, In progress,
  Blocked, Review, Done. `create_task` and `add_subtask` always land TODO: in the board's first TODO
  column, and a `columnId` naming a column of any other status is `VALIDATION` (`create_task` already
  took no `status`); every unassigned create rings the broadcast doorbell. Every tool schema's status
  enum is the five values, from `domain.AllStatuses()`.
- Migration `0004_five_states` on both engines (zone Z2, approved with the plan): every task in either
  retired state becomes TODO; each board's parking columns fold into its first claimable column, which
  keeps its WIP limit and takes their tasks and integrations (a board without one keeps them as TODO
  columns); the default column names become "To do"; both status CHECKs become the five values. On
  SQLite `columns` and `tasks` are rebuilt outside goose's transaction with foreign keys off, since
  dropping `tasks` with them on would cascade through every table that references it; `rid` is kept,
  so the FTS5 index stays valid. Down maps TODO back and restores the six-value CHECKs, lossily (no
  parking column comes back). `internal/store/five_states_migration_test.go` runs up, down and up on
  a seeded database on both engines and pins the schema to 0003's, CHECKs aside.
- `contract/deltas.json` records the divergence from conductor: 58 `renamedEnumValues` (conductor's
  claimable state to TODO, a new delta kind in `contract/canonical.go`, every entry required to hit and
  refused onto a value already present) and 58 more `removedEnumValues`; `contract/expected.json` is
  re-derived. `TestNoRemovedStatusRemains` refuses the two retired names in the surface.

### Added
- The `image-staging` workflow (`.github/workflows/image-staging.yml`): on demand, with a commit on
  main, it builds the two-architecture image and pushes it as `ghcr.io/batondeck/batondeck-kanban:staging`,
  then runs it and checks the version it reports, `<version>-staging+<sha>`. One staging build runs at a
  time. It cuts no release and moves no version tag (owner 2026-11-04).
- `trusted_proxies` (`BATONDECK_TRUSTED_PROXIES`): the reverse proxies, as IP addresses or CIDRs,
  whose `X-Forwarded-For` the server reads (P-291). A request from one counts against the rightmost
  address in that header that is not itself a trusted proxy, so behind a proxy the sign-in limit
  (`/api/login` and the OAuth sign-in page) and `/register`'s limit count each client, not every
  user as the proxy. Unset, the header is never read and the peer counts, as before; a malformed
  entry stops `serve` at start. The README's step 6 and `compose.yaml` say how to set it.
- **`batondeck-kanban user enable LOGIN`**, the way back from `user disable`: it clears the
  user's disabled mark, and their unexpired browser sessions and tokens resolve again within the
  account cache's 60 s. Enabling a user who is not disabled says so and changes nothing; an unknown
  login exits 1 (P-46).
- **CI for pull requests.** `.github/workflows/ci.yml` runs `make check` on every pull request and
  every push to main, with a Postgres 16 service, so the store conformance suite runs on both
  engines. `make check` gains `web-check`, the SPA's `tsc --noEmit` and its vitest suite, which
  `vite build` never ran; `make web-check` runs them alone. The suite's per-test timeout is 20 s and
  Testing Library's `findBy`/`waitFor` wait 5 s (vitest's and its defaults are 5 s and 1 s), so a
  loaded host does not fail a correct tree.
- **The plugin follows `BATONDECK_URL`.** `plugin/.mcp.json` connects to
  `${BATONDECK_URL:-http://localhost:8080}/mcp`, the base `scripts/mcp.sh` already read, so a board on
  another port or host needs no edit to the plugin.
- **Backup and restore**, in the README, for the binary on SQLite and for `compose.yaml` on Postgres:
  what to copy (the database, the blobs and the master key), the commands, and what a restore under
  another key loses.
- `GET /api/session` answers `displayName`: the signed-in account's display name, else its login,
  absent when the identity has no account row. The web UI's identity chip reads it (P-314).
- The `release-publish` workflow (`.github/workflows/release-publish.yml`): publishes a version's draft
  release on demand, after checking it targets the given candidate and carries the seven assets; the
  publish creates the tag (owner 2026-10-29).
- The `release-draft` workflow (`.github/workflows/release-draft.yml`): RELEASING.md's steps 1, 4, 5 and 6
  on demand, with step 3's tag made locally and no gate run, ending in a draft release a maintainer
  publishes (Z-03 from the cloud, owner 2026-10-29). Its `replace` input first deletes an existing
  release of that version and its tag, so a version is re-cut from a later commit by CI alone (owner
  2026-10-30).

- `docs/adr/`: the five decision records the tree cites, ADR-0001 to ADR-0005, copied unchanged
  from the planning workspace, until now their only home (P-309, audit D4), and a README.
  `TestADRReferencesResolve` (`internal/arch`, so `make check`'s `arch` step) fails on any
  `ADR-NNNN` in a tracked or new file with no `docs/adr/NNNN-*.md`, and on a record whose name or
  heading does not carry its id. The plan of record gains a one-line "Superseded by" banner on each
  passage ADR-0001, ADR-0002 or ADR-0004 replaced (the channel, `/wake`, subscribe push, the Actions
  workflow) and a "superseded by measurement" note on the query pins, the resource template count
  and the CLI list. The plan's "plan Amendment B/E" citations and `internal/wake/doorbell.go`'s
  name ADR-0004 and ADR-0005; the two `migrations/*/0004_five_states.sql` headers keep "plan
  Amendment E" (zone Z2, left for the owner). ADR-0001 is amended in place to the code's rules
  (cancel by the live lease holder; un-poison lands TODO), and ADR-0004 to the doorbell frame's
  `boardId`/`boardIds`. Docs and comments only.
- Browser security headers on every response (P-250). One middleware around the composed handler
  sets `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY` and
  a `Content-Security-Policy` on every response; the `/mcp`, `/hooks` and `/blobs` routes drop the
  policy (JSON, SSE and attachments), per route, so an unanswered path under them still gets the SPA
  shell with it. The policy: `default-src 'self'`, the theme bootstrap's script hash, inline styles
  (mermaid's `<style>` and style attributes, which no hash can allow), Google Fonts, images from this
  origin, `data:`, and for the link favicons `www.google.com` and the `*.gstatic.com` it redirects
  to, only; `object-src 'none'`, `base-uri 'self'`, `frame-ancestors 'none'`. The app could be framed
  before, and a chronicle page could make its readers load any external image. The OAuth pages keep
  their stricter policy. A test recomputes the hash of every inline script in the embedded
  `index.html`.

- `grant_capabilities` (R2.4), the last tool in `contract/pending.json`, which is now `{"tools": {}}`:
  a project admin (conductor's `requireAdmin`) narrows one member's mutation surface below their
  role. No `scope` writes the worker default, `null` removes the grant, an object replaces it; a
  non-member is `VALIDATION`, and so is a `tools`/`deny` entry naming a tool that takes no projectId.
  The grant is the new `Grants` store area (`capability_grants`, both engines and memstore, its
  conformance battery) and the core now reads it on every governed call: until this change no grant
  could be written and none was enforced. A grant on a project admin is stored and not enforced, as
  in conductor. `tools/list` is narrowed too: a governed project tool is left out when the grant of
  every membership the caller holds refuses it by name, so a tool callable in any project stays
  listed. The change sends the member's sessions `notifications/tools/list_changed` at their next
  request (R1.7's mechanism, its audience now the admin flag plus the hidden tools), read through a
  per-identity cache dropped by the grant, a membership change or `create_project`. The router
  facade (`find_tools`, `get_tool_schema`, `invoke_tool`) is not registered (ADR-0004).
- The operations summary (R2.11): `GET /api/analytics/operations?projectId&days` replaces R1.12's
  stub with conductor's OpsSummary (`total`, `errors`, `errorRate`, `avgLatencyMs`, `byTool`,
  `byDay`, `byAgent`, as `aggregateOps` rolls it up) over the `tool_calls` rows, plus a `retention`
  row (`days` 45, `oldestDay`) naming how far back rows are kept. `days` defaults to 7 and is clamped
  to 90. The membership check is the service's, under `get_project`'s posture: a non-member is 403
  `FORBIDDEN`, never an empty summary. One statement per call on each engine
  (`Store.SummarizeToolCalls`, grouped by tool, outcome, agent and UTC day), pinned in `test/perf`.
  Each `tool_calls` row now carries the `projectId` the call named (it was always empty, so no
  project could have counted a call), except for a tool nobody registered and a call refused
  `FORBIDDEN` or `UNAUTHENTICATED`, so a non-member moves no project's summary. Analytics and Reports read the summary unchanged, and a
  cancelled task (DONE with the `cancelled` label) counts in the total and in no done figure (P-208).
  This was the last stubbed route, so R1.12's stub handler and `TestStubsNameTheirLane` are gone.
- Agent sessions, your settings and the audit log (R2.5): `list_agent_sessions`, `disconnect_agent`,
  `get_my_settings`, `set_my_settings` and `list_audit_log` leave `contract/pending.json`.
  `disconnect_agent` closes every live MCP session of `(identityId, agentId)` and deletes its row:
  the next request on an ended session is 404 `session not found` and the agent initialises again
  (**named deviation:** conductor's revocation was sticky; here a new initialize is admitted). Your
  own agents, or anyone's as the workspace admin; it writes `agent.disconnected`. A session that
  disconnects itself gets its answer first. `get_my_settings`/`set_my_settings` store the ticket-token
  ceiling, rung and starred assets on the caller's `identities` row (a new `Settings` store area, both
  engines and memstore, no migration); nothing enforces the ceiling yet. `list_audit_log` is the
  workspace admin's (hidden from anyone else's `tools/list`, `FORBIDDEN` by `service.authorize` when
  called by name), with conductor's `actor`, `subject`, `action`, `days` (90, max 365) and `limit`
  filters; no cursor (its schema has none). The `/api/agents/sessions`, `/api/me/settings` and
  `/api/me/cli-tokens` routes replace R1.12's stubs: a CLI token minted on the Settings page answers
  its plaintext once, is listed without it, and revoked there is 401 at the next MCP initialize;
  another user's token id is 404; the page mints at most 10 live tokens per identity (conductor's cap,
  400 `VALIDATION` past it; the host's `token create` has no cap) and keeps no plaintext in react-query's mutation cache after Done. Minting and revoking a CLI token, on the page or with `token
  create|revoke` (actor `host`), write `cli_token.minted` and `cli_token.revoked` (P-159). New `GET
  /api/audit`, list_audit_log for the Workspace Log's admin-only audit panel. A device entry in the
  presence frames carries `lastDeliveredCursor` (`wake_sessions.last_cursor`), and a delivery signals
  the frames, so the Agents screen shows each device session's last delivered doorbell ("none" when
  never rung). The Agents screen composes its data from `get_project` and each board's tasks and feed
  (no overview route, P-182); Settings no longer shows or calls the marketplace key (P-184). A nullable
  enum field's schema (`tokenPolicy`, chronicle `coverage`) now admits `null` beside its values.

- The Chronicle routes and screens (R2.7): the eight routes `web/src/api/chronicle.ts` calls replace
  R1.12's stubs in `internal/api/chronicle.go`, over R2.6's `service.Chronicle`: `GET` and `POST
  /api/projects/{projectId}/chronicle` (the index, `?cursor=`, and create), `GET` and `DELETE
  …/chronicle/{pageId}` (the page view, and delete pinned by `?expectedHeadVersionId=`, a moved head
  `STALE`), `GET …/{pageId}/versions` and `…/versions/{versionId}` (the history, `?cursor=`, and one
  version), `POST …/{pageId}/edit` and `POST …/{pageId}/revert`. A revert writes a new version
  holding the chosen version's blocks over the head it read (an edit, never a rewind). A non-member
  is 403 `FORBIDDEN` on each; a page asked for through another project is absent (`null`, or 404
  `NOT_FOUND` on a write). `ingest_chronicle_page` stays MCP-only. The `/chronicle`,
  `/chronicle/new`, `/chronicle/:pageId` and `/chronicle/:pageId/history` screens now load real
  records: a mermaid fence renders as mermaid's SVG, and an image embedded by its signed blob URL
  loads through `/blobs/…`.
- Notifications, agent profiles, anomalies, rank and orchestrate (R2.3): seven tools leave
  `contract/pending.json`. `list_notifications` and `mark_notifications_read` read and clear the
  caller's own in-app feed across projects (no projectId); the rows are written in the event's own
  transaction by `service.mutate` from the events conductor materialised them from
  (`domain.NotificationsForEvent`): a comment's resolved @mentions, a follow-up's verified target
  and a completion's reviewer, never the actor. A handoff or an assignment writes no row (conductor's
  notify.ts), and comments still ring no device. `mark_notifications_read` answers the rows it
  flipped from unread. Each row adds a change-log row of kind `notification` scoped to its
  recipient, and every `/api/stream` (board or none) sends a `notifications` frame, the caller's
  `list_notifications {limit:50}`, on open and per such row; `GET /api/notifications` and `POST
  /api/notifications/read` replace R1.12's stubs. Delivery to SendGrid and Slack is dropped (plan
  §Target surface): in-app and the stream only. `register_agent_profile` and `get_skill_stats` keep
  an agent's capability profile and its per-capability outcomes; `complete_task` and `block_task`
  roll the outcome up, in their transaction, when the task names required capabilities.
  `detect_anomalies` ports conductor's six detectors over one board (at most 1,000 tasks and the
  last 200 feed events), with a dependent of a poisoned task (`blockedReason: "poison"`) a `warn`
  `dep_deadlock` and no `CANCELLED` row, an all-zero `anomalyPolicy` read as the defaults, and
  `task.anomaly_detected` appended once per first sighting. `rank_tasks` ports the cost-aware
  scorer and the profile match (exact, alias, tag) over the board's claimable TODO tasks.
  `orchestrate_subtasks` creates the children TODO under the held parent with a blocks edge each
  and parks the parent BLOCKED, lease dropped, assigned to the caller; each child rings broadcast and
  the parent's unblock rings the caller. A nullable enum in a tool's output (`costClass`,
  `maxEffort`) now accepts null: the schema is `anyOf [enum, null]`, the same canonical node.
- The on-call routes and screen (R2.10): the ten `/api/oncall/…` routes of `internal/api/oncall.go`
  replace R1.12's stubs, each one `service.Oncall` call under the session and CSRF, workspace admin
  only (a project admin who is not one is 403 `FORBIDDEN`, as the tools are). `GET
  /api/oncall/sources` serves `oncall.Sources` with each source's modes, note and guide name
  (`oncall-<id>`); create, update (an auth switch to hmac) and rotate answer a minted secret once
  with `shownOnce`, and the listing never carries one. `DELETE …/integrations/{integrationId}`
  takes its `version` in the query, as the SPA sends it: the BFF now reads a DELETE route's named
  query keys (`api.in.query`) over its empty body. `GET …/deliveries/{deliveryId}/raw` answers the
  kept bytes themselves as an `application/octet-stream` attachment with `nosniff` and `private,
  no-store`, which the setup drawer's viewer reads as text; a body no longer kept is 404
  `NOT_FOUND`. An integration addressed under another project is 404 and its log an empty page; a
  member of another project only is 403. The Oncall screen and setup drawer run unchanged on these
  routes, and a delivery whose `taskId` autoclose deleted shows the id as text with no link and no
  lookup (`web/src/__tests__/oncall.test.tsx`). The setup drawer's create, save and rotate mutations
  set `gcTime: 0`, so a secret in their response or variables leaves react-query's cache when the
  drawer closes rather than 5 minutes later.
- Runs and sprints (R2.2): `start_runs`, `open_run`, `pick_run`, `cancel_runs`, `list_runs`,
  `create_sprint`, `get_sprint`, `list_sprints`, `set_sprint` and `update_sprint`, with conductor's
  names and inputs, leave `contract/pending.json`. **Named deviation (plan §Fold 2):** a losing run is
  deleted through `delete_task`'s cascade (`service.deleteTree`), never marked `CANCELLED`: `pick_run`
  deletes every run but the winner, a DONE one included, `cancel_runs` every run, each leaving a
  `run.discarded` on the parent and a `task.deleted` on the board, and `list_runs` lists what exists.
  `pick_run` lands the parent REVIEW (the project reviews) or DONE with the winner's deliverable and
  `racing` false; `cancel_runs` lands it TODO; `start_runs`, `pick_run` and `cancel_runs` move the
  parent one version (`store.StatusChange` gains `Racing` and `RaceOpenedAt`, both engines and
  memstore, no migration). A poisoned task cannot be raced. `get_sprint`'s `workflow.transitions` is
  the fixed table, its `counts.poisoned` replaces `deadLetter` (a cancelled member is not `done`),
  and update_sprint's close warning names the members not DONE. New: `store.Runs` and `store.Sprints`
  with their batteries, `service.Runs` and `service.Sprints`, `internal/domain/sprints.go` (the sprint
  table, the membership and activation gates), the eight routes `internal/api/{runs,sprints}.go` in
  place of R1.12's stubs, and the Runs screen without a discarded state. `pick_run` promotes the
  winner's live artifacts onto the parent (conductor's `mergeArtifacts`, de-duplicated, up to 50), so
  a picked parent carries its evidence; a race resolves when its parent shares a dependency edge with
  a losing run or that run's subtask (the cascade strips the parent's caches into its one write).
- Artifacts, comments and follow-ups (R2.1): seven tools leave `contract/pending.json` (30 to 23)
  and equal `contract/expected.json`. `add_artifact` binds a pr, branch, commit, file, doc or
  attachment to a task (a url or a ref each, de-duplicated on kind, case-folded url and ref against
  live entries, at most 50 a task) and `retract_artifact` tombstones the live entries a selector
  matches (none at all is `NOT_FOUND`); both take an optional `version`. `get_task` now carries the
  task's `artifacts` (its statement pin 2 to 3), the drawer's evidence list reads them, and
  `list_tasks {missingArtifacts:true}` keeps the tasks with no live artifact in one statement for
  the page. `add_comment`/`list_comments` hold a task's thread, its `@mentions` resolved against the
  project's members; a comment rings nobody (ADR-0004), the master doorbell included.
  `add_follow_up` (master tier) directs a note at the live lease holder, else the assignee, and rings
  that target's `assigned` doorbell; `reopen:true` sends a REVIEW or DONE task back to TODO.
  `ack_follow_up` is the target's or a project admin's; `list_follow_ups` takes `openOnly`. A
  comment, a follow-up without a reopen and an ack leave the task's version as it was, as
  conductor's do. The comment and follow-up routes under `/api/projects/{projectId}/tasks/{taskId}`
  replace R1.12's stubs. No migration: 0001 holds the three tables. Autoclose (R2.9) now keeps an
  incident that carries a comment or follow-up by anyone but the ingest, and annotates it instead,
  since those writes leave the version its untouched stamp compares.
- The webhook ingest, in-binary (R2.9, plan §Architecture "Oncall ingest is in-binary"): `POST
  /hooks/{integrationId}` is mounted by `serve` outside every bearer, session and CSRF check, over TCP
  and the socket, behind `api.CapBody` (512 KiB, hdtp's, read before any lookup). `service.Core.Hook`
  then takes, in order: a coarse per-id pre-check, the integration by its id alone
  (`Store.GetIntegrationByID`; unknown, tombstoned and disabled are one flat 404), the integration's
  `payloadCapKb` (413, nothing written), verification by its auth mode before a byte is parsed
  (`hmac` in BatonDeck's `v0=` form or PagerDuty's raw-body `v1=`, `api_key` in `X-Batondeck-Key`,
  `basic` with a `WWW-Authenticate` challenge, `jwt` RS256 against the integration's JWKS), the
  source's connector, and one `Core.ingest` per event under `<source id or body hash>#<fingerprint>`.
  Accepted is 202 with each event's result; a refused signature is 401 and a stderr line naming the
  integration; a refused signature or an unparsable body is a `rejected` delivery whose raw body (64
  KiB) is kept 7 days, 20 an hour per integration. The route's own refusals (an id of the wrong
  shape, a body over the 512 KiB cap) are stderr lines too, naming the presented id. The tradeoff the plan names is accepted under its
  open question 6: the parser of attacker-controlled JSON shares a process with full DB access, and
  single-workspace removes the cross-tenant blast radius.
- The eight connectors (`internal/oncall/connectors`): alertmanager, datadog, dynatrace, gcp,
  generic, grafana, pagerduty and webhook, with Prometheus' family shared, each judged against a
  recorded fixture payload. The webhook connector's mapper (dotted paths, a fallback chain, literal
  defaults; no regular expressions) is ported, and `create_integration` / `update_integration` refuse
  a mapping path it cannot evaluate, naming it.
- `internal/oncall/auth`: API key and Basic verification by hashed key, and JWT bearer verification
  in the standard library (RS256 only, issuer, audience, `exp` required). The JWKS fetch is the one
  outbound call ingest makes and is logged as such (`ingest: outbound JWKS fetch GET <url> for
  integration <id>`); it holds conductor's SSRF posture: https only, every resolved address vetted
  (private, loopback, link-local, unspecified, the metadata host), the dial pinned to the vetted set,
  no redirects, 64 KiB, 5 s.
- Incident correlation and autoclose (plan §Fold 2): the fingerprint's alert state
  (`Store.GetAlertState`, `PutAlertState`) names the open ticket (one not DONE); a repeat firing
  appends (`incident.occurrences`), a resolve under `autoclose` deletes an untouched ticket through
  `delete_task`'s cascade (the delivery keeps its dangling `taskId`) and annotates any other, and
  `correlate` always annotates. Untouched is TODO, unclaimed, never attempted and nothing written: the
  `incident.ingestVersion` field is the version the ingest last left the ticket at, carried forward
  only while it still matches, so a write by anyone else is never laundered by a later append — and,
  since some writes hang things off a ticket without bumping it, a ticket the cascade would take
  anything more with (a subtask or run, a dependency edge of any type, an attachment, task- or
  shared-scope memory, a context item not by the ingest) is annotated, never deleted; a project-scope
  memory entry naming it as provenance survives the cascade and does not hold it. A new
  ticket carries the event's snapshot as a note (annotations filtered by `annotationAllowlist`).
  Ingest runs under the project lock.
- One per-integration rate budget (conductor's token bucket, `ceil(ratePerMin/4)` refilled at
  `ratePerMin` a minute, a delivery id charged once) that `POST /hooks`, `send_test_event` and
  `replay_delivery` all draw on: over it is 429 on the route and `RATE_LIMITED` on the tools.
- The retention sweep deletes expired deliveries and raw bodies (`Swept.Deliveries`, `RawBodies`),
  on the expiry stamped on each row at write time (30 and 7 days).

- `make bench` (R3.5, plan §Performance), outside `make check` and without `-race`: the claim race
  (50 go-sdk MCP clients × `claim_next` on one board of 50 TODO tasks, 50 distinct winners and 0
  errors asserted, p50/p95 of the median of 5 rounds after a dropped warm-up round), the idle cost (200 device-directory entries
  and 10 `/api/stream` clients held 60 s with no write: at most 3 statements per entry per sweep and
  none between sweeps, asserted) and test/perf's statement pins in three cache states, on SQLite
  and, with `BATONDECK_TEST_POSTGRES_DSN`, on Postgres. `bench/bench_test.go` writes
  `bench/results.json` (gitignored) and fails, against the committed `bench/baseline.json` measured
  on the same machine, on a latency beyond +20 % or any rise in a statement count, and on a README
  performance block that is not the baseline's; `make bench-baseline` rewrites both. The README
  gains its performance section.
- `store.StatementCounter`: each SQL engine counts every statement it issues, from any goroutine and
  under any context, which the bench's idle cost needs (the bus tail and the HTTP handlers run on
  contexts no caller holds). The auth areas (users, tokens, sessions, OAuth) are now counted too, by
  both counters; no pin moved.
- The Chronicle (R2.6): `create_chronicle_page`, `ingest_chronicle_page`, `edit_chronicle_page` and
  `delete_chronicle_page` (tier master since the pre-public review, grant-governed), and the six chronicle resource templates
  (`chronicle`, `chronicle-cursor`, `chronicle-page`, `chronicle-history`, `chronicle-history-cursor`,
  `chronicle-version`), all leaving `contract/pending.json`. `store.Chronicle` is conductor's eleven
  chronicle Store methods on SQLite, Postgres and memstore, judged by `conformance_chronicle.go`.
  A version is immutable; a page's head moves only from the version the writer read (`STALE` with
  `expectedVersionId`/`actualVersionId`, in the statement too, so racing writers land one). A
  re-ingest is conductor's three-way merge (`domain.MergeVersions`) in one transaction with one
  idempotency key. Bodies are `chronicle_blobs` rows keyed `<pageId>/<sha256>`, written and deleted in
  the page's own transaction; they are never files and never signed URLs. A body over
  `blob.max_bytes` is `VALIDATION` on every write. `delete_chronicle_page` leaves no version and no
  body of the page. New: `service.Chronicle` (the four tools and the four reads R2.7's routes need).
- The on-call integrations and their delivery log (R2.8, ADR-0004): nine tools,
  `create_integration`, `update_integration`, `delete_integration`, `list_integrations`,
  `rotate_integration_secret`, `list_deliveries`, `read_delivery_raw`, `replay_delivery` and
  `send_test_event`, each workspace-admin only (hidden from a non-admin's `tools/list`, `FORBIDDEN` by
  name with `requiredCapability: workspace-admin`) and then tiered as conductor's manifest: the four
  mutations structural, the rest master. An hmac secret is minted (`whsec_` and 256 bits), sealed
  with the keyring into conductor's seven-segment `enc1:` envelope (`internal/oncall/envelope.go`, AAD
  `integrations.auth:<id>`) and answered once (`secret`, `shownOnce`); an idempotent replay answers no
  secret, `list_integrations` only auth's shape, and an api_key or basic key is stored as its sha256.
  A rotation keeps the previous secret verifying for 24 hours unless `revokePrevious`
  (`internal/oncall/hmac.go`: the BatonDeck `v0=` and PagerDuty `v1=` forms, dual-accept over the
  current and the unexpired previous secret). `send_test_event` and `replay_delivery` ingest a
  normalized event in-process: the create and record arms of conductor's lifecycle table
  (`oncall.Decide`), filing a TODO incident labelled `incident` (and `test`) that rings its on-call
  identity `assigned` or the project `broadcast`, and one delivery row. `read_delivery_raw` is
  `NOT_FOUND` once a raw body is swept. New: the store's `Oncall` area on both engines and memstore
  with its conformance battery, `service.Oncall`, `Core.VerificationSecrets` and the retention
  sweeps' `DeleteExpiredDeliveries`/`DeleteExpiredDeliveryRawBodies` (wired into the sweeper by R2.9),
  `/hooks/<id>` as the endpoint path. The nine leave `contract/pending.json`; the surface snapshot
  pins the admin-only set apart.
- The contract against the live server and the per-call statement pins (R1.10).
  `internal/cli/contract_live_test.go` boots `serve`'s composition (`compose`, `serveUntil`) on every
  engine and reads it with a workspace admin's go-sdk client: `TestLiveContract` holds tools/list,
  canonicalised, to `contract/expected.json` less the new `contract/pending.json` exactly (43 tools
  compared, 43 pending, 86 in the contract), and to the derivation from `tools.json`,
  `admin-tools.json` and `deltas.json` computed afresh; prompts/list to `tools.json`'s four by name and
  arguments; resources/templates/list to `tools.json`'s less the six pending chronicle templates plus
  `board-feed-cursor`. A pending tool that is registered fails, and so does a contract tool neither
  registered nor pending, so `pending.json` only shrinks: each lane removes its names when it
  registers them. `TestSurfaceSnapshot` pins the served tool, prompt and template names for an admin
  and a member. `contract.CheckLive` and `contract.Pending` carry the rules (`contract/live.go`).
- `test/perf/queries_per_call_test.go`: `get_task` 2 (3 since R2.1's artifacts), `list_tasks` 2,
  `get_board` 2, `claim_next` 4
  (Postgres 5: the change row's `NotifyChanges`), `get_task_context` 6, `next_task` 3, each a
  commented constant measured on SQLite and Postgres with the membership cached and the board
  snapshot in the state the pin names; the other snapshot state and a fresh core are printed beside
  it. New: `store.CountingStatements` (`internal/store/count.go`, over the existing counted handles),
  `make perf` in `make check`, and the live half of `make contract`.
- `deltas.json` `errorDeltas`: wire-code departures no schema shows; the first is P-138, a missing
  project answering `FORBIDDEN` where conductor answers `NOT_FOUND`.
- `plugin/`, the batondeck-kanban plugin for Claude Code (R3.4, ADR-0004, ADR-0005), loaded with
  `claude --plugin-dir plugin`: `.mcp.json` points the session at `http://localhost:8080/mcp` with
  `Authorization: Bearer ${BATONDECK_TOKEN}` (a CLI token from `batondeck-kanban token create`), and
  `hooks/hooks.json` registers exactly SessionStart and SessionEnd, both `hooks/session.sh`, which run
  `batondeck-kanban session register --role "${BATONDECK_ROLE:-worker}"` (with `--agent
  "$BATONDECK_AGENT"` when set) and `session unregister`, stdout sent to stderr, always exit 0. The
  `batondeck-worker` and `batondeck-chronicle` skills and the `worker`, `master`, `work`,
  `work-assigned`, `runs` and `chronicle` commands are rewritten for the five states, the fixed table
  and the socket doorbell; `scripts/mcp.sh` makes one tool call from a shell, the bearer passed to
  curl on stdin, never in its argv. `plugin/TRIM.md` has a
  keep / drop / rewrite row for each of conductor's 51 plugin files.
- `make skill-lint` (`scripts/skill-lint.py`, in `make check`): every lowercase snake_case word a plugin
  skill or command writes, in any form, is a tool in `contract/expected.json` (or a contract enum value,
  or one of the server's four prompts), every script it or `hooks.json` names is in `plugin/`, no plugin
  file but `TRIM.md` names a dropped conductor tool or a retired state, and `hooks.json` registers
  SessionStart and SessionEnd only. It also warns, without failing, about the named tools the server does
  not register yet (read from the `mcp.Tool` calls in `internal/mcp/tools/`).
- `make hook-check` (`scripts/hook-check.py`, in `make check`): runs the real binary's `session register`
  and `unregister`, and every command `plugin/hooks/hooks.json` declares (as Claude Code runs it, under
  each scenario of its event), as subprocesses against a scratch server on a Unix socket, and fails on a
  declared script that does not exist, any byte written to stdout, or a hook exit other than 0.
- The join (R1.18, ADR-0003 B3): the transports' tests run on the real service. The new
  `internal/service/servicetest` opens the real service, its store and a bus over one engine;
  `BATONDECK_TEST_ENGINE=memstore|sqlite|postgres` picks one, unset runs all three (Postgres skips
  without `BATONDECK_TEST_POSTGRES_DSN`, and a named `postgres` without one fails). Over it: every one
  of the 43 registered tools answers once on the real stack (`TestEveryToolOnTheRealStack`, which
  fails when a registered tool has no call there), the per-area schema comparison against
  `contract/expected.json`, the bearer test, `search_tasks` end to end, `GET` board's shape and the
  five `/api/stream` tests. The mapping tests that assert on servicefake's recorded calls stay on it.
- The plan's proofs through real clients over the serve composition (`internal/cli/proofs_test.go`,
  every engine): the 25 `move_task` verdicts (14 legal with 5 same-status, 11 `INVALID_TRANSITION`
  carrying `allowed` and `path`); poison by three `fail_task`, skipped by `claim_next`, refused to a
  worker and lifted by a master; the last-dependency poison refusal and its control; the eight
  `delete_task` cases; two concurrent `claim_task` with one winner (20 rounds); 50 concurrent
  `claim_next` with 50 distinct winners and their latency; a claim writing no doorbell row and a
  handoff one `assigned` row; an `/api/stream` board frame after an MCP `move_task`.
- The rest of the REST BFF's route table as stubs (R1.12): 41 more routes in `internal/api/{me,agents,
  notifications,comments,followups,sprints,runs,oncall,chronicle,analytics}.go`, each answering `404
  {"code":"NOT_FOUND","message":"<tool> lands in <lane>"}` until its lane (R2.1, R2.2, R2.3, R2.5, R2.7,
  R2.10, R2.11) replaces it, the three `me/cli-tokens` routes R2.5's over R0.12's TokenService (ADR-0004);
  `routes.json` is 80 routes, every in-scope `/api` route of conductor's
  ui-backend less the transitions pair, `me/marketplace-key`, `/api/healthz`,
  `/api/client-error` and `/api/projects/{projectId}/overview`. `TestOutOfScopeRoutesAreRefused` fails
  on a studio, admin, tenants, billing or onboarding route, or an excluded one, in the router or in
  `routes.json`, by name; `TestStubsNameTheirLane` checks every stub's answer. `web/src/api/<area>.ts`
  holds the 41 `api.ts` functions for these routes over a `req` copy in `web/src/api/http.ts`, held to
  `api.ts`'s signatures by `web/src/__tests__/api-areas.test.ts`.
- `search_tasks` (R1.9), keyword only: the project's tasks whose title, description or search tokens
  hold every term of the query, best first, at most `k` (default 10, at most 50), each with the
  engine's rank as `score`; `mode` semantic is `VALIDATION` (the plan keeps no embeddings), hybrid and
  keyword run keyword and answer `mode: "keyword"`. The query is split into letters-and-digits terms
  (`word*` a prefix) and only those reach the engine: an FTS5 match of quoted tokens ranked by how many of
  the task's own tokens it matched (FTS5 `highlight`; not `bm25`, whose IDF counts every project's
  tasks) on SQLite, a `to_tsquery('simple', …)` of quoted lexemes ranked by `ts_rank` on Postgres, each a
  bound parameter; a query with no term is `VALIDATION`. Two statements (the page, then its tasks by
  id), pinned on both engines. `store.Search` is built on SQLite, Postgres and memstore, and the
  search battery gains the index following a title and token update and a delete, scores another project's tasks cannot
  move, and no index row left by a delete under a reused row id; with search, the
  last unbuilt contract II area, `internal/store/conformance/reference_test.go` is gone.
- `batondeck-kanban session register --role worker|master [--agent LABEL]` and `session unregister`
  (R3.3), the client half of the device session directory, for the plugin's SessionStart and
  SessionEnd hooks (R3.4). They reach `POST /internal/wake-session` and `DELETE
  /internal/wake-session/{pid}` only over the first `unix://` entry of `listen`, never TCP, with the
  bearer in `BATONDECK_TOKEN`. The client process is the live Claude Code process whose
  `~/.claude/sessions/<pid>.json` names `$CLAUDE_CODE_MESSAGING_SOCKET` (`wake.SessionOf`, the
  directory's own registry parser), sent with that entry's `sessionId` and `procStart`; without the
  variable it is the parent process, registered with no socket. `CLAUDE_CODE_MESSAGING_TOKEN` is not
  sent: it is the session's child token (R3.1). Usage errors exit 2; every other failure (no Unix
  listen entry, no token, no registry entry, the server down, a refusal) is one stderr line and exit
  0, so a hook never breaks a session start; stdout stays empty, since a SessionStart hook's stdout
  becomes the model's context.
- The REST BFF core (R1.11): `internal/api` mounted under `/api` behind R0.12's session cookie and CSRF
  check, with conductor ui-backend's paths and JSON, each route one `service.*` call whose arguments
  (the body, or a GET's query typed by the schema, under the path's ids) are validated against the
  MCP tool's own input schema. 39 routes, listed with their `app.ts` lines in
  `internal/api/routes.json`, which `TestRouteTable` compares with the mounted table: the session,
  the public config and a new `POST /api/login` (R0.12's form sign-in), projects and members, boards
  and columns, tasks, the UI's lifecycle (claim, release, complete, block, handoff), context items,
  summary, memory, subtasks, dependencies, attachments, search, the board feed and `/api/stream`.
  Errors carry ui-backend's status per code (409 for the conflicts, 429 for quota and rate) and
  `{code, message, details}`.
- `GET /api/stream?projectId&boardId` (ADR-0004's web transport): `ready`, `ping` every 20 s, and per
  100 ms of a board's changes a `board` frame (ui-backend's `{board, tasks, feed}`, the board from
  `service.Feed.BoardSnapshot`) and an `updated {uri, count}` frame, fed by the bus's project
  subscription; `presence` frames from R0.25's device session directory (`service.DeviceSessions`, wired in
  `serve`), listing the caller's own live processes. The board is authorized as the caller before the stream opens
  (403 for a non-member) and at every frame. A board frame's SSE id is its change-log cursor; a
  reconnect with it (`Last-Event-ID` or `?cursor=`) replays the missed changes as one frame, and past
  5,000 change-log rows answers `resync`, so a reconnect never scans the whole retained log.
- `/healthz` names the store's engine: `batondeck-kanban <version> ok sqlite` (or `postgres`).

- The MCP prompts, readable resources and server instructions (R1.7; ADR-0004, ADR-0005). The four
  prompts `pick_up_next_task`, `triage_inbox`, `summarize_for_handoff` and `decompose_into_subtasks`
  take conductor's arguments and text, with the five states: `triage_inbox` makes each TODO task a
  self-contained brief instead of moving it to a claimable state. Five resource templates under
  `batondeck://`: `board` (service.Feed board snapshot), `task`, `task-context` (the
  `get_task_context` briefing), `board-feed` and `board-feed-cursor` (`…/feed{?cursor}`, service.Feed),
  each read through the service as the caller; conductor's six chronicle templates are not served
  (R2.6's area) and neither is `conductor://`. A failed read is a JSON-RPC error whose data is the
  BatonDeckError. The `initialize` instructions describe the board, the five states, `claim_next` and
  `get_task_context` first, and the two ways a client learns of work: a CLI on the device is rung on
  its own messaging socket and then calls `claim_next`, then `list_notifications`; a hosted client
  (claude.ai, ChatGPT) and Cursor pull each turn.
- `notifications/tools/list_changed` to a session whose caller's workspace-admin flag changed since its
  previous request (the only thing that changes a caller's `tools/list`), sent to that session alone.
- The device session directory and its socket dispatcher (R0.25, ADR-0004 Transport 1), `internal/wake`.
  A CLI client process registers with `POST /internal/wake-session {pid, role, agent, socket, token,
  sessionId, procStart, cursor}` over the server's 0600 Unix socket only (TCP 404): the pid must be
  alive and owned by the peer's uid (`LOCAL_PEERPID`/`LOCAL_PEERCRED` on darwin, `SO_PEERCRED` on
  Linux, `core.PeerOf`), and a socket must be the one the pid's Claude Code registry entry
  (`~/.claude/sessions/<pid>.json`, `$CLAUDE_CONFIG_DIR/sessions`) names. `DELETE
  /internal/wake-session/{pid}` unregisters. An entry is dropped the moment its process exits
  (`core.ExitWatcher`: kqueue `EVFILT_PROC NOTE_EXIT` on darwin, `pidfd_open` + poll on Linux), on a
  delivery that finds the socket gone (ENOENT, ECONNREFUSED), the registry entry gone or its
  procStart moved, and on a revoked bearer or disabled account at a delivery or the 60 s sweep (3
  statements per entry); a membership change updates the entry's scopes in place at the sweep (owner
  ruling 2026-10-23). The dispatcher is one `bus.Tap`: workers ring `assigned` on their identity or
  agent label and `broadcast` on their projects; masters ring `changed` on their projects' rows except
  claims, heartbeats, context writes and their own (the change payload gains an internal `actor`).
  Coalesced per entry and kind, `assigned` at 300 ms quiet with a 1 s cap, `broadcast` and `changed`
  at 5 s. The doorbell is R3.1's frame, one connection per doorbell: an optional
  `{"type":"auth","token":<peer token from the registry's key file>}` line, then
  `{"type":"user","session_id":<registry sessionId>,"message":{"role":"user","content":"<one plain
  line>\n{\"kind\",\"cursor\",\"projectId\"}"}}`; the cursor is the lower of the row's id and the
  bus's frontier (`Bus.Frontier`), recorded as `last_cursor`. At start-up every row is re-validated
  (pid, uid, registry, bearer) and caught up from its last delivered cursor, or dropped. The directory
  is exposed to the service as `service.DeviceSessions` (`Core.DeviceSessions`, entries plus a change
  signal) for R1.11's presence frames.
- Migration `0005_wake_session_directory` on both engines (zone Z2, owner-approved 2026-10-23):
  `wake_sessions` gains `pid`, `role` (CHECK master|worker), `client_session_id`, `proc_start` and
  `bearer_sealed` (the keyring, AAD `wake_sessions.bearer:<id>`; without a keyring registration is
  refused), and a partial unique index on `pid`. No row is deleted. The store's wake-session area adds
  `ListWakeSessions` and `SetWakeSessionCursor`, with conformance cases on every backend;
  `internal/store/wake_directory_migration_test.go` runs up, down and up on both engines.
- The ten context MCP tools (R1.6): `add_context_item`, `get_task_context`, `set_summary`,
  `write_memory`, `read_memory`, `recall_memory`, `add_dependency`, `remove_dependency`,
  `add_subtask` and `list_subtasks`, registered in `internal/mcp/tools/context.go`, each one
  `service.Context` call (R1.17's semantics), wired in `serve`'s composition. Their input and output schemas equal
  `contract/expected.json`; each description is at most 200 characters. `get_task_context`'s
  `resume` is always `null` until the resume scan is ported (P-117), and its description says so
  and that `null` is not permission to start a fresh branch, where conductor's text says it is.
- The six task tools (R1.2): `create_task`, `get_task`, `update_task`, `move_task`, `list_tasks` and
  `delete_task`, registered in `internal/mcp/tools/tasks.go`, each one `service.Tasks` call; `serve`
  wires `Tasks` into the MCP services. Their schemas equal `contract/expected.json`; `create_task` takes
  no `status`; `delete_task`, `create_task`, `update_task` and `move_task` are master tier, `get_task`
  and `list_tasks` read tier, as `internal/domain/grants.go` classifies them.
- The eleven lifecycle MCP tools (R1.4): `claim_task`, `claim_next`, `heartbeat_task`, `release_task`,
  `next_task`, `complete_task`, `block_task`, `fail_task`, `reap_stale_leases`, `handoff_task` and
  `cancel_task`, registered in `internal/mcp/tools/lifecycle.go`, each one `service.Lifecycle` call
  (R1.5's semantics), wired in `serve`'s composition. Their input and output schemas equal
  `contract/expected.json`; each description is at most 200 characters. `fail_task`'s `retryable`
  reaches the service unset when absent (read as true) and is `VALIDATION` when null. The authz
  matrix pins each tool's tier and where it lands its task: claims IN_PROGRESS; release, handoff
  TODO; fail, reap TODO or BLOCKED; block BLOCKED; complete REVIEW or DONE by the project's review;
  cancel DONE; `next_task` grant-exempt.
- Attachments on local disk behind HMAC-signed URLs (R1.8). `attach_file` creates a pending
  attachment (`attachment.created`, the task's version +1) and answers an `uploadUrl`,
  `PUT /blobs/{projectId.taskId.attachmentId}?exp=…&sig=…`, valid 15 minutes; the PUT's body is read
  to `blob.dir/tmp` while hashed, moved to `blob.dir/<2 hex>/<sha256>`, and the blob row, the
  attachment `ready` and `attachment.updated` written in one mutation (version +1 again).
  `list_attachments` answers each ready row with a `downloadUrl`, `GET /blobs/{sha256}?exp=…&sig=…`,
  served as `application/octet-stream`, `Content-Disposition: attachment`, `nosniff`. The signature
  is HMAC-SHA256 over (purpose, expiry, id) under a key `core.Keyring.DeriveKey` derives (HKDF) from
  the master key; an upload signature does not download and a download signature does not upload.
  The routes sit outside every bearer, session and CSRF check; an expired, wrong-purpose or tampered
  signature is 403 with a log line naming the reason, a body over `blob.max_bytes` 413 (by
  Content-Length and by bytes read, nothing left on disk), a second upload 409, a second PUT while
  one is still reading the same id's body 409, a body still arriving at the URL's expiry 403 (the
  connection's read deadline is the expiry), a body not the declared size 400. New config: `blob.dir` (`BATONDECK_BLOB_DIR`, default `blobs` beside the SQLite
  file) and `blob.max_bytes` (`BATONDECK_BLOB_MAX_BYTES`, default 50 MiB). Without a keyring
  `attach_file` is INTERNAL and `downloadUrl` null, said once at boot. Both engines and memstore build
  the `Attachments` and `Blobs` store areas; delete_task's blob hook now removes each freed blob's row
  and file unless another attachment or memory entry holds it, and the retention sweep gains a step
  handing the blobs unreferenced for an hour to the same collector (`serve` runs the sweep since
  P-109).

- The R3.1 socket-wake spike findings (`docs/release/socket-wake-spike-2026-10.md`) and its scratch
  writer (`docs/release/spikes/socket-wake/doorbell.py`), measured on Claude Code 2.1.309: a
  newline-delimited `{"type":"user",…}` frame written to a session's `CLAUDE_CODE_MESSAGING_SOCKET`
  starts a turn in an idle session (8–105 ms to dequeue) and is queued between tool calls in a busy
  one; the auth line is optional on macOS; `crossSessionInbound` `hold`/`refuse` start no turn. No
  product code.
- The bus's project subscription and all-rows tap (R0.24, ADR-0004 B). `bus.SubscribeProjects(ctx,
  projectIDs, buffer)` receives every change-log row whose stored `ProjectID` is one of the bare ids
  (`"P-1"`), whatever its token scope: a `handoff_task` row scoped to its new assignee reaches it,
  which no recipient subscription but the assignee's did. `bus.Tap(ctx, buffer)` receives every row
  in cursor order. Their catch-ups are `bus.ReplayProjects` (pages `ReadChanges` and keeps the
  projects' rows: one statement per 1000 rows read, no `(project_id, id)` index yet) and
  `bus.ReplayAll` (one `ReadChanges`). Both are in-memory indexes the tail consults per row, so an
  idle tail still issues 0 statements, and each drops for itself on a full buffer.
  `Subscribe(["project:<id>"])` is unchanged: that string is the broadcast token scope
  (`service.BroadcastScope`), so `/wake` hears no other identity's assigned row.

- The lifecycle service (R1.5): `Core.Lifecycle()` implements `service.Lifecycle` in
  `internal/service/lifecycle.go`, every write through `service.mutate`. `Claim` and `ClaimNext`
  (one `store.Claims.ClaimNext` statement; 4 read statements cold, 2 with the board snapshot cached,
  `TestClaimNextAndNextStatementPins`) ring nobody, and neither does a self-assignment (owner ruling
  2026-10-23; `txn.RingFor` and `txn.RingAssigned` drop it); an empty frontier writes no
  row and reaps the board's expired leases (lazy recovery, no sweeper). A lost claim race is
  re-judged (`retryStale`): two concurrent claims give one lease and one `CONFLICT_LOCKED` on
  memstore, SQLite and Postgres. `Heartbeat` moves the lease without bumping the version. The
  lease-guarded outcomes (`Release`, `Complete`, `Block`, `Fail`, `Handoff`) check the caller's live
  lease (any mismatch is `LEASE_EXPIRED`) and land the task in one versioned write. `Complete` lands
  REVIEW (the reviewer assigned and rung `assigned`, conductor `lifecycle.ts:935`) or DONE, which runs the poison-aware unblock of every
  dependent under the project lock; `Fail` and `Reap` apply `domain.ApplyFailure` (READY with a
  backoff, or BLOCKED `poison` with the `poisoned` label, never a third state); `Block` writes its
  `blockedBy` edges in the same transaction under the project lock and refuses the reason `poison`;
  `Cancel` (ADR-0001) lands DONE with the `cancelled` label and the reason as a note, for a master
  or the lease holder. `Next` previews the frontier (2 statements, 3 when empty) and its inbox splits
  BLOCKED into `blocked` and `poisoned`. Refused as VALIDATION rather than ignored: `shard` lanes,
  `sprintId`, `maxConcurrency`, `strategy: score`, `explain` and `artifacts`. Store:
  `StatusChange` gains `Assignee`, `Deliverable` and `Review` (the `WriteTaskState` statement writes
  them; no migration), and `AddEdge` takes the project lock itself, so block_task cannot reopen
  P-110 (`TestConcurrentBlockClosesNoCycle` on Postgres).
- The MCP tool registry and the first thirteen tools (R1.1). `internal/mcp/tools` holds one file per
  area, each registering through its `Register<Area>` hook; `registry.go` pre-lists every area a plan
  task names a tools file for, with the task that writes it, so a lane adds a file. `mcp.Tool`
  (`internal/mcp/middleware.go`) registers a tool as one service call, its input and output schemas inferred from the service DTOs
  by `internal/service/dtoschema` (the recipe moved out of `contract/dto_test.go`, which now compares
  exactly what the server serves), and refuses a tool with no `domain.ToolPosture`; `Server.Manifest`
  lists what is registered, by area, with its posture. Arguments are checked by go-sdk against that
  schema before the service runs: an unknown key, a wrong type, a value outside an enum, a null sent
  to an `Optional` field and a number its integer cannot hold are all `VALIDATION`. Registered and
  served by `serve`: `create_project`, `get_project`, `list_projects`, `update_settings`,
  `delete_project`, `add_member`, `remove_member`, `create_board`, `list_boards`, `get_board`,
  `add_column`, `update_column`, `remove_column`. Each records one `tool_calls` row. A tool's own
  output failing its output schema is now `isError` INTERNAL (logged) rather than a JSON-RPC error.
- The tasks service and the `delete_task` cascade (R1.3): `Core.Tasks()` implements `service.Tasks`
  in `internal/service/{tasks,delete}.go`, every mutation through `service.mutate`. `Create` lands
  in `columnId` or the board's first column with its status, takes the project's next `T-n`,
  replays on `idempotencyKey` and rings `broadcast` on a READY landing; `Get` is 2 statements,
  `List` 1 (status, assignee and label in the store's filter; `unblockedOnly`, `missingArtifacts`,
  `sprintId` and `updatedSince` post-filtered over the page as conductor's `taskPostFilter`).
  `Update` is version-checked with tri-state `assignee`, `dueAt`, `reviewer`, `estimate` and
  `modelHint`. `Move` runs `domain.DecideMove` (the fixed table, 17 legal of 36; promote-with-
  dependencies to BLOCKED; WIP counted by status in the transaction under the project lock, which
  a move into IN_PROGRESS or onto DONE takes first: without it two concurrent moves both took a
  wipLimit-1 slot in 40 of 40 Postgres runs, P-146), refuses a racing task and
  another identity's live lease (CONFLICT_LOCKED), un-poisons through `SetStatus(Unpoison)` when
  it leaves a poisoned BLOCKED (master: `move_task`'s tier), and on DONE unblocks the dependents
  through `txn.unblockDependentsOf`. `Delete` (master) runs `txn.deleteCascade` under the project
  lock: the subtasks and runs recursively, each dependent's `blockedBy` stripped and unblocked
  before the store drops the edges (poison-aware), each blocker's `blocks` stripped, the parent's
  `blockedBy`, a racing parent whose last run went back to READY with `racing:false`, task memory
  and attachments, then one board-level `task.deleted {id, title, status}`, an audit row and a
  change-log row per deleted task; `Options.BlobGC` is called after the commit with the freed blob
  hashes (none until R1.8). memstore gains the attachments area (R0.19's battery: 3 pass, 1 skip
  on `PutBlob`, unbuilt).

- The `/wake` doorbell, the Unix socket listener and `/internal/wake-session` (R0.15).
  `GET /wake[?since=<cursor>]` (bearer required; 401 with `WWW-Authenticate: Bearer` without one, 400
  for a cursor that is not one non-negative integer) is an SSE stream of the caller's doorbells:
  `id: <cursor>` and `data: {"agent","kind":"assigned|broadcast","taskId","projectId","boardId","cursor"}`,
  the cursor being the change-log id. Its scopes are the caller's token scope plus
  `project:<id>` of every project it is a member of (`service.Core.WakeScopes`, one `ListProjects`
  at connect); `changed` rows ring nothing. With `since`, every doorbell after the cursor is replayed
  from the log through `bus.Replay`, oldest first and uncoalesced, before the stream goes live; live
  doorbells of one kind are one frame carrying the highest cursor once that kind has been quiet for
  300 ms (the bus's 250 ms read interval plus 50 ms), at most 1 s after the first, so five doorbells
  in 4 ms are one frame. Every 60 s an open stream, in its catch-up as in its live part,
  re-resolves its bearer (the account read from the store, not the 60 s cache) and re-reads its
  scopes: a revoked or expired token or a disabled user ends it with `: reauthenticate`, a changed
  membership (added or removed) with `: scopes changed`, and a recheck that cannot read either with
  `: recheck failed`, each followed by `, reconnect with since=<cursor>`; a disabled user's stream
  therefore ends within 60 s of `user disable`. Every write has a deadline (40 s), so a peer that
  stops reading is cut off. Comments: `: ok` when subscribed, `: keepalive` every 20 s, and
  `: dropped, reconnect with since=<cursor>` before ending a stream whose subscription overflowed.
  Cost: 3 statements per open stream per 60 s (GetToken or GetOAuthToken, GetUser, ListProjects;
  `TestWakeRecheckKeepsAValidStreamAtThreeStatements`)
  and 0 in between. Measured on the real binary over Postgres: a row's frame 294 ms after its
  commit; an idle stream issued 0 statements in 45 s (`log_statement=all`).
  `listen` is now a list of `tcp://host:port` and `unix:///abs/path` entries (default
  `["tcp://127.0.0.1:8080"]`; `BATONDECK_LISTEN` comma-separated; anything else is refused by name,
  `listen_entries_are_tcp_or_unix_urls`; the Dockerfile sets `tcp://:8080`), every entry serving the
  same handler. A Unix socket is made mode 0600 in a 0700 directory, a stale socket file is replaced
  and a live one or any other file refused, and every accepted connection's peer uid is checked
  (`SO_PEERCRED` on Linux, `LOCAL_PEERCRED` on macOS via `golang.org/x/sys/unix`, now a direct
  requirement at the version already linked): another uid is closed before HTTP reads it, and logged.
  `healthcheck` probes the first entry, a socket by its path.
  `POST /internal/wake-session {socket, token}` registers a Claude Code session inbox for the
  bearer's identity (201 `{id, expiresAt}`); it is served only on a Unix socket. Over TCP every
  path under `/internal/` is 404 whatever its method, so the route answers there exactly as a
  missing one under `/internal/` does. A socket directory that is a symlink, is owned by another
  uid, or is writable by group or other is refused by name.
  The inbox token is stored sealed with the keyring (`RegisterWakeSessionReq.SocketToken`,
  `service.Options.Sealer`, AAD `wake_sessions.socket_token:<id>`), never as given. The key chain:
  `BATONDECK_MASTER_KEY`, else `master.key` (generated 0600 on first run) beside the SQLite file,
  or on Postgres beside the `-config` file; on Postgres with neither, serve starts without a keyring,
  logs one WARN line, and refuses a token (500 INTERNAL naming `BATONDECK_MASTER_KEY`). Losing
  `master.key` makes the sealed tokens unreadable. `serve` now starts the bus (closed before the HTTP
  drain, so open streams do not hold the stop).
- The OAuth 2.1 authorization server for MCP remote connectors (R0.13), `internal/auth/oauth`, mounted
  by `serve` when `public_url` (`BATONDECK_PUBLIC_URL`) is set, every URL built from it:
  `GET /.well-known/oauth-authorization-server` (RFC 8414, no `jwks_uri`, PKCE `S256` only, token
  endpoint auth `none` only), `GET /.well-known/oauth-protected-resource` and
  `/.well-known/oauth-protected-resource/mcp` (RFC 9728, resource `<public_url>/mcp`),
  `POST /register` (RFC 7591 public clients; 20 per client address per hour, then 429 with
  `Retry-After`; each call first deletes registrations no authorization used within 24 h),
  `GET /authorize` (the sign-in page, then the consent page), `POST /authorize` (the consent
  decision, under `Authenticate` and `CSRF`), `POST /login` (R0.12's `LoginHandler`, which now
  takes a local-path `next` and answers 303 there, or back with `login=failed`) and `POST /token`
  (`authorization_code` and `refresh_token`). The one departure from conductor's `as.ts`: tokens are
  opaque (`bd_at_`/`bd_rt_`/`bd_ac_` + 32 random bytes), stored only as SHA-256 in `oauth_codes` and
  `oauth_tokens`, and `/mcp` resolves them through `auth.Service.ResolveCaller` beside API tokens.
  Hardening: `redirect_uri` matched exactly against the registration (conductor also took a loopback
  URI on any port); a code is used once, and a second exchange revokes what the first issued; refresh
  tokens rotate, and presenting a rotated one revokes its whole family; RFC 8707 `resource` is bound
  into the code and the tokens, and a token for another resource does not resolve; `/mcp`'s 401 says
  `WWW-Authenticate: Bearer resource_metadata="<public_url>/.well-known/oauth-protected-resource"`
  (`mcp.Options.ResourceMetadataURL`). Scope values are the five role names: consent grants one, a
  ceiling on the member's project role (`Caller.Role`); only `admin` keeps the workspace-admin flag.
  Access tokens live 1 h, refresh tokens 14 d, codes 10 min. `store.OAuth`
  (`internal/store/contract_oauth.go`) on SQLite, Postgres and memstore, judged by the new `oauth`
  conformance battery; no migration (the tables are 0001's).
- The context service (R1.17): `Core.Context()` implements `service.Context` over the store in
  `internal/service/{context,memory,dependencies,subtasks}.go`, every mutation through
  `service.mutate`. `GetTaskContext` composes the task, its items, the caller's task memory, the
  summary, both sides' edges, the fields and `workflow.allowedMoves` (the fixed table's row) with no
  `id`, `title` or `description`: 6 statements once the membership and the board snapshot are
  cached, 8 with a cold snapshot (`store.GetBoard`'s 2 for the lanes)
  (`TestGetTaskContextCostsAtMostSixStatements`); the snapshot cache serves only once the bus feeds
  `ObserveChange` (P-100), so until then every call is 8. `RecallMemory` is an exact key or the newest `k`,
  the expired dropped; naming a project needs membership in it on every branch, and `agent_global`
  is the caller's. `WriteMemory`'s `ttl` is the expiry instant (RFC 3339), refused as VALIDATION
  when it does not parse. `AddDependency` writes the edge and both caches and bumps each task once,
  never a status; `RemoveDependency` runs `domain.DecideUnblock` and moves a non-poisoned dependent
  whose last blocker went to READY with one `task.dependency_cleared` (a poisoned one stays BLOCKED);
  `txn.unblockDependent` is the auto-unblock entry R1.5 and R1.3 call. `AddSubtask` creates the
  child in the first column with `parentTaskId` and leaves the parent untouched; it lands at the
  column's head until the store has a last-key read (P-69). A new store primitive,
  `store.Locks.LockProject` (a transaction-scoped Postgres advisory lock; a no-op inside SQLite's and
  memstore's transactions, INTERNAL outside one), is taken first in `mutate` when
  `mutation.LockProject` is set, which add_dependency and remove_dependency set: two concurrent
  calls on disjoint pairs closed a 4-edge cycle on Postgres without it (the R0.10 review's P-110,
  `TestConcurrentAddDependencyOnDisjointPairsClosesNoCycle` on memstore, SQLite and Postgres).
- The change-log bus, audit rows, the tool_calls writer and the retention sweep (R0.11).
  `internal/bus`: `bus.New(store.ChangeLog, bus.Options{Watcher, PollInterval, GapTimeout, OnError})`,
  `Subscribe(ctx, scopes []string, buffer) (*Subscription, error)` (a subscriber holds several token
  scopes; `C()` carries `store.Change` as-is, its id the cursor; `Dropped()` counts what a full
  buffer refused), `Replay(ctx, scopes, since, limit)`, `Running()` and `Close()`. The tail runs
  only while a subscriber exists, reads the log at most once per 250 ms, and with an engine wake
  (`store.ChangeWatcher`, new in `internal/store/watch.go`: Postgres `LISTEN batondeck_changes` on
  one pooled connection, `AppendChange` now sending `pg_notify` in its transaction; SQLite an
  in-process signal fired after the commit of a transaction that appended a change) it reads only
  after a wake: 200 subscribers held 10 s with no write issue 0 statements on both engines, and one
  write reaches all 200 in 2.6 ms (SQLite) and 5.9 ms (Postgres) (`TestIdle`). Delivery never
  blocks: a subscriber that never reads (buffer 8) drops 992 of 1,000 changes while a reading one
  gets all 1,000. On Postgres ids follow appends, not commits, so the tail keeps a frontier below
  which every id was delivered and re-reads from it; an id missing for `GapTimeout` (10 s) from when
  the frontier stopped at it — each hole on its own clock — is taken for a rolled-back transaction
  (`TestPostgresDeliversChangesCommittedOutOfIDOrder`,
  `TestPostgresPassesAnIDARolledBackTransactionTook`,
  `TestPostgresDeliversAnInFlightIDAfterAnEarlierHoleFills`); a passed id is counted per id
  (`GapsPassed`) and its range reported (`ErrGapPassed`), and a stalled frontier moves on the poll's
  tick with no write. The
  tail's start is no watermark either: the last page's changes of the past `GapTimeout` are held as
  seen and the frontier stops below them, so a change in flight when the first subscriber arrives
  still arrives (`TestAChangeInFlightWhenTheTailStartsIsDelivered`,
  `TestPostgresDeliversAChangeInFlightWhenTheTailStarts`). It looks back one page (500 changes): when
  all of them are that recent, an id in flight below the page is not waited for, and `OnError` hears
  `bus.ErrSeedWindowFull` (`TestASeedWindowAllRecentIsReported`). Processes sharing a Postgres
  database need clocks within `GapTimeout` of each other, since a change's `Ts` is its writer's
  clock. `Subscribe` racing `Close` returns
  `bus.ErrClosed`. `AppendChange` is pinned at 1 statement on SQLite and 2 on Postgres
  (`TestAppendChangeStatements`). The SQLite engine supports exactly one `serve` process per data
  dir: its wake is in-process, so a second process's writes are not heard; run several on
  Postgres. `store.Audit` and `store.ToolCalls` are built
  on SQLite and Postgres (`internal/store/{audit,toolcalls}.go` over
  `queries/{sqlite,postgres}/{audit,toolcalls}.sql`; the eight stubs are gone) and their batteries
  pass on both. `internal/retention`: a `Sweeper` deleting memory past its own expiry, change-log
  rows past `retention.change_log_days` (7) and tool_calls rows past `retention.tool_calls_days`
  (45) — new config keys, env `BATONDECK_RETENTION_CHANGE_LOG_DAYS` and
  `BATONDECK_RETENTION_TOOL_CALLS_DAYS` — 500 rows a statement and at most 50,000 a table a sweep,
  hourly; task events are kept. Deliveries and raw webhook bodies are not swept yet (no Store
  method). `internal/store/storetest` opens migrated engine stores for tests above the store.
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
  `setTransitions`. `api.ts` is kept whole. The brand is BatonDeck's own logo, copied from the
  BatonDeck product and pinned in `web/brand.lock.json`, in place of the generated logo artwork. The
  build is committed in `web/dist`, embedded in the binary and served at `/`. `make check` gains
  `web-public-check` (refuses `web/public/pitch/` and `web/ds-bundle/` anywhere under `web/`, and
  holds `web/public/` to `scripts/web-public-allowlist.txt`) and `brand-check` (every logo file
  against its sha256 in the lock, and no other file in `web/public/brand/`).
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

- The on-call setup's "Read the guide" and "Wiring guide" buttons (P-247). They linked to
  `/docs/<page>.html`, which this binary does not serve (the SPA shell answered), and the
  repository ships no on-call guide; the connector cards keep their own copy. The server still
  sends `docsPage` in `GET /api/oncall/sources`.
- Four on-call settings nothing read (P-310, owner decision 6, audit D5): the integration
  `policy` input (`autoReviewCycle`, `openPrForReview`, `reviewerAgent`) and
  `routing.fallbackBroadcast`. `create_integration` and `update_integration`, over MCP and REST,
  refuse them as unexpected properties (`contract/deltas.json` records the four removed props,
  each with its reason), and the integration form no longer shows the four toggles. A row stored
  with them loads, lists, saves and routes with no migration: the keys are ignored, a new routing
  replaces the stored one whole, and the stored `policy` object is still echoed as stored.

- MCP resource subscriptions (ADR-0004): `initialize` no longer advertises `resources.subscribe`, the
  subscribe and unsubscribe handlers are gone, and `resources/subscribe` is answered method not found
  (-32601).
- `GET /wake` (R0.15's SSE doorbell) and its TCP mount (ADR-0004; plan open question 2, default
  removed): it answers 404 over TCP and the Unix socket, rather than the SPA shell. Its catch-up and
  coalescer are the dispatcher's parts. `service.Sessions.RegisterWakeSession` now registers a
  directory entry, and `internal/api`'s `WakeSessionHandler` moved to `internal/wake`.

- The contract trim (R0.23, ADR-0004; the owner, 2026-10-23: "go with trimmed version"):
  `contract/expected.json` goes from 93 tools to 86. `deltas.json` `removedTools` gains the router
  facade (`find_tools`, `get_tool_schema`, `invoke_tool`), the CLI-token tools (`create_cli_token`,
  `list_cli_tokens`, `revoke_cli_token`; the `token` subcommand and the REST settings page cover
  them) and `prune_agent_sessions` (sessions are collected after 30 minutes idle), and `removedProps`
  gains `disconnect_agent`'s `wakeRevoked`; `wakeRevokeFailed` leaves with `prune_agent_sessions`.
  The 7 leave `internal/domain`'s posture table and the audit catalogue with them.
  `internal/mcp/description_test.go` fails on any registered tool description over 200 characters,
  or carrying conductor-only text, naming the tool and its length. The plan of record says 86, that
  `add_subtask` leaves the parent alone, and that `tools/list` is memoised per workspace-admin flag.
- The GitHub Actions `check` workflow (`.github/workflows/check.yml`), by the owner's ruling
  (ADR-0002): the local hooks are the gate. `pre-commit` runs gofmt, the 5 MiB cap and the INDEX
  secret scan; `pre-push` runs `make check` on both engines and refuses the push without Docker. A
  pull request now reports no status check; the reviewer re-runs `make check` on its head.

### Fixed

- **The web UI carries BatonDeck's own logo.** The logo, the favicon, the app icon and the loading
  mark were the HDTP gateway's bridge mark from a shared brand kit, with the BatonDeck name beside
  it. They are now BatonDeck's own mark and wordmark, in light and dark cuts, copied from the
  BatonDeck product and pinned by `make brand-check`.

From the pre-public review (2026-11-10), in which every subsystem was read by a reviewer, each
finding was checked against the code, and each code defect was shown by a failing test before its
fix:

- **A worker can no longer create boards, add columns or delete Chronicle pages.** `create_board`,
  `add_column` and `delete_chronicle_page` now need master. A worker could delete a record with all
  its versions, or add a column whose low order and WIP limit took every claim and then refused it,
  and could not undo either. In the web UI, "+ New board", "+ Add Column" and a record's Delete are
  closed below master and say why.
- **The consent role caps the tools that take no project.** A viewer OAuth grant could call
  `create_project`, `set_my_settings`, `mark_notifications_read` and `disconnect_agent`, because the
  cap applied only to tools with a `projectId`. A token that carries a role now needs at least
  worker for those writes; reads and API tokens are unchanged.
- **An OAuth access token no longer opens the REST API.** The web UI's `/api` routes took a
  connector's access token, which is issued for `/mcp` alone and capped at the role chosen on the
  consent page. A viewer or worker grant could call `POST /api/me/cli-tokens` to mint itself an
  uncapped, year-long API token with the user's full roles and workspace-admin rights, and could
  list or revoke the user's tokens. Such a bearer now gets 401 on every `/api` route; `/mcp` still
  takes it.
- **A client's chosen name no longer passes for another agent on a lease.** `cancel_task` let a
  viewer OAuth grant act as the lease holder when its client's name matched the claim's agent. A
  client whose OAuth `client_name` or MCP `clientInfo.name` equalled one of the user's API-token
  labels held that token's leases: `claim_task` handed it the token's own `leaseId`, and
  `cancel_task`, `move_task`, `delete_task` and `start_runs` let it through. The holder pass now
  needs a role that could have claimed, a live token label belongs to that token alone, and such a
  re-claim answers `CONFLICT_LOCKED` and leaves the token's lease as it was.
- **A client named `id:<identity>` no longer borrows another identity's agent id.** An MCP
  `clientInfo.name` or OAuth `client_name` of that shape was stamped as the agent on claims, feed
  events, reviews and artifacts. It now names no agent, as a token label of that shape already did.
- **A disabled user's OAuth grant is no longer refreshed.** `POST /token` rotated a disabled user's
  refresh token and exchanged a code issued before the disable, which moved the grant's 14 days
  forward for as long as the account stayed disabled. It now answers `invalid_grant` and leaves the
  refresh token unspent.
- **OAuth registration refuses look-alike redirect URIs and client names.** A redirect URI with a
  user name (`https://claude.ai@evil.example/cb`) registered, and the consent page showed it as
  returning to `https://claude.ai@…`; a `client_name` with a bidi override or zero-width character
  could make the page show another name. `/register` now refuses both, `/authorize` refuses a stored
  registration whose redirect URI carries a user name, and the sign-in and consent pages drop those
  characters from a name stored before the rule.
- **Client registration limits hold.** `/register` counted each IPv6 address on its own, so one /64
  could register without limit, and at 10,000 addresses it forgot every count. It now counts an IPv6
  client by its /64, as sign-in does, keeps its counts when its table is full, and allows at most
  500 registrations an hour from all addresses together.
- **Another site cannot sign a browser in with an `Origin: null` form.** On a plain-http board,
  where browsers send no Fetch Metadata, a cross-site form posted from a no-referrer page signed
  visitors in to the attacker's account. A sign-in with `Origin: null` is now refused, and the app's
  pages send `Referrer-Policy: same-origin`, so their own POSTs carry their Origin and other sites
  still get no Referer.
- **An https `public_url` makes the sign-in cookies Secure.** Behind a TLS proxy that sends no
  `X-Forwarded-Proto` (nginx's bare `proxy_pass`), the session cookie went out without Secure, and a
  chain's `https, http` was not read as https. Both cookies are now Secure with an https
  `public_url`, and a browser whose Origin is that URL passes the CSRF check, also when `public_url`
  writes out port 443 or 80. The README names the headers the proxy should send, and the "cross-site
  request refused" that a proxy rewriting `Host` causes.
- **A password change ends a sign-in that was mid-check.** A sign-in with the old password whose
  check overlapped `user passwd` or `user disable` kept a 7-day session. That session is now ended
  and the sign-in refused.
- **Sign-in limits count only checked passwords, and the OAuth sign-in page says when they apply.**
  When both password-check slots were busy, a refused sign-in still used one of the address's 10
  attempts a minute, so retries could lock a user out although no password was checked; such a
  refusal now costs nothing. Past the limits, the OAuth sign-in page said the login and password did
  not match, and now says there were too many attempts and to wait a minute.
- **A user can change their own password.** A teammate added under Settings → Users could never
  replace the generated password the admin saw. Settings → Your password now takes the current
  password and a new one of at least 12 characters, signs out the user's other sessions, keeps this
  browser signed in, and says that CLI tokens and agents keep working.
- **A CLI-token `ttlSeconds` over a year is refused.** A huge value wrapped around when converted to
  nanoseconds, and `POST /api/me/cli-tokens` answered 200 with a one-minute or 30-day token.
  Anything over 365 days is now `VALIDATION`.
- **The audit log records the sign-in and OAuth events it was built for.** The served binary dropped
  CSRF refusals, client registrations, consents granted or denied, and grants revoked after a
  replayed code or refresh token, because nothing connected them to the log. They are now audit
  rows. Refusals are recorded at most 30 a minute for each account, and 30 a minute for all requests
  with no credential together. A registration's row names the client and the hosts of its redirect
  URIs, and holds at most 1 KB.
- **Account changes and Chronicle page deletes are audited.** Adding an account under Settings →
  Users, and the host's `user add`, `disable`, `enable` and `passwd`, wrote no audit row; they now
  write `user.added`, `user.disabled`, `user.enabled` and `user.password_changed`, naming the admin
  or `host` and the account, and an add whose row cannot be written leaves the account disabled. A
  deleted Chronicle page keeps no version, and only its project's event feed noted the delete;
  `delete_chronicle_page` now also writes a `chronicle.page_deleted` audit row, as `delete_task`
  does. A change to `checkpointPolicy`, `tokenPolicy` or `maxTicketTokens` recorded
  `settings.updated` with no data; the row now records them.
- **`list_audit_log` pages.** Only the newest 500 rows of a filter could be read through the tool
  and `GET /api/audit`, and only the newest 100 in the web UI. Both now take a `cursor` and answer
  `nextCursor`, and the Workspace Log's audit panel offers Load more.
- **A flood of wrong passwords no longer locks people out of signing in.** Every check waited in one
  queue for the 2 password-check slots, so a steady stream of bad sign-ins from many IPv6 addresses
  (thousands of /64s in one /48) kept every correct sign-in waiting until it was refused after 3 s.
  Now a freed slot goes first to a login that signed in from that address in the last 30 days, and a
  flood from the same /48 does not count against that login. Each IPv6 /48 also has its own budget
  of 50 failed attempts a minute, on top of each /64's 10. The record of where people signed in from
  is kept in memory only, so a restart forgets it.
- **A long project id can no longer exhaust the server's memory.** Any token could name a
  `projectId` of any length, and each one was cached for a minute with no cap. A `projectId` longer
  than 64 bytes is now refused as `VALIDATION` before any lookup, and the access cache holds at most
  10,000 entries.
- **A member added by login before the account exists no longer gets stuck or stands in for the last
  admin.** Once the account existed, Remove on the login's row removed the account's own membership
  instead, adding the login again left two rows, and an admin row kept for a login let the last real
  admin leave a project nobody could then manage. Now the row is removed by its own key, adding the
  login or the account id again folds the row into the account's (its grant included, named in the
  audit log), and the last account admin cannot leave or step down while only such a row stays
  admin. Settings marks a member row with no account "no account · grants nothing".
- **Two admins removing or demoting each other at once on Postgres leave one admin.** `add_member`
  and `remove_member` checked the admin count and then wrote without the project lock, so both calls
  could pass. They now take the project lock, and the second call is refused as the last admin.
- **Membership, role and grant changes hold in every serve process once the change reaches it.**
  Only the process that made the change dropped its cached access, so another process on the same
  Postgres kept admitting a removed or narrowed identity for up to 60 s. Every process now drops the
  project's cached access when the change reaches it over the bus, and a change the bus missed
  clears the cache.
- **Capability grants judge what a call writes.** `update_task` could raise a task above a grant's
  `maxPriority` or relabel it outside its labels, and `add_subtask` and `orchestrate_subtasks` could
  create children above the cap, while `create_task` within the cap was always refused. Every
  written priority, and the labels an update writes, are now checked. Under a boards grant
  `detect_anomalies` is judged on the board it scans, and under a statuses grant `pick_run` on where
  it lands; both were always refused before.
- **Malformed calls from a non-member no longer count in a project's operations summary.** A call
  naming another team's project with an unknown key, a wrong type or a NUL character was refused
  before membership was checked, but still counted in that project's summary under an agent name the
  caller chose. Calls refused before the service now count in no project.
- **Columns keep every card placeable, and claim_next serves the oldest task first.** Claims,
  completions and other lifecycle moves carried a task's old order key into its new column, new
  tasks reused the key the last claimed task had taken away, and tasks landing in one column at once
  on Postgres could all take one key, so a card dropped between two equal-keyed cards snapped back.
  New tasks, subtasks and on-call alerts, and tasks moved into a column by `move_task`, landed at
  its top, so `claim_next` and `next_task`'s fifo always took the newest task and older ones could
  wait for ever. Now a new or moved task lands at the end of its column and a lifecycle move at the
  head of the column it enters, each with a key of its own, so concurrent landings get different
  keys and the column is in arrival order. Boards that already exist keep their order. The board
  says why a drop between two cards that still share a position cannot be placed, and `move_task`
  refuses an order key over 256 characters or with a character outside 0-9, A-Z and a-z as
  `VALIDATION`.
- **A reopened task is no longer counted as cancelled.** A cancelled task reopened by `move_task` or
  `add_follow_up {reopen}` kept the reserved `cancelled` label, so its later delivery never counted
  as done. Leaving DONE now drops the label, and cancelling a poisoned task drops `poisoned`.
- **A dependency on a finished task no longer locks its dependent out for ever.** `add_dependency`
  from a blocker that was already DONE put it in the dependent's `blockedBy`, and nothing clears a
  finished blocker, so `claim_next` skipped the dependent and `claim_task` refused it. The edge is
  now recorded (`get_task_context` still shows it) but does not gate, and `block_task` naming a DONE
  blocker is refused, naming it, with the caller's lease kept. Re-adding a dependency after its
  blocker was reopened gates the dependent again, and a cleared edge no longer makes the reverse
  dependency fail as a cycle.
- **A task freed of its last blocker wakes its worker.** When a TODO dependent became claimable
  because its last blocker finished, was cancelled or deleted, or its dependency was removed, only
  masters were notified. Its assignee is now rung, or every worker when it is unassigned. Removing a
  dependency that no longer gated a task leaves a task that is BLOCKED for its own reason alone.
- **Expired leases no longer hold a WIP limit's slots against claims.** With a WIP limit on In
  progress, agents that crashed while holding tasks made every `claim_next` and `claim_task` answer
  `WIP_EXCEEDED` indefinitely. A claim refused by the limit now reaps the board's expired leases and
  tries once more. A move into the lane still counts them until `reap_stale_leases` runs.
- **`claim_task` names a task's blockers and respects its retry backoff.** On a blocked task it
  answered "Illegal status transition TODO → IN_PROGRESS"; it now names the tasks it waits on and
  returns them as `blockedBy`. A task inside the backoff that `fail_task` or the reaper set could be
  claimed again at once, which used up the poison threshold in seconds; it is now refused with the
  time the backoff ends.
- **`next_task` never writes, and shows the task's resume.** On an empty board `next_task` reaped
  expired leases in the caller's name, so a viewer's preview could send a task back to TODO or
  poison it. Expired leases are now recovered only by `claim_next`, `claim_task` and
  `reap_stale_leases`. Its `resume` field (branch, head commit, last checkpoint) was always null and
  is now filled as `claim_task` fills it.
- **An idempotency key reused on another task is no longer answered with the first task's result.**
  `complete_task` on T-2 under a key already used for T-1 returned T-1's DONE task as success and
  left T-2 in progress, dropping its deliverable. A key is now bound to the call's arguments, apart
  from `create_integration`'s secrets, which are never kept: the same call still replays, and
  different arguments make a new call.
- **Matching by profile or score looks beyond the first 200 claimable tasks.** `claim_next` and
  `next_task` with `useProfile`, and `next_task` with strategy score, judged only the first 200
  tasks, so a task the agent's profile covers further down was reported as no work. They now look
  through up to 1000.
- **Finishing, cancelling or deleting a blocker no longer fails STALE on Postgres.** A versioned
  move to DONE, `cancel_task` or `delete_task` of a task with dependents could fail with the version
  of a dependent instead of the task's own.
- **A column holding tasks cannot be removed or re-statused.** On Postgres, `remove_column` racing a
  move, create or claim into that column could leave the task in a column the board no longer has;
  the two now take turns. `update_column` changing the status of a column that holds tasks is
  refused until the column is empty.
- **`search_tasks` on Postgres finds words inside paths, addresses and ids.** "tasks.go", "T-123" or
  "bob@example.com" found nothing on Postgres while SQLite found the task. The search vector now
  splits on punctuation as the query does.
- **A follow-up to another agent of your own account wakes it.** With one login and labelled tokens,
  `add_follow_up` named the holding agent as target but rang nobody. It now rings that agent's
  sessions.
- **Deleting a sprint member lowers the sprint's member count.** `delete_task`, and the delete of
  losing runs at `pick_run` and `cancel_runs`, left `memberCount` counting the deleted tasks, so a
  sprint could refuse new members as full (`SPRINT_MAX_TASKS`) while holding fewer. The delete now
  lowers each sprint's count in the same transaction, and upgrading recounts every sprint.
- **An unfinished member of a closed or abandoned sprint can be carried to a successor sprint.** An
  ended sprint still counted for exclusivity and refused changes to its members, so such a task
  could never join another sprint, although the close warning said to carry it over. An ended sprint
  now stays in the task's `sprintIds` for history and blocks no other sprint, the warning names
  `set_sprint` add, and a claim's sprint and the board card's sprint badge name the task's newest
  sprint.
- **A sprint's record holds after the fact.** `update_sprint` could rewrite a CLOSED or ABANDONED
  sprint's name, goal and acceptance, the criteria it had been approved against; that is now
  `VALIDATION`, and `checkpoint` and `eventCursor` stay patchable. When the proposal task named no
  reviewer, `approvedBy` was whoever last wrote to that task, so a worker who added a note to the
  DONE proposal was recorded as the approver; it is now whoever moved the proposal to DONE.
- **A pick or a sprint close in flight no longer slips past run and sprint writes on Postgres.**
  `open_run` could commit a fresh, claimable run under a parent that a concurrent `pick_run` had
  already moved to DONE, where no race tool could reach it, and `set_sprint` or `create_task` with a
  `sprintId` could add a member to a sprint just closed. These calls, and `update_sprint`, now take
  the project lock as `pick_run` and `cancel_runs` do.
- **Races respect a task's blockers and its sprint.** `start_runs` created runs with no `blockedBy`,
  so they were claimable at once and carried the task's work past its dependencies, and `pick_run`
  could then land the task DONE ahead of its blockers; `start_runs` on such a task, and `open_run`
  on a racing parent that has gained a blocker, now answer `INVALID_TRANSITION` naming the blockers.
  A raced sprint member's runs did not belong to the sprint, so `claim_next`, `next_task` and
  `rank_tasks` with a `sprintId` found nothing for it and `get_sprint` showed an empty frontier;
  they now offer and list the runs of the sprint's members, which do not become members.
- **A resolved Chronicle conflict stays resolved.** Resolving a sweep conflict either way was
  flagged again by the next identical ingest, and a block landed as the generator's text stayed
  "edited" for good. The human write that clears a conflict now settles it against that derivation,
  and a block whose text both sides agree on is derived again.
- **A Chronicle record keeps the attachment images it embeds.** Deleting the source task, or a
  losing run at `pick_run` or `cancel_runs`, deleted the blob a record still showed, and the image
  broke on every read. Chronicle bodies now hold the blobs they embed against the blob GC (migration
  0009, which backfills existing records), and deleting the record releases them.
- **The Chronicle sweep no longer skips finished tasks.** The plugin's Chronicle skill moved its
  cursor after reading only the first page of `list_tasks`, which pages in board order and filters
  `updatedSince` after paging, and set the cursor to the newest `updatedAt` it had listed, which
  passed over a task that finished mid-sweep on a page already read. It now reads every page, keeps
  a cursor per board, and moves it once, at the end, to the sweep's start by the server's clock less
  a minute.
- **`list_tasks` pages past a task whose stored order key holds a control byte.** `move_task` once
  stored any key the order-key parser accepted, control bytes included, and once list cursors
  refused every control character, a page ending on such a task could not be followed. A cursor is
  now refused only for U+0000, the one character Postgres cannot bind, and `move_task` refuses a key
  outside the key alphabet.
- **Task memory reads count only what the reader can see.** `get_task_context` and `read_memory`
  took the newest entries of every author, live or expired, before filtering, so another agent's
  private notes or expired entries could push a task's shared memory and handoff note out of the
  answer. The filters now run first, and the limit counts entries the reader may see.
- **Private memory keys stay private.** The `memory.written` event every member sees in a task's
  history and the board feed carried the key of `agent` and `agent_global` entries. For those scopes
  it now carries only the scope.
- **`add_comment` checks its parent, `retract_artifact` trims its selector, and `write_memory`
  points to `attach_file`.** A `parentCommentId` that named no comment, or a comment on another
  task, was stored as given at any length; it is now `NOT_FOUND`, nothing is written, and the
  argument is bounded. `retract_artifact` matched an untrimmed url, ref or repo against the trimmed
  value `add_artifact` stored, so retracting with the same string answered `NOT_FOUND`; it now trims
  too. `write_memory` told an oversized value to use `largeArtifact`, which is itself refused; both
  refusals now name the 32768-character cap and `attach_file`.
- **Attachments keep their file names.** `attach_file` required `fileName` and then threw it away,
  so attachments showed as "file · 2.3 MB" and downloaded under a 64-hex name with no extension. The
  name is now stored (migration 0008; empty for older attachments), returned as `fileName` by
  `list_attachments` and in `get_task_context`'s attachments, and shown and used for the download by
  the task's Files panel. A name over 255 characters, not UTF-8, or holding a control character is
  refused.
- **An upload finishes, and an attachment never uploaded no longer stays pending.** A client that
  disconnected right after its upload's body made the database write fail after the file was in
  place, leaving a file no blob row named and the attachment pending; once the body is in, the
  upload now finishes under its own one-minute bound. A replay of `attach_file` under its
  idempotency key answered the stored upload URL, which had expired after 15 minutes, so every
  answer, a replay included, now carries a freshly signed URL. The retention sweep deletes
  attachments still pending 25 hours after they were created.
- **Attachments can no longer fill the disk the database writes to.** `attach_file` checked only
  each file's size, so a token repeating uploads could fill the disk the database shares, and every
  write for every project then failed. `serve` now keeps 512 MiB free on the blob disk, counting
  what uploads in progress are about to write: an `attach_file` or an upload that would go past it
  is refused with `QUOTA_EXCEEDED` (507 for the upload) and leaves nothing behind.
- **The retention sweep removes blob files no row names, and uploads are synced to disk.** A file
  left by a crash or timeout between an upload's move and its row, a file whose unlink failed after
  its row was deleted, and an upload's temporary file cut off by a crash stayed on disk for good.
  The sweep now removes such files once last written over an hour ago, and removes none when most of
  the files it checks have no row, so a board started against the wrong or an unrestored database
  deletes nothing. An upload's file and directory are fsynced before its row says ready, so a power
  loss right after a 200 no longer leaves a ready attachment whose file is empty or missing.
- **One identity can no longer lock everyone out of MCP.** The server allows 1000 live MCP sessions
  in total, and one token, or a client that never ended its sessions, could fill the table so that
  every other client, the workspace admin included, was refused for up to 30 minutes. An identity
  now holds at most 64 sessions. When it opens another, its longest-idle session with no call in
  progress is closed to make room; if all of them are mid-call, the new session is refused.
- **MCP list and resource results are marked private.** `tools/list`, which differs per caller, the
  other lists and every resource read carried the protocol's default `cacheScope: "public"`, which
  lets a shared cache serve one caller's result to another. They are now `private`.
- **One caller can no longer erase other callers' tool-call records on Postgres.** A tool call whose
  name or `projectId` held a NUL made Postgres refuse the whole batch of call records it was written
  with, so other users' and agents' calls were lost from the operations summary, and a NUL in an
  initialize `clientInfo` left a live session out of the admin's session list. A call to an unknown
  tool is now recorded as "(unknown)" and counted as `VALIDATION`, the error its client was told; it
  was counted as `INTERNAL`. A `projectId` that is not an id records no project, `clientInfo` is
  cleaned and cut to an agent name's length, and a row that still carries a NUL is dropped on its
  own.
- **Malformed text in a request is refused as `VALIDATION` on every engine, and a resource read
  survives a panic.** A NUL or a byte that is not UTF-8 in a resource URI's id or cursor came back
  `NOT_FOUND` on SQLite and as a retryable internal error on Postgres, and was a 500 on Postgres on
  the REST chronicle reads; a list cursor holding a NUL gave an empty page on SQLite and a retryable
  `INTERNAL` on Postgres. Such a value in a resource URI, in any `/api` path or query, or in a list
  cursor is now refused before any lookup. A REST body with bytes after its JSON object, such as
  `{"name":"x"}]{...}`, was acted on by its first object and is now refused 400. A panic while
  reading a resource ended the process; that read now returns an internal error.
- **The plugin's `scripts/mcp.sh` ends the MCP session it opens.** Each run left its session open
  for 30 minutes. It now ends the session on exit, after success or failure, and a server it cannot
  reach gets one line and exit status 1 in place of curl's status 7.
- **The Alertmanager, PagerDuty and Datadog connectors tell separate alerts and events apart.**
  Alertmanager deliveries were keyed by `groupKey`, which repeats on every notification of a group,
  so an alert's resolve and its next firing were answered 202 replayed and changed nothing for 30
  days; each alert is now keyed by its status and its start and end times. Every PagerDuty v3 event
  was filed as an incident keyed by its own id, and the subscription's test ping was refused; only
  events about the incident itself are alerts now, and anything else is accepted as no event. All
  groups of a Datadog multi-alert monitor shared the monitor's `alert_id`, so one host's recovery
  closed another host's ticket; the fingerprint now includes `aggreg_key` when the payload carries
  it, and the Datadog source's setup note names the fields to send.
- **A sender with no delivery id of its own no longer loses a second outage.** Without a source id,
  an identical body counted as a retry for the whole 30-day delivery window, so a check that went
  down, up and down again filed nothing the second time. An identical body now counts as a retry
  only within ten minutes, and under Correlate or Auto-close only while nothing else has arrived for
  that alert since. A recovery of an alert never seen is recorded once instead of on every retry.
- **On-call integrations file into a TODO column.** `create_integration` and `update_integration`
  now refuse a new target column that is not TODO. If the target column is later re-statused or
  removed, alerts go to the board's first TODO column instead of being refused one by one, and the
  integration can still be edited without changing its column. A verified delivery that is refused
  anyway now shows as a rejected row on the delivery log.
- **One webhook delivery buys a bounded amount of work.** An unbounded `alerts[]` array ran one
  transaction per entry on a single rate token. Repeated alerts in one delivery are now ingested
  once and entries that are not objects are skipped. When a delivery carries more than 500 distinct
  alerts, the first 500 are filed, and a rejected row on the delivery log says how many were
  dropped.
- **A replayed delivery writes nothing, and a body refused again is logged.** A delivery answered
  from its recorded outcome wrote a change row, which dropped the project's board snapshots and woke
  every master session for no change. A second refusal of the same body went unrecorded for 30 days,
  even for a new reason; a new reason now gets a new row at once, and the same refusal at most one
  row an hour.
- **Odd text from a sender no longer stalls ingest or breaks replay.** Postgres cannot store a NUL,
  so a payload holding one was answered 503 on every retry and the alerts after it never landed, and
  a refusal quoting one failed as a 500; the ingest now drops NULs from everything it keeps. A
  source's `deliveryId`, `groupKey` or event id went into the delivery id as sent, so a 200 KB one
  was stored and logged whole and could never be replayed, since `replay_delivery` and
  `read_delivery_raw` take at most 1,024 characters; a source id or fingerprint over 200 bytes is
  now replaced by `sha256:` and 32 hex characters of its hash.
- **A webhook sender's text is bounded and quoted where it lands.** A `jwt` delivery's refusal named
  the token header's `alg` and `kid` as sent, a sender with no credential could store up to the body
  cap of its own text on every rejected delivery row, and a sender's newline could forge server log
  lines. The `alg` and `kid` are now cut to 32 characters and quoted, every rejected delivery's
  error is cut to 500 characters, and every ingest log line quotes the refusal reason and the
  delivery id.
- **The JWKS fetch refuses carrier-grade NAT and the other reserved ranges.** A `jwksUrl` that
  resolved to 100.64.0.0/10 (where a tailnet's or a cloud's internal services live), 192.0.0.0/24,
  198.18.0.0/15, 240.0.0.0/4, a NAT64, 6to4, Teredo or v4-compatible (`::/96`) address, or the
  deprecated site-local `fec0::/10` was fetched. It is now refused like a private address.
- **`create_integration` and `update_integration` refuse a `jwksUrl` that can never be fetched.** An
  http, malformed, credentialed, `localhost` or private-address URL was saved, and the integration
  showed as enabled while every delivery was refused 401. Such a URL is now refused as `VALIDATION`,
  naming `auth.jwt.jwksUrl`.
- **An `hmac` integration given its own secret verifies its source's signature form.** Supplying
  `externalSecret` on any source switched verification to PagerDuty's untimestamped raw-body `v1`
  form, which refused a generic or webhook sender signing the documented timestamped `v0` form, and
  the previous secret after a rotation that pasted a new one. Only the `pagerduty` source uses `v1`
  now; every other `hmac` source verifies `v0`, whoever issued the secret. Upgrading: a
  non-PagerDuty sender that was made to sign the `v1` form must switch to `v0`.
- **A `jwt` integration can pin who the token names.** Issuer and audience alone let any account on
  a shared issuer (Google, GitHub Actions) mint a token that filed incidents. Two optional pins now
  exist over MCP, REST and the setup form: `auth.jwt.subject`, matched against `sub`, and
  `auth.jwt.email`, matched when `email_verified` is true. On an edit, the form says the JWT
  settings are replaced whole, so a blank pin removes the stored one.
- **The on-call setup says where to post and how to sign.** The endpoint panel showed a bare path to
  paste onto an "ingest function" this product does not have, and the signing scheme was written
  down only in the Go source. The panel now shows the full URL and the headers the integration's
  auth mode reads, with the `v0` signing string and a ready `curl` for `hmac`, and links the new
  on-call guide, `docs/oncall.md`.
- **Clearing an on-call integration's mapping or target column takes effect.** `update_integration`
  with a present empty `mapping` answered success, bumped the version and kept the stored mapping,
  and the web editor's Edit left out a cleared mapping or a blanked column. An absent `mapping`
  still keeps the stored one; a present one, `{}` included, now replaces it whole, and Edit sends an
  explicit clear for both.
- **A new on-call key or secret is shown, and stays on screen until it is stored.** Generate then
  Save changes on an `api_key` or `basic` integration replaced the key without showing the new one,
  so nobody held it and every delivery was refused; the drawer now shows it once, as on create. A
  click on the dimmed overlay or Escape closed the drawer while it showed a minted secret or key,
  which was then lost for good; both are now ignored until the operator confirms they stored it. In
  Settings, a new user's initial password no longer stays in the page's memory after Done.
- **The on-call editor takes typed lists and asks only for what an edit needs, and the
  refused-payload dialog shows real errors.** The comma-separated fields (Fingerprint, Title,
  Severity and the other fallback chains, stamped labels and the annotation allowlist) dropped a
  typed comma or space, so they could only be pasted. Editing a `jwt` integration demanded its
  issuer, audience and JWKS URL again; blank now keeps them. A switch of auth mode now asks for a
  key when the new mode needs one, and offers "leave blank to keep" only in the mode already
  configured. The refused-payload dialog explained every failure as a body aged out after a fixed 7
  days; only a 404 is explained that way now, and Escape in it closes only the dialog.
- **serve's exit watcher no longer writes into a reused descriptor or stalls shutdown.** On Linux, a
  session that unregistered or re-registered while serve was shutting down could write 8 bytes into
  whatever file, connection or database file had just reused the closed watcher's descriptor; the
  watcher now touches no descriptor once it is closed. When more than 64 watched sessions exited as
  serve stopped (a logout, say), its shutdown waited out its whole time budget; the watcher now
  stops at once.
- **serve starts with many sessions to catch up.** When more than 16 restored wake sessions each had
  a doorbell they had missed, serve's start-up blocked for good before it served anything: no
  `/healthz`, no `/mcp`, no UI. Start-up now hands their catch-up to the dispatcher, and every
  session is rung once.
- **Serves on several machines can share one Postgres database.** At start-up a serve restored or
  deleted every wake-session row by pid, so one machine's restart wiped another machine's sessions,
  and a pid number could be registered only once in the whole database. Each row now names the
  device that registered it (a `device-id` file in the data dir), a serve starts from its own
  device's rows, and a pid is unique per device. Rows written by an earlier development build name
  no device, and any serve may restore or drop them until their sessions register again.
- **A session's registration holds.** Without `CLAUDE_CODE_MESSAGING_SOCKET`, `session register`
  registered its parent, which under the plugin's hook is the script's own shell, so it reported
  success and the entry was gone the same second; it now looks past the shells a hook runs under to
  the client process, which may be a container's first process. A session registered with its
  messaging socket keeps that socket and its doorbell when a later hook registers it again without
  the variable. A session that re-registered while the dispatcher was rechecking its old, revoked
  entry could lose its fresh registration; a drop now waits for a registration of the same process
  in flight and removes only the entry it judged.
- **`session register` and `session unregister` send the bearer only to your own server.** They sent
  `BATONDECK_TOKEN` to whatever listened on the configured socket path, including a socket another
  local user had bound first. They now check that the server runs as the same user, and otherwise
  refuse in one stderr line without sending the token.
- **A doorbell goes only to a session socket of serve's own user.** The dispatcher wrote the
  session's Claude Code peer token to whatever listened at the socket path Claude Code's session
  registry names, including a socket another local user had bound first. It now checks that the
  socket is served by serve's own user; if it is not, it logs the refusal and writes nothing.
- **The SQLite database is created readable by its owner only.** It was created under the umask
  (0644), and its -wal and -shm files followed, so a database in an existing 0755 directory such as
  the README's `/srv/batondeck` was readable by every local account. It is now created 0600, and for
  an existing file with a looser mode, serve logs a warning and doctor prints one, each naming the
  chmod.
- **A refused `database.url` is never echoed.** A mistyped scheme (`Postgres://`, `postgress://`,
  `pg://`) was refused by quoting the whole URL, password included, on stderr and in doctor's
  output; now only the scheme is named. A Postgres URL whose password holds an unencoded `/` or `@`
  is refused, naming the encoding, before part of the password can reach serve's store line or a
  connection error.
- **Every command opens the SQLite file it names, and a second serve on that file hears the first.**
  A `#`, `?` or `%` in the database path (a "C#" folder, "team#1") was read as part of a URI, so
  `migrate` and `serve` made the database in the parent directory while `user`, `token` and `doctor`
  said it did not exist. A serve never noticed another serve's changes to the same file, so its
  boards went stale, `claim_next` through it ignored a WIP limit set through the other, and its
  stream and doorbells stayed silent; another process's commits now reach it within a quarter of a
  second.
- **Mistakes in the command line and the listen config are caught before anything runs.** `migrate
  prod.json` or `serve /data/config.json` (a config file named without `-config`) ran against the
  default SQLite file and listen address and reported success; `serve`, `migrate`, `doctor`,
  `healthcheck` and `version` now exit 2, naming the argument. A `unix://` listen entry longer than
  the kernel allows failed only at bind, as "invalid argument", after the store had migrated; the
  config is now refused when it is read, with the path's length and the limit.
- **master.key is created whole and once, and serve says when it creates one.** Two first starts at
  the same moment, such as replicas on Postgres, could each write their own key, leaving one sealing
  secrets under a key no longer on disk, and a crash mid-write left an empty file that stopped every
  later start; the key now appears whole or not at all, and a process that loses the race uses the
  winner's key. A key missing beside a database was replaced silently, after which every delivery to
  an HMAC-signed integration was refused 401; serve now logs the key it creates, as a warning that
  says what no longer opens and how to recover when the database holds secrets sealed under another
  key. With no key file path and no `BATONDECK_MASTER_KEY`, the keyring is refused before anything
  is written.
- **The unused admin socket is gone.** Nothing served or called it, and its fallback path in the
  temp directory had no owner or peer check.
- **`doctor` and `migrate` say what is wrong.** doctor printed ok and exited 0 for a bad
  `BATONDECK_MASTER_KEY`, a master.key at mode 0644, an unusable blob dir or a schema newer than the
  binary; each now has its own line and fails the exit code, a schema not migrated yet is a warning,
  and doctor still creates nothing it checks. migrate beside a running serve waited a minute in
  silence for a lock serve holds for its whole life, then failed without naming the cause; it now
  says at once that a serve is running and that serve migrates the store as it starts.
- **Deleting tasks and the retention sweep keep pace with the data.** A task's delete walked every
  event of its project inside the write transaction, and each blob sweep checked every blob against
  scans of the attachments and memory tables; new indexes serve both, as well as a task's memory
  window and the memory expiry sweep. Each keyed call kept its full result, a deleted task's title
  included, for the life of the project; the retention sweep now deletes idempotency keys past their
  replay window.
- **The web UI works behind nginx and behind a proxy that asks for HTTP Basic auth.** `/api/stream`
  now sends `X-Accel-Buffering: no` and `Cache-Control: no-cache, no-transform`, so a stock nginx
  `proxy_pass` passes its frames at once; before, the board stopped updating live and kept
  reconnecting, and the README now says what to set behind other proxies. Any `Authorization` header
  was read as an API token, so the Basic credentials a proxy asks for made every `/api` call 401
  despite a valid session; a header of another scheme now leaves the session cookie to decide.
- **`/assets/` no longer lists the bundle, and a refused `/blobs` request can no longer forge log
  lines.** `GET /assets/` served a directory listing of the embedded bundle, cached as immutable for
  a year, and now answers 404. A refused `/blobs` request's id was logged unquoted before any
  signature was checked, so a newline in the path wrote a line of the sender's choosing; the id is
  now quoted and cut to 80 characters.
- **The board's Load more shows the rest of a board past 200 tasks.** It asked the server for more
  than the 200 tasks a request may list, the board went blank, and every later refresh of the list
  failed. It now reads the board page by page, a failed Load more says so and keeps the cards, live
  updates keep the extra rows, and the header gives the board's total when only part of it is
  loaded.
- **A board or a ticket panel that could not be read says so, with Retry.** A board whose task list
  failed to load was drawn as an empty board ("0 tasks", "No tasks" in every column), and in the
  task drawer and on the ticket page a failed read of the comments, follow-ups, runs, context,
  subtasks, memory, attachments or activity looked like an empty one ("No comments yet.", "Blocked
  by: None"). Each now says the read failed and offers Retry, and the ticket's panels show their
  empty state only once a read has come back empty.
- **Live board updates reach the right board and stay.** The browser's one stream follows one board,
  but every subscriber got that board's snapshots and was told it was live; only that board's
  subscribers get them now, and a subscriber for another board is told it is not live and polls. A
  change that reached the board while its task list was being read again (after a drag, or during
  Load more) could be undone by that older read until the board changed again; the board now lays
  the newer update over such a read.
- **The live stream copes with many tabs and with a passing store error.** Every tab held one
  `/api/stream` connection, and a browser allows only six per host over HTTP/1.1, so over plain HTTP
  a sixth tab made every request in every tab wait; a tab hidden for 10 seconds now closes its
  stream and reopens it when shown, refetching what it missed, and a tab opened in the background
  opens none until it is shown. When `/api/stream` could not build a board or notifications frame (a
  failed re-check of the caller or a failed read), the change waited for the next one; the frame is
  now retried after 100 ms, backing off up to the 20 s ping.
- **The board's keyboard follows the focus.** Once the ticket search answered, the command palette
  stopped filtering and listed every command, so typing a ticket's title and pressing Enter ran the
  first command, often the selected card's "Open"; ticket hits are now listed beside the filtered
  commands, and Enter opens what was typed. The board's Enter shortcut took the key from every
  button and link ("Hide activity", "Load more", a nav link, "Sign out") and opened the last
  selected card; now a focused button or link does what it says, and Enter or Space on a focused
  card opens that card.
- **The board's cards, hints and Activity rail show what is true.** A task created on the board
  showed twice for a moment while its placeholder faded out; the placeholder is now replaced by the
  server's card in one step. A racing card's tooltip showed a literal "N" for its open runs, the
  search box advertised `/`, which opens the command palette, and every empty column offered "press
  c", though a new task lands in the board's first To do column, which alone offers it now. Beside a
  long action the Activity rail cut an actor's name to a few letters, so claude-code and
  claude-codex looked the same; the name now has a line of its own.
- **The activity feed shows one write's events in order, grouped by the viewer's day.** Events a
  mutation writes share a millisecond and were ordered by a random id, so the feed could show "added
  to sprint" before "created"; ids now sort in append order. Events were grouped by UTC date, so
  west of UTC an afternoon split between "Today" and "Yesterday"; groups now follow the viewer's
  local day.
- **Agents show as online, and on every board and project they work on.** The board read the agent
  sessions once, so an agent that pulls each turn (Cursor, a claude.ai connector) was shown offline
  75 seconds after the page loaded and one that connected later never appeared; the list is now
  refreshed while the board is open. A screen that joined the already-open stream, such as Agents
  with no board selected or the full task page, never saw a plugin session registered before it
  opened, and visiting Settings dropped those sessions; the stream now hands its last presence to a
  screen that joins later. The Agents page refreshed only on the first board's changes, and now also
  refreshes every 30 seconds when the project has more than one board. No call ever set an agent
  session's `lastProjectId`, so no card on the Projects grid showed an agent avatar; it is now the
  project of the agent's last successful call.
- **A claim made in the web UI can be finished after the ticket is closed.** Only the claim's own
  answer carries the lease id, and the drawer kept it in component state, so closing the ticket,
  walking with j/k or reloading left its holder with no Mark complete, Block, Hand off or Release
  until the lease lapsed. The web UI now remembers its claims' lease ids per account and ticket,
  another tab of the same browser picks a claim up at once, and a claim made in the browser holds an
  hour, since the REST surface cannot renew it.
- **Saving a ticket's description, title or digest no longer overwrites a concurrent edit.** The
  editors sent the version current at Save time, not the one the draft was taken from, so an agent's
  edit made while a person was typing was silently lost. Save now sends only the changed fields, and
  when someone else changed the text meanwhile it shows theirs and offers Keep my edit or Discard my
  edit.
- **A ticket's panels show what is there.** Comment and follow-up threads longer than 100 entries
  never showed their newest ones; they now read every page. Recent Activity filtered the board's
  newest 50 events, so an older ticket on a busy board read "No activity yet"; it now reads the
  ticket's own events, through a new index. The full ticket page now follows its board's live
  updates, so new comments, context items, subtasks, attachments and dependency statuses appear
  without a reload. The lease holder, "Held by", note avatars and link captions name the person
  instead of the account id.
- **A subtask's checkbox offers only legal moves, and Escape cancels a half-typed chip.** Ticking a
  TODO, In progress or Blocked subtask sent a move straight to Done, which the status table refuses;
  the box is now open only for Review to Done and Done back to In progress, and otherwise says why.
  In the drawer, Escape saved a half-typed label or capability; it now discards it.
- **The task drawer behaves as a modal.** A text selection released outside it no longer closes it
  and loses the draft. Tab stays inside it, also while the ticket loads, and skips hidden controls;
  focus returns to where it was on close, and the board's `c` key no longer opens the new-task row
  behind it.
- **A board write after opening Analytics is sent, and Analytics and Reports each show their own
  window.** Analytics cached its task census under the key every optimistic board write walks, in a
  shape those writes could not read, so for five minutes afterwards every card move, assign, status
  change and create in that project failed with a JavaScript error before reaching the server; the
  census now has its own key, and the board's writes skip any cached entry they cannot read. The
  7-day and 30-day operations summaries shared one cache entry, so whichever screen opened second
  showed the other's numbers; the window is now part of the cache key.
- **The web UI makes no request to a third party.** Link cards loaded their icon from Google's
  favicon service, which sent every linked hostname, internal hosts included, and the viewer's
  address to Google, and no setting in this build could turn it off; every page also loaded Google
  Fonts. Link cards now show a generic icon, Inter, JetBrains Mono and Newsreader are bundled with
  the UI, and the Content-Security-Policy names no host but the board's own.
- **The web UI works at whatever address it is opened at.** With `public_url` set, every signed
  `/blobs` URL started with it, so at another address (localhost, a LAN IP) the board's own
  Content-Security-Policy refused its uploads, leaving attachments pending, and blocked its embedded
  images; the UI now uses those URLs on its own origin. At a plain-http address such as
  `http://devbox:8080`, where browsers offer neither `crypto.randomUUID` nor the clipboard API, "New
  record" crashed, On-call's Generate and the Copy buttons did nothing, and clicking a project id
  threw an error. Ids and keys now use a random source every origin has, and copying falls back to
  another way, shows the link to copy by hand if nothing works, and says when it could not copy.
- **A shared Chronicle link opens the record it names, and a broken diagram leaves nothing behind.**
  Record URLs carried no project, so a link opened "Pick a project", "No such record" or another
  project's record with the same slug, depending on the reader's sidebar. Record links now carry
  their project, opening a record selects its project in the sidebar, choosing another project there
  opens that project's Chronicle index, and older links still fall back to the sidebar. A mermaid
  block that failed to parse left mermaid's "Syntax error" graphic at the bottom of the page, one
  more each time it was shown, and now leaves nothing.
- **A double click no longer creates things twice.** Double-clicking a new project's card created
  two boards, pressing Enter again in the CLI token label while the first mint was running minted a
  second live token, and "+ Add Column" could add a column twice. Each now goes out once.
- **Settings refuses a WIP limit that is not a whole number, and no longer says a token was never
  used.** A limit such as "five" or "1O" was sent as no limit, so a typo removed the column's WIP
  limit; Settings now takes a whole number of 1 or more, or blank for none, and otherwise explains
  the rule and sends nothing. The CLI tokens table read "never" under Last used for every token,
  because the server records no use; the column is gone.
- **On-screen text describes this server.** The connect dialog said the plugin talks to
  http://localhost:8080 and never mentioned `BATONDECK_URL`; on another address its plugin command
  now sets the variable. The crash screen said "It has been reported" while posting to
  `/api/client-error`, which this server does not serve; nothing is posted now, and the screen asks
  the user to report the message it shows. The on-call body-limit hint, a promise of incident
  metrics and the 503 error message described the hosted system this code came from, and now
  describe this binary.
- **Release workflows run pinned actions, and only the jobs that push hold write tokens.** The
  `release-draft`, `image-staging` and `release-publish` workflows ran actions by version tag, which
  an action's owner can move, in jobs whose token could push the image to ghcr.io and create
  releases. Every action is now named by commit SHA and workflow-level permissions are read-only;
  `release-draft` builds under a read-only token, only the job that pushes the image holds
  `packages: write`, and only the job that creates the draft holds `contents: write`.
- **The image and `compose.yaml` start from bases named by digest.** The Dockerfile's final stage
  used `gcr.io/distroless/static-debian12:nonroot` and `compose.yaml` ran `postgres:16`, both by
  tag, so a moved tag could change what the image is built on or what holds the database. Both are
  now pinned by digest, as the Go builder already was (`postgres:16.15@sha256:…`), and `make check`
  refuses a workflow action, base image or compose image that is not pinned.
- **The release is built with Go 1.26.10.** Go 1.26.9 lacked crypto/x509's denylist of compromised
  intermediate certificates (GO-2026-6809), which the server reaches through a `jwt` integration's
  JWKS fetch and a Postgres connection over TLS. CI now runs govulncheck and fails on any
  vulnerability the code reaches.
- **The web UI's development tools are past their published advisories.** vite 8.2.4, vitest 4.1.12,
  undici 8.12.4 and source-map-js 1.2.2 replace versions with published advisories (the vite 8.2.0
  dev server behind `npm run dev` could serve files outside the project). The bundled UI is not
  affected, and CI runs `npm audit`.
- **`make check` runs each slow suite once, and timing tests hold on a loaded runner.** The race run
  already covered the store conformance and live contract tests that their own steps then ran again,
  about 8 more minutes on CI's runner; each now runs once, and CI's job limit is 90 minutes. The
  board stream's 200 ms bound is checked by a new `make stream-latency` step without the race
  detector, the retention timer test waits for its sweeps, and the wake tests' other-user refusals
  run when the tests run as root.
- **The plugin's skills and commands can be followed.** `/master` had no step for a sprint's
  proposal task or its activation, so a sprint stayed PROPOSED, workers looking for ACTIVE sprints
  never found it, and the closing step was refused; `/master` and the worker skill now give the
  proposal, the admin's activation, closing from ACTIVE (else abandoning) and carrying unfinished
  members, and `/runs` says a task with open blockers cannot be raced. The worker prompt sent agents
  to fetch attachments by a `downloadUrl` that `get_task_context` does not carry; it, the worker
  skill, `/work` and the server's initialize instructions now name `list_attachments`, and the
  worker skill and `/master` tell a blocked task by the `poisoned` label and the board feed's block
  reason, where they looked for a `blockedReason` field no tool returns. `plugin/SECURITY.md` now
  says the server keeps the registering token sealed while a session is registered and reads the
  session's peer token for each doorbell.

Earlier fixes made while 0.1.0 was built:

- The task page's Review panel names who completed the task, the way the rest of the ticket names
  a person: the member's name, else their login, else the shortened id when nothing names the
  account. It printed the stored account id ("completed by U-4e68…") (P-431).
- A role below an action's tier is shown that action closed, with its reason, as Status and Claim
  already were: the board's "+ New task" and its `c` shortcut below master, a task's Comment and
  Reply for a viewer, and in Settings "+ Add Member" and Remove below project admin, the column
  edits below member (adding one below master, since the pre-public review) and the review-step switch below project admin.
  Before, a viewer was offered them and learnt of the refusal only from the server's error. The
  server's checks are unchanged (P-432).
- Every Settings control has an accessible name: the top bar's project select (captioned
  "Project", named "Project to configure"), the column name and WIP inputs and their Save and
  delete buttons (named by column), the new column's inputs, the Members add input and role select,
  each Remove (named by member), the "Require review step" switch (named by its caption) and your
  ticket token limit. On-call's project select is named "On-call project", apart from the
  sidebar's "Project". Screen readers announced them without a name (P-433).
- Settings and On-call work on the project selected in the sidebar, and picking a project in
  their own picker selects it in the sidebar: one selection, not three. Each kept its own pick, so
  the sidebar could name one project while Settings configured another (P-434; on "All projects"
  they ask for a project, P-442).
- The change bus no longer hands a change to a subscription made after its tail stopped. When the
  last subscription closed during a read, that read could deliver what it found to the next
  subscription, which is served from the end of the log, and set the frontier under the new tail.
  A read that finds its tail stopped when it comes to deliver now delivers nothing and does not
  move the frontier. (P-424)
- A stopped change-bus tail no longer stores its frontier over the frontier of the tail started
  after it. Its store after a read, on a stalled tick or after its seed could land after the stop
  and rewind `Bus.Frontier()`, which the wake directory reads. Every store is now made under the
  bus's lock and only while that tail is live. (P-427)
- `list_audit_log`'s `subject` filter, and `/api/audit?subject=`, find an identity's rows by the id
  as the server hands it out (`U-…`). The rows store an identity subject trimmed and lower-cased, and
  the filter compared it exactly, so the handed-out id found nothing. The filter now matches the
  subject as given or in that stored form; a project or task id still matches exactly. (P-435)
- `complete_task`'s description says what it does with `artifacts`: they are merged into the task's
  own. It said they were refused, though the call has always stored them. (P-436)
- `move_task` with an `order` and no `toColumnId` or `toStatus` reorders the task within its own
  column, as its description's "and/or reorder it" says, under the same master tier, version and
  lease checks as any move. It was refused with VALIDATION "move_task requires toColumnId or
  toStatus". A call naming none of the three is still VALIDATION, now "requires toColumnId, toStatus
  or order". A capability grant that narrows statuses refuses the order-only form, as it refuses a
  `toColumnId`-only move, since the call does not name where the task lands. The web UI's drag,
  which always names its column, is unchanged. (P-437)
- `grant_capabilities` refuses with VALIDATION a `deny` or `tools` entry that is no tool, or a tool
  no grant governs (an exempt read or self-scoped tool), as it already refused one that takes no
  `projectId`. The refusal names each entry, with `details.unknown`, `details.exempt` and
  `details.unscoped`. Such names were stored, so a typo in `tools`, an allowlist, silently denied
  that member every governed tool. A grant stored before this release that holds such a name still
  loads and is enforced by the names it holds; writing it again needs the names corrected. (P-438)
- `mark_notifications_read`, and `POST /api/notifications/read`, refuse a call naming neither `ids`
  nor `all: true` (`{}`, `{all: false}`, `{ids: []}`) with VALIDATION, `details.field` `ids`, as the
  tool's description requires. It answered `updated: 0`. The web UI, which always sends one id or
  `all: true`, is unchanged. (P-439)
- The audit log stores a CLI token's `cli_token.minted` and `cli_token.revoked` rows with the identity
  subject lower-cased, as it stores every other identity subject, so `list_audit_log` filtered by the
  stored form finds them too. Rows written before keep their `U-…` subject, which the id as handed
  out still finds. (P-441)
- Every write control in the web UI that the caller's project role cannot call is closed, with its
  reason, the way P-432 closed "+ New task" and Comment. On the board: "+ New board", the card's
  "+ assign" chip, dragging a card, the sprint lifecycle, and the palette's Set status and Set
  priority, which stay listed but disabled. On a task: Edit, every Details field, the description and
  digest, dependencies, subtasks, notes, links, memory, the file drop, the Follow-ups composer and
  Ack, and the race controls. Also Projects' "New board", and Chronicle's New record, Create, Edit,
  Delete, Land and Revert, and On-call Setup's Enable or Disable, Rotate, Delete, Create and Save,
  which need a member or an admin while a master may read the integrations. Closed "+ assign" chips
  and palette items now also look closed. Opening a project with no boards, from Projects or the palette, no longer
  creates one for a role that cannot. The role is the caller's in the project (`useMyProjectRole`),
  and the floors are one table in `web/src/toolFloors.ts`, copied from `internal/domain/grants.go`
  and checked against it by a test. Each control showed open and failed only after the server
  refused. The server's checks are unchanged (P-440).
- On "All projects", Settings and On-call show their project picker with nothing chosen and the
  prompt "Pick a project to configure". They load, edit and store nothing until a project is picked,
  and picking one sets the shared selection as before. They took the first project and stored it,
  which configured a project nobody chose and left the sidebar off "All projects" after leaving
  Settings (P-442, replacing P-434's fallback).
- A caller who opens, by URL, a project they are not a member of (a workspace admin, say) sees its
  closed controls say "You are not a member of this project, so you cannot …". They said "Checking
  your role" for good, because the project roster lists only the caller's own projects. While the
  roster loads they still give no reason, and a task's own member list still wins over a roster read
  before the caller was added. The server's checks are unchanged (P-443).
- A follow-up's closed Ack button shows its reason beside it and is described by it, like every other
  closed control. The reason was a hover title only, which neither a touch screen nor a screen
  reader shows (P-444).
- A board that cannot be shown because the caller is not a member of its project says so ("You are
  not a member of this project, so its board cannot be shown. A project admin can add you."), as the
  closed controls above it do. It said only "Couldn't load this board.", which other failures still
  say (P-445).
- A page title no longer breaks mid-word beside closed controls and their reasons. The title keeps at
  least 16 characters and wraps between words; actions that do not fit beside it move to the next
  row, each closed button with its reason. "Board" read "Boar / d" for a caller who is not a member.
  The board's columns are sized from where they start, not a fixed 140 px, so their scroll track
  stays on the bottom of the window when the header takes more than one row (P-446).
- A full board header packs its wrapped actions: the wrapped row is right-aligned, as a one-row
  header's actions are, and a closed control's reason wraps within 20 characters. At 1024 px a
  viewer's header took three rows, with Activity alone on the third (P-447).
- The board's columns are re-measured when anything above them changes height without a render or
  a window resize (a web font arriving late), through a ResizeObserver on everything the board
  renders into the page when it mounts: its header, the filter bar, the reasons and messages, and
  the body; what appears later is measured when it renders (P-448).
- A session that disconnects itself (`disconnect_agent` on its own agent) gets its answer whole. The
  session was closed when any request on it finished after the disconnect, so another request
  finishing first (the client's standalone stream, a second call) could close it under the answer,
  which then arrived as an empty 200. Only the request that made the disconnect closes it now, and
  at once when that request's client has already gone (P-450).
- `SECURITY.md` (report a vulnerability privately through GitHub's private vulnerability reporting)
  and `CONTRIBUTING.md` (build, test, pull requests, contributions under Apache-2.0), linked from the
  README (P-449).
- `serve` stops at once when a client holds a connection it has not sent a request on yet (an HTTP
  client's spare one). `http.Server.Shutdown` keeps such a connection until it is 5 s old, so the
  stop took 5 s or more; serve now closes them while it shuts down, as it closes idle keep-alive
  ones. Found when `TestRunServerStopsWithAnAttachedSession` failed on a slow CI runner.
- The README's compose quickstart and restore steps, and `compose.yaml`'s header, start the stack
  with `docker compose up -d --wait`, and the board's service has a 5 s health check. A plain
  `up -d` returns before the server listens, so the `curl …/healthz` pasted right after it got an
  empty reply (P-421, the e2e on macOS).
- A task's context items now keep the order they were added in, even when two land in the same
  millisecond. Each item is stamped the current time, or one millisecond past the task's newest
  item when that is not earlier. Before, two such items were ordered by their random ids, so
  `get_task_context` could list them out of order and the resume that `claim_task`, `claim_next`
  and `get_task_context` hand back could name the older of two checkpoint notes (P-117). Items
  already stored keep their times.
- A path the board does not have answers 404, not the web UI's page with 200 (P-303): the
  catch-all serves the UI only for the paths it routes (`/projects`, a board or task link,
  `/chronicle/…`, `/agents`, `/settings`, `/oncall` and the rest of `web/src/App.tsx`'s routes), and
  any other GET, such as `/docs`, `/mcp/`, `/healthz/`, `/blobs/` or bare `/api`, is 404. With OAuth
  off (no `public_url`), `/.well-known/oauth-authorization-server` and
  `/.well-known/oauth-protected-resource[/mcp]` are therefore 404, so an MCP client probing
  discovery after `/mcp`'s 401 reads "no OAuth" instead of failing to parse a web page (P-302). A
  mistyped address in the browser now shows the 404 instead of redirecting to `/projects`.
- **A Chronicle record that shows an attachment keeps showing it** (P-244). A block that embeds a
  download URL from `list_attachments` used to store it as written, and the image broke once the
  URL's 15 minutes ran out. A write now stores such a URL in one stable form when it is still live,
  or when the page already held that attachment (an editor left open past the 15 minutes), and every
  read of the page, a version or a conflict, over REST and MCP, signs that form afresh. Any other
  expired URL is stored as written and stays expired, so a URL cannot be renewed by pasting it into a
  page after it ran out; the stable form itself is refused where the page did not hold it. Records
  written before the fix are re-signed on read when their URL was live as it was written. A forged or
  foreign URL is left as it is. The web editor's conflict panel and the history diff compare block
  text with those signatures masked, so an untouched image no longer shows as changed.
- **`resume` is filled in** (P-117, P-161). `claim_task`, `claim_next` and `get_task_context` always
  answered `resume: null`, which reads as "nothing to resume". They now answer the task's newest live
  `branch` and `commit` artifacts and its newest checkpoint note (DONE, NEXT, REJECTED, UNCERTAIN),
  the checkpoint sought among its newest 200 context items, and null when none of the three is
  found. Nothing is stored for it and no migration runs.
  `claim_next` costs two more read statements for it, after the claim commits.
- **The retention sweep deletes lapsed browser sessions and API tokens.** A session expired, and
  an API token expired or revoked, more than 7 days ago (`retention.LapsedGrace`) is deleted, a
  page at a time like the other steps; before, every sign-in and every token rotation added a row
  for good. Each identity's oldest and newest token rows of each label stay, because the tokens
  table is also the record of who owns a label: a lapsed label stays its first minter's, and its
  earliest row is never swept. For the week of grace `token list` still shows a lapsed token with
  its status. After the week `token list` keeps only each label's oldest and newest rows, whatever
  their status: a lapsed row between them is gone, and `token revoke ID` of a swept row answers
  `NOT_FOUND: token not found` (exit 1), where it had printed the time the token was revoked (P-44).
- **The retention sweep deletes lapsed OAuth codes and tokens.** An authorization code, access
  token or refresh token expired more than 7 days ago (`retention.LapsedGrace`) is deleted, used or
  not and whatever its grant's state, a page at a time like the other steps; before, every
  connector authorization and every hourly refresh added rows for good. Inside the week of grace a
  rotated refresh token or a spent code presented again still finds its row and revokes its grant.
  After the week such a replay is refused as an unknown grant (`invalid_grant`) and no longer
  revokes the grant: in 0.1.0 reuse detection had no end. The client and the grant itself stay
  (P-125).
- **The retention sweep deletes the alert states of DONE or deleted on-call tickets.** A
  fingerprint's correlation row, stamped with the delivery window (`retention.deliveries_days`, 30
  days by default), is deleted once it has expired and its ticket is DONE or deleted; before, an
  integration that sees a new fingerprint per incident added a row per incident for good. An open
  ticket's row stays whatever its expiry, so a late firing still appends to that ticket. A DONE
  ticket reopened after its expired row was swept is no longer correlated: its alert's next firing
  files a new ticket, and a resolved event is recorded without annotating or closing the reopened
  one (P-226).
- **Help at every level exits 0 on stdout.** `user`, `token` and `session` take `help`, `-h` and
  `--help` in place of a subcommand, and every command and subcommand's `-h` and `--help` print its
  usage on stdout and exit 0; they had exited 2 with "unknown subcommand" or the usage on stderr
  (P-311). A usage error still exits 2 on stderr.
- **Two tests no longer fail at random under load.** The idempotency-window conformance test
  bounded the replay's expiry by sleeping past the first call's 100 ms window, then renewed the key
  inside a second 100 ms window that a slow machine could outrun; the renewed key now has an hour.
  The Postgres bus test that fills an earlier hole took A before D, an order the bus does not
  promise; it now takes both in either order and still judges C's hole (P-157, which delivers
  P-94).
- **An open agent connection keeps its row past 30 idle minutes (P-279).** The retention sweep no
  longer deletes the agent session of a client still connected to this serve process (one that only
  pings, or waits on a long task); its row goes 30 minutes after its last request once it
  disconnects.
- **A device session hears a project from the moment it joins (P-229, P-331).** A registered
  Claude Code session added to a project, or whose user creates one, gets that project's broadcast
  and changed doorbells at once instead of after the next 60 s directory sweep; removed or deleted,
  it stops hearing them at once. The re-read runs only on a membership change: an idle directory
  still makes no statements between sweeps.
- **serve stops the device session directory before it closes the store (P-205).** serve waits for
  the directory's dispatcher to finish a doorbell in flight before it closes the store (at most the
  10 s shutdown budget), so no directory write follows serve's return.
- **The config refuses more of what it cannot use.** `public_url` (`BATONDECK_PUBLIC_URL`) must be an
  http or https origin, `scheme://host[:port]`: a bare host, a path, a query, a non-empty fragment or
  userinfo is refused naming `public_url_is_an_http_origin`, and a trailing `/` is dropped (with one,
  serve's log line printed `…//blobs/`). A config file with anything after its JSON object but
  whitespace is refused, a trailing `//` comment included, where 0.1.0 loaded the first object and
  ignored the rest. A `.dockerignore` keeps `.env`, `master.key`, SQLite files, `dist/`,
  `web/node_modules` and `.git` out of the image's build context; the binary it builds is unchanged.
- `/api/stream` answers `event: resync` to a cursor (`Last-Event-ID` or `?cursor=`) whose change-log
  row the retention sweep has deleted (P-281). The replay read only rows after the cursor, so a tab
  that came back after the sweep got the surviving rows as if nothing were missing and kept a board
  without the changes swept in between; the SPA already refetches on `resync`.
- The web UI goes to the sign-in form when its session ends while a tab is open (P-282). After a
  logout in another tab, a `user passwd`, a `user disable` or an expiry, the server closes
  `/api/stream` and the browser's reconnect meets 401, which EventSource reports only as a closed
  stream; the tab kept reconnecting, backing off to every 30 s, on a screen that still looked
  signed in. A closed stream now asks `/api/session`, and a 401 there ends the reconnects and shows
  sign-in; any other answer reconnects as before.
- The board's "agents online" pill and task detail keep showing a live, idle Claude Code session
  (P-197). Their `/api/agents/sessions` answer, which never lists the device's own sessions, replaced
  the rows the stream's presence frame had put in place, and a frame comes only when the device's
  session directory changes, so an idle session read "no agents online" from the board's load, and
  again after any refetch, until the next change. The answer now keeps those rows, as the Agents
  screen already did, and task detail applies the presence frames itself, so on the full task page
  too a session that leaves is gone with the next frame.
- An MCP session is bound to how its bearer authenticated and the agent it carries, as well as its
  identity: a later request on the same identity's token with another label, on its default-label
  token or on its OAuth token is refused 403 (`session user mismatch`), instead of acting as another
  agent under the session's `agent_sessions` row. A rotated token of the same label still works
  (P-323).
- An agent is its identity plus its name wherever two users' agents meet. The operations summary's
  `byAgent` has one row per (identity, agent), each with its `identityId`, so two users'
  `claude-code` sessions are two agents; on the board and in the assign menus, bob's live claim no
  longer shows alice's own same-named session as Busy, and two users' same-named agents stay two
  entries (P-335).
- A worker registered under a name no token carried no longer keeps its wake doorbell once that
  name is a label it may not claim: another identity mints it, or its own identity mints it on a
  token other than the one the worker registered from, while that token is live. Delivery re-reads
  the label's owner and drops the entry, logged as `its agent "<name>" is another agent's label`,
  instead of ringing it with that label's work. A worker under its own token's label is rung with
  no extra read (P-344).
- A mention reads a worker's label the way it resolves one. A body token names a label when it is
  the label or the label's local part (`@pbob` names `pbob@x.example`), and never names a label it
  only starts (`@pt` does not name `pt bob`). So a free name a mention of another identity's label
  also names no longer rings that worker, and a free name that only begins another label still
  does. A free name equal to another project member's login, email local part or display name no
  longer aliases its registrant: `@will` rings will alone. The `session` usage, the plugin README
  and the register refusal now say a live label of the bearer's own account, on a token that does
  not carry it, is refused as well (P-348, P-349).
- A mention of a label is its first minter's. A member who mints a case or local-part variant of
  a label another member minted first (`OPS-BOT` of `ops-bot`, `deploy-bot@evil.example` of
  `deploy-bot`) no longer takes that label's mentions: `@ops-bot` and `@deploy-bot` mention, ring
  and notify the first minter alone, and `@deploy-bot@evil.example` still reaches the variant's
  own worker (P-348).
- **`cancel_task` refuses a task already DONE** with `INVALID_TRANSITION`, so a delivered task is
  not relabelled out of every done figure, and **refuses a racing parent** with `CONFLICT_LOCKED`
  (reason `race`) as `move_task` does, until `pick_run` or `cancel_runs` resolves the race. Neither
  refusal writes anything (ADR-0001, P-150).
- **A cancelled task no longer counts as delivered** (ADR-0001: DONE with the label `cancelled`).
  `pick_run` refuses a cancelled run with `INVALID_TRANSITION` even when it had a deliverable,
  activating a sprint refuses a cancelled proposal task, and in the web UI the task drawer's Runs
  section offers no Pick on a cancelled run, the sprint rail counts a cancelled member in its total but
  not in done and does not read a cancelled proposal as approved, and subtask progress on the card and
  in the drawer counts a cancelled subtask in the total only. Status displays still show it under DONE
  (P-208).
- **`sprintId` works on `create_task`, `next_task` and `claim_next`.** `create_task` answered
  `NOT_FOUND` for every sprint, existing or not; it now creates the task a member of the sprint in the
  same write, under the rules `set_sprint` applies (the sprint on the task's board, PROPOSED or ACTIVE,
  below its size limit), and the task gets `sprint.task_added`. `next_task` and `claim_next` refused
  `sprintId`; they now choose among the sprint's members only, answer null when none is claimable,
  `NOT_FOUND` for an unknown sprint and `VALIDATION` for another board's. A claim of a sprint member,
  by `claim_task` or `claim_next`, now answers `sprint` {id, name, goal, status} instead of null
  (P-239).
- **`next_task {strategy: "score"}`, `explain` and `useProfile` work.** `strategy: "score"` and
  `explain` answered `VALIDATION` ("not ported"), and `useProfile` on `next_task` and `claim_next` was
  accepted and did nothing. `strategy: "score"` now ranks the frontier with the scorer `rank_tasks`
  uses (the project's `selectionWeights` and `agingCeilingMs`); `explain` adds the chosen task's
  factors, reason and runner-up, and needs `strategy: "score"` (`VALIDATION` without it).
  `useProfile` judges eligibility by your own registered agent profile (exact, alias or tag match)
  instead of `capabilities`, falls back to `capabilities` when you have no profile, and is
  `FORBIDDEN` under another agent's label or another identity's id, as on `rank_tasks`. Both paths
  judge the frontier's first 200 tasks (P-258).
- The web UI names people instead of printing their `U-<hex>` account id: comment authors and
  "Replying to", the comment composer's @mention suggestions (found and shown by name, with the
  login or a shortened id beside two people who share one; a picked person is posted by their
  account id, so the mention reaches exactly them, while a pick the author then edits is posted as
  typed) and the @mentions in a comment's body (shown by name as plain text, with code left as
  written), follow-up authors and targets,
  notification lines ("Alice handed you T-2"), the ticket's "created by" on the drawer and the page,
  the Agents "Recent Handoffs" list and the sidebar identity chip. Names come from the
  `memberDisplay` the project reads already return; an id nothing names is shortened (P-314).
- The On-call page's subtitle no longer says incidents are "worked to a PR": it says webhooks
  become incident tickets, assigned by the integration's routing or broadcast to the project's
  agents (P-342).
- A ticket's Status control is disabled, with the reason, for anyone below master, the tier
  `move_task` requires, instead of sending a move the server refuses; the claim hint no longer
  tells a worker to make that move, and a viewer is not offered "Claim to work" (P-209).
- The web UI asks for a project's integrations only as a workspace admin who is master or above
  there, as the service requires, so a project master who is not a workspace admin no longer
  draws a FORBIDDEN on the board, Activity and Analytics screens or on a ticket (P-248).
- `web/package.json` declares `"license": "Apache-2.0"` (P-75), and the lockfile's root entry
  carries the same field, so npm tooling and licence scanners no longer read the SPA as
  unlicensed.
- /mcp accepts the `public_url` host (P-97). go-sdk's DNS-rebinding protection refused every
  request arriving on a loopback listener with a non-localhost Host, so a deploy behind a reverse
  proxy on the same machine answered every MCP call 403 `invalid Host header`. It is replaced by an
  equivalent check in front of the bearer that also admits the host of `public_url` (any port, any
  case); any other foreign Host on loopback is still 403, now as `{"code":"FORBIDDEN","message"}`
  JSON instead of text/plain. No new config key. The check runs before the bearer, so a foreign
  Host with no bearer is now 403, where it was 401.
- Postgres claims and concurrent writes, one lease per task, heartbeats that keep their lease, NUL
  refused (P-278; audit 2026-10-24 A2-A6, glitches G1 G2 G3 G6 G7). `claim_next`'s frontier locks
  `FOR NO KEY UPDATE ... SKIP LOCKED`: under `FOR UPDATE` it passed over a TODO task whose row a
  concurrent comment's foreign key check held `FOR KEY SHARE`, and answered no work. When that finds
  nothing, Postgres looks once more without `SKIP LOCKED`, waiting for a writer that holds the task
  (a context item, task memory) and claiming it if it is still TODO (G1 on Postgres, 20 rounds:
  comments 5/20 → 20/20, context items 0/20 → 20/20, task memory 11/20 → 20/20; one more statement
  only on that miss). `LockTask` locks `FOR NO KEY UPDATE`, and every mutation that bumps a task
  (not a comment, a follow-up or a claim) loads it with `Store.LockTask` (new, all three stores)
  before it writes any row beside it: two `add_context_item` or `write_memory` calls each inserted
  their row and then waited on the other's lock, a deadlock answered INTERNAL (G2: 30 INTERNAL in 36
  calls, now 0). A mutation with no expected version whose versioned write loses a race is run again
  inside the service instead of answering STALE (concurrent `add_follow_up {reopen}`). A NUL
  character (U+0000) in any argument string, at any depth, is VALIDATION naming the field, on every
  MCP tool and every REST route that is a service call (`domain.CheckText`; the hand-decoded
  `POST /api/me/cli-tokens` and `POST /api/users` and the oncall webhook ingest are not covered yet,
  P-284): Postgres answered INTERNAL, retryable, on nine
  fields of eight tools while SQLite stored it (G3). `claim_task` on a task whose live lease the
  caller already holds answers that lease, lengthened to this claim's `leaseSeconds` if that is
  later, never shortened: a second session of one identity minted a new lease and silently ended the
  first (G6, 20/20 rounds). The two sessions now share one lease and neither is told. `heartbeat_task` with no
  `leaseSeconds` never moves the expiry earlier (`Store.Heartbeat` takes `keepLonger`): it cut a
  3600 s claim to 300 s (G7); an explicit `leaseSeconds` still sets it, shorter included.
- Cursor can list tools (P-301, rev76 I1). An `any`-typed field inferred the empty schema, which
  jsonschema-go serves as the boolean `true`, and cursor-agent refuses the whole `tools/list` over
  one. Seven positions carried it: `write_memory`'s input `value` and the memory `value` /
  notification `data` in six output schemas. They are now `{"description":"any JSON value"}`,
  which takes the same values and canonicalises to the same node, so `contract/expected.json` and
  `deltas.json` are unchanged. `TestNoBooleanSchemaServed` walks every served schema and refuses a
  boolean schema at any depth, except `additionalProperties: false`.

- P-219: an open `/api/stream` no longer survives sign-out, a disabled account or a revoked token.
  The caller was resolved once at connect, so a copied session cookie kept receiving board frames
  after logout (the R1.15 review measured the stream still open 20 s after it). The stream now
  resolves the request's credential again before every ping, board, presence and notifications
  frame, through the same resolver as the session check (`auth.Service.Reauthenticate`, new), and
  closes without another frame once it is refused: a logout or a revoke at the next frame or ping
  (20 s), a disable once the 60 s account cache has let the entry go. A store error keeps the stream
  and skips that frame. The reconnect is 401.

- P-210: a rotated-out secret that no longer opens does not refuse every delivery during its
  window; ingest verifies with the current secret alone and logs why. A current secret that will not
  open refuses the delivery as `secret_unavailable`, on the delivery log.

- Request fields whose false, 0 or null means something can now be sent (R0.22).
  `internal/service/optional.go` adds `Optional[T]` (absent or a value; JSON null refused, as
  conductor's `.optional()` refuses it) and `Nullable[T]` (absent, null or a value), each with
  `Set`, `V` (and `Null`), `IsZero` for `omitzero`, and the constructors `Some`, `Value`, `Null`.
  Migrated: `UpdateSettingsReq.ReviewEnabled`, `.QuotaEnabled` (Optional[bool]), `.Color`,
  `.DefaultReviewer` (Nullable[string]), `.TxnCap` (Nullable[int64]); `UpdateColumnReq.Order`,
  `AddColumnReq.Order` (Optional[float64]), `UpdateColumnReq.WIPLimit` (Nullable[int64]);
  `TaskPatch.Assignee`, `.DueAt`, `.Reviewer` (Nullable[string]), `.Estimate` (Nullable[float64]);
  `FailTaskReq.Retryable` (Optional[bool]); `ListTasksReq.Assignee` (Nullable[string]); and, from
  review, `TaskPatch.ModelHint` (Nullable[ModelHintInput], null clears it), `TaskPatch.Description`
  (Optional[string], "" clears it), `UpdateSettingsReq.AnomalyPolicy`
  (Optional[AnomalyPolicyPatch], {} resets every threshold), with `capabilityAliases:{}` now
  removing every alias. The DTO
  recipe types each instantiation as its value (a Nullable as a pointer), so `TestDTOCanonical`
  stays exact for all 44 tools. `update_settings {reviewEnabled:false}` turns review off,
  `{color:null}` removes the colour, `update_column {wipLimit:null}` clears the limit, `order:0` is
  a position, and an omitted field changes nothing. `quotaEnabled`/`txnCap` in any state stay
  FORBIDDEN (not a workspace admin) or VALIDATION (a workspace admin). `TestRequestPointersAreDeliberate`
  fails on any pointer field reachable from a tool's request type that is not on its allowlist
  (`AddColumnReq.WIPLimit`, `ColumnSpec.WIPLimit`, `CreateTaskReq.ParentTaskID`), since a pointer and
  a Nullable infer one schema and the canonical comparison cannot tell them apart.
- Contract comments corrected to conductor: `create_project` appends no event, and the board feed
  is newest first with the cursor paging older; the `// DEVIATION` notes that cited the old wording
  are gone. `TestCloneSnapshotSharesNothing` fills every pointer, slice and map of the cached
  snapshot type by reflection and fails on any the copy shares, so a reference field added later
  is judged with no edit to the test.
- Two processes migrating one SQLite file at once no longer race in goose's Up (`SQLITE_BUSY`,
  `table projects already exists`): `migrate` holds the data-dir lock alone, waiting up to a
  minute for another holder, and `serve` takes it through `core.AcquireServeLock`, migrates only
  when alone, then shares it for its lifetime, so a serve beside another does not migrate.
  Postgres keeps goose's session lock. `TestFirstMigrationsOnOneFileBothSucceed` moved to
  `internal/cli` and runs the real migrations through `migrate`, 30 rounds.
- `POST /hooks/{id}` answers a verified delivery whose ingest failed retryably (a lost race,
  `STALE`, or a store fault, `INTERNAL`) with 503, `Retry-After: 1` and
  `{"code","message","retryable":true,"results"}` naming the events that landed ahead of it, not
  400 (P-233). Alertmanager and PagerDuty drop a 4xx other than 429 and retry a 5xx, so the alert
  was lost; the retry now replays the landed events and lands the failed one once. Every 429 carries
  `Retry-After`: `ceil(60/ratePerMin)` seconds for the integration's budget, 1 for the pre-check.
  A non-retryable refusal stays 400 `VALIDATION`.
- `POST /hooks/{id}` charges a delivery to the integration's rate budget before verifying it, and a
  failed JWKS fetch or an unusable set is remembered for 30 s, so a sender with bad bearers can no
  longer make the server fetch a `jwt` integration's JWKS on every request (P-231). Over the budget
  a delivery is 429 with no fetch; a refused delivery is paid by that charge, and one that parses
  gets it back before its events charge themselves. Because the charge comes before the delivery id
  is known, on an empty bucket a retry of an already recorded delivery is now 429 until a token
  refills rather than a free replay. Concurrent first misses on one URL are bounded by the bucket's
  capacity, `ceil(ratePerMin/4)`; they are not coalesced into one fetch.
- `complete_task` takes `artifacts` (P-235, P-143). It answered UNSUPPORTED ("there is no artifact
  store until R2.1") on any entry, so the skill's one-call completion with its pr and branch failed.
  The entries now merge into the task's by `add_artifact`'s rules (normalised, de-duplicated against
  the live entries, the 50 cap counting tombstones) in the completion's own transaction, before the
  status change, and the answer's task carries its artifacts. `artifactPolicy` is judged after the
  merge: a task with no live artifact is refused VALIDATION under `enforce` (nothing written), and
  under `warn` or an unset policy (read as `warn`, as `get_sprint` does) completes with a warning
  and a `task.artifact_missing` event; `off` judges nothing. `task.completed`'s `artifacts` is the
  number of entries added.
- `get_task_context` returns the task's artifacts, attachments and open follow-ups (P-236, P-154).
  All three came back empty, and `openFollowUpCount` 0, whatever the task held, so an agent
  resuming another's task saw none of its evidence, files or open questions. `artifacts` is always
  read (a retracted entry with its tombstone); `attachments` with the `attachments` section
  (`gcsUri` the `blob:` locator, `sha256` once the upload is ready, no download URL:
  `list_attachments` signs those); `openFollowUps` and `openFollowUpCount` with the `followups`
  section (open ones only, at most 200). One statement each: the `get_task_context` pin in
  `test/perf` rises from 6 to 9.
- Attachment links no longer point at `0.0.0.0` (P-337). With `public_url` unset, the signed
  upload and download URLs took the listener's address, and the image listens on `tcp://:8080`, so
  under the compose quickstart every link named `0.0.0.0:8080` or `[::]:8080`. They now start with
  the scheme and Host of the request they are signed for (`X-Forwarded-Proto: https` gives https),
  on the web UI and MCP alike; `public_url`, when set, is still the base. A request on the Unix
  socket takes the first TCP listener's address, a wildcard host made `127.0.0.1`. No new setting.
- A request body is read within 30 s (P-232). Only the headers were bounded (10 s), so a client
  that sent headers and then stalled its body held a handler and its connection for as long as it
  liked, on every route, `/hooks` included; an idle keep-alive connection was never closed either.
  The server now bounds reading a request at 30 s from its first byte (a stalled body gets 400 and
  the connection closes); the bound ends once the request has been read, so `/api/stream`, the
  MCP stream and a long tool call run as before; a blob upload keeps its signed URL's expiry as its
  deadline. An idle keep-alive connection closes after 30 s. No new setting.
