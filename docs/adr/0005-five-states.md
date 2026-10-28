# ADR-0005 Five states: TODO replaces READY, BACKLOG removed

- Status: accepted
- Date: 2026-10-23
- Raised by: the owner ("rename Ready to TODO and remove backlog. if task is added, it can't be draft"), plan Amendment E
- Deciders: the owner (2026-10-23, in chat) / the orchestrator

## Context
The board had six states and a fixed transition table: BACKLOG, READY, IN_PROGRESS, BLOCKED, REVIEW, DONE (plan "Ticket states", owner rulings 2026-10-22). BACKLOG is a parking state: a task there is not claimable, so a created task is a draft until someone moves it. The owner, verbatim: "rename Ready to TODO and remove backlog. if task is added, it can't be draft". No release has shipped, so only dev databases carry BACKLOG or READY rows. `contract/expected.json` names the status enum 122 times.

## Decision
- **States:** `TODO · IN_PROGRESS · BLOCKED · REVIEW · DONE`, five in all. Poison stays BLOCKED with `blockedReason: "poison"`, and delete stays a hard delete.
- **The fixed table.** Same-status moves stay legal, giving 9 edges plus 5 same-status moves: 14 legal of 25 pairs.

  | From | To |
  |---|---|
  | TODO | IN_PROGRESS |
  | IN_PROGRESS | REVIEW, BLOCKED, TODO |
  | BLOCKED | IN_PROGRESS, TODO |
  | REVIEW | DONE, IN_PROGRESS |
  | DONE | IN_PROGRESS |

- **Creation:** every created task lands in TODO and is claimable at once; `create_task` takes no initial `status`. An unassigned task created in TODO rings the broadcast doorbell, which is the old "became READY" rule renamed.
- **Landings:** unblock, retry backoff, release, lease expiry and un-poison all land on TODO.
- **Board:** the default board has 5 columns: To do, In progress, Blocked, Review, Done.
- **No parking state:** unwanted work is deleted.
- **Migration 0004 (Z2), on both engines.** Accepting this plan is the owner's approval of it.
  - Up: `UPDATE tasks SET status='TODO' WHERE status IN ('BACKLOG','READY')`, and the same for the second status table. Both CHECKs are rewritten to the five values. The Backlog board column folds into To do, and To do keeps its WIP limit.
  - Down: TODO maps back to READY, and the six-value CHECK is restored.
- **Contract:** the `status` enums in `expected.json` and `deltas.json` become the five values. A `deltas.json` entry records the divergence from conductor, and the comparator stays exact against the edited file.

## Consequences
- New task R0.26 is the one mechanical PR. It lands after #29 (R1.3) and #31 (R1.5) merge, and before R1.2, R1.4, R1.6, R0.23 or R0.25 start, so no later lane writes the old names. Done means `git grep -n 'BACKLOG\|READY\|domain.Backlog\|domain.Ready'` finds nothing outside the down migration and `deltas.json`.
- R1.2, R1.4 and R1.6 depend on R0.26 and use the five names. R1.2's `create_task` takes no initial status.
- R1.3 and R1.5, in review, keep their acceptance criteria; R0.26 renames their state constants after they merge.
- The 6×6 state-machine test becomes 5×5. R1.3's 36-move test becomes 25 moves (14 legal, 11 `INVALID_TRANSITION`), and R1.18 and R1-accept follow.
- R1.14 (the SPA), R2.2 (runs), R2.9 (on-call autoclose), R3.4 (the skills) and R3.5 (the bench) use the five names.
- This supersedes the plan's "Ticket states" section, which R0.26's PR points at this ADR.
- Easier: no draft state to explain, and no move needed before a task is claimable.
- Harder: conductor's six-value enum differs, so the contract diverges and `deltas.json` must say so. Work that is not ready yet has no parking state; it is either a TODO task or not created.

## Alternatives considered
- Keep BACKLOG as a non-claimable parking state: the owner ruled against drafts ("if task is added, it can't be draft").
- Keep the name READY: the owner asked for TODO. TODO also matches the column name the SPA shows.
- Keep conductor's six-value enum on the wire and map it internally: two names for one state, and a contract that lies about what the server stores.
