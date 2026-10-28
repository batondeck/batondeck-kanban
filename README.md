# batondeck-kanban

An open-source, self-hosted BatonDeck: one Go binary that serves the MCP task orchestrator (agents claim,
complete and are woken on dependency-gated tasks) and the embedded React UI for the humans on the board.

Status: 0.1.0, the first release (`CHANGELOG.md`). The plan of record is
`docs/release/plan-2026-10-22.md`.

Build from source: `make build` (Go from `go.mod`), then `./batondeck-kanban version`. `make hooks`
installs the git hooks, and `make check` is the gate they run.

## Quickstart

A board on this machine, a Claude Code session working it, and optionally a claude.ai connector.
`you@example.com` stands for your login throughout.

### 1. Install

**The binary.** Each release carries one binary per platform (linux and darwin, amd64 and arm64) and
`SHA256SUMS`; `RELEASING.md` says how to rebuild one and compare.

```bash
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m); case $arch in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac
base=https://github.com/batondeck/batondeck-kanban/releases/download/v0.1.0
curl -fL -O "$base/batondeck-kanban_0.1.0_${os}_${arch}" -O "$base/SHA256SUMS"
shasum -a 256 -c SHA256SUMS --ignore-missing   # or: sha256sum -c SHA256SUMS --ignore-missing
chmod +x "batondeck-kanban_0.1.0_${os}_${arch}"
sudo mv "batondeck-kanban_0.1.0_${os}_${arch}" /usr/local/bin/batondeck-kanban
batondeck-kanban version
```

Create the store, then run the server. With no `BATONDECK_DATABASE_URL` the SQLite file, the blobs
and `master.key` live in `$XDG_DATA_HOME/batondeck-kanban`, else `~/.local/share/batondeck-kanban`.
The listen list below adds the Unix socket the plugin's hooks use (step 4; `serve` makes its
directory, mode 0700); the web UI and `/mcp` are on `http://localhost:8080`.

```bash
export BATONDECK_LISTEN=tcp://127.0.0.1:8080,unix://$HOME/.batondeck/bk.sock
batondeck-kanban migrate
batondeck-kanban serve          # leave it running; the next steps are in another terminal
```

**Or the image**, on Postgres 16, with `compose.yaml` from this repository. On Postgres there is no
`master.key` file beside a SQLite file, so the key comes from `.env`; compose refuses to start
without it.

```bash
printf 'BATONDECK_MASTER_KEY=%s\n' "$(openssl rand -base64 32)" > .env
docker compose up -d
curl -fsS http://127.0.0.1:8080/healthz     # batondeck-kanban 0.1.0 ok postgres
```

In the steps below, run `batondeck-kanban …` as `docker compose exec -T batondeck-kanban
batondeck-kanban …`. The image serves the web UI, `/mcp` and pull clients; the plugin's device wake
(step 4) needs the binary on the machine the Claude Code sessions run on, because the server watches
each registered session's process and writes into that session's own socket.

### 2. The first admin and a token

There is no sign-up page: accounts are made on the host. The password (12 characters or more) is
read from stdin, never from a terminal, so it is not echoed:

```bash
read -rs PW; printf '%s\n' "$PW" | batondeck-kanban user add --admin you@example.com; unset PW
batondeck-kanban token create --user you@example.com
```

`token create` prints the token (`bd_…`) once, on stdout; it lives 30 days unless `--ttl` says
otherwise. The same login and password sign in to the web UI at `http://localhost:8080`, where
Settings → CLI tokens makes more.

### 3. Claude Code

```bash
claude mcp add --transport http batondeck-kanban http://localhost:8080/mcp --header "Authorization: Bearer <token>"
```

`/mcp` in a Claude Code session then lists `batondeck-kanban` with the board's tools. A client with
no token is answered 401.

### 4. The plugin: a session is rung when work arrives

`plugin/` is the Claude Code plugin. Its SessionStart hook registers the session in the server's
device directory (`batondeck-kanban session register`, over the Unix socket from step 1) and its
SessionEnd hook removes it, so the server rings the session over its own messaging socket when a task
is assigned to it or becomes claimable, and Claude Code starts a turn, even in an idle session.

```bash
git clone https://github.com/batondeck/batondeck-kanban.git
export BATONDECK_TOKEN=<token>
export BATONDECK_LISTEN=tcp://127.0.0.1:8080,unix://$HOME/.batondeck/bk.sock
claude --plugin-dir batondeck-kanban/plugin
```

The plugin brings its own MCP entry for the same server (`plugin:batondeck-kanban:batondeck-kanban`,
with the bearer from `BATONDECK_TOKEN`), so a session started with it does not need step 3's
(`claude mcp remove batondeck-kanban` drops that one). `BATONDECK_ROLE=master` registers a master
session, which is rung when a task in its projects changes. `plugin/README.md` has the rest: what
holds a doorbell, and what the hooks may do.

### 5. Every other client pulls each turn

Sessions off this device (claude.ai, ChatGPT, Cursor, Codex CLI, Gemini CLI, or Claude Code without
the plugin) are never rung. They pull: each turn, `list_notifications`, then
`claim_next {projectId, boardId}`; the server's MCP instructions say so to every client. The web UI's
**Connect MCP** page shows the setup for Claude Code, Codex CLI, Gemini CLI and Cursor.

### 6. The claude.ai connector

**Prerequisite: the server at a public HTTPS URL.** claude.ai reaches a connector from the
internet, so put a TLS reverse proxy in front of `127.0.0.1:8080` and give the server that URL:

```bash
export BATONDECK_PUBLIC_URL=https://board.example.com
```

With `public_url` set, `serve` also runs the OAuth 2.1 authorization server: `/mcp` answers a client
with no token 401 and the RFC 9728 metadata that leads it to dynamic client registration, the
board's sign-in page, a consent page (worker preselected) and a token. In claude.ai, add a custom
connector (Settings → Connectors) with the URL `https://board.example.com/mcp`, then sign in with the
login from step 2 and allow. That flow has been run end to end with `mcp-remote`; it has not been
tried from claude.ai itself.

### The database: SQLite or Postgres

`BATONDECK_DATABASE_URL` (or `database.url` in a JSON config file named by `-config` or
`BATONDECK_CONFIG`) picks the engine by its form:

| Form | Engine |
|---|---|
| a file path, such as `/srv/batondeck/batondeck-kanban.db` (relative paths are relative to the working directory) | SQLite, the default |
| `postgres://user:password@host:5432/dbname?sslmode=disable` (or `postgresql://…`) | Postgres |

SQLite runs with `synchronous=FULL`. `BATONDECK_DATABASE_SYNCHRONOUS=NORMAL` (`database.synchronous`)
opts into `NORMAL` for write throughput: in WAL mode the file stays consistent, but the last
transactions before a power loss may be lost. `batondeck-kanban serve -h` lists every
`BATONDECK_*` variable with its default, and how the config file and the environment combine.

### doctor

```bash
batondeck-kanban doctor
```

checks the config and names the engine the connection itself reports (`the connection reports:
SQLite 3.x`, or the Postgres server version), the store's location and the blob dir, and exits non-zero
on a failure.

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
