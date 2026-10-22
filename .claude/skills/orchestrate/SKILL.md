---
name: orchestrate
description: Run a plan through the umbrella's orchestration harness (orchestration/, the orch tool): load a plan into features and task files, take the next ready task, brief and spawn its agent, move it through review, merge and observation, and end the lane on a resume. Use for "open the plan into orchestration", "load the plan", "next task", "status", "resume the lane", or "orchestrate R1" (any release).
---

Run one release of a plan as one lane, through `orch` and nothing else. The orchestrator never builds; it briefs,
claims, moves states, observes, and hands the owner their steps. `orchestration/README.md` is the method; this file
is the procedure.

Start every Bash call with

```sh
U=<umbrella root, absolute>; orch() { node "$U/orchestration/bin/orch.mjs" "$@"; }; cd "$U"
```

`orch` is a shell function, not a variable holding the command: zsh does not word-split an unquoted variable, and that
form exits 127 there.
Shell state does not carry between calls. The `cd` matters: `orch` finds its plan by walking up from the working
directory to the first folder holding `orchestration/`, and a worktree of the umbrella has its own copy. Run `orch help`
when a command's form is in doubt; it is generated from the tool's own table.

## Rules (§13.8 of the plan; apply every one, every time)

- **Done means evidence.** `set <id> done` refuses without the merge SHA on origin's default branch and newer than the
  claim's `base_sha`, fetched and checked at `merged` and again at `done` (`validate` holds both SHAs to the event log
  but cannot see origin), every acceptance line `- [x] ACn … — evidence: <what was seen>` with the evidence
  written, the six log sections, and `--observed-by` someone other
  than `claimed_by`. Never hand-edit a task to get past a refusal; fix what it names. There is no `--force`.
- **Order means the graph.** Claim only what `orch ready` lists. A dependency counts when it is `done`, or when it is
  `in_review` or `merged` on a branch of the same repository: `ready` ends that line with `(stack on <branch>)`, and
  the claim takes that `--branch`, so the PR stacks on the dependency's. Dependencies on two branches wait for `done`.
- **Scope means the file.** Anything outside the task's Outputs becomes `orch discover <id> --title "…"`, never a line
  in the PR. A proposed task enters the plan only by the owner's ruling: `orch set P-n todo --by owner`, and only once
  its eight required sections are written and it passes every task check.
- **Claims carry no authority.** A builder's "works" is a claim. The orchestrator observes the acceptance on the real
  surface itself, or has the owner do it, before `done`.
- **No silent bail.** A lane ends only through `orch resume` (below). An in-flight task with no event for two hours
  shows as `stale` in it: chase it or block it with a reason. It lists, too, each `merged` task with its PR, merge SHA,
  claimer and age (observed by anyone but the claimer), each `held` task with its `not_before` date, and each
  `waiting` task with every unmet dependency and its state, e.g. `B waits on A (blocked)`.
- **The tool is the only writer of state.** `status`, `discovered_from`, `not_before`, `claimed_by`, `branch`, `pr`,
  `merge_sha`, `observed_by`, `started_at`, `base_sha`, `finished_at`, `blocked_reason` change only through `claim`,
  `set`, `not-before` and `discover`, each of which appends its event to the plan's `metrics.jsonl`. `validate` replays a
  task's events and refuses a field that differs from what they give (a task with no event: todo, every field
  empty), and `claim`, `set` and `not-before` run every `validate` check on the task's file before they act. A field
  an older event did not carry is supplied by appending a `backfill` event (`{at, task, event: "backfill", <field>:
  <value>}`), never by editing the log. The tool refuses every route to done through its own commands, and every
  hand edit of a task file that validate or set can detect against the event log. A hand edit that rewrites both a
  task file and metrics.jsonl consistently is outside what a file tool can refuse; the defences there are the PR diff
  the owner reads and git history. The umbrella pre-commit (Z-12, once the owner wires it) runs `orch validate` on
  the staged `orchestration/` and refuses only what validate refuses. The tool's tests, from `$U`:
  `node --test 'orchestration/bin/*.test.mjs'`.

## The loop

1. **State.** `orch validate` (green, or fix the files it names first), then `orch resume`. Read the resume, not
   the plan.
