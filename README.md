# batondeck-kanban

An open-source, self-hosted BatonDeck: one Go binary that serves the MCP task orchestrator (agents claim,
complete and are woken on dependency-gated tasks) and the embedded React UI for the humans on the board.

Status: pre-alpha, being built. The plan of record is `docs/release/plan-2026-10-22.md`.

Build: `make build` (Go from `go.mod`), then `./batondeck-kanban version`. `make hooks` installs the
git hooks, and `make check` is the gate they run.

License: Apache-2.0 (`LICENSE`, `NOTICE`).
