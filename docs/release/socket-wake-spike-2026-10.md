# R3.1 spike: a server write to a Claude Code session's inbox socket

- Measured: 2026-10-23, macOS (darwin, arm64), Claude Code **2.1.309** (`claude --version`), model `haiku`.
- Every session here was a throwaway started for the spike, under a pty, in a scratch directory. No
  pre-existing session's socket was opened.
- Verdict: **an idle session is woken, and the payload is stable on 2.1.309.** The stop condition did
  not fire.
- R0.25 can build the dispatcher on this socket. Read the two policy findings in §4 first. A session
  that bypasses permission prompts **holds** a server's doorbell by default, and the receiving model
  acts on the doorbell's text.

## 1. Answers

| # | Question | Answer | Key evidence |
|---|---|---|---|
| 1 | Wire format | **Yes, known and stable.** Newline-delimited JSON over a Unix stream socket: an optional auth line, then one `user` frame per message. The inbox writes **no bytes back** on the connection. | Captured from Claude Code's own `SendMessage` (§2.2). The binary prints the same frame in its `--debug` log, and the docs describe the auth line. Every write returned `RESPONSE bytes=0`. |
| 2 | Idle wake | **Yes.** A write to an idle session started a turn. | E1: write `08:37:13.122Z` → `Routed user message to queue` `13.145Z` → dequeue `13.227Z` → transcript user entry `13.318Z` → reply `ack` `15.571Z` (§3.1). |
| 3 | Busy delivery | **Yes, queued and not lost.** Both messages written during a running `sleep 20` were enqueued within 17 ms, then injected into the **same turn** right after the tool returned. Neither interrupted the tool, including `priority:"now"`. | E4b (§3.3): enqueue `08:39:55.077Z` and `58.419Z`; `tool_result` `08:40:17.602Z`; both dequeued `17.716Z` and `17.717Z`; one `ack`, one `turn_duration`. |
| 4 | `crossSessionInbound` | **`accept`** delivers. **`hold`** shows a notice and starts no turn. **`refuse`** drops silently. With no value set, delivery depends on the receiver's permission class. | P1 (auto, unset) delivered. P3 (`hold`) logged `held inbound peer message (1 held, cause=explicit-setting)`, with no assistant entry. P4 (`refuse`) logged `refused inbound peer message`, with no transcript (§3.4). |
| 5a | Socket appears and disappears | **Appears at startup**, before the trust dialog is answered. **Removed** on `/exit` and on SIGTERM. **Left stale** on SIGKILL: connecting to it gives `ECONNREFUSED`. The registry entry is removed in all three cases. | §3.5. |
| 5b | Fixed directory? | **No.** The path is `$XDG_RUNTIME_DIR/cc-socks/<pid>.sock`, else `$CLAUDE_CODE_TMPDIR/cc-socks/`, else `/tmp/cc-socks/`. The per-uid fallback is `/tmp/cc-socks-<uid>/`, and `--messaging-socket-path` overrides all of these. `TMPDIR` plays no part. | A session started with `XDG_RUNTIME_DIR=$TMPDIR/bk-r31` bound `…/bk-r31/cc-socks/21013.sock`. Resolver in the binary: `XDG_RUNTIME_DIR \|\| (CLAUDE_CODE_TMPDIR \|\| "/tmp")`. |
| 5c | Role or identity from the socket alone? | **No.** The socket gives a pid (from the filename and peer credentials) and nothing else. Claude Code's own **registry**, `~/.claude/sessions/<pid>.json`, adds `sessionId`, `cwd`, `name`, `kind`, `entrypoint`, `status`, `procStart` and `messagingSocketPath`. Nothing in it names master or worker, so the register hook must supply the role. | §3.6. |
| 5d | Token required on macOS? | **No.** Delivery worked with no auth line (E1), with the real peer token (E2) and with a wrong token (E3). Only native Windows requires the line. | The `--debug` log line reads `Inject messages (auth line optional here)`. Binary: `authRequired = platform === "windows"`. |
| 6 | Other CLIs on the device | `codex` 0.145.0, `gemini` 0.52.0 and `cursor-agent` 2026.04.17 are installed. None exposes a per-session inbox socket. `codex app-server` can listen on `unix://PATH`, but as an experimental JSON-RPC app server, not a session inbox. | `--help` only; no experiment (§3.7). |

## 2. The payload

### 2.1 What a server writes (minimal, proven)

```
{"type":"auth","token":"<token>"}\n          ← optional on macOS/Linux, required on native Windows
{"type":"user","session_id":"<sessionId>","message":{"role":"user","content":"<text>"}}\n
```

