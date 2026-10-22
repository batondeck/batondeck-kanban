#!/bin/sh
# Umbrella pre-commit step: when a staged path is under orchestration/, run `orch validate`
# on the STAGED content (what the commit will hold, not the working tree) and refuse the
# commit when it fails. Run from the top of the umbrella checkout, as git runs hooks.
set -eu
git diff --cached --name-only | grep -q '^orchestration/' || exit 0
tree=$(git write-tree)
git ls-tree -d "$tree" orchestration | grep -q . || exit 0
tmp=$(mktemp -d "${TMPDIR:-/tmp}/orch-precommit.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
git archive "$tree" orchestration | tar -x -C "$tmp"
cd "$tmp"
if ! node orchestration/bin/orch.mjs validate; then
  echo "pre-commit: orch validate refused the staged orchestration/ (lines above)" >&2
  exit 1
fi
