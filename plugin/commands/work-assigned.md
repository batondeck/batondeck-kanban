---
description: Work the batondeck-kanban tasks assigned to you — drain your inbox by assignee.
---

Work every task the board has routed to **you** (the task's `assignee` is your identity or your agent
label), over MCP.

Inputs: $ARGUMENTS — your agent label (or nothing, to use your identity), and optionally the project
and board. Missing → `list_projects` → `list_boards`.

Loop until the inbox is empty:

1. **Claim yours:** `claim_next { projectId, boardId, assignee: "<you>" }` → the best claimable TODO
   task routed to you, claimed, or `{ task: null }`. Assignment is advisory: another agent may claim it
   first, so take the next.
2. **Sweep the rest of the inbox** when the claim is null: `next_task { projectId, boardId,
   assignee: "<you>", includeInbox: true }` returns the `review`, `blocked` and `poisoned` buckets.
   REVIEW assigned to you is rework (act on `openFollowUps`) or yours to judge; BLOCKED needs its
   blocker resolved or a handoff; poisoned goes to a master with a note on the task.
3. **Load the context:** `get_task_context { projectId, taskId, includeUpstream: true }`, every
   section; a handoff note is additional instructions.
4. **Processable** → clear and `ack_follow_up` each open follow-up, do the work recording as you go,
   `heartbeat_task` before the lease expires, bind artifacts with `add_artifact`, then
   `complete_task { projectId, taskId, leaseId, deliverable, artifacts }`; name `handover.reviewer` if
   it went to REVIEW. **Not processable** → say so, record why with
   `add_context_item { kind: "note" }`, then `handoff_task` or `block_task`.
5. **Repeat** until the claim is null and the inbox buckets are empty, then end your turn. If this
   session is registered as a worker, the next assignment rings it.

Follow the batondeck-worker skill for the full loop. Agent (and optional project/board): $ARGUMENTS
