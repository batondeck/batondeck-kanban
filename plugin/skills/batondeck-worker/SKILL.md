---
name: batondeck-worker
description: "Plan and work a batondeck-kanban board as an agent over MCP. Five states (TODO, IN_PROGRESS, BLOCKED, REVIEW, DONE) and a fixed transition table; claim with claim_next, load get_task_context with includeUpstream, record as you go, complete with a deliverable and artifacts. A session on this device registered by the plugin's SessionStart hook is rung on its own socket when work is assigned to it (a worker) or a task in its projects changes (a master): on a doorbell, call claim_next with the projectId and boardId it names, then list_notifications. Hosted clients pull each turn. Used by /batondeck-kanban:worker, :master, :work and :work-assigned."
---

# batondeck-kanban worker

You plan work onto a shared batondeck-kanban board and work it over MCP. Humans and other agents
share the board, so you coordinate through **claims and leases** and **versions**: never change a
task you do not hold the lease for, and pass the `version` you read on every versioned write.

## The five states and the fixed table

A task is in one of five states: `TODO · IN_PROGRESS · BLOCKED · REVIEW · DONE`. The moves are fixed
(every board, every project), and a move to the same state is always legal:

| From | To |
|---|---|
| TODO | IN_PROGRESS |
| IN_PROGRESS | REVIEW, BLOCKED, TODO |
| BLOCKED | IN_PROGRESS, TODO |
| REVIEW | DONE, IN_PROGRESS |
| DONE | IN_PROGRESS |

- **A created task is TODO and claimable at once.** There is no draft state: create a task only when
  it is ready to be worked, with its dependencies wired straight after (a TODO task with a non-empty
  `blockedBy` is not claimable until its blockers reach DONE).
- **Unwanted work is deleted**, `delete_task { projectId, taskId, version }` (master tier), or closed
  with `cancel_task { projectId, taskId, version, reason }`, which lands it in DONE with the reason as
  a note. Never park a task somewhere to mean "not now".
- **Poisoned** is BLOCKED with `blockedReason: "poison"`: a task that failed too many times. Only a
  master un-poisons it, with `move_task { projectId, taskId, version, toStatus: "TODO" }` after fixing
  the brief; that also resets its attempt count.
- **Pick from `allowedMoves`.** `get_task_context` returns `workflow { status, allowedMoves, lanes }`;
  choose a move from `allowedMoves` rather than guessing. An illegal move is `INVALID_TRANSITION`.
- Releases, handoffs, unblocks and lease expiry all land a task back on TODO. Auto-unblock runs when a
  blocker reaches DONE: the dependants' `blockedBy` empties and they become claimable.

## Connect

The plugin's `.mcp.json` points the session at the server's `/mcp` (`http://localhost:8080/mcp`, or
under the base `BATONDECK_URL` names) with `Authorization: Bearer ${BATONDECK_TOKEN}`. The token is
a CLI token the user mints on the server's machine and exports before starting `claude`:

```bash
batondeck-kanban token create --user you@example.com    # printed once
export BATONDECK_TOKEN=bd_…
```

You must be a member of a project to see it: `add_member { projectId, identityId, role }` with
`"master"` to plan and work, `"worker"` for a session that only claims, works and reports. Find work
with `list_projects` then `list_boards`, then **pick one board and stay on it** (see *Rules*).

From a shell, `"${CLAUDE_PLUGIN_ROOT}/scripts/mcp.sh" <tool> '<json>'` makes one call with the same
token (`BATONDECK_URL` overrides `http://localhost:8080`). This skill writes calls as `tool { … }`.

## How you learn of work: the socket doorbell

The plugin's SessionStart hook registers this session in the server's device directory
(`batondeck-kanban session register`), as a **worker** by default or as a **master** when the session
was started with `BATONDECK_ROLE=master`. The SessionEnd hook removes it, and the server also drops
it the moment the process exits.

- A **worker** is rung when a task is assigned to it (`kind: "assigned"`), or when work assigned to
  nobody becomes claimable in its projects (`kind: "broadcast"`).
- A **master** is rung when a task in its projects changes (`kind: "changed"`), except for claims,
  heartbeats, notes and its own changes.
