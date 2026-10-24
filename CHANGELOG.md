# Changelog

Notable changes to batondeck-kanban. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project will use
[semantic versioning](https://semver.org/spec/v2.0.0.html) from its first release.

## [Unreleased]

### Changed

- Local sign-in, sign-out and Settings → Users (R1.15). The sign-in screen is a login form posting
  R0.12's form fields to `POST /api/login`, which now answers a refusal as `{code, message}` (one
  message for an unknown login, a wrong password and a disabled account) and the form shows it; on
  success the route asked for renders, with no navigation. The WorkOS button, the tenant and access
  copy and the dead demo button are gone. New `POST /api/logout` (behind the session and CSRF)
  deletes the session row (`Store.DeleteSession`, both engines) and expires both cookies: sign-out
  called the unmounted `/auth/logout` before (P-198). New `GET` and `POST /api/users`, the workspace
  admin's only (anyone else is 403 `FORBIDDEN`): list the local accounts (login, name, role,
  disabled) and add a member, whose generated password the answer carries once; disable, password
  reset and role changes stay with `batondeck-kanban user`. Settings shows the Users panel to the
  workspace admin in place of the WorkOS invitation panel, and `web/src/api/tenants.ts` is gone. A
  disabled user's open session is 401 within the 60 s account-cache window.
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
  `media`, `feed`, `stream`, and `tenants` for the four unmounted invitation calls Settings still
  makes), `http.ts` holds `req`, `ApiError` and the local-edit marks, and every screen imports its
  areas directly. The studio, admin, billing, tenant-switch and onboarding functions, which no kept
  screen called, are dropped with the routes.
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
  lookup (`web/src/__tests__/oncall.test.tsx`).
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
  `delete_chronicle_page` (tier write, grant-governed), and the six chronicle resource templates
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
- `test/perf/queries_per_call_test.go`: `get_task` 2, `list_tasks` 2, `get_board` 2, `claim_next` 4
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
  handing the blobs unreferenced for an hour to the same collector (the sweep is not yet run by
  `serve`).

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
