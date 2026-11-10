---
description: Master mode — plan a goal onto the batondeck-kanban board, assign it, and supervise on the socket doorbell until it ships; can also work tasks itself.
---

Enter **master mode**: put work on the board, watch it and close the loop on every change. A master
may also claim and work a task when that is fastest. Several masters can run at once; versions and
claims are the mutex.

**Start the session with `BATONDECK_ROLE=master`** (in the environment you run `claude` from). The
SessionStart hook then registers it as a master, and the server rings it when a task in its projects
changes. A session registered as a worker is rung only on assignments.

Inputs: $ARGUMENTS — the goal to ship, or an existing project and board to supervise.

Setup:

1. Resolve the project and board, or `create_project { name }` and `create_board { projectId, name }`.
2. **Plan for the team** (the batondeck-worker skill's *Plan*): one fully briefed task per unit of
   work with `create_task`, the edges with `add_dependency` straight after (blockers first), and on
   each task:
   - **`assignee`** (`update_task { projectId, taskId, version, patch: { assignee } }`): the member or
     agent label that should take it, when routing matters; leave it open for the pool otherwise.
     Assigning rings that worker.
   - **`modelHint { model, effort }`**: the complexity estimate the worker runs at.
   - `requiredCapabilities`, so profile matching routes it.
   A created task is TODO and claimable at once, so create only what is ready to be worked.
   Wrap a durable objective in a sprint (`create_sprint`, then `create_task { …, sprintId }`).

Supervision, each turn the doorbell starts:

1. **Read the board; the doorbell says only that something changed, and on which board.**
   `next_task { projectId, boardId, includeInbox: true }`, with the doorbell's `projectId` and
   `boardId` (each id of its `boardIds`; none → `list_boards`): the best claimable task that is
   unassigned or yours (pass `assignee` to see another's) plus the `review`, `blocked` and `poisoned`
   buckets of the whole board. Then `list_notifications` for what was addressed to you.
2. **Act by state:**
   - REVIEW → judge the deliverable and its `artifacts` (`get_task_context`, `get_task`); re-run one of
     its proofs rather than trusting it ran. Good → `move_task { projectId, taskId, version,
     toStatus: "DONE" }`, which auto-unblocks its dependants. Not good →
     `add_follow_up { projectId, taskId, body, reopen: true }` with concrete changes; the rework goes
     back to TODO assigned to its completer, who is rung. Never approve a
     task you completed yourself.
   - DONE → check what the auto-unblock opened; assign or re-prioritise the new frontier.
   - BLOCKED → read the reason: its `blockedBy`, the blocker's note in `get_task_context`, or the
     `task.blocked` event's `reason` in the board feed (`batondeck://{projectId}/board/{boardId}/feed`);
     no task field carries it. Resolve it (answer as a follow-up, `remove_dependency`, reassign, or do
     it yourself).
   - poisoned (BLOCKED with the label `poisoned`, the inbox's `poisoned` bucket) → fix the brief, then
     `move_task { toStatus: "TODO" }`.
   - Work nobody should do → `delete_task { projectId, taskId, version }`.
3. **Quiet board** → a health pass: `reap_stale_leases { projectId, boardId }`, `rank_tasks` for the
   frontier, and if TODO work sits unclaimed with no live workers, claim a leaf and work it yourself.
4. **Supervising a sprint?** After each handled batch, `update_sprint { projectId, sprintId, version,
   patch: { eventCursor, checkpoint } }`. On `STALE`, re-read with `get_sprint`, keep the larger
   cursor, retry.
5. **Keep planning**: fold discoveries back into the board as new tasks and edges.
6. Then end your turn; the next change starts the next one. **Goal fully DONE** → post a final summary
   (what shipped, deliverables, loose ends) and close the sprint, `update_sprint { patch: { status:
   "CLOSED" } }`.

High-stakes or ambiguous task? Race it: `/batondeck-kanban:runs`.

Follow the batondeck-worker skill for planning and working rules. Goal / project / board:
$ARGUMENTS
