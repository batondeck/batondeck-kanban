#!/usr/bin/env bash
# Runs this checkout's `make dist` against fixture repositories, with no network and nothing
# published: refused on a dirty tree (untracked file, modified file), on an untagged HEAD and on a
# HEAD tagged with another version; passing on a clean checkout of the tag, with binaries that
# record vcs.modified=false. `make image` against the same fixtures and a stubbed docker: refused
# on a dirty tree and an untagged HEAD; otherwise both platforms, both tags and the version, --push
# only with PUSH=1. Then the binary check alone: a build from a modified tree, a build with no VCS
# stamp and a build of another commit are each refused. Run by `make check`.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
makefile=$root/Makefile
check=$root/scripts/release-check.sh

# A hook in a linked worktree exports GIT_DIR and friends; the fixtures are repositories of their own.
while IFS= read -r v; do unset "$v"; done < <(env | sed -n 's/^\(GIT_[A-Za-z_]*\)=.*/\1/p')
export GOWORK=off GOFLAGS='' GOTOOLCHAIN=local GOPROXY=off GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null

tmp=$(mktemp -d "${TMPDIR:-/tmp}/release-check.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

fail=0
pass() { echo "release-check: ok   $1"; }
bad() { echo "release-check: FAIL $1" >&2; fail=1; }

g() { git -c user.name=fixture -c user.email=fixture@example.invalid -c commit.gpgsign=false -c tag.gpgSign=false "$@"; }

goline=$(go env GOVERSION | sed -E 's/^go([0-9]+\.[0-9]+).*/\1/')

fixture() {
  local dir=$tmp/$1
  mkdir -p "$dir/cmd/batondeck-kanban"
  printf 'module example.invalid/fixture\n\ngo %s\n' "$goline" > "$dir/go.mod"
  printf 'package main\n\nvar version string\n\nfunc main() { println(version) }\n' > "$dir/cmd/batondeck-kanban/main.go"
  printf '/dist/\n' > "$dir/.gitignore"
  (cd "$dir" && g init -q && g add -A && g commit -q -m fixture && g tag -a v9.9.9 -m v9.9.9)
  echo "$dir"
}

# docker, stubbed: it records its arguments in $tmp/stub.log and builds and pushes nothing.
mkdir -p "$tmp/stub"
cat > "$tmp/stub/docker" <<'STUB'
#!/bin/sh
echo "docker $*" >> "$STUB_LOG"
STUB
chmod +x "$tmp/stub/"*
export STUB_LOG=$tmp/stub.log

dist() {
  : > "$STUB_LOG"
  PATH=$tmp/stub:$PATH make -s -C "$1" -f "$makefile" dist VERSION=9.9.9 PLATFORMS="${2:-linux/amd64}" >"$tmp/out" 2>&1
}

expect_refused() {
  local name=$1 dir=$2 why=$3
  if dist "$dir"; then
    bad "$name: dist built"
  elif ! grep -q "$why" "$tmp/out"; then
    bad "$name: refused, but not for '$why':"; cat "$tmp/out" >&2
  elif [ -e "$dir/dist" ]; then
    bad "$name: refused, but after writing dist/"
  else
    pass "$name: refused"
  fi
}

# The control: a clean checkout of the tag builds, and its binary records the tag's commit, unmodified.
d=$(fixture clean)
if dist "$d"; then
  if go version -m "$d/dist/batondeck-kanban_9.9.9_linux_amd64" | grep -qx "[[:space:]]*build[[:space:]]vcs.modified=false" &&
    grep -q batondeck-kanban_9.9.9_linux_amd64 "$d/dist/SHA256SUMS"; then
    pass "clean tagged tree: built"
  else
    bad "clean tagged tree: built, but the binary or SHA256SUMS is wrong"
  fi
else
  bad "clean tagged tree: refused:"; cat "$tmp/out" >&2
fi

d=$(fixture untracked); touch "$d/stray"
expect_refused "untracked file" "$d" "not clean"

d=$(fixture modified); echo '// edit' >> "$d/cmd/batondeck-kanban/main.go"
expect_refused "modified tracked file" "$d" "not clean"

d=$(fixture untagged); (cd "$d" && g commit -q --allow-empty -m later)
expect_refused "HEAD past the tag" "$d" "not v9.9.9"

d=$(fixture othertag); (cd "$d" && g tag -d v9.9.9 >/dev/null && g tag -a v9.9.8 -m v9.9.8)
expect_refused "HEAD tagged with another version" "$d" "no tag v9.9.9"

# The tree changing while dist builds: a `go` that leaves a stray file before building. The tree
# check has passed by then, so only dist's check of the binaries can refuse.
mkdir -p "$tmp/gostub"
realgo=$(command -v go)
cat > "$tmp/gostub/go" <<STUB
#!/bin/sh
[ "\$1" = build ] && touch stray
exec "$realgo" "\$@"
STUB
chmod +x "$tmp/gostub/go"
d=$(fixture duringbuild)
if PATH=$tmp/gostub:$PATH dist "$d"; then
  bad "tree modified during the build: dist passed"
elif ! grep -q "vcs.modified=false" "$tmp/out"; then
  bad "tree modified during the build: refused, but not by the binary check:"; cat "$tmp/out" >&2
else
  pass "tree modified during the build: refused"
fi

# sbom is a release asset too: refused on a dirty tree before it runs its tool (GOPROXY=off, so a
# check that let it through fails on the fetch, not on "not clean").
d=$(fixture sbom); touch "$d/stray"
if make -s -C "$d" -f "$makefile" sbom VERSION=9.9.9 >"$tmp/out" 2>&1; then
  bad "sbom on an untracked file: ran"
elif ! grep -q "not clean" "$tmp/out"; then
  bad "sbom on an untracked file: failed, but not for 'not clean':"; cat "$tmp/out" >&2
else
  pass "sbom on an untracked file: refused"
fi

# image: the published image's build, against the stubbed docker (which builds and pushes nothing).
# The control first: a clean checkout of the tag builds both platforms with the vendored crates and
# the version, under both tags, and keeps it (--load) unless PUSH=1.
image() {
  : > "$STUB_LOG"
  PATH=$tmp/stub:$PATH make -s -C "$1" -f "$makefile" image VERSION=9.9.9 ${2:+PUSH=$2} >"$tmp/out" 2>&1
}
want_build='^docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=9.9.9 -t ghcr.io/batondeck/batondeck-kanban:9.9.9 -t ghcr.io/batondeck/batondeck-kanban:latest'
d=$(fixture image)
if image "$d"; then
  if grep -q "$want_build --load \.\$" "$STUB_LOG" && ! grep -q -- '--push' "$STUB_LOG"; then
    pass "image of the clean tag: two platforms, both tags, the version, kept and not pushed"
  else
    bad "image of the clean tag: built, but not as make image must:"; cat "$STUB_LOG" >&2
  fi
else
  bad "image of the clean tag: refused:"; cat "$tmp/out" >&2
fi
for p in 1 0; do
  case $p in 1) want=--push ;; *) want=--load ;; esac
  if image "$d" "$p" && grep -q "$want_build $want \.\$" "$STUB_LOG"; then
    pass "image with PUSH=$p: $want"
  else
    bad "image with PUSH=$p: not $want:"; cat "$tmp/out" "$STUB_LOG" >&2
  fi
