---
description: Worker mode — work the batondeck-kanban board, end the turn when it is empty, and let the socket doorbell start the next turn when work is assigned.
---

Enter **worker mode**: claim work, do it, complete it, and end your turn when nothing is left. This
session was registered as a worker by the plugin's SessionStart hook, so the server rings it when a
task is assigned to it or unassigned work becomes claimable; the doorbell starts the next turn. Workers
**accept and do** work; they do not create or assign it (that is `/batondeck-kanban:master`).

Inputs: $ARGUMENTS — optionally your agent label (the session's `BATONDECK_AGENT`, to serve only what
is assigned to that label), the project and board, and capabilities. Missing project or board →
`list_projects` → `list_boards`.

Each turn, the doorbell's or the user's:

1. **Is there an active sprint?** `list_sprints { projectId, boardId, status: "ACTIVE" }`, then
   `get_sprint`. While one is ACTIVE, pass its `sprintId` to selection; its frontier includes the
   runs of its raced members. A PROPOSED sprint is not worked as a sprint yet: until an admin
   activates it, its members are claimed like any other task.
2. **Claim.** `claim_next { projectId, boardId }` → keep the `leaseId`; it takes unassigned work and
   work assigned to you, never another's. With an agent label, call it with `assignee: "<label>"`
   too. On a doorbell,
   take `projectId` and `boardId` from it (each id of its `boardIds`; none → `list_boards`). After a
   `broadcast` doorbell, also call it without `assignee`: that work is assigned to nobody.
3. **Sweep the inbox** when the claim comes back null: `next_task { projectId, boardId, assignee,
   includeInbox: true }` returns the `review`, `blocked` and `poisoned` buckets. A task assigned to you
   in REVIEW is real work (rework, or yours to judge). Then `list_notifications`.
4. **Honour the task's `modelHint`**: run the task in a subagent on that model and effort, with the
   task id, the lease and the instruction to work it per the batondeck-worker skill —
   `get_task_context { includeUpstream: true }` first, clear and `ack_follow_up` every open follow-up,
   record as it goes, `heartbeat_task` on long work, bind artifacts, then
   `complete_task { projectId, taskId, leaseId, deliverable, artifacts }`. Trivial tasks matching your
   own model can be worked inline. Dispatching keeps this session's context small over a long shift.
5. Not processable → `block_task`, `handoff_task` or `fail_task`, honestly.
6. Print one line per finished task (id, title, outcome, and `handover.reviewer` if it went to
   REVIEW), then go back to step 2.
7. **Nothing claimable and the inbox empty → say "inbox empty" in one line and end your turn.** There
   is nothing to wait on: the next doorbell starts the next turn.

A reopened task is a correction loop: re-claim it, address the new follow-ups, ack them, re-complete.

Follow the batondeck-worker skill for the full rules. Agent / project / board / capabilities:
$ARGUMENTS
