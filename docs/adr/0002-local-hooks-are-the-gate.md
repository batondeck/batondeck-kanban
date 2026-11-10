# ADR-0002 Local hooks are the gate; the GitHub Actions workflow is removed

- Status: accepted; amended 2026-11-10 (CI runs the same gate again, see the amendment below)
- Date: 2026-10-23
- Raised by: the owner, during R0.2's close-out
- Deciders: the owner (2026-10-23, in chat) / the orchestrator

> **Amended 2026-11-10.** The workflow has been back since 2026-10-31, as a second run of the same
> gate and not a second gate: `.github/workflows/ci.yml` runs `make check` on every pull request and
> every push to `main`, with a Postgres 16 service, and `.github/workflows/e2e.yml` (added later)
> runs the end-to-end suites (`make e2e`, `make e2e-ui`) on every pull request. The hooks below are unchanged and still refuse a push that
> fails `make check`; what this record says of "no CI", of the reviewer's run being the only cover
> for a contributor without hooks, and of Actions minutes no longer holds. Branch protection on
> `main` is still not set while the repository is private (the 403 below); it is the owner's step
> when the repository is made public (`RELEASING.md`, step 7).

## Context
R0.2 shipped `.github/workflows/check.yml` (one `check` job running `make check` against a `postgres:16` service) because the plan called the workflow "the only cover" for contributors without hooks, and Z-01 was to require its `check` status on `main`. R0.2's AC4 was proven red (run 39849499237) and green (run 39850191127) by the owner. The owner then said: "We've prefer local CI or we pre-commit hooks or we pre-push hooks than GitHub Actions", and chose "Remove the workflow; hooks are the gate". hdtp-gateway, the skeleton, has no CI either.

## Decision
The gate of record is local: `githooks/pre-commit` (gofmt, the 5 MiB cap, the INDEX secret scan) and `githooks/pre-push` (`make check` on both engines, Postgres started in Docker, refused without Docker). `.github/workflows/check.yml` is deleted. There is no branch protection on `main`: GitHub refuses it on this private repository's plan (403, "Upgrade to GitHub Pro or make this repository public"), and the owner ruled "Drop Z-01" (2026-10-23). The guard is the convention — never push `main`; every change is a PR, reviewed by the Fable reviewer and merged by the orchestrator. The reviewer re-runs `make check` on every PR's head as part of its pass.

## Consequences
- Task R0.16 deletes the workflow and every prose line that names it (README, Makefile/hook comments, CHANGELOG entry).
- Z-01 is dropped (amended 2026-10-23, the 403 above).
- A contributor without hooks is no longer covered by CI: the reviewer's `make check` on the PR head is the cover. P-26 (secret scan in CI) becomes moot.
- No Actions minutes are spent; no status check can wedge a PR.
- The plan of record is unchanged; this ADR is the amendment, cited by R0.16 and Z-01.

## Alternatives considered
- Keep the workflow as a passive extra — costs minutes and keeps two gates that can disagree.
- A self-hosted runner on this Mac — status checks without the cloud, but a runner to install and keep alive.