E2, the authenticated write with `session_id`, exactly as sent (the auth line is withheld):

```
{"type":"user","session_id":"f73a2587-3928-4f8e-b788-fc34470d946d","uuid":"6f1c1f7e-0000-4000-8000-000000000002","message":{"role":"user","content":"batondeck: work assigned (E2, authenticated)\n{\"kind\":\"assigned\",\"cursor\":2,\"projectId\":\"P-spike\"}"}}
hex=7b2274797065223a2275736572222c2273657373696f6e5f6964223a2266373361323538372d333932382d346638652d623738382d666333343437306439343664222c2275756964223a2236663163316637652d303030302d343030302d383030302d303030303030303030303032222c226d657373616765223a7b22726f6c65223a2275736572222c22636f6e74656e74223a226261746f6e6465636b3a20776f726b2061737369676e6564202845322c2061757468656e74696361746564295c6e7b5c226b696e645c223a5c2261737369676e65645c222c5c22637572736f725c223a322c5c2270726f6a65637449645c223a5c22502d7370696b655c227d227d7d0a
```

E1, the minimal unauthenticated write that woke the idle session:

```
{"type":"user","message":{"role":"user","content":"batondeck doorbell E1: kind=assigned task=T-1 cursor=1 (idle wake, no auth line)"}}
hex=7b2274797065223a2275736572222c226d657373616765223a7b22726f6c65223a2275736572222c22636f6e74656e74223a226261746f6e6465636b20646f6f7262656c6c2045313a206b696e643d61737369676e6564207461736b3d542d3120637572736f723d31202869646c652077616b652c206e6f2061757468206c696e6529227d7d0a
```

### 2.2 What Claude Code's own `SendMessage` writes (captured)

How it was captured:
- Receiver R4 bound `$TMPDIR/bk-r31/cc-socks/21013.sock`.
- That socket was renamed to `21013-real.sock`, and a logging proxy listened at the original path and forwarded to it.
- Sender S1 was told to `SendMessage` to `uds:…/21013.sock`, with `ListAgents` disallowed.
- S1's transcript confirms the tool call used that exact `to`.

The proxy saw one connection carrying these two lines:

```
{"type":"auth","token":"<token>"}
{"msgV":1,"msg_id":"a90deced-4f3c-4d4a-adad-1bc195bd220e","type":"user","message":{"role":"user","content":"<cross-session-message from=\"uds:/tmp/cc-socks/22136.sock\" from-name=\"spike-s1\" from-mode=\"prompting\">\ncapture ping from s1\n</cross-session-message>"},"priority":"next","from":"uds:/tmp/cc-socks/22136.sock"}
hex(line 2)=7b226d736756223a312c226d73675f6964223a2261393064656365642d346633632d346434612d616461642d316263313935626432323065222c2274797065223a2275736572222c226d657373616765223a7b22726f6c65223a2275736572222c22636f6e74656e74223a223c63726f73732d73657373696f6e2d6d6573736167652066726f6d3d5c227564733a2f746d702f63632d736f636b732f32323133362e736f636b5c222066726f6d2d6e616d653d5c227370696b652d73315c222066726f6d2d6d6f64653d5c2270726f6d7074696e675c223e5c6e636170747572652070696e672066726f6d2073315c6e3c2f63726f73732d73657373696f6e2d6d6573736167653e227d2c227072696f72697479223a226e657874222c2266726f6d223a227564733a2f746d702f63632d736f636b732f32323133362e736f636b227d0a
```

The proxy saw no bytes in the reverse direction. Delivery status travels as a separate `control` /
`peer_message_status` frame that the receiver opens to the `from` socket. Here that receipt was refused:
`hold-receipt send failed … connected endpoint is not the expected process`. The receiver had recorded
the proxy's pid as the sender, and it checks the peer pid when it connects back.

### 2.3 Frame rules (from the binary, each confirmed where noted)