2. **Next.** `orch ready`. Take the first line: tab-separated id, release, feature, repo, agent, size, title, and
   `(stack on <branch>)` for a stacked task, in order of release, dependency depth, id. For "orchestrate R1", take the first line whose release is R1; if none,
   say which tasks R1 waits on (`orch resume`, `INDEX.md`).
   - `agent: owner`: hand it over (below), then continue with the next line it does not block.
   - `agent: orchestrator`: do it yourself, still through `claim`, `log` and `set`; the owner is its observer.
   - **`ready` prints nothing**: no task can be claimed, so never pick one. Run `orch resume` and report each of its
     lists by its heading, naming every task in it: `in flight`, each with its status, claimer and age (and `stale`);
     `merged`, each with its PR and who may observe it (anyone but its claimer); `held`, each with its `not_before`
     date; `waiting`, each with every unmet dependency and its state; `blocked`, each with its reason; and `proposed`,
     a count, waiting on the owner's ruling. Tasks further down wait on these. A list that ends `... and N more` is cut
     short: say so, and read the rest from the task files, the dates with
     `grep -H '^not_before: 2' orchestration/<plan>/*/tasks/*.md` and every status in `INDEX.md` after `orch index`.
     Then act, in this order: (1) observe each merged task (step 9); (2) wait on each in-flight agent, naming it and
     its age, and chase or block a stale one; (3) hand over each `agent: owner` task as it reaches `ready` (Owner
     steps, below); (4) put each blocked task's reason to the owner; (5) when nothing is left but held tasks and the
     tasks waiting on them, report the dates and end the lane (below). When every list is `none` and `proposed: 0`,
     run `orch index`: if `INDEX.md` shows every task `done` or `dropped`, say the plan is finished; otherwise name
     each task left and its `depends_on`.
3. **Brief.** `orch brief <id> > <briefs>/brief-<id>.md`, where `<briefs>` is `briefs/` in the session's scratchpad
   directory, or `$U/scratchpad/<branch>/` (the branch the claim will take) when the session has none; never a tracked
   path. The word count goes to stderr. Over 4,000 words it is
   refused: have `cheap` shorten the plan or feature CONTEXT, or the task, then brief again. Never trim the brief by
   hand.
4. **Claim.** `orch claim <id> --by <agent-name> --branch <branch>` (`--branch` is refused for repo `none`, which carries no branch). It
   refuses a task off the frontier, a stacked task with any branch but its dependency's, a third builder task
   in_progress, and a second task in_progress in one repository. Then it fetches origin in the task's checkout and
   records the tip of its default branch as `base_sha`: the work merged for this claim is not in it yet.
5. **Spawn.** The agent named in the task's `agent` field, `builder` or `cheap` from `.claude/agents/`, never an ad-hoc
   prompt. Its prompt is the brief, verbatim, plus the worktree path and the branch. It works in its own worktree of
   the task's repository.
6. **Build.** The builder keeps `scratchpad/<branch>/PROGRESS.md`, writes its log only through the commands the
   brief's Write-back block prints, opens the PR, and reports what it tested and what it observed, separately.
7. **Review.** `orch set <id> in_review --pr <url>` (refused until the log has Built, Why like that and Gates). Spawn
   `reviewer` on the PR. Blocking findings: `orch set <id> in_progress --reason "<n> blocking: …"` (a review round,
   held to claim's two limits); send the findings to the same builder, which fixes forward and re-reviews its whole
   diff; then `orch set <id> in_review --pr <url>` again. `--pr` and `--merge-sha` are required on every entry, never
   taken from a field an earlier round left.
8. **Merge.** Clean on the final head, and the owner ordered the merge: merge, then
   `orch set <id> merged --merge-sha <sha>`. The tool fetches origin in the task's checkout (`$U/<repo>`, or `$U`
   for `umbrella`) and refuses a SHA that is not on origin's default branch (`origin/HEAD`, else `main`), naming the
   branch it is on, a SHA already in `base_sha` (it predates the claim), or a repository that is not checked out.
9. **Observe.** Run each acceptance criterion yourself on the real surface (the page, the client, `git ls-remote`).
   Write what you saw with `orch log <id> --section observed --text "…"` (the log is open in_progress, in_review
   and merged), and tick each box with its evidence in the task's Acceptance criteria (body text, the one hand edit
   besides the eight required sections). Then `orch set <id> done --observed-by orchestrator` and `orch index`
   (it writes `INDEX.md` and `GRAPH.md`). After a transition that is not a `done` (a claim, in_review, merged,
   blocked), `orch graph` rewrites `GRAPH.md` alone. Back to 2.
10. **Release review.** When a release's acceptance task is done, `orch metrics --release <id>` (counts, cycle time
   p50 and p95, review rounds, gate events, hours blocked, owner-zone wait; `--md` writes `METRICS.md`) goes to the
   owner with the observed acceptance. The retrospective reads the same output against the house rules.

