---
description: Race one batondeck-kanban task across several agents and pick the best attempt.
---

Open a **race** on a high-stakes or ambiguous task: several runs take independent shots at the same
task, and you pick the winner. A run is a real, claimable task linked to its parent; workers treat it
like any other task. Master tier.

Inputs: $ARGUMENTS — the project and the task to race, optionally how many runs or which agents.

1. **Open it:** `start_runs { projectId, taskId, version, count?, agents?, brief? }` on a TODO parent
   (or one you hold IN_PROGRESS). Each run is created TODO with the parent's brief; `agents` assigns one
   run per agent, which rings each of them. The parent leaves the claim pool while the race is open.
   Add a latecomer with `open_run { projectId, taskId, agent?, brief? }`. A task still waiting on a
   blocker (`blockedBy` not empty) cannot be raced, and a racing parent that gains one takes no new
   run (`INVALID_TRANSITION` naming the blockers): race it once they are DONE. The runs of a sprint
   member are in that sprint's frontier, so `claim_next { sprintId }` offers them.
2. **Compare:** `list_runs { projectId, taskId }` gives each run's status, assignee, lease holder and
   deliverable. Wait until the runs you want to judge have submitted a deliverable.
3. **Pick:** `pick_run { projectId, taskId, runTaskId, version }` promotes the winner's deliverable
   and artifacts onto the parent, which moves on (REVIEW when review is on, else DONE, which
   auto-unblocks its dependants). The losing runs are deleted.
4. **Or abandon:** `cancel_runs { projectId, taskId, version }` deletes every run and puts the parent
   back to TODO with nothing promoted.

Use a race sparingly: for work where one wrong attempt is expensive and divergent approaches are worth
comparing. Runs are alternatives with one winner; subtasks are parts that all must finish.

Task to race: $ARGUMENTS