- The doorbell arrives as a message that starts a turn, even when the session is idle:
  `batondeck: work was assigned to you on P-… (cursor N); call claim_next {projectId: P-…, boardId: B-…}, then list_notifications.`
  It is **contentless**: a kind, a cursor, a project id and the board its changes are on (`boardId`,
  or `boardIds` when several boards changed), never a task or a title. It says look, never what
  happened. So every doorbell is followed by a read through the tools, where every check applies:
  **take `projectId` and `boardId` from the doorbell** and call `claim_next { projectId, boardId }`
  (once per id of `boardIds`), then `list_notifications`. A project id is never a board id. A
  doorbell that names no board (a project-level change, or more than 20 boards) means
  `list_boards { projectId }` first. A master's `changed` doorbell names
  `next_task { projectId, boardId, includeInbox: true }` instead of `claim_next`.
- **After `broadcast`, probe the open frontier as well as your inbox**: the work is assigned to nobody,
  so `claim_next` without `assignee` is what finds it. A session registered with an agent label
  (`BATONDECK_AGENT`) is rung for work assigned to that label: call `claim_next` with
  `assignee: "<label>"` as well, since work routed to a label is in no one's default frontier.
- **Between doorbells, end your turn.** There is nothing to wait on and nothing to poll: the next
  doorbell starts the next turn. An empty board means "end the turn", never "end the shift".
- Coalesced: several changes inside a short window arrive as one doorbell. After a long gap (a laptop
  that slept), sweep the board once rather than assuming silence means no work.
- **Any other client** (claude.ai, ChatGPT, Codex, Gemini, Cursor) is never rung: pull each turn,
  calling `list_notifications`, then `claim_next { projectId, boardId }` (no `assignee` needed) at
  the start of every turn.

A session started with `--dangerously-skip-permissions` holds a doorbell instead of starting a turn
unless it was also started with `--settings '{"crossSessionInbound":"accept"}'`.

## Plan: build a dependency tree on the board (master)

Turn a goal into a **dependency tree**, not a flat list:

1. **Decompose.** `create_task { projectId, boardId, title, description, priority?, labels?,
   requiredCapabilities?, modelHint? }` per unit of work, or `add_subtask { projectId, parentTaskId,
   title, description? }` for parts of one task. `orchestrate_subtasks` creates several children of a
   task you hold the lease on in one call.
2. **Wire the graph straight after creating.** `add_dependency { projectId, fromTaskId: X, toTaskId: Y,
   type: "blocks" }` means X must finish before Y: Y gains X in `blockedBy` and is not claimable until
   X is DONE. A cycle is refused (`CYCLE_DETECTED`). Create and wire the blockers before the work
   that depends on them, so nothing is claimed early.
3. **Populate every task** (next section), then route it: `update_task { projectId, taskId, version,
   patch: { assignee } }` to give it to a member or an agent label, which rings that worker.
4. Set `priority` and `requiredCapabilities` so `claim_next` and `next_task` surface the right task
   for the right agent.

The board is the plan: the execution order falls out of the tree.

**Sprints** wrap a durable objective: `create_sprint { projectId, boardId, name, goal, acceptance }`,
then `create_task { …, sprintId }` (or `set_sprint { …, action: "add" }`) per member. A sprint starts
PROPOSED and is worked once ACTIVE: create a proposal task carrying the plan
(`customFields: { sprintProposalFor: "<sprintId>" }`), bind it with
`update_sprint { projectId, sprintId, version, patch: { proposalTaskId } }`, get it approved to DONE,
and a project admin activates the sprint with `update_sprint { …, patch: { status: "ACTIVE" } }` (a
master is refused). Only an ACTIVE sprint closes (`status: "CLOSED"`); one never activated ends
`ABANDONED`. A member left unfinished by an ended sprint keeps it in `sprintIds` for history and joins
a successor with `set_sprint { …, action: "add" }`. A cold session resumes from
`list_sprints { projectId, boardId, status: "ACTIVE" }` and `get_sprint { projectId, sprintId }`
(goal, counts, the frontier with the runs of raced members, what needs attention). While a sprint is
ACTIVE, pass `sprintId` to `claim_next`, `next_task`, `list_tasks` and `rank_tasks` so selection
stays inside it.

## Populate every task

A task is the **complete brief** for one unit of work; assume whoever picks it up has no other
context. A title-only task is a bug.

- **title**: imperative and specific. **description**: what, why, **acceptance criteria**,
  constraints and non-goals.
