# batondeck-kanban

An open-source, self-hosted BatonDeck: one Go binary that serves the MCP task orchestrator (agents claim,
complete and are woken on dependency-gated tasks) and the embedded React UI for the humans on the board.

Status: pre-alpha, being built. The plan of record is `docs/release/plan-2026-10-22.md`.

Build: `make build` (Go from `go.mod`), then `./batondeck-kanban version`. `make hooks` installs the
git hooks, and `make check` is the gate they run.

## Performance

`make bench` measures the three numbers the plan pins (`docs/release/plan-2026-10-22.md`
§Performance), on SQLite and, with `BATONDECK_TEST_POSTGRES_DSN` set, on Postgres. It is not part of
`make check`, and it runs without `-race`, which inflates the latencies about fifteen times.

- **Claim race:** 50 agents, each its own MCP session (the go-sdk client over Streamable HTTP, with
  an API token), call `claim_next` at once on one board of 50 TODO tasks: exactly 50 distinct winners
  and 0 errors, or the bench fails. p50 and p95 are the median round's of 5, each on a fresh server,
  after a warm-up round that is dropped. The 50 calls arrive together and SQLite has one write
  connection, so they queue: the latencies rise evenly from the first answer to the last, p95 is
  mostly waiting, and the wall time divided by 50 is one claim's share.
- **Idle cost:** 200 device-directory entries and 10 `/api/stream` clients held for 60 s with no
  write. The directory's 60 s sweep re-reads every entry's token, user and memberships, so it may
  cost at most 3 statements per entry; any other statement in the hold fails the bench. The count is
  the engine's own (every statement it issues, from any goroutine), not a per-request one.
- **Statement pins:** the six hot tools' statements, measured at the service method as `make perf`
  pins them, in three cache states: with the board snapshot cached (the steady state), with it
  dropped (what an agent pays on the call after its own write, such as `claim_next` after
  `complete_task`) and on a fresh core (no membership cached either: a process's first call). `pin`
  is the ceiling `make perf` holds in `make check`.

The gate compares a run with `bench/baseline.json` measured on the same machine (model, CPU and
cores; on another machine it says so and compares nothing): a latency (p50, p95) fails beyond
+20 %, and a statement count fails on any rise, since +20 % of a count of 6 would let a seventh
statement through. `make bench-baseline` rewrites the baseline and the block below from a run.

<!-- bench:begin: make bench-baseline writes this block from bench/baseline.json; make bench checks it -->
Measured by `make bench` on Mac14,5: Apple M2 Max, 12 cores, 32 GiB, macOS 27.0.1 (26A434) arm64; go1.26.9.

| Number | sqlite | postgres | Gate |
|---|---:|---:|---|
| claim race winners (of 50) | 50 | 50 | recorded |
| claim race p50 | 21.5 ms | 50.2 ms | fails beyond +20 % |
| claim race p95 | 32.4 ms | 60.3 ms | fails beyond +20 % |
| claim race max | 33.4 ms | 61.3 ms | recorded |
| claim race wall | 33.9 ms | 61.6 ms | recorded |
| idle: directory entries | 200 | 200 | recorded |
| idle: /api/stream clients | 10 | 10 | recorded |
| idle: hold | 60.0 s | 60.0 s | recorded |
| idle: statements per entry per sweep | 3 | 3 | fails on any rise |
| idle: statements between sweeps | 0 | 0 | fails on any rise |
| get_task: snapshot cached | 3 | 3 | fails on any rise |
| get_task: snapshot dropped | 3 | 3 | fails on any rise |
| get_task: fresh core | 4 | 4 | fails on any rise |
| get_task: pin | 3 | 3 | recorded |
| list_tasks: snapshot cached | 1 | 1 | fails on any rise |
| list_tasks: snapshot dropped | 1 | 1 | fails on any rise |
| list_tasks: fresh core | 2 | 2 | fails on any rise |
| list_tasks: pin | 2 | 2 | recorded |
| list_tasks missingArtifacts: snapshot cached | 2 | 2 | fails on any rise |
| list_tasks missingArtifacts: snapshot dropped | 2 | 2 | fails on any rise |
| list_tasks missingArtifacts: fresh core | 3 | 3 | fails on any rise |
| list_tasks missingArtifacts: pin | 2 | 2 | recorded |
| get_board: snapshot dropped | 2 | 2 | fails on any rise |
| get_board: snapshot cached | 0 | 0 | fails on any rise |
| get_board: fresh core | 3 | 3 | fails on any rise |
| get_board: pin | 2 | 2 | recorded |
| get_task_context: snapshot cached | 6 | 6 | fails on any rise |
| get_task_context: snapshot dropped | 8 | 8 | fails on any rise |
| get_task_context: fresh core | 9 | 9 | fails on any rise |
| get_task_context: pin | 6 | 6 | recorded |
| next_task: snapshot cached | 2 | 2 | fails on any rise |
| next_task: snapshot dropped | 2 | 2 | fails on any rise |
| next_task: fresh core | 3 | 3 | fails on any rise |
| next_task: pin | 3 | 3 | recorded |
| claim_next: snapshot cached | 4 | 5 | fails on any rise |
| claim_next: snapshot dropped | 6 | 7 | fails on any rise |
| claim_next: fresh core | 7 | 8 | fails on any rise |
| claim_next: pin | 4 | 5 | recorded |

The plan's target, claim race p95 < 25 ms on local SQLite: **missed** (32.4 ms).
<!-- bench:end -->

License: Apache-2.0 (`LICENSE`, `NOTICE`).
