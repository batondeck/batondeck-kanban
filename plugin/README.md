# batondeck-kanban plugin for Claude Code

The plugin connects a Claude Code session to a batondeck-kanban server and puts the session in the
server's device directory, so the server can start a turn in it when there is work.

- **`.mcp.json`**: the board's MCP tools, at `http://localhost:8080/mcp`, with the bearer from
  `BATONDECK_TOKEN`.
- **`hooks/hooks.json`**: two hooks, both `hooks/session.sh`. SessionStart registers the session
  (`batondeck-kanban session register`); SessionEnd removes it (`batondeck-kanban session unregister`).
- **`skills/`**: `batondeck-worker` (plan and work the board) and `batondeck-chronicle` (sweep DONE tasks
  into decision records).
- **`commands/`**: `/batondeck-kanban:worker`, `:master`, `:work`, `:work-assigned`, `:runs`,
  `:chronicle`.
- **`scripts/mcp.sh`**: one tool call from a shell, with the same token.

`TRIM.md` records which of conductor's plugin files this plugin rewrote and which it dropped, and why.

## Set up

1. Run the server with a Unix socket in its listen list: the session directory is served only there
   (mode 0600), never over TCP.

   ```bash
   export BATONDECK_LISTEN=tcp://127.0.0.1:8080,unix://$HOME/.batondeck/bk.sock
   batondeck-kanban serve
   ```

   A config file works as well: `listen` holds the same two entries, and `BATONDECK_CONFIG` names the
   file.
2. Mint a CLI token for the user the session works as. It is printed once:

   ```bash
   batondeck-kanban token create --user you@example.com
   ```

3. In the shell you start `claude` from, export what the hooks and `.mcp.json` read:

   | Variable | Read by | Value |
   |---|---|---|
   | `BATONDECK_TOKEN` | `.mcp.json` (the `Authorization` header) and both hooks | the token from `token create` |
   | `BATONDECK_LISTEN` or `BATONDECK_CONFIG` | both hooks | the same listen list or config file the server runs with; the hooks use its first `unix://` entry |
   | `BATONDECK_ROLE` | SessionStart | `worker` (the default) or `master` |
   | `BATONDECK_AGENT` | SessionStart | optional: an agent label; a task assigned to that label rings this session. It must be this token's own label (`token create --label`) or a name no other account's token and no live token of your own carries (your own revoked or expired label included); the server refuses another account's label (revoked or expired tokens included), a live label of your own account that this token does not carry, and `id:<identity>` for any identity but this token's own, and the session then starts without a wake entry |

   `batondeck-kanban` must be on `PATH`.
4. Start Claude Code with the plugin loaded for that session:

   ```bash
   claude --plugin-dir /path/to/batondeck-kanban/plugin
   ```

   `/mcp` lists `plugin:batondeck-kanban:batondeck-kanban` with the server's tools.

## How a session is woken

The SessionStart hook registers the session's process with the server, as a worker or a master
(`BATONDECK_ROLE`). The server then writes a doorbell into the session's own messaging socket
(`CLAUDE_CODE_MESSAGING_SOCKET`), and Claude Code starts a turn with it, even when the session is idle:

- a **worker** is rung when a task is assigned to it or to its agent label, and when unassigned work
  becomes claimable in its projects;
- a **master** is rung when a task in its projects changes, except for claims, heartbeats, notes and
  its own changes.

The doorbell carries a kind, a cursor, a project id and the board (`boardId`) or boards (`boardIds`)
its changes name (neither: look at every board), nothing else; the session reads the board with
`claim_next` and `list_notifications`. Several changes inside a short window arrive as one doorbell.

Two things that stop a doorbell from starting a turn:

- **Bypass mode.** A session started with `--dangerously-skip-permissions` holds the doorbell unless it
  was also started with `--settings '{"crossSessionInbound":"accept"}'` (measured on Claude Code
  2.1.309, `docs/release/socket-wake-spike-2026-10.md` §3.4). A `crossSessionInbound` of `hold` or
  `refuse` holds or drops it in any mode.
- **No registration.** The hooks fail quietly by design (one line on stderr, exit 0, so a session
  always starts): with no `BATONDECK_TOKEN`, no `unix://` listen entry, the server stopped, or the binary
  missing from `PATH`, the session starts unregistered and is never rung. `claude --debug-file F`
  shows the hook's stderr line in `F`.

Sessions off this device (claude.ai, ChatGPT, Cursor) are never rung: they pull each turn.

## When the session ends

On `/exit` the SessionEnd hook runs `session unregister`, which may not find the session: on Claude
Code 2.1.309 the hook's environment no longer carries `CLAUDE_CODE_MESSAGING_SOCKET`, so the command
cannot name the session's process and the server answers 404 (one stderr line). **The server removes
the entry anyway**: it watches every registered process and drops its entry the moment the process
exits (measured: within 1 s of `/exit`).

## What the hooks can do

`hooks/session.sh` runs one command, `batondeck-kanban session register` or `session unregister`, which
talks to the server over its Unix socket and nowhere else. It writes nothing to stdout (a SessionStart
hook's stdout is added to the model's context) and always exits 0. See `SECURITY.md`.