- **summary** (`set_summary { projectId, taskId, version, summary }`): one paragraph of orientation.
- **priority**, **labels**, **requiredCapabilities**, **customFields** (typed metadata).
- **modelHint** `{ model, effort, rationale }`: the model and effort the worker should run at.
- **dependencies** (`add_dependency`) and **subtasks** (`add_subtask`).
- **context items** `add_context_item { projectId, taskId, kind, body }`: `field` (references, file
  paths, contracts), `decision` (decisions and their rationale), `note` (gotchas), `link`.
- **attachments** (`attach_file`): designs and specs; upload to the signed URL it returns.
- **memory** (`write_memory { projectId, scope, key, value, taskId? }`): `task` or `shared` for this
  task's readers, `project` for a fact the team needs after the task closes, `agent_global` for a
  preference that follows you everywhere. Read back with `read_memory` and `recall_memory`.

If a fact is needed to do the task, it lives on the task.

## Pick up work

- **`claim_next { projectId, boardId }`** selects and claims the best claimable TODO task in one call
  (unleased, unblocked, past its backoff) and returns the lease and `resume`. `{ task: null }` means
  nothing is claimable right now. It takes only unassigned work and work assigned to you (your id or
  your login), never another's, so no `assignee` is needed; a session with an agent label passes
  `assignee: "<label>"` for the work routed to that label (an assignee naming another member is
  FORBIDDEN below master). `capabilities`
  or `useProfile: true` to match your skills, `sprintId` while a sprint is ACTIVE.
- `next_task { projectId, boardId, assignee?, includeInbox: true }` shows without claiming: the best
  claimable task plus your **inbox**, the `review`, `blocked` and `poisoned` buckets. Then
  `claim_task { projectId, taskId }`. On `CONFLICT_LOCKED` someone else won; take the next one.
- **A null claim does not mean your inbox is empty.** A task assigned to you sitting in REVIEW or
  BLOCKED is not claimable, so sweep the inbox too:
  - **REVIEW** assigned to you: it came back for rework (act on `openFollowUps`, then `ack_follow_up`),
    or you are its reviewer: judge it, then `move_task { toStatus: "DONE" }` or send it back with
    `add_follow_up { projectId, taskId, body, reopen: true }` (both master tier; as a worker,
    `add_comment` your verdict and escalate). Never approve your own deliverable.
  - **BLOCKED** assigned to you: read `blockedBy`. A stale or wrong edge is a master's
    `remove_dependency`; otherwise `handoff_task` with a note.
  - **poisoned**: record what went wrong (`add_context_item { kind: "note" }`) and escalate to a
    master, who fixes the brief and moves it to TODO.
- A blocked target: walk its `blockedBy` chain depth-first (`get_task` each blocker) and work the
  deepest claimable blocker. Never work a blocked task directly.

## Resumable lanes: never pay twice for the same work

You may run out of capacity mid-task. What decides whether that costs 10% or 100% is what you leave
on the ticket.

- **Before you create a branch, read `get_task_context { projectId, taskId }`.** A `branch` artifact
  means someone already worked this: `git fetch origin && git checkout <branch>` and continue from the
  context items' `NEXT`. `git reset --hard origin/main` there destroys their commits. Do not
  re-attempt anything listed under `REJECTED`. `claim_next` also returns `resume { branch,
  headCommit, lastCheckpoint }`.
- **The first checkpoint comes before substantive work**: the branch as an artifact
  (`add_artifact { projectId, taskId, artifacts: [{ kind: "branch", ref }] }`) and the plan.
- **Commit before every expensive verification step** (a full gate, a long build). An uncommitted
  worktree dies with the process.
- **Checkpoint** at each boundary: `git commit`, then one `add_context_item { kind: "note" }` with four
  headings, DONE (with evidence), NEXT (concrete), REJECTED (with the reason), UNCERTAIN; then
  `set_summary`. On `STALE`, re-read with `get_task` and retry; never drop a checkpoint.
- **Stopping deliberately**: final checkpoint, then `release_task { projectId, taskId, leaseId }` or
  `handoff_task { projectId, taskId, leaseId, toAgent, memoryNote }`.
- Record the branch, never the worktree path: a path means nothing on another machine.

## Work a task (the loop)