done
expect_image_refused() {
  local name=$1 dir=$2 why=$3
  if image "$dir"; then
    bad "$name: image built"
  elif ! grep -q "$why" "$tmp/out"; then
    bad "$name: refused, but not for '$why':"; cat "$tmp/out" >&2
  elif grep -q '^docker ' "$STUB_LOG"; then
    bad "$name: refused, but after calling docker"
  else
    pass "$name: image refused"
  fi
}
d=$(fixture imageuntracked); touch "$d/stray"
expect_image_refused "image on an untracked file" "$d" "not clean"
d=$(fixture imageuntagged); (cd "$d" && g commit -q --allow-empty -m later)
expect_image_refused "image of HEAD past the tag" "$d" "not v9.9.9"

# The binary check on builds dist would not make, each in a tree that is otherwise a clean tag.
d=$(fixture binaries)
build() { (cd "$d" && CGO_ENABLED=0 go build -trimpath "$@" -o "$tmp/bin" ./cmd/batondeck-kanban); }
refuse_bin() {
  if (cd "$d" && "$check" binaries 9.9.9 "$tmp/bin") >"$tmp/out" 2>&1; then
    bad "$1: binary check passed"
  elif ! grep -q "$2" "$tmp/out"; then
    bad "$1: refused, but not for '$2':"; cat "$tmp/out" >&2
  else
    pass "$1: binary refused"
  fi
}
build
if (cd "$d" && "$check" binaries 9.9.9 "$tmp/bin") >"$tmp/out" 2>&1; then
  pass "binary of the clean tag: accepted"
else
  bad "binary of the clean tag: refused:"; cat "$tmp/out" >&2
fi
touch "$d/stray"; build; rm "$d/stray"
refuse_bin "binary built with an untracked file present" "vcs.modified=false"
build -buildvcs=false
refuse_bin "binary built without VCS stamping" "vcs.modified=false"
(cd "$d" && g commit -q --allow-empty -m later); build; (cd "$d" && g reset -q --hard v9.9.9)
refuse_bin "binary of another commit" "vcs.revision="

exit $fail
