# Releasing, and verifying a release

Releases are cut locally, by a maintainer, from a tag. There is no release workflow: a release is
built where its gate runs, on the machine that pushes. batondeck-kanban holds a team's tasks and
its agents' credentials, so "download this binary" has to be checkable: the checksums are published,
and anyone can rebuild a tag and compare. One binary ships; there is no sidecar.

No release has been cut yet. This is the sequence for v0.1.0, line by line; a later release runs
the same lines with its own version. The Makefile's `dist`, `sbom` and `image` targets and
`scripts/release-check.sh` implement it, and `make release-check` (part of `make check`) runs each of
them against fixture repositories.

## Cutting a release

The steps run on the maintainer's machine that runs the gate: Go from `go.mod`, Docker with the
containerd image store and arm64 emulation (Docker Desktop has both), `gh` signed in to
`batondeck/batondeck-kanban`. `CANDIDATE` is the full SHA of the commit being released, on `main`.

1. **The versions agree on the candidate.** A release bumps the version in `compose.yaml`'s image
   line, `plugin/.claude-plugin/plugin.json` and a `## [VERSION]` section of `CHANGELOG.md` (the
   release notes) in the commit it tags; the binary takes its version from `make`. The check needs
   no tag:

   ```
   dir=$(mktemp -d)/batondeck-kanban
   git clone https://github.com/batondeck/batondeck-kanban.git "$dir" && cd "$dir"
   git checkout --detach "$CANDIDATE"
   make hooks
   scripts/release-check.sh 0.1.0
   ```

   `make hooks` points this clone at the versioned hooks in `githooks/` (`core.hooksPath`); a clone
   starts without it, and step 3's push is gated only by the pre-push hook it turns on. The check
   prints `release-check: 0.1.0 agrees in compose.yaml, plugin/.claude-plugin/plugin.json,
   CHANGELOG.md and the binary`, or names each file that disagrees and exits 1.

2. **The bench numbers stand.** The README's performance block is the numbers of
   `bench/baseline.json`, measured on the machine it names. On that machine, in the same clone:

   ```
   BATONDECK_TEST_POSTGRES_DSN=postgres://… make bench
   ```

   Green means the candidate is within the gate of the README's numbers. A failure is a regression
   to fix, or new numbers that `make bench-baseline` writes and a PR commits; either way the
   candidate changes and this sequence starts again. On another machine `make bench` compares
   nothing and says so: re-measure there with `make bench-baseline` and commit before going on.

3. **Gate green on the tag commit, annotated tag.** Tag the candidate in the same clone, with
   `$CANDIDATE` still checked out, and push the tag; the pre-push hook runs `make check` on both
   engines on the checked-out tree, which is the tag's commit, and refuses the push if it fails. The
   hook is required: unless both `test`s pass (`make hooks` ran in this clone, and HEAD is the
   candidate), nothing is tagged or pushed.

   ```
   test "$(git config core.hooksPath)" = githooks && test "$(git rev-parse HEAD)" = "$CANDIDATE" &&
     git tag -a v0.1.0 -m "batondeck-kanban 0.1.0 — the first release; CHANGELOG.md has the notes" "$CANDIDATE" &&
     git push origin v0.1.0
   ```

   The hook gates HEAD, not the ref it is handed, so it prints `pre-push: NOTE — gating the
   checked-out tree (<candidate>), not <tag object>` for the annotated tag's own SHA; that is
   expected, and is why `$CANDIDATE` has to stay checked out.

   `scripts/release-check.sh` resolves the tag to its commit (`v0.1.0^{commit}`), and the fixtures
   `make release-check` builds are annotated.

4. **`make dist` from a fresh clone of the tag**, then a second one to compare:

   ```
   dir=$(mktemp -d)/batondeck-kanban
   git clone --branch v0.1.0 https://github.com/batondeck/batondeck-kanban.git "$dir" && cd "$dir"
   make dist VERSION=0.1.0
   make sbom VERSION=0.1.0

   dir2=$(mktemp -d)/batondeck-kanban
   git clone --branch v0.1.0 https://github.com/batondeck/batondeck-kanban.git "$dir2"
   make -C "$dir2" dist VERSION=0.1.0
   diff "$dir/dist/SHA256SUMS" "$dir2/dist/SHA256SUMS" && echo reproduced
   ```

   `make dist` builds `dist/batondeck-kanban_0.1.0_<os>_<arch>` for the four platforms of the
   Makefile's `PLATFORMS` (linux and darwin, amd64 and arm64) and writes `SHA256SUMS` over them. It
   builds with `GOTOOLCHAIN` set to `go.mod`'s `toolchain` line exactly (the go command downloads it
   once when the Go on PATH is another), `CGO_ENABLED=0`, `-trimpath` and `-buildvcs=true`. It
   refuses to build unless `git status --porcelain` is empty and HEAD is the commit `v0.1.0` names.
   Untracked files count: the go command stamps a binary built beside any of them
   `vcs.modified=true`, and that stamp changes the checksum. After building, it refuses unless every
   binary's build info (`go version -m`) records `vcs.modified=false`, that commit as
   `vcs.revision`, and the pinned Go. A working checkout usually holds something untracked, which
   is why the build is done in a fresh clone. `make sbom` holds to the same tree check.

   `make sbom` reads the repository through its own git library (cyclonedx-gomod), so run it in
   a checkout whose `.git` is a directory, which a clone is: a linked worktree, whose `.git` is a
   `gitdir:` file, fails with `reference not found`. The version it stamps on the main module is the
   tag on HEAD, or a pseudo-version when there is none.

