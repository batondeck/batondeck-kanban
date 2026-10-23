# What this plugin keeps from conductor's

One row per file in conductor's `plugin/` at `5e2baafa` (`find plugin -type f`: 51 files). Each
row says whether this plugin keeps the file as it is, rewrites it for this server, or drops it, and
why. Nothing from conductor's plugin is carried here except through a `rewrite` row.

The reasons cite the rulings they follow: ADR-0004 (the tool trim; wake is the device socket
doorbell only, registered by SessionStart and SessionEnd hooks; no Stop hook), ADR-0005 (five states,
`TODO · IN_PROGRESS · BLOCKED · REVIEW · DONE`, and the fixed table) and the plan's §Clarifications 9.

| # | conductor `plugin/` file | Verdict | Reason |
|---|---|---|---|
| 1 | `.claude-plugin/marketplace.json` | drop | A marketplace listing is publishing (zone Z3). The plugin loads from a checkout with `claude --plugin-dir plugin`. |
| 2 | `.claude-plugin/plugin.json` | rewrite | Name `batondeck-kanban`, version `0.1.0`, Apache-2.0. No `hooks` key: `hooks/hooks.json` is loaded by convention, and naming it again loads it twice. |
| 3 | `.cursor-plugin/marketplace.json` | drop | Cursor is a hosted client here: it pulls each turn and is never rung (ADR-0004). No Cursor package ships. |
| 4 | `.cursor-plugin/plugin.json` | drop | As row 3. |
| 5 | `.mcp.json` | rewrite | The session's MCP is this server's `/mcp` at `http://localhost:8080/mcp`, with `Authorization: Bearer ${BATONDECK_TOKEN}` (a CLI token from `batondeck-kanban token create`). Conductor's hosted URL and its agent-id `headersHelper` go. |
| 6 | `LICENSE` | drop | Conductor's proprietary licence. The repository's Apache-2.0 `LICENSE` covers this directory. |
| 7 | `README.md` | rewrite | Install, the environment the hooks read, what each hook does. Conductor's marketplace, release and hosted-sign-in sections go. |
| 8 | `SECURITY.md` | rewrite | The hook surface is two hooks that run one binary. Conductor's Stop-gate and listener sections describe files this plugin does not ship. |
| 9 | `agents/release-auditor.md` | drop | Conductor's own release audit, written against its repository and gates. |
| 10 | `commands/chronicle.md` | rewrite | Points at the rewritten chronicle skill. Conductor's version runs `scripts/chronicle/*.py` from conductor's repository, which this repository does not have. |
| 11 | `commands/master.md` | rewrite | Five states and the fixed table; the planning steps of `plan.md` folded in; supervise on the doorbell; poisoned tasks un-poisoned with `move_task → TODO` (`master.md:52,77` named `requeue_task` and a parked state). |
| 12 | `commands/off.md` | drop | It disarmed the mode flag the Stop gate read. There is no flag and no gate; `/exit` ends the session and SessionEnd unregisters it. |
| 13 | `commands/plan.md` | drop | Its steps (BACKLOG → READY promotion, the seed script) are gone under ADR-0005. What remains of planning is in `commands/master.md`. |
| 14 | `commands/release-audit.md` | drop | Delegates to row 9. |
| 15 | `commands/runs.md` | rewrite | The race tools stay (`start_runs`, `list_runs`, `pick_run`, `cancel_runs`); losing runs are deleted, not cancelled (plan §Fold 2; `runs.md:32`). |
| 16 | `commands/work-assigned.md` | rewrite | Drain the inbox by assignee; TODO replaces READY. |
| 17 | `commands/work.md` | rewrite | One loop over `claim_next`; artifacts written by hand (row 44 is dropped). |
| 18 | `commands/worker.md` | rewrite | The worker loop ends its turn and the doorbell starts the next one: no background wait, no mode flag (`worker.md:33-35` named a parked state). |
| 19 | `hooks/hooks.json` | rewrite | Exactly two hooks: SessionStart and SessionEnd, both `hooks/session.sh` (ADR-0004). |
| 20 | `hooks/session.sh` | rewrite | Runs `batondeck-kanban session register --role worker|master` at start and `session unregister` at end, stdout to stderr, always exit 0. Conductor's version exported a session id for the mode flag. |
| 21 | `hooks/stop-gate.sh` | drop | No Stop hook (ADR-0004): a doorbell starts a turn in an idle session, so nothing has to hold a session on shift. |
| 22 | `icon-256.png` | drop | Conductor brand asset; relicensing brand art is the owner's call. |
| 23 | `icon.svg` | drop | As row 22. |
| 24 | `logo.svg` | drop | As row 22. |
| 25 | `release.yml` | drop | Conductor's plugin release workflow. A release of this plugin is zone Z3. |
| 26 | `scripts/agent-id.sh` | drop | Minted the `x-batondeck-agent-id` header; this server reads no such header. A session's agent label is `BATONDECK_AGENT`, passed to `session register --agent`. |
| 27 | `scripts/chronicle.sh` | drop | Wrapped conductor's `scripts/chronicle/sweep.py`, which this repository does not have. |
| 28 | `scripts/cursor-mcp.sh` | drop | As row 3. |
| 29 | `scripts/fleet.sh` | drop | Launched `worker.sh` processes (row 39). Several sessions each registered as a worker are the fleet. |
| 30 | `scripts/listener-start.sh` | drop | Started conductor's Pub/Sub wake listener (ADR-0004 drops it). |
| 31 | `scripts/listener-stop.sh` | drop | As row 30. |
| 32 | `scripts/mcp.sh` | rewrite | One-shot tool caller for this server (`BATONDECK_URL`, `BATONDECK_TOKEN`). One copy only: conductor tracked three (`plugin/scripts/`, `plugin/skills/batondeck-worker/scripts/`, `skill/scripts/`), each listing `get_transitions` (`mcp.sh:154`), which is not ported. |
| 33 | `scripts/mode.sh` | drop | Armed the mode flag for the Stop gate (row 21). |
| 34 | `scripts/seed-tasknet.py` | drop | Its plan format promotes BACKLOG → READY and wires a conductor sprint latch; ADR-0005 removes both. Planning is `create_task` + `add_dependency`. |
| 35 | `scripts/token.sh` | drop | Printed conductor's hosted connection env. A token here comes from `batondeck-kanban token create`. |
| 36 | `scripts/wake-start.sh` | drop | Started the per-session Pub/Sub listener (ADR-0004). The SessionStart hook registers the session instead. |
| 37 | `scripts/wake-stop.sh` | drop | As row 36. |
| 38 | `scripts/worker-assigned.sh` | drop | A shell worker blocking on the listener (`worker-assigned.sh:7,89` named a parked state). The registered session is the worker. |
| 39 | `scripts/worker.sh` | drop | A shell loop over `next_task` that hands each task to a command; a registered session replaces it. |
| 40 | `skills/batondeck-chronicle/SKILL.md` | rewrite | The sweep over DONE tasks into `ingest_chronicle_page`, composed by the agent from ticket fields verbatim, since the deterministic emitter is not in this repository. |
| 41 | `skills/batondeck-worker/SKILL.md` | rewrite | Five states, the fixed table, the socket doorbell, `delete_task` where conductor moved to CANCELLED, poisoned where it had a dead-letter state; the Studio, the Pub/Sub listener and the mode flag go (`SKILL.md:295-304,746,806,829`). |
| 42 | `skills/batondeck-worker/references/parallel-delivery.md` | drop | Conductor's own repository method. The rule that matters (one worktree per task; file overlap bounds concurrency) is one paragraph in row 41. |
| 43 | `skills/batondeck-worker/references/tools.md` | drop | A hand-written tool list rots (`tools.md:41-43,83` still named conductor's statuses); the server's `tools/list` and `contract/expected.json` are the reference. |
| 44 | `skills/batondeck-worker/scripts/artifacts.sh` | drop | Built the `artifacts` array from git; the skill now says which `kind`s to record, and an agent reads its branch and commit with `git`. |
| 45 | `skills/batondeck-worker/scripts/mcp.sh` | drop | The second copy of row 32. |
| 46 | `skills/batondeck-worker/scripts/seed-tasknet.py` | drop | The copy of row 34. |
| 47 | `skills/batondeck-worker/scripts/token.sh` | drop | The copy of row 35. |
| 48 | `skills/batondeck-worker/scripts/wake-wait.mjs` | drop | The background wait on the listener (ADR-0004 drops the `wait` command and the Stop hook it fed). |
| 49 | `skills/batondeck-worker/wake-listener/listener.d.mts` | drop | Conductor's Pub/Sub listener (also `skill/wake-listener/listener.mjs`), dropped by ADR-0004. |
| 50 | `skills/batondeck-worker/wake-listener/listener.mjs` | drop | As row 49. |
| 51 | `skills/batondeck-worker/wake-listener/main.mjs` | drop | As row 49. |

Kept as they are: none. Rewritten: 15. Dropped: 36.