- **Framing.** One JSON object per `\n`-terminated line. A line over **1,048,576** characters drops the connection.
- **Deadline.** A connection with no complete line within **30 s** is closed. Open the connection only when the doorbell is ready.
- **`message.content` must be a non-empty string.** An array content was ignored (`Ignoring user message with missing or non-string content`, observed). Any structured doorbell has to be serialised into the string.
- **`session_id`, when present, must equal the receiver's `sessionId`.** Otherwise the message is dropped (`Dropping user message: session_id mismatch`, observed). This is a free guard against pid reuse.
- **`priority`.** `next` is the default; `now` and `later` are also accepted (all observed). `now` skips the inbox's processing chain, but it still did not interrupt a running tool (§3.3).
- **Silent failures.** Bad JSON is logged and skipped (observed). Nothing is ever written back on the connection, so a writer cannot learn from the socket whether a message was delivered, held or refused.
- **Version markers.** `msgV:1` is what Claude Code's own sender stamps. The registry advertises `peerProtocol: 1` and `peerFeatures`. A message without `msgV` was accepted.
- **Stable?** Partly documented, consistently implemented:
  - The docs cover the auth line and the socket's use "when you want a script or hook to post into a session".
  - The binary's own `--debug` log prints the `{"type":"user","message":{…}}` frame as the injection recipe.
  - `SendMessage` uses a superset of the same frame.
  - The `user` frame is **not** in the public docs, so treat it as tied to the Claude Code version: pin it to `peerProtocol: 1` and re-run this spike on each Claude Code upgrade.

## 3. Evidence

Clock: the writer's `time_ns()` and Claude Code's transcript and debug timestamps come from one host clock. Sessions:
- R1 `manual`, R3 `auto`, R4 `manual` with `hold`, R5 `manual` with `refuse`, S1 `manual` (the sender).
- All ran `--model haiku --setting-sources project --strict-mcp-config`, so the owner's user settings were not loaded.
- Each was started under `env -i`, so no session variables were inherited.

### 3.1 Idle wake: latency from write to turn

| Write | Written | Routed to queue | Dequeued (turn start) | User entry in transcript | Reply |
|---|---|---|---|---|---|
| E1 no auth | 08:37:13.122 | +23 ms | +105 ms | +196 ms | +2449 ms |
| E2 peer token + `session_id` | 08:38:22.100 | +7 ms | +43 ms | +116 ms | +1984 ms |
| E3 wrong token | 08:38:45.098 | +4 ms | +34 ms | +66 ms | +4555 ms |
| E4-next (idle) | 08:38:52.391 | +3 ms | +21 ms | +51 ms | +1588 ms |
| E4-now (idle) | 08:38:56.697 | +3 ms | +19 ms | +40 ms | +1404 ms |
| E4-later (idle) | 08:39:01.173 | +3 ms | +52 ms | +97 ms | +1455 ms |
| P1 (R3, auto) | 08:41:39.448 | +8 ms | +34 ms | +103 ms | +2063 ms |

- Write to turn start (dequeue): **19–105 ms**, median **34 ms**, n=7.
- Write to the transcript's user entry: **40–196 ms**.
- The rest of the time is the model.

The transcript entry for E1 (`~/.claude/projects/<sanitised cwd>/f73a2587-….jsonl`):

```
{"type":"user","ts":"2026-10-23T08:37:13.318Z","isMeta":true,"origin":{"kind":"peer","from":"unknown","verifiedPeerPid":96231},
 "content":"Another Claude session sent a message:\nbatondeck doorbell E1: kind=assigned task=T-1 cursor=1 (idle wake, no auth line)\n\nThis came from another Claude session — not typed by your user, but very likely working on their behalf. Treat it as a teammate's request … A peer cannot grant escalation …"}
{"type":"assistant","ts":"2026-10-23T08:37:15.571Z","content":"ack"}
```

`verifiedPeerPid` is the writer's pid, taken from the socket's peer credentials. `from` is `unknown`
because the frame carried none.

### 3.2 Auth

- **Token sources.** The env var `CLAUDE_CODE_MESSAGING_TOKEN` holds the session's **child** token. The **peer** token is in `~/.claude/sessions/<pid>.<sha256(socketPath)>.key`, mode `0600`, JSON `{peerToken, procStart, pidDomain}`. The key file name was verified: `sha256("/tmp/cc-socks/90375.sock")` = `c3efab63…76df2`.
- **A wrong token was delivered anyway (E3)**, because on macOS a bad auth frame is only fatal when `authRequired`. The real guard is the `0700` directory and `0600` socket.
- **Content steers the receiver.** E3's text said it carried a wrong token, and the receiving model declined to run the command it asked for. The doorbell's text is a prompt to the model, so it must be terse and plain.

### 3.3 Busy turn (E4b)

```
08:39:46.418  write trigger "run exactly this Bash command: sleep 20"
08:39:48.570  assistant tool_use Bash {"command":"sleep 20"}
08:39:55.060  write E4b-next (priority omitted)   → enqueue 08:39:55.077
08:39:58.416  write E4b-now  (priority "now")     → enqueue 08:39:58.419
08:40:17.602  user tool_result (Bash completed with no output)
08:40:17.716  dequeue ; 08:40:17.717 dequeue
08:40:17.763  user (peer) "E4b-next: …" ; 08:40:17.776 user (peer) "E4b-now: …"
08:40:19.178  assistant "ack" ; 08:40:19.203 system turn_duration   ← one turn
```

