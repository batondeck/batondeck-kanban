# ADR-0001 cancel_task and system labels replace the CANCELLED and DEAD_LETTER outcomes

- Status: accepted
- Date: 2026-10-23
- Raised by: R0.4 (discoveries P-27, P-28, reviewer round 1)
- Deciders: the owner (2026-10-23, in chat) / the orchestrator

## Context
The six-state ruling removed CANCELLED (plan §Fold 2: abandoning a ticket is a hard `delete_task`) and DEAD_LETTER (plan §Fold 1: poison is BLOCKED with `blockedReason: "poison"`). R0.4's contract work found two leftovers in conductor's surface: `settings.transitionTable` in the outputs of `get_project` and `update_settings` (plan §Removed contradicts itself on it), and `get_sprint`'s `attention.why` value `dead_letter`, unreachable once a poisoned task is BLOCKED (`sprints.ts:338-340`). Asked how an abandoned ticket should be represented, the owner said: "we can have cancelled queue / tag which will be auto applied when its moved to done since we dont have dead letter now. suggest options", then chose "cancel_task → DONE + auto tag".

## Decision
1. **`cancel_task {projectId, taskId, version, reason}`** is an added tool: it moves the task to DONE, appends the system label `cancelled`, and records `reason` as a context note. Allowed to the master role or the holder of the task's live lease (amended 2026-10-28, audit D16: the code's rule); version-checked (`STALE`); `CONFLICT_LOCKED` on another identity's live lease, and on another agent's of the caller's identity (amended 2026-11-04, P-399: a lease that recorded an agent is that agent's, as P-320 made it for the lease-id outcomes; at master, the identity's caller that names no agent still passes: its browser (through `move_task`) or another agentless caller, of which only an MCP session with no token label or OAuth `client_name` and an empty `clientInfo.name` reaches `cancel_task`; `move_task`, `delete_task` and `start_runs` follow the same rule, `delete_task` over every task it would delete, P-416). A task already DONE is not cancelled (`INVALID_TRANSITION`), and a racing parent is `CONFLICT_LOCKED` (reason `race`) until `pick_run` or `cancel_runs`, as `move_task` refuses it (amended 2026-10-30, P-150). `delete_task` stays for real removal.
2. **Poison carries a label:** when `fail_task` or the reaper poisons a task (BLOCKED, `blockedReason: "poison"`), it also appends the system label `poisoned`; un-poisoning (master, BLOCKED → TODO since ADR-0005; amended 2026-10-28, audit D16) removes it.
3. **The "queue" is a label filter** (`cancelled`, `poisoned`) over the existing task labels — no new state, field or view.
4. **`settings.transitionTable` is dropped** from the `get_project` and `update_settings` outputs (owner: "Drop it").
5. **`attention.why` `dead_letter` is renamed `poisoned`**, tested on `blockedReason == "poison"` before the plain `blocked` reason.

## Consequences
- Contract (R0.4): `addedTools` gains `cancel_task` (93 tools); `removedProps` gains the two `settings.transitionTable` outputs; `attention.why`'s enum swaps `dead_letter` for `poisoned`.
- Domain (R0.6): the poison transition appends/removes the `poisoned` label; the two system labels are reserved names a caller cannot add or remove by hand.
- Lifecycle tools (R1.5): `cancel_task` lands with complete/block/fail; its happy path and every declared error get a harness test.
- UI (R1.14): the poison badge and a `cancelled` tag read the labels; the board's label filter is the queue.
- Plan of record: unchanged file; this ADR is the amendment, cited by the tasks it touches.

## Alternatives considered
- DONE + a `resolution` field — a new field and two input props; the owner preferred labels.
- A "Parked" board view with nothing written — the owner wanted the tag on the task.
- No cancelled concept (the approved plan) — the owner asked for one.
