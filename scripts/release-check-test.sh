#!/usr/bin/env bash
# Runs this checkout's `make dist` against fixture repositories, with no network and nothing
# published: refused on a dirty tree (untracked file, modified file), on an untagged HEAD and on a
# HEAD tagged with another version; passing on a clean checkout of the tag, with binaries that
# record vcs.modified=false. `make image` against the same fixtures and a stubbed docker: refused
# on a dirty tree and an untagged HEAD; otherwise both platforms, both tags and the version, --push
# only with PUSH=1, and refused when compose.yaml pulls another version. Then the repository's
# Dockerfile read for cross-compiling (the build stage on $BUILDPLATFORM, GOOS/GOARCH from the
# target, nothing run in the final stage), and copies of it with each part broken refused. Then the
# pins: every workflow action by commit SHA under read-only workflow-level permissions, every
# Dockerfile base image and compose.yaml's Postgres by digest, and copies with one pin loosened
# refused. Then the binary check alone: a build from a modified tree, a build with no VCS stamp, a build of
# another commit and a build by a Go other than go.mod's pin are each refused. Then the versions check
# (`release-check.sh VERSION`): passing when compose.yaml, README.md, plugin.json, CHANGELOG.md and the
# binary agree, and refused, naming the file, when any one of them does not. Run by `make check`.
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
gotoolchain=$(go env GOVERSION)

