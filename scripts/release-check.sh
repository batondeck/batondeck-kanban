#!/usr/bin/env bash
# What `make dist` holds a release build to, so the published checksums are ones anyone can
# reproduce from the tag.
#
#   scripts/release-check.sh tree VERSION
#       The working tree is clean as the go command sees it (`git status --porcelain` empty,
#       untracked files included: that is what it stamps as vcs.modified) and HEAD is the commit
#       tag vVERSION names.
#   scripts/release-check.sh binaries VERSION FILE...
#       Every binary's build info (`go version -m`) records vcs.modified=false, vcs.revision
#       equal to the commit vVERSION names, and the Go go.mod pins (its `toolchain` line, else its
#       `go` line). A binary with no VCS stamp is refused. `make dist` passes it
#       dist/batondeck-kanban_*.
#   scripts/release-check.sh compose VERSION
#       compose.yaml pulls ghcr.io/batondeck/batondeck-kanban:VERSION, and no other tag of it.
#       `make image` runs it before docker, so an image is never pushed under a version the compose
#       file does not run.
#   scripts/release-check.sh VERSION
#       The release candidate's versions agree: compose.yaml (as above), the plugin's
#       plugin/.claude-plugin/plugin.json "version", a "## [VERSION]" section in CHANGELOG.md, and
#       the binary: `make build VERSION=VERSION` in a scratch file prints
#       "batondeck-kanban VERSION" from its `version` command. Each file that disagrees is named;
#       it needs no tag, so it runs before the tag is made (RELEASING.md).
#
# Run in the checkout being built. scripts/release-check-test.sh holds each to fixtures.
set -euo pipefail

die() { echo "release-check: $*" >&2; exit 1; }

usage="usage: $0 VERSION | tree VERSION | binaries VERSION FILE... | compose VERSION"
[ $# -ge 1 ] || die "$usage"
if [ $# -eq 1 ]; then
  mode=versions version=$1
  shift
else
  mode=$1 version=$2
  shift 2
fi
case $version in
'' | -* | *[!0-9A-Za-z.+-]* | tree | binaries | compose) die "not a version: '$version' ($usage)" ;;
esac
tag=v$version
image=ghcr.io/batondeck/batondeck-kanban

# compose_check prints why compose.yaml does not pull $image:$version, and nothing when it does.
compose_check() {
  [ -f compose.yaml ] || { echo "compose.yaml: missing"; return; }
  local refs
  refs=$(sed -n "s|^[[:space:]]*image:[[:space:]]*[\"']\{0,1\}\($image[^\"'[:space:]]*\).*|\1|p" compose.yaml)
  [ -n "$refs" ] || { echo "compose.yaml: pulls no $image image"; return; }
  printf '%s\n' "$refs" | while IFS= read -r ref; do
    [ "$ref" = "$image:$version" ] || echo "compose.yaml: pulls $ref, not $image:$version"
  done
}

case $mode in
compose)
  [ $# -eq 0 ] || die "compose takes no files"
  why=$(compose_check)
  [ -z "$why" ] || die "$why"
  exit 0
  ;;
versions)
  bad=$(compose_check)
  plugin=plugin/.claude-plugin/plugin.json
  if [ ! -f $plugin ]; then
    bad+=$'\n'"$plugin: missing"
  else
    got=$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' $plugin)
    [ "$got" = "$version" ] || bad+=$'\n'"$plugin: version is '$got', not $version"
  fi
  if [ ! -f CHANGELOG.md ]; then
    bad+=$'\n'"CHANGELOG.md: missing"
  elif ! awk -v h="## [$version]" 'index($0, h) == 1 { found = 1 } END { exit !found }' CHANGELOG.md; then
    bad+=$'\n'"CHANGELOG.md: no '## [$version]' section"
  fi
  scratch=$(mktemp -d "${TMPDIR:-/tmp}/release-check-bin.XXXXXX")
  trap 'rm -rf "$scratch"' EXIT
  makefile=$(cd "$(dirname "$0")/.." && pwd)/Makefile
  if ! make -s -f "$makefile" build VERSION="$version" BINARY="$scratch/bin" >"$scratch/build.log" 2>&1; then
    bad+=$'\n'"the binary: make build VERSION=$version failed: $(tail -3 "$scratch/build.log")"
  else
    got=$("$scratch/bin" version 2>&1) || true
    [ "$got" = "batondeck-kanban $version" ] || bad+=$'\n'"the binary: version prints '$got', not 'batondeck-kanban $version'"
  fi
  bad=$(printf '%s\n' "$bad" | sed '/^$/d')
  [ -z "$bad" ] || die "the versions do not agree on $version:
$bad"
  echo "release-check: $version agrees in compose.yaml, $plugin, CHANGELOG.md and the binary"
  exit 0
  ;;
esac

commit=$(git rev-parse -q --verify "refs/tags/$tag^{commit}") || die "no tag $tag"

case $mode in
tree)
  [ $# -eq 0 ] || die "tree takes no files"
  dirty=$(git status --porcelain)
  [ -z "$dirty" ] || die "the working tree is not clean; a release is built from the tag alone:
$dirty"
  head=$(git rev-parse HEAD)
  [ "$head" = "$commit" ] || die "HEAD is $head, not $tag ($commit)"
  ;;
binaries)
  [ $# -gt 0 ] || die "binaries needs at least one file"
  pinned=$(sed -n 's/^toolchain //p' go.mod)
  [ -n "$pinned" ] || pinned=go$(sed -n 's/^go //p' go.mod)
  for f in "$@"; do
    info=$(go version -m "$f") || die "$f: no Go build info"
    printf '%s\n' "$info" | grep -qx "[[:space:]]*build[[:space:]]vcs.modified=false" ||
      die "$f: build info does not record vcs.modified=false (built from a modified tree, or without VCS)"
    printf '%s\n' "$info" | grep -qx "[[:space:]]*build[[:space:]]vcs.revision=$commit" ||
      die "$f: build info does not record vcs.revision=$commit ($tag)"
    built=$(printf '%s\n' "$info" | sed -n '1s/^.*: //p')
    [ "$built" = "$pinned" ] || die "$f: built with $built, not the $pinned go.mod pins"
  done
  ;;
*)
  die "unknown mode $mode"
  ;;
esac
