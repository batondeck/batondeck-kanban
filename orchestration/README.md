# orchestration: how a plan is run

One folder per plan, per feature, a file per task, and one tool, `bin/orch.mjs` (Node built-ins only), the
only writer of task state, run from anywhere in the umbrella.

## Objects

| Object | File (template) |
|---|---|
| Plan: source path and sha256, releases in order, each release's acceptance, feature map | `<plan>/PLAN.md` (`schema/plan.md`) |
| Plan context, ≤ 1,500 words | `<plan>/CONTEXT.md` (`schema/context.md`) |
| Feature: goal, boundary, releases, acceptance, task order | `<plan>/<id>-<slug>/FEATURE.md` (`schema/feature.md`) |
| Feature context, ≤ 1,000 words: what to read, in order, and what not to | `<plan>/<feature>/CONTEXT.md` |
| Task, the unit of work | `<plan>/<feature>/tasks/<id>-<slug>.md` (`schema/task.md`) |
| ADR, a decision made while executing | `<plan>/adr/ADR-NNNN-<slug>.md` (`schema/adr.md`) |

Generated: `INDEX.md` (the board), `GRAPH.md` (mermaid), `METRICS.md`. `metrics.jsonl` gets a line per
event and is never rewritten.

## States, and who moves them

| From → to | Command | The tool checks |
|---|---|---|
| todo → in_progress | `claim <id> --by --branch` | on the frontier; a stacked task takes its dependency's `--branch`; under two builder tasks in_progress (repo none uncounted), one per repository; records `base_sha`, origin's default-branch tip, or forty zeros (`empty_origin`, nothing in base) if origin has no refs; no `origin` remote: refused |
| in_progress → in_review | `set <id> in_review --pr` | `--pr` every time; the log's Built, Why like that, Gates |
| in_review → in_progress | `set <id> in_progress --reason` | the findings; a review round; claim's in_progress limits, naming whom to block |
| in_review → merged | `set <id> merged --merge-sha` | `--merge-sha` every time; fetched, on origin's default branch (`origin/HEAD`, else `main`), not in `base_sha`; a refusal names its branch |
| merged → done | `set <id> done --observed-by` | items 4–6 and 8 of the definition of done |
| todo, in_progress, in_review → blocked | `set <id> blocked --reason` | the reason; not from merged: `discover` instead |
| blocked → todo; proposed → todo | `set <id> todo`; `--by owner` for proposed | the eight sections filled; blocked clears the claim |
| not done or dropped → dropped | `set <id> dropped --reason` | the order's words, appended to Deviations |
| (new) proposed | `discover <from-id> --title` | never on the frontier |

The orchestrator alone runs `claim` and `set`; they and `not-before` run every task check on the file first. Builders
write only through `orch log`. `repo: none` tasks (owner and orchestrator only) carry no branch, PR, base or
merge SHA.

## Definition of ready (`orch validate`)

Front-matter keys exactly the template's, in order; `status`, `agent`, `size`, `zones` from their sets; `release` one
of PLAN.md's; `feature` the folder's; file name `<id>-<slug>.md`; ids unique; every `depends_on`, `next` and
`discovered_from` exists; no cycle; claim's in_progress limits; the template's headings in order, the first
`# <id> <title>`; the eight sections from Why to Zones non-empty except on a proposed task, template sentences alone counting as empty; every acceptance
line `- [ ] ACn … — evidence: …`, a ticked one with its evidence; the budgets; each field the tool writes as the
replay of the task's events in `metrics.jsonl` gives it (none: a todo, every field empty); from a claim, `repo` and `agent` as its event gives. `orch ready` offers a
`todo` task past its `not_before` (`orch not-before <id> <date|->`, todo or blocked only) whose dependencies are
`done`, or `in_review`/`merged` on one branch of its repository: that line ends `(stack on <branch>)`.

## Definition of done (the standard list)

1. The gate green from the repository's hooks, command and result in the log's Gates.
2. The PR opened, its body listing every surface it adds.
3. A `reviewer` pass with zero blocking findings on the final head, that SHA in the log's Review.
4. Merged when the owner ordered it, the SHA on the default branch. **Checked by the tool** on origin at merged and at
   done.
5. Every acceptance box ticked with its evidence. **Checked by the tool.**
6. The log's six sections filled. **Checked by the tool.**
7. Discovered work filed with `orch discover`, never folded into the PR.
8. `observed_by` set by the orchestrator or the owner, not the claimer. **Checked by the tool.**
9. `next` set where there is one; `orch index` run.

Items 1–3, 7 and 9: the orchestrator's and reviewer's.

## The loop: one release is one lane, one lane is one session

1. `orch validate`, then `orch resume`.
2. `orch ready`; take the first task. An `agent: owner` task goes to the owner as one exact command.
3. `orch brief <id>`, `orch claim <id>`; spawn `builder` or `cheap` from `.claude/agents/` with the brief, in its
   own worktree. One heavy gate at a time.
4. The builder builds, logs through `orch log`, opens the PR, reports tested and observed separately.
5. `orch set <id> in_review`; spawn `reviewer`. Findings: `set <id> in_progress --reason`. Clean and ordered:
   merge, `set <id> merged --merge-sha`.
6. Observe the acceptance on the real surface; `set <id> done --observed-by`; `orch index`. Back to 2.
7. End the lane on `orch resume` before the window fills, never on a half-moved state.

## Scrum, mapped

Backlog: the task files; board: `INDEX.md`. Epic: feature. Sprint: release. Planning: `orch ready`. Story: task.
Daily: `orch resume`. Review: the release's acceptance observed, and `orch metrics`. Retrospective: `orch metrics`
against the house rules. Product owner: the owner. Scrum master: the orchestrator, which never builds. Team:
`builder`, `reviewer`, `cheap`.

## Measurement (`orch metrics`)

Counts from the task files. From `metrics.jsonl`: cycle time, claim to last `done`, p50 and p95 hours; review rounds
per task that reached merged, mean and max; gate events (`log --section gates`); hours blocked, each entry into
blocked to the next exit, or now; owner-zone wait, for each `agent: owner` task with every dependency done, from the
latest of those `done` events, the plan's first event and `not_before`, to its own `done`, or now.

## Governance

- There is no `--force`. The tool refuses every route to done through its own commands, and every hand edit that
  validate or set can detect against the event log. A task file and metrics.jsonl rewritten consistently are beyond a
  file tool; the defences there are the PR diff the owner reads and git history; the pre-commit (Z-12) refuses only
  what validate does.
- `orch resume`: in flight (stale after two hours without an event), merged (awaiting an observer),
  ready, held (`not_before` ahead), waiting (every unmet dependency in flight, merged, blocked or held), blocked.

## Context budgets

A brief is the agent definition, both CONTEXT files, the task without its log (≤ 1,200 words) and the write-back
block; `orch brief` refuses one over 4,000 words.

## Commands

`orch help` prints every form; others:

```sh
orch() { node orchestration/bin/orch.mjs "$@"; }
orch log R1.3 --section gates --text "make check: green"
orch graph
orch metrics --release R1
```

Tests: `node --test 'orchestration/bin/*.test.mjs'`.

## The pre-commit wiring (the owner's, zone Z7)

`bin/pre-commit.sh` runs `orch validate` on the staged tree when the commit touches `orchestration/`, and refuses
a failing commit. The owner wires it once, from the umbrella's main checkout:

```sh
! printf '%s\n' '#!/bin/sh' 'exec sh orchestration/bin/pre-commit.sh' > githooks/pre-commit && chmod +x githooks/pre-commit
```