# A fixture is a release candidate at 9.9.9: go.mod pins the Go on PATH (dist builds with exactly
# that toolchain), and compose.yaml, README.md, plugin.json and CHANGELOG.md name 9.9.9.
fixture() {
  local dir=$tmp/$1
  mkdir -p "$dir/cmd/batondeck-kanban" "$dir/plugin/.claude-plugin"
  printf 'module example.invalid/fixture\n\ngo %s\n\ntoolchain %s\n' "$goline" "$gotoolchain" > "$dir/go.mod"
  printf 'package main\n\nimport "os"\n\nvar version string\n\nfunc main() {\n\tif len(os.Args) > 1 && os.Args[1] == "version" {\n\t\tos.Stdout.WriteString("batondeck-kanban " + version + "\\n")\n\t}\n}\n' > "$dir/cmd/batondeck-kanban/main.go"
  printf 'services:\n  batondeck-kanban:\n    image: ghcr.io/batondeck/batondeck-kanban:9.9.9\n' > "$dir/compose.yaml"
  printf '{\n  "name": "batondeck-kanban",\n  "version": "9.9.9"\n}\n' > "$dir/plugin/.claude-plugin/plugin.json"
  printf '# Changelog\n\n## [Unreleased]\n\n## [9.9.9] - 2026-01-01\n\n- fixture\n' > "$dir/CHANGELOG.md"
  printf '# fixture\n\nbase=https://github.com/batondeck/batondeck-kanban/releases/download/v9.9.9\ncurl -O "$base/batondeck-kanban_9.9.9_${os}_${arch}"\nThe image is `ghcr.io/batondeck/batondeck-kanban:9.9.9`.\n' > "$dir/README.md"
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
# The control first: a clean checkout of the tag builds both platforms with the version, under both
# tags, and keeps it (--load) unless PUSH=1.
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
d=$(fixture imagecompose)
(cd "$d" && sed -i.bak 's/:9\.9\.9$/:9.9.8/' compose.yaml && rm compose.yaml.bak && g commit -q -am 'compose at 9.9.8' && g tag -f -a v9.9.9 -m v9.9.9 >/dev/null)
expect_image_refused "image of a VERSION compose.yaml does not pull" "$d" "compose.yaml: pulls ghcr.io/batondeck/batondeck-kanban:9.9.8, not ghcr.io/batondeck/batondeck-kanban:9.9.9"

# The Dockerfile cross-compiles, so `make image` builds linux/arm64 without emulation (P-336): the
# build stage runs on the builder's platform (`FROM --platform=$BUILDPLATFORM ... AS build`), declares
# TARGETOS and TARGETARCH and hands them to `go build` as GOOS and GOARCH (undeclared, they are empty
# and the arm64 image would carry an amd64 binary), and the final stage runs nothing. The stubbed
# docker builds nothing, so this reads the file; the repository's Dockerfile is the control, and
# copies of it with each part broken are refused.
cross_compiles() {
  awk '
    function norm(s) { gsub(/\$\{/, "$", s); gsub(/\}/, "", s); return s }
    /^[[:space:]]*#/ { next }
    { line = (cont ? line " " : "") $0; cont = sub(/\\[[:space:]]*$/, "", line); if (cont) next }
    { l = norm(line); line = "" }
    toupper(l) ~ /^[[:space:]]*FROM[[:space:]]/ {
      build = (tolower(l) ~ /[[:space:]]as[[:space:]]+build[[:space:]]*$/); last_run = 0
      if (build) { saw_build = 1; if (l !~ /--platform=\$BUILDPLATFORM[[:space:]]/) print "the build stage does not run on $BUILDPLATFORM" }
      next
    }
    build && toupper(l) ~ /^[[:space:]]*ARG[[:space:]]/ { if (l ~ /[[:space:]]TARGETOS([[:space:]=]|$)/) os = 1; if (l ~ /[[:space:]]TARGETARCH([[:space:]=]|$)/) arch = 1 }
    build && l ~ /go build/ {
      saw_go = 1
      if (!os || !arch) print "the build stage does not declare ARG TARGETOS and TARGETARCH before go build"
      if (l !~ /GOOS=\$TARGETOS[[:space:]]/ || l !~ /GOARCH=\$TARGETARCH[[:space:]]/) print "go build does not take GOOS=$TARGETOS and GOARCH=$TARGETARCH"
    }
    toupper(l) ~ /^[[:space:]]*RUN[[:space:]]/ { last_run = 1 }
    END {
      if (!saw_build) print "no build stage (FROM ... AS build)"
      else if (!saw_go) print "the build stage has no go build"
      if (last_run) print "the final stage runs a command, which needs emulation for another platform"
    }' "$1"
}
expect_cross() {
  local name=$1 file=$2 why=$3 out
  out=$(cross_compiles "$file")
  if [ -z "$why" ]; then
    if [ -z "$out" ]; then pass "$name: cross-compiles"; else bad "$name: $out"; fi
  elif [ -z "$out" ]; then
    bad "$name: accepted"
  elif ! grep -qF "$why" <<<"$out"; then
    bad "$name: refused, but not for '$why': $out"
  else
    pass "$name: refused"
  fi
}
expect_cross "the repository's Dockerfile" "$root/Dockerfile" ""
sed 's/^FROM --platform=\$BUILDPLATFORM /FROM /' "$root/Dockerfile" > "$tmp/Dockerfile.noplatform"
expect_cross "Dockerfile without --platform=\$BUILDPLATFORM" "$tmp/Dockerfile.noplatform" "does not run on \$BUILDPLATFORM"
sed '/^ARG TARGETOS TARGETARCH$/d' "$root/Dockerfile" > "$tmp/Dockerfile.noarg"
expect_cross "Dockerfile without ARG TARGETOS TARGETARCH" "$tmp/Dockerfile.noarg" "does not declare ARG TARGETOS and TARGETARCH"
sed 's/ GOARCH=\$TARGETARCH / /' "$root/Dockerfile" > "$tmp/Dockerfile.nogoarch"
expect_cross "Dockerfile whose go build drops GOARCH" "$tmp/Dockerfile.nogoarch" "GOARCH=\$TARGETARCH"
sed 's/^USER nonroot$/RUN ["\/batondeck-kanban", "version"]\nUSER nonroot/' "$root/Dockerfile" > "$tmp/Dockerfile.finalrun"
expect_cross "Dockerfile running a command in the final stage" "$tmp/Dockerfile.finalrun" "the final stage runs a command"

# What the release runs and starts from is named by content, not by a tag its owner can move (a moved
# tag on an action runs with the release's token, which can push the image and upload the assets):
# every workflow `uses:` names a full commit SHA, and every workflow's top-level permissions grant
# nothing beyond read, so a write is granted on the job that needs it alone; every FROM in the
# Dockerfile names a digest; every image in compose.yaml names a digest, but the board's own, whose
# tag the compose check holds to the version and whose digest exists only once it is pushed. The
# repository's files are the control, and copies of them with one pin loosened are refused.
unpinned() {
  awk -v kind="$1" '
    function bare(s) { sub(/^[[:space:]]*(-[[:space:]]+)?[A-Za-z]+:[[:space:]]*/, "", s); sub(/[[:space:]]+#.*$/, "", s); gsub(/["\047]/, "", s); sub(/[[:space:]]+$/, "", s); return s }
    function hex(s, n) { return length(s) == n && s !~ /[^0-9a-f]/ }
    function digested(ref, d) { if (ref !~ /@sha256:/) return 0; d = ref; sub(/^.*@sha256:/, "", d); return hex(d, 64) }
    /^[[:space:]]*#/ { next }
    kind == "workflow" && /^permissions:/ {
      saw_perm = 1; top = 1; v = bare($0)
      if (v ~ /write/) print FILENAME ":" FNR ": the workflow-level permissions are " v
      next
    }
    kind == "workflow" && top && /^[^[:space:]]/ { top = 0 }
    kind == "workflow" && top && /:[[:space:]]*write/ { s = $0; sub(/^[[:space:]]+/, "", s); sub(/[[:space:]]*#.*$/, "", s); print FILENAME ":" FNR ": the workflow-level permissions grant " s }
    kind == "workflow" && /^[[:space:]]*(-[[:space:]]+)?uses:/ {
      ref = bare($0); sha = ref; sub(/^[^@]*@/, "", sha)
      if (ref ~ /^\.\// || (ref ~ /^docker:\/\// && digested(ref))) next
      if (ref !~ /@/ || !hex(sha, 40)) print FILENAME ":" FNR ": uses " ref ", not a full commit SHA"
    }
    kind == "dockerfile" && toupper($1) == "FROM" {
      img = ""; name = ""
      for (i = 2; i <= NF; i++) if ($i !~ /^--/) { img = $i; break }
      for (i = 2; i < NF; i++) if (tolower($i) == "as") name = $(i + 1)
      if (!(img in stage) && tolower(img) != "scratch" && !digested(img)) print FILENAME ":" FNR ": FROM " img " names no digest"
      if (name != "") stage[name] = 1
    }
    kind == "compose" && /^[[:space:]]*image:/ {
      ref = bare($0)
      if (index(ref, "ghcr.io/batondeck/batondeck-kanban:") != 1 && !digested(ref)) print FILENAME ":" FNR ": image " ref " names no digest"
    }
    END { if (kind == "workflow" && !saw_perm) print FILENAME ": no workflow-level permissions (the repository default applies)" }' "$2"
}
expect_pinned() {
  local name=$1 kind=$2 file=$3 why=$4 out
  out=$(unpinned "$kind" "$file")
  if [ -z "$why" ]; then
    if [ -z "$out" ]; then pass "$name: pinned"; else bad "$name: $out"; fi
  elif [ -z "$out" ]; then
    bad "$name: accepted"
  elif ! grep -qF "$why" <<<"$out"; then
    bad "$name: refused, but not for '$why': $out"
  else
    pass "$name: refused"
  fi
}
for f in "$root"/.github/workflows/*.yml; do
  expect_pinned "the repository's ${f#"$root"/}" workflow "$f" ""
done
expect_pinned "the repository's Dockerfile" dockerfile "$root/Dockerfile" ""
expect_pinned "the repository's compose.yaml" compose "$root/compose.yaml" ""
wf=$root/.github/workflows/release-draft.yml
sed -E 's|(uses: docker/login-action)@[0-9a-f]+.*|\1@v3|' "$wf" > "$tmp/wf.tag"
expect_pinned "a workflow using docker/login-action@v3" workflow "$tmp/wf.tag" "uses docker/login-action@v3, not a full commit SHA"
sed -E 's|(uses: actions/checkout)@([0-9a-f]+)|\1@\2abc|' "$wf" > "$tmp/wf.longsha"
expect_pinned "a workflow using a ref longer than a SHA" workflow "$tmp/wf.longsha" "not a full commit SHA"
awk '{ print } /^permissions:/ && !done { print "  packages: write"; done = 1 }' "$wf" > "$tmp/wf.topwrite"
expect_pinned "a workflow granting packages: write to every job" workflow "$tmp/wf.topwrite" "the workflow-level permissions grant packages: write"
sed 's/^permissions:.*/permissions: write-all/' "$wf" > "$tmp/wf.writeall"
expect_pinned "a workflow granting write-all" workflow "$tmp/wf.writeall" "the workflow-level permissions are write-all"
awk '/^permissions:/ { skip = 1; next } skip && /^[^[:space:]]/ { skip = 0 } !skip' "$wf" > "$tmp/wf.noperm"
expect_pinned "a workflow with no workflow-level permissions" workflow "$tmp/wf.noperm" "no workflow-level permissions"
sed -E 's|^(FROM gcr.io/distroless/static-debian12:nonroot)@sha256:[0-9a-f]+|\1|' "$root/Dockerfile" > "$tmp/Dockerfile.distrolesstag"
expect_pinned "Dockerfile naming distroless by tag" dockerfile "$tmp/Dockerfile.distrolesstag" "FROM gcr.io/distroless/static-debian12:nonroot names no digest"
sed -E 's|^(FROM --platform=\$BUILDPLATFORM golang:[^@ ]+)@sha256:[0-9a-f]+|\1|' "$root/Dockerfile" > "$tmp/Dockerfile.golangtag"
expect_pinned "Dockerfile naming the Go builder by tag" dockerfile "$tmp/Dockerfile.golangtag" "FROM golang:"
sed -E 's|^([[:space:]]*image: postgres:[^@ ]+)@sha256:[0-9a-f]+|\1|' "$root/compose.yaml" > "$tmp/compose.pgtag"
expect_pinned "compose.yaml naming postgres by tag" compose "$tmp/compose.pgtag" "image postgres:"

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
# A binary of the clean tag, judged against a go.mod pinning another Go: the pin is read from the
# checkout, the compiler from the binary.
build; (cd "$d" && sed -i.bak "s/^toolchain .*/toolchain go1.0.1/" go.mod && rm go.mod.bak)
refuse_bin "binary built by another Go than go.mod pins" "built with $gotoolchain, not the go1.0.1 go.mod pins"
(cd "$d" && g checkout -q go.mod)

# The versions check: the five agree in the control; each one changed alone is refused by name.
versions() { (cd "$1" && "$check" 9.9.9) >"$tmp/out" 2>&1; }
d=$(fixture versions)
if versions "$d" && grep -q "9.9.9 agrees" "$tmp/out"; then
  pass "versions of the candidate: agree"
else
  bad "versions of the candidate: refused:"; cat "$tmp/out" >&2
fi
refuse_versions() {
  local name=$1 why=$2
  if versions "$d"; then
    bad "$name: versions check passed"
  elif ! grep -qF "$why" "$tmp/out"; then
    bad "$name: refused, but not for '$why':"; cat "$tmp/out" >&2
  else
    pass "$name: versions refused"
  fi
  (cd "$d" && g checkout -q -- .)
}
(cd "$d" && sed -i.bak 's/:9\.9\.9$/:9.9.8/' compose.yaml && rm compose.yaml.bak)
refuse_versions "compose.yaml at 9.9.8" "compose.yaml: pulls ghcr.io/batondeck/batondeck-kanban:9.9.8, not ghcr.io/batondeck/batondeck-kanban:9.9.9"
(cd "$d" && sed -i.bak 's|download/v9\.9\.9|download/v9.9.8|' README.md && rm README.md.bak)
refuse_versions "README.md downloading v9.9.8" "README.md: names 'releases/download/v9.9.8', not 9.9.9"
(cd "$d" && sed -i.bak 's|kanban:9\.9\.9|kanban:9.9.8|' README.md && rm README.md.bak)
refuse_versions "README.md naming the 9.9.8 image" "README.md: names 'ghcr.io/batondeck/batondeck-kanban:9.9.8', not 9.9.9"
(cd "$d" && printf '# fixture\n\nBuild from source.\n' > README.md)
refuse_versions "README.md naming no release" "README.md: names no release"
(cd "$d" && sed -i.bak 's/"9\.9\.9"/"9.9.8"/' plugin/.claude-plugin/plugin.json && rm plugin/.claude-plugin/plugin.json.bak)
refuse_versions "plugin.json at 9.9.8" "plugin/.claude-plugin/plugin.json: version is '9.9.8', not 9.9.9"
(cd "$d" && sed -i.bak 's/^## \[9\.9\.9\]/## [9.9.8]/' CHANGELOG.md && rm CHANGELOG.md.bak)
refuse_versions "CHANGELOG.md without a 9.9.9 section" "CHANGELOG.md: no '## [9.9.9]' section"
# main.version is still declared, but `version` prints another variable, which -X does not reach.
(cd "$d" && sed -i.bak 's/var version string/var version, shown string/; s/+ version +/+ shown +/' cmd/batondeck-kanban/main.go && rm cmd/batondeck-kanban/main.go.bak)
refuse_versions "a binary whose version command does not print the stamp" "the binary: version prints 'batondeck-kanban ', not 'batondeck-kanban 9.9.9'"

exit $fail
