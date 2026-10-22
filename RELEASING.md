# Releasing, and verifying a release

Releases are cut locally, by a maintainer, from a tag. There is no release workflow: a release is
built where its gate runs, on the machine that pushes. batondeck-kanban holds a team's tasks and
its agents' credentials, so "download this binary" has to be checkable: the checksums are published,
and anyone can rebuild a tag and compare. One binary ships; there is no sidecar.

No release has been cut yet. This is the procedure the Makefile's `dist`, `sbom` and `image` targets
implement, and `make release-check` (part of `make check`) runs each of them against fixture
repositories.

## Cutting a release

1. Update `CHANGELOG.md` — the release notes are its section for this version.
2. The gate green on the commit being tagged: the pre-push hook runs it (`make check`).
3. Tag and push, with an annotated tag whose message is one line naming the release:
   `git tag -a v1.2.3 -m "batondeck-kanban 1.2.3 — <summary>; CHANGELOG.md has the notes" && git push origin v1.2.3`.
   `scripts/release-check.sh` resolves the tag to its commit (`v1.2.3^{commit}`), and the fixtures
   `make release-check` builds are annotated.
4. Build every platform and the SBOM from a fresh clone of that tag:

   ```
   dir=$(mktemp -d)/batondeck-kanban
   git clone --branch v1.2.3 https://github.com/batondeck/batondeck-kanban.git "$dir"
   cd "$dir"
   make dist VERSION=1.2.3
   make sbom VERSION=1.2.3
   ```

   `make dist` builds `dist/batondeck-kanban_*` for the four platforms of the Makefile's
   `PLATFORMS` and writes `SHA256SUMS` over them. It refuses to build unless
   `git status --porcelain` is empty and HEAD is the commit `v1.2.3` names. Untracked files count:
   the go command stamps a binary built beside any of them `vcs.modified=true`, and that stamp
   changes the checksum. After building, it refuses unless every binary's build info
   (`go version -m`) records `vcs.modified=false` and that commit as `vcs.revision`. A working
   checkout usually holds something untracked, which is why the build is done in a fresh clone.
   `make sbom` holds to the same tree check.

   `make sbom` reads the repository through its own git library (cyclonedx-gomod), so run it in
   a checkout whose `.git` is a directory, which a clone is: a linked worktree, whose `.git` is a
   `gitdir:` file, fails with `reference not found`. The version it stamps on the main module is the
   tag on HEAD, or a pseudo-version when there is none.

5. Build and push the image, from the same fresh clone, after `docker login ghcr.io` with a
   token that may write the organisation's packages:

   ```
   make image VERSION=1.2.3 PUSH=1
   docker buildx imagetools inspect ghcr.io/batondeck/batondeck-kanban:1.2.3
   ```

   `make image` builds the `Dockerfile` for linux/amd64 and linux/arm64 with
   `--build-arg VERSION=1.2.3`, which the binary's `main.version` is stamped with, and tags it
   `1.2.3` and `latest`. It holds to `make dist`'s tree check; `make release-check` runs it against
   fixtures with a stubbed docker. Without `PUSH=1` the image is kept in Docker's own store. The
   image carries the OCI labels `source` (this repository, which links the package to it),
   `version`, `licenses` and `description`. Put the manifest digest `imagetools inspect` prints in
   the release notes.

6. Publish, with the notes taken from `CHANGELOG.md`:

   ```
   gh release create v1.2.3 --title v1.2.3 --notes-file <notes> \
     dist/batondeck-kanban_* dist/SHA256SUMS dist/sbom.cdx.json THIRD_PARTY_NOTICES
   ```

   `THIRD_PARTY_NOTICES` is the licence text of every Go module the binary links, taken for every
   platform in the Makefile's `PLATFORMS`; `make notices-check`, in the gate, holds it to that list
   and to `go.sum`.

7. The package's visibility is the organisation owner's. After the first push, check it on the
   package's page.

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
show `vcs.modified=false` and the tag's commit. A mismatch is a bug worth reporting. No rebuild of
this repository has yet been compared across two clones.

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
