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
  and 0 errors, or the bench fails. p50 and p95 are those of the median of 3 rounds, each round on a
  fresh server. On SQLite the claims queue on the one write connection, so p95 is about the time
  the 48th claim waits for the 47 before it, and the wall time divided by 50 is one claim's cost.
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
<!-- bench:end -->

License: Apache-2.0 (`LICENSE`, `NOTICE`).