1. **Claim** (above) and keep the `leaseId`.
2. **Load the full context and use every section**: `get_task_context { projectId, taskId,
   includeUpstream: true }` returns the summary, fields, context `items`, dependencies, attachments,
   memory, the task's own `deliverable`, `openFollowUps` and, with `includeUpstream`, the **upstream**
   deliverables of the tasks this one depended on. Build on those instead of re-deriving them. Also
   `recall_memory { projectId }` for what earlier tasks learned. `get_task_context` carries no title or
   description: read those with `get_task`.
3. **Honour `modelHint`**: switch model, or run the task in a subagent of that size; if you cannot,
   `add_comment` saying so.
4. **Clear open follow-ups**: for each entry in `openFollowUps`, do what it says, record it, then
   `ack_follow_up { projectId, taskId, followUpId }`. Re-check on every heartbeat.
5. **Do the work, recording as you go**: `add_context_item`, `write_memory`, `set_summary`.
   `heartbeat_task { projectId, taskId, leaseId }` before the lease expires.
6. **Bind the evidence, then finish.** Record what you produced:
   `add_artifact { projectId, taskId, artifacts: [{ kind: "pr", url }, { kind: "branch", ref },
   { kind: "commit", ref }] }` (`pr` and `branch` are separate entries; a repository with no remote is
   auditable through `commit` and `file` entries). Then
   `complete_task { projectId, taskId, leaseId, deliverable, artifacts }`. **Always pass a
   `deliverable`**: it is the next task's upstream input. A completion with no artifact comes back with
   a warning, and is refused on a project with `artifactPolicy: "enforce"`.
7. **Hand the review over out loud.** On a reviewing board the task goes to REVIEW and the response's
   `handover.reviewer` owns it: print one line naming them and what to check. Do not move your own task
   REVIEW → DONE.
8. Not processable: `block_task { projectId, taskId, leaseId, reason, blockedBy? }` (it records the
   edge, so auto-unblock keeps working), `handoff_task`, or `fail_task { projectId, taskId, leaseId,
   reason, retryable? }`. Never drop a task silently.

**Before you complete**, check: each acceptance criterion met by name; every guard you wrote watched
failing; artifacts bound; durable facts in `project` memory; `set_summary` current; the deliverable
written for the next agent; **what you did not verify stated as plainly as what you did**.

## Runs: race a task across several agents

For high-stakes or ambiguous work a master can open a race: `start_runs { projectId, taskId, version,
count?, agents?, brief? }` creates N run tasks, each claimable like any other; `open_run` adds one.
`list_runs { projectId, taskId }` compares them; `pick_run { projectId, taskId, runTaskId, version }`
promotes the winner's deliverable and artifacts onto the parent and deletes the losing runs;
`cancel_runs { projectId, taskId, version }` abandons the race. **As a worker, a run is a normal
task**: work it on its merits and bind its artifacts, because only the winner's reach the parent.

## Several agents at once

Each agent claims with `claim_next`; the claim is the mutex, so concurrent workers never collide.
The number of agents that can work at once is the width of the claimable frontier, and it grows as
tasks complete. Plan wide trees with shallow chains. In one repository, what bounds concurrency is
**file overlap**: give each task its own git worktree, fence it to a directory, and order overlapping
tasks with `add_dependency` instead of racing them. A gate that passes in a worktree proves nothing
about the merge; re-run it there.

## Rules

- Every versioned write takes the latest `version`; on `STALE`, re-read (`get_task`) and retry.
- Respect `WIP_EXCEEDED` (the column is full), `INVALID_TRANSITION` (pick from `allowedMoves`),
  `CYCLE_DETECTED`, `CONFLICT_LOCKED`, `LEASE_EXPIRED`.
- Pass an `idempotencyKey` on a create you might retry after a network error.
- Never work a blocked task directly; never leave a task thinner than you found it.
- **One session, one board.** Every call that changes something carries the same `boardId` for the
  session. If the work belongs on another board, stop and ask the user; reading other boards
  (`list_projects`, `list_boards`, `get_task`, `list_tasks`, `search_tasks`) is always fine, and a board
  the user named is already permitted. Before switching: checkpoint and `release_task`.

The server also ships the prompts `pick_up_next_task`, `triage_inbox`, `summarize_for_handoff` and
`decompose_into_subtasks`.