5. **`make image`, after `docker login ghcr.io`**, from the first clone, with a token that may write
   the organisation's packages:

   ```
   cd "$dir"
   echo "$GHCR_TOKEN" | docker login ghcr.io -u <github-user> --password-stdin
   make image VERSION=0.1.0 PUSH=1
   docker buildx imagetools inspect ghcr.io/batondeck/batondeck-kanban:0.1.0
   docker run --rm ghcr.io/batondeck/batondeck-kanban:0.1.0 version
   ```

   `make image` builds the `Dockerfile` for linux/amd64 and linux/arm64 with
   `--build-arg VERSION=0.1.0`, which the binary's `main.version` is stamped with, and tags it
   `0.1.0` and `latest`. It holds to `make dist`'s tree check, and refuses a version
   `compose.yaml` does not pull (`scripts/release-check.sh compose 0.1.0`), before it calls docker.
   `make release-check` runs it against fixtures with a stubbed docker. Without `PUSH=1` the image
   is kept in Docker's own store. The image carries the OCI labels `source` (this repository, which
   links the package to it), `version`, `licenses` and `description`. `imagetools inspect` must list
   both platforms; put the manifest digest it prints in the release notes. The last line prints
   `batondeck-kanban 0.1.0`.

6. **`gh release create`**, with the notes taken from `CHANGELOG.md`'s 0.1.0 summary (the section
   down to its first `###`):

   ```
   awk '/^## \[0\.1\.0\]/ { f = 1; next } /^(## |### )/ { f = 0 } f' CHANGELOG.md > "$dir/notes.md"
   echo "Image: ghcr.io/batondeck/batondeck-kanban:0.1.0 (<digest from step 5>)" >> "$dir/notes.md"
   gh release create v0.1.0 --repo batondeck/batondeck-kanban --title v0.1.0 --notes-file "$dir/notes.md" \
     dist/batondeck-kanban_0.1.0_* dist/SHA256SUMS dist/sbom.cdx.json THIRD_PARTY_NOTICES
   ```

   `THIRD_PARTY_NOTICES` is the licence text of every Go module the binary links, taken for every
   platform in the Makefile's `PLATFORMS`; `make notices-check`, in the gate, holds it to that list
   and to `go.sum`.

7. **Visibility is the organisation owner's, not this sequence's.** The repository is private until
   the owner makes it public at v0.1.0 (the README's download URLs and `git clone` need that), and
   the package's visibility is set on its page on ghcr.io after the first push.

## Verifying a release you downloaded

**Checksum:**

```
shasum -a 256 -c SHA256SUMS --ignore-missing
```

**Rebuild it yourself.** The builds are meant to be reproducible: `CGO_ENABLED=0` and `-trimpath`
mean no host libc and no embedded build paths. In a fresh clone of the tag, with the Go toolchain
from `go.mod`:

```
make dist VERSION=1.2.3
shasum -a 256 dist/batondeck-kanban_1.2.3_linux_amd64
go version -m dist/batondeck-kanban_1.2.3_linux_amd64 | grep vcs
```

The checksum must equal the published one, and the published binary's `go version -m` must
show `vcs.modified=false`, the tag's commit and the Go `go.mod` pins. A mismatch is a bug worth
reporting.

**The image:** its binary prints the release (`docker run --rm ghcr.io/batondeck/batondeck-kanban:1.2.3 version`
prints `batondeck-kanban 1.2.3`), and the release notes carry the manifest digest it was pushed
under. The image is not reproducible byte for byte: of the `Dockerfile`'s two base images only the
Go one is named by digest; `gcr.io/distroless/static-debian12:nonroot` is named by tag, so a rebuild
later may start from other bytes. What ties it to the tag is that it was built by `make image` from
a fresh clone of it.

**SBOM:** `sbom.cdx.json` (CycloneDX 1.6) lists the Go modules `cmd/batondeck-kanban` links
(`cyclonedx-gomod app`).

## What this does not prove

There is no build-provenance or SBOM attestation: releases are built on a maintainer's machine, so
nothing binds a binary to a hosted build. A matching rebuild says only that the artifact matches
this repository at that commit — nothing about whether the code is correct or the design sound.
