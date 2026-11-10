# batondeck-kanban plugin: security model

Everything below is a claim about the files in this directory. Check it on the copy you load:

```bash
cat hooks/hooks.json                       # the complete hook registration
python3 -c 'import json; print(sorted(json.load(open("hooks/hooks.json"))["hooks"]))'
# → ['SessionEnd', 'SessionStart']
grep -rn "curl\|wget" hooks/          # → nothing
```

`hooks/hooks.json` is the only hook file. Claude Code loads it by convention; `plugin.json` declares no
`hooks` key (declaring it loads the file twice).

## The complete hook surface: two hooks

| Hook | Runs | What it does | Timeout |
|---|---|---|---|
| `SessionStart` | once, when a session starts | `hooks/session.sh start` runs `batondeck-kanban session register --role "${BATONDECK_ROLE:-worker}"` (plus `--agent "$BATONDECK_AGENT"` when set) | 5 s |
| `SessionEnd` | once, when a session ends | `hooks/session.sh end` runs `batondeck-kanban session unregister` | 5 s |

No other event is registered, so the plugin **cannot**:

- see, rewrite or append to your prompts (`UserPromptSubmit`),
- intercept, block or approve a tool call, or bypass a permission prompt (`PreToolUse`),
- change a tool's result (`PostToolUse`),
- keep a session from ending its turn (`Stop`).

`hooks/session.sh` sends the binary's stdout to stderr and always exits 0: it adds nothing to the
model's context and never stops a session from starting.

## What `session register` sends, and to whom

- **Where:** the server's Unix socket only (the first `unix://` entry of `BATONDECK_LISTEN` or of the
  config file `BATONDECK_CONFIG` names). The socket is mode 0600, so only your uid can connect; the
  client has no TCP path at all, and the server serves the directory only on the socket. The client
  reads the uid of the process serving the socket from the kernel and sends the bearer only to one
  running as your uid: a socket another local user bound first gets one stderr line and no request.
- **What:** the session's pid, its role and agent label, and from Claude Code's own session registry
  (`~/.claude/sessions/<pid>.json`) its messaging socket path, session id and start time.
- **Credential:** `BATONDECK_TOKEN` as the bearer. It is not the session's `CLAUDE_CODE_MESSAGING_TOKEN`,
  which the client never sends.
- **The server checks** the bearer, that the pid is live and owned by the connecting uid, and that the
  registry entry names the socket the client claims.
- **The server keeps** that bearer for the life of the entry: in its database (its wake-session
  table), sealed with its keyring (`BATONDECK_MASTER_KEY`, or the `master.key` file that by default sits
  beside the SQLite file), so it can re-check the token's access after a restart and at least once a
  minute. The entry and its sealed copy go when the session ends (SessionEnd), when its process exits,
  when a doorbell finds its registry entry or socket gone, or when the token is revoked.

## What the server writes into the session

A doorbell: one line of text that names a kind (`assigned`, `broadcast` or `changed`), a cursor and a
project id, and asks the session to call `claim_next`, then `list_notifications`. It carries no task
title, note or other text someone else wrote, and it grants nothing: the session reads the board
through the MCP tools, which apply every membership and role check. Claude Code shows it as a message
from another session, and its own peer-message rules apply (`crossSessionInbound`).

To write it, the server reads the session's peer token from Claude Code's session registry on this
machine (`<pid>.<sha256 of the socket path>.key` beside `~/.claude/sessions/<pid>.json`, or under
`$CLAUDE_CONFIG_DIR/sessions`) and sends it as the auth line of each doorbell it writes into that
session's socket. It reads the token afresh for each doorbell, and never stores, logs or sends it
anywhere else.

## Network, credentials, data

- `.mcp.json` sends `Authorization: Bearer ${BATONDECK_TOKEN}` to `${BATONDECK_URL:-http://localhost:8080}/mcp`:
  the bearer goes to whatever server `BATONDECK_URL` names in the shell `claude` starts from. The file
  holds no literal token. Revoking the token (`batondeck-kanban token revoke`) ends both the MCP access
  and the session's directory entry; the server re-reads every entry's bearer at least once a minute.
- `scripts/mcp.sh` sends the same header to `${BATONDECK_URL:-http://localhost:8080}/mcp` and writes no
  file.
- Nothing in this directory writes to disk.

## Reporting

Report a vulnerability through the repository's GitHub security advisories
(`https://github.com/batondeck/batondeck-kanban/security/advisories/new`), not a public issue.