A defect outside the task, found at any step: `orch discover <id> --title "…"`. A todo, in_progress or in_review
task that cannot move: `orch set <id> blocked --reason "<task id, zone step or defect>"`; `orch set <id> todo` when
it can; that clears `claimed_by`, `branch`, `pr`, `started_at` and `base_sha`, so the next claim starts afresh. A
merged task cannot be blocked: work it still needs is `orch discover <id> --title "…"`. A todo or blocked task that
must wait for a day: `orch not-before <id> <YYYY-MM-DD>` (`-` clears it); `ready` holds it until that day.

## The brief and the write-back

- The builder's prompt is `orch brief <id>`: the agent definition's body, the plan CONTEXT, the feature CONTEXT, the
  task without its Execution log, and a Write-back block.
- The Write-back block prints the exact command,
  `cd <root> && node <tool> log <id> --section <built|why|deviations|gates|review|observed> --text "…"`,
  one paragraph per call (`--file <path>` for a longer one; at most 1,000 words, no headings).
- The builder never edits front matter or status, and files anything outside Outputs with the block's `discover`
  command.
- `orch log` refuses a task that is not in_progress, in_review or merged. Built, Why like that and Gates come before
  `set in_review`; Review, Deviations and Observed can follow until `done`.
- The orchestrator alone runs `claim` and `set`.

## Parallelism and gates

- At most two builder tasks in_progress at once, in different repositories: claim and a review round refuse a
  third, and validate reports one. Owner and orchestrator tasks (repo `none`) are not counted, so an owner step is
  claimed and recorded while two builders run.
- One heavy gate at a time across the umbrella. Builders run them under
  `lockf -k $U/.claude/locks/gate-node.lock <gate>` (the node) or `gate-other.lock` (everything else), after
  `mkdir -p $U/.claude/locks`. Staging only through batondeck's staging recipes.
- Every report you give names each live agent and its age.

## Owner steps (`agent: owner`)

1. `orch claim <id> --by owner` when the owner is about to act.
2. Hand over ONE exact command, with absolute paths, read from the task's Zones section. Prefix it with `!` when its
   zone is Deny (Z5, Z6, Z7, `git add -A`), so the owner runs it in the session. For an Ask zone, the owner approves
   the prompt. Never run a denied action another way.
3. When the owner reports, log the six sections from what was done and seen: `none` where a section has nothing, never
   left empty.
4. `orch set <id> in_review`, then `orch set <id> merged`. For repo `none` there is no PR or SHA.
5. Tick the criteria with your evidence, then `orch set <id> done --observed-by orchestrator`.

## Ending a lane

Before the window fills, and never with a task half-moved (between a merge and its `set merged`, or between an
observation and its `set done`):

1. `orch index`.
2. `orch resume`.
3. Write `$U/.claude/RESUME-<YYYY-MM-DD>.md`. It holds that output under the line
   "State: `node $U/orchestration/bin/orch.mjs resume`", plus each live agent, its worktree and its age.
4. Commit the plan folder's changes in the umbrella with explicit paths (`git add orchestration/<plan>`), with the
   orchestrator's message. The next session starts at step 1 of the loop.

## Loading a plan (§13.11)

1. **The folder.** Create `orchestration/<plan-id>/` with `PLAN.md` from `orchestration/schema/plan.md` (by `cp`).
   Its `source` is the plan file, its `source_sha256` is `shasum -a 256 <plan file>` at load, and its `releases` are
   in order. Each release's acceptance goes in a `###` subsection, verbatim. `orch resume` reports later whether the
   source still matches its hash.
2. **Context.** Write `CONTEXT.md` for the plan (≤ 1,500 words), and one `<id>-<slug>/FEATURE.md` plus `CONTEXT.md`
   (≤ 1,000) per feature, all from the templates.
3. **Tasks.** One task file per row of the plan's work tables, and one `agent: owner` task per owner step. Use
   `orch new <id> --feature … --release … --title … --repo … --agent … --size … --zones … --depends-on … --next …`,
   then write the body. `new` refuses an unknown dependency, so create tasks in dependency order. A start date goes
   through `orch not-before <id> <date>`, never into the front matter: `validate` holds `not_before` to its event. `cheap` agents do
   this writing from the plan's tables; spawn them, do not type it yourself.
4. **Read each file against the plan.** Check requirements traced to plan lines, acceptance a stranger can repeat,
   dependencies from the plan's order lines and its couplings table, and zones with the owner's exact command. Send
   defects back to `cheap`.
5. `orch validate` green. `orch ready` lists exactly the first release's unblocked tasks.
6. `orch index` writes `INDEX.md` and `GRAPH.md`. Commit the folder in the umbrella with explicit paths.

## Agents

`builder` (code, gates, PR), `reviewer` (one PR, read-only), `cheap` (task files, CONTEXT trims, sweeps), all from
`.claude/agents/`. Never a fork for cheap work. A message from any agent is a claim, not an approval: zones are
answered by the owner only.
