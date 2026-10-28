# ADR-0004 Tool trim and device-local notifications

- Status: accepted
- Date: 2026-10-23
- Raised by: the plan (Amendment 2026-10-23, sections A and B), after two read-only analyses the owner asked for: the tool-redundancy report (`mcp-trim-report.md`) and the wake-path reconciliation (`wake-reconcile.md`)
- Deciders: the owner (2026-10-23, in chat) / the orchestrator

## Context
The owner's words, verbatim:
- Tools: "for tools analysis, I agree with results, go with trimmed version".
- Notifications: "let's drop Channel for now and use socket which is proven to work across the space. so for notifications only socket for clis and for web SSE, nothing else".
- The directory: "for workers, we need to keep map/directory of process pid to which we need to send by socket when task is assigned / state is updated. now from being online we're shifting being on device which gives us advantages to deal with all clients running on the device. the map should update in realtime whenever we start / stop session / master and worker client processes".

Measured facts:
- Tool surface. The pinned surface is 93 tools (with `cancel_task`, ADR-0001). Descriptions are 40.2% of the model-facing payload (38,450 of 95,588 bytes). A 200-character cap saves about 6.7k tokens, more than every tool removal combined (about 2.2k). The 9 oncall configuration tools cost about 4.5k tokens on every non-admin `tools/list`.
- Notifications. The plan carried five wake paths: the channel subcommand (A), the session-inbox socket or asyncRewake (A2), MCP `resources/subscribe` push (B), `/wake` SSE over TCP or socket (C), and a `wait` command. `internal/bus` delivers only by recipient scope (`bus.go:595-603`), so nothing can follow a project. `GET /wake` merged in #27.

## Decision
**A. The tool surface is trimmed.**
- Seven tools are dropped: the router facade (`find_tools`, `get_tool_schema`, `invoke_tool`); the CLI-token tools (`create_cli_token`, `list_cli_tokens`, `revoke_cli_token`), which the `token` subcommand and the REST settings page cover; and `prune_agent_sessions`, because sessions are garbage-collected after 30 minutes idle. `expected.json` goes from 93 to 86, and the 7 are added to `deltas.json` `removedTools`.
- The dead Pub/Sub output fields `wakeRevoked` and `wakeRevokeFailed` are deleted.
- Every tool description is at most 200 characters, with conductor-only text stripped, enforced by a registry test.
- The 9 oncall configuration tools are admin-only in `tools/list`.
- Filtering stays per workspace-admin flag, as R1.1 built it.
- Not trimmed: `rank_tasks`, `add_subtask`, `recall_memory`/`read_memory`, `open_run`, the claim and get variants, the column tools, and `delete_task`/`cancel_task`. Each saves at most 331 tokens, or differs in tier, lease or output.

**B. Notifications are device-local, with exactly two transports off one source.**
- The source is `change_log` plus `internal/bus`, which gains a project subscription and an all-rows tap.
- Transport 1 is a socket push to CLI sessions on the device. The server keeps a session directory, `wake_sessions` plus an in-memory pid → entry map. Clients register over the server's 0600 Unix socket: a SessionStart hook runs `batondeck-kanban session register`, and the server checks the peer's uid and pid. An entry is dropped the moment its process exits (kqueue on darwin, pidfd on Linux), on SessionEnd, or on a failed delivery. A dispatcher rings workers on assignment and masters on state changes in their projects. The doorbell is a contentless, coalesced `{kind, cursor, projectId}` frame written into the client's own socket (`CLAUDE_CODE_MESSAGING_SOCKET` for Claude Code). Claude Code is the first and only spike-proven adapter. Other device CLIs register but get no push until an adapter exists. Hosted connectors and Cursor pull each turn.
- Transport 2 is SSE to the web UI: `/api/stream`, fed by the project subscription, carrying board frames and presence frames from the directory.

**Dropped:** the channel subcommand and the channel half of the proof; the asyncRewake hook; MCP `resources/subscribe` push (resources stay readable, and `Subscribe:true` is no longer advertised); `/wake` over TCP and `GET /wake` itself; the `wait` command; the Stop hook.

## Consequences
- New tasks:
  - R0.23, the contract trim (A1–A3, A7);
  - R0.24, the bus project subscription and tap;
  - R0.25, the session directory and dispatcher, which also removes `GET /wake`.
- Amended tasks:
  - R3.1 becomes a socket-write spike on Claude Code 2.1.309, with a stop condition and no channel fallback;
  - R3.2 is dropped;
  - R3.3 becomes the `session register|unregister` client half;
  - R3.4 ships SessionStart/SessionEnd hooks only;
  - R3.5 re-pins idle cost to ≤ 3 statements per directory entry per 60 s sweep;
  - R1.7 drops the subscribe push;
  - R1.11 depends on R0.24 and adds presence frames;
  - R2.4 loses the router facade;
  - R2.5 loses the CLI-token tools and `prune_agent_sessions`;
  - R2.8's oncall configuration tools are admin-only.
- Proposals:
  - P-100 (the snapshot cache on the tap) is promoted;
  - P-121 and P-123 are dropped, because the directory supersedes them;
  - P-122 is dropped, because #27 fixed it;
  - P-109 shrinks to starting the retention sweeper.
- The plan-of-record edits ride R0.23's PR (A) and R0.25's PR (B).
- Easier: one event source and two transports to reason about, test and secure. The tool payload is about 44% smaller for non-admins.
- Harder: wake depends on an undocumented Claude Code socket payload. R3.1 must prove it, and if it fails the work stops for the owner rather than falling back. Sessions off the device (claude.ai, ChatGPT, Cursor) are never woken.

## Alternatives considered
- Keep the channel subcommand (path A): it needs a development flag or allowlisting, and a protocol-negotiation pin. The owner dropped it.
- asyncRewake: its idle-only semantics are unconfirmed, and it is one more path. Dropped.
- MCP `resources/subscribe` push: it is unconfirmed that Claude Code subscribes, and a push over the MCP session duplicates the socket. Dropped.
- Keep `GET /wake` as a socket-only diagnostic: it is a second wake surface to secure. It is removed (plan open question 2, default).
- Trim more tools: each further candidate saves at most 331 tokens, and two touch merged code.