This matches the docs: the message is "read between tool calls during an active turn", and a running
tool is never interrupted.

### 3.4 `crossSessionInbound`

| Receiver | Setting | Writer | Result |
|---|---|---|---|
| R1 `manual` | unset | spike writer | delivered, turn (E1–E4) |
| R3 `auto` | unset | spike writer | delivered, turn (P1) |
| R4 `manual` | `hold` (via `--settings`) | spike writer | `held inbound peer message (1 held, cause=explicit-setting)`; system entry `Held peer message — from an unidentified session [verified pid 21399]`; **no turn** |
| R4 `manual` | `hold` | Claude `SendMessage` (S1) | `held … (2 held)`; no turn; S1's tool result `success:true … in that session's inbox, not yet read` |
| R5 `manual` | `refuse` (via `--settings`) | spike writer | `refused inbound peer message`; no transcript at all; **no turn** |
| bypass receiver | unset | — | **not run** (see §6). From the binary and the docs: **held** (`holdCause: no-mode-asserted`) unless the sender attests `from-mode="bypass"` |

- **The setting is read once at launch.** A file passed via `--settings` was **not** hot-reloaded: P2 changed R3's file to `hold`, and the next write was still delivered.
- **Default policy with no value set** (binary function `E` and the docs):
  - A message the receiver verifies came from **its own child process** is accepted.
  - Otherwise the receiver groups itself as **bypass** or **prompting**. `auto`, `acceptEdits` and `dontAsk` count as prompting.
  - A prompting receiver accepts anything that is not attested as bypass.
  - A bypass receiver holds everything not attested as bypass.
- **The attestation is self-asserted.** It is the `from-mode` attribute inside the `<cross-session-message>` envelope in `content`.
- **Precedence.** Managed (`policySettings`) beats `--settings` (`flagSettings`), which beats user settings. Project and local settings can only tighten.
- **The owner's sessions.** `~/.claude/settings.json` sets `crossSessionInbound: "accept"`, so on this machine every session that loads user settings delivers regardless of mode.

### 3.5 Lifecycle of the socket and the registry

| Event | `/tmp/cc-socks/<pid>.sock` | `~/.claude/sessions/<pid>.json` + `.key` |
|---|---|---|
| start | bound at once (R1 `Listening` at 08:36:24.029, while the trust dialog was still open) | written (`status: idle`) |
| `/exit` (R3) | removed | removed |
| SIGTERM (R5, R4, S1) | removed | removed |
| SIGKILL (R1) | **stale file left**; `connect()` → `ConnectionRefusedError [Errno 61]` | removed within 5 s; inferred to be the `sessionRegistry` sweep that other live sessions run |

### 3.6 The pid → socket map

- **`/tmp/cc-socks/` is not a reliable map.**
  - It can be relocated per session (`XDG_RUNTIME_DIR`, `CLAUDE_CODE_TMPDIR`, `--messaging-socket-path`).
  - A crash leaves stale files in it.
  - It carries no identity.
- **The registry is the better source.** An entry looks like this:

  ```
  {"pid":90375,"sessionId":"f73a2587-…","cwd":"…/sandbox/recv","kind":"interactive","entrypoint":"cli","status":"idle",
   "name":"spike-r1","messagingSocketPath":"/tmp/cc-socks/90375.sock","procStart":"Fri Oct 23 08:36:23 2026",
   "pidDomain":"darwin","peerProtocol":1,"peerFeatures":["notify_idle","reply_across_default_dirs","artifact_yield"],"version":"2.1.309"}
  ```

- **Role is still missing.** Nothing in the registry says master or worker, so ADR-0004's register hook remains necessary. The registry is the cross-check: same `pid`, `procStart`, `sessionId` and `messagingSocketPath`.

### 3.7 Other CLIs (report only)

```
codex -> ~/.nvm/versions/node/v24.13.0/bin/codex        codex-cli 0.145.0   (app-server [experimental]; listen accepts unix://PATH)
gemini -> ~/.nvm/versions/node/v24.13.0/bin/gemini      0.52.0              (nothing socket-like in --help)
cursor-agent -> ~/.local/bin/cursor-agent               2026.04.17-479fd04  (nothing socket-like in --help)
~/.config/opencode exists; no opencode binary on PATH
```

## 4. Recommendations

### R0.25: server side, the directory and dispatcher

1. **The frame.** Write exactly §2.1, then close the connection:
   - one connection per doorbell, opened only when the doorbell is ready;
   - `{"type":"user","session_id":<sessionId>,"message":{"role":"user","content":<string>}}` plus `\n`;
   - no `priority` (defaults to `next`), and no `from`, unless the server also runs a listener to receive `peer_message_status` receipts;
   - expect nothing back.
2. **The content is a prompt.** The receiving model reads it and acts on it.
   - First line: one readable sentence, e.g. `batondeck: T-12 assigned to you on B-… (cursor 4711)`, which is also the human's one-line preview.
   - Second line: the `{kind, cursor, projectId}` JSON.
   - Nothing third-party, and nothing alarming. ADR-0004's zero-authority rule holds.
3. **Never forge `from-mode`.** It is a self-asserted attestation, and writing `bypass` to get past a hold would launder the receiving user's permission decision.
4. **Auth line.** Send it when a key file exists, using the peer token from `<pid>.<sha256(path)>.key`. It is optional on macOS and Linux but forward-compatible, and required on Windows. Never log it. It is not the security boundary; the 0700 directory and same-uid check are.
5. **Liveness is not file existence.**
   - Drop an entry on process exit (kqueue/pidfd, as ADR-0004 says).
   - Treat `ECONNREFUSED` or `ENOENT` on connect as a failed delivery, so the entry is dropped.
   - Refuse when the pid's `procStart` differs from the registered one.
   - The `session_id` field makes a pid-reuse write a no-op.
6. **Delivery is unacknowledged.** A held or refused doorbell looks identical to a delivered one from the server's side. The cursor catch-up (`next_task` and the change log) must stay correct when doorbells are lost. Also, Claude Code drops identical repeats in a short window, rate-limits per sender and queues at most 50, so coalescing (one doorbell per cursor) is required.

### R3.3: client side, `session register|unregister`

1. **What the hook sends.** From the SessionStart hook, send:
   - `pid`: the hook's parent claude process. Do not assume; read it from `~/.claude/sessions/*.json` where `messagingSocketPath == $CLAUDE_CODE_MESSAGING_SOCKET`;
   - `CLAUDE_CODE_MESSAGING_SOCKET`;
   - `sessionId` and `procStart` from that registry entry;
   - the role (master or worker).

   The server cross-checks these against the registry, as `uid` + `pid` + `procStart`. Do **not** send `CLAUDE_CODE_MESSAGING_TOKEN`. It is the child token, the server is not the session's child, and on macOS a token never makes a server write count as "own-child" while the server is alive (binary: process evidence wins).
2. **The worker launch line must make the doorbell deliverable.** That means `--settings '{"crossSessionInbound":"accept"}'`, which is also the docs' recipe for `-p` workers, or a non-bypass permission mode. A `bypassPermissions` worker with no setting holds every doorbell for a human dialog, which defeats unattended work. `session register` should report the effective policy and permission mode so the UI can show "will not wake".
3. **Unregister on SessionEnd**, but do not rely on it. A SIGKILL skips it and leaves the socket file. R0.25's pid watch is the authority.

## 5. Spend

**10 turns**, all on `haiku`:

| Session | Turns | What |
|---|---|---|
| R1 | 7 | E1, E2, E3, E4-next, E4-now, E4-later, E4b |
| R3 | 2 | P1, plus P2, which was meant as a hold test but was delivered because `--settings` files do not hot-reload |
| S1 | 1 | the `SendMessage` capture |
| R4, R5 | 0 | held and refused |

## 6. Not verified

- **Bypass-class receivers.** Starting a `bypassPermissions` throwaway was denied by this session's auto-mode classifier, and I did not work around it. "Held by default, delivered when the sender attests `bypass`" rests on the binary and the docs only.
- The held-message approval dialog (approve or deny), and release of held messages when `accept` later applies.
- `claude -p` workers, `--bg` background sessions, and Desktop or VS Code sessions. The owner's pid 12655 is the Desktop app's 2.1.308.
- Linux and native Windows (`authRequired` is true only on Windows), and any version other than 2.1.309.
- Which token `SendMessage` sent: it was redacted at capture. That it is the peer token from the key file is inferred from the code.
- The rate-limit, duplicate-drop and 50-message queue thresholds: taken from the docs, not exercised.
- Receipts (`peer_message_status`) to a non-Claude sender that sets `from`. The one attempt failed because of the capture proxy.
- What happens to a write made while the folder-trust dialog is still open.
- **The committed writer has not been run.** `docs/release/spikes/socket-wake/doorbell.py` is a distillation of the scratch writer used here, and it has only been syntax-checked. The scratch writer additionally refused any socket whose pid was not one the spike had spawned.
