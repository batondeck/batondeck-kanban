---
description: Work the batondeck-kanban board once — claim the best task and run the loop.
---

Work a batondeck-kanban board over MCP:

1. **Claim:** `claim_next { projectId, boardId }` selects and claims in one call and returns the
   `leaseId` and `resume`. It takes only unassigned work and work assigned to you, never another's; an
   agent label's work needs `assignee: "<label>"`. Missing project or
   board → `list_projects` → `list_boards`.
2. **Resume check:** a `branch` in `resume` or in the task's artifacts means someone already worked it:
   `git fetch origin && git checkout <branch>` and continue from the last checkpoint's `NEXT`.
3. **Load the context:** `get_task_context { projectId, taskId, includeUpstream: true }` and use every
   populated section before you act: `field` items say what, `decision` and `note` items say why,
   `memory` carries durable facts, `attachments` carry designs and specs, `upstream` carries the
   deliverables of the tasks this one depended on (build on them), and `openFollowUps` carries
   directives you must satisfy. `get_task` for the title and description.
4. **Clear open follow-ups:** do what each says, record it, then
   `ack_follow_up { projectId, taskId, followUpId }`.
5. **Do the work, recording as you go:** `add_context_item`, `write_memory`, `set_summary`;
   `heartbeat_task { projectId, taskId, leaseId }` before the lease expires.
6. **Bind the evidence, then finish:** `add_artifact { projectId, taskId, artifacts: [{ kind: "pr",
   url }, { kind: "branch", ref }, { kind: "commit", ref }] }`, then
   `complete_task { projectId, taskId, leaseId, deliverable, artifacts }` (always a `deliverable`).
   On a reviewing board the response's `handover.reviewer` owns it now: print a sign-off line naming
   them, and do not move your own task REVIEW → DONE. Stuck → `block_task` with a reason and
   `blockedBy`; passing it on → `handoff_task`.

A task you finished that comes back to IN_PROGRESS or TODO was reopened with a follow-up: re-claim it,
address the new `openFollowUps`, ack them, re-complete.

Follow the batondeck-worker skill for the full loop. Focus: $ARGUMENTS
