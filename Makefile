BINARY := batondeck-kanban
VERSION ?= 0.1.0-dev

# Absolute, so `make -f <this file>` from another directory (scripts/release-check-test.sh runs
# dist in a fixture repository) still finds it.
SELF := $(abspath $(lastword $(MAKEFILE_LIST)))
RELEASE_CHECK := $(dir $(SELF))scripts/release-check.sh

.PHONY: release-check notices notices-check image hooks staticcheck sqlc-check web-dist-check dist sbom build check fmt vet test conformance contract clean

# The Go the analyzers run under: the one go.mod names (its `toolchain` line when it has one, else
# its `go` line, which is a full version: the go command refuses a bare language version here, by
# name), with the go command free to switch higher. Each recipe is `go run <tool>@<version>` under
# whatever Go is on PATH, and with GOTOOLCHAIN=local in the environment a machine whose Go is older
# than go.mod's fails at the tool's `go list` ("go.mod requires go >= …; GOTOOLCHAIN=local") and
# scans nothing. Exported for these targets alone, the name wins over the environment: the go
# command downloads that toolchain once and the recipe runs under it. A Go older than 1.21 has no
# switching and is not helped.
GO_TOOLCHAIN := $(shell sed -n 's/^toolchain //p' go.mod)
ifeq ($(GO_TOOLCHAIN),)
GO_TOOLCHAIN := go$(shell sed -n 's/^go //p' go.mod)
endif
staticcheck: export GOTOOLCHAIN = $(GO_TOOLCHAIN)+auto

staticcheck:
	go run honnef.co/go/tools/cmd/staticcheck@2025.1.1 ./...

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(BINARY) ./cmd/batondeck-kanban

# PLATFORMS is what a release ships. CGO is off and -trimpath is set for every
# one, so the binary depends on no host libc and embeds no build paths — which is
# what lets a third party rebuild a tag and compare checksums.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

# dist builds every platform and writes SHA256SUMS. A release is cut locally from this exact target
# (RELEASING.md), so what ships is what anyone can rebuild from the tag. It refuses unless the tree
# is clean (untracked files included) and HEAD is tag v$(VERSION), and refuses the binaries unless
# each records vcs.modified=false and that tag's commit (scripts/release-check.sh): a build from a
# modified tree stamps vcs.modified=true, which no rebuild from the tag reproduces.
dist:
	@$(RELEASE_CHECK) tree $(VERSION)
	@rm -rf dist && mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; out=dist/$(BINARY)_$(VERSION)_$${os}_$${arch}; \
		echo "building $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -buildvcs=true \
			-ldflags "-s -w -X main.version=$(VERSION)" -o $$out ./cmd/batondeck-kanban || exit 1; \
	done
	@$(RELEASE_CHECK) binaries $(VERSION) dist/$(BINARY)_$(VERSION)_*
	@cd dist && shasum -a 256 * > SHA256SUMS && cat SHA256SUMS

# release-check runs dist against fixture repositories: refused on a dirty or untagged tree,
# passing on a clean tagged one, and the binary check refusing a modified or unstamped build;
# sbom refused on a dirty tree, before it fetches anything; and image against a stubbed docker.
release-check:
	scripts/release-check-test.sh

# hooks points git at the versioned hooks in githooks/. The path is relative, so
# it resolves against each worktree's top level and every worktree runs its own
# copy. One setting, and the hooks travel with the repository instead of living in
# an untracked .git/hooks that every clone starts without.
hooks:
	git config core.hooksPath githooks
	@echo "hooks installed: $$(git config core.hooksPath)"
	@echo "pre-commit scans the index for credentials, styles staged Go and refuses files over 5 MiB; commit-msg refuses a local path in the message; pre-push runs make check on both engines."

# sbom emits CycloneDX for the shipped binary's dependency graph. Pinned, not
# @latest: a supply-chain document produced by an unpinned tool is worth less.
# It is a release asset, so it holds to dist's tree check: clean, and HEAD is tag v$(VERSION).
sbom:
	@$(RELEASE_CHECK) tree $(VERSION)
	@mkdir -p dist
	go run github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@v1.9.0 \
		app -json -licenses=false -main cmd/batondeck-kanban -output dist/sbom.cdx.json .
	@echo "wrote dist/sbom.cdx.json"

# image builds the container image: the Dockerfile for linux/amd64 and linux/arm64,
# --build-arg VERSION stamping main.version, tagged $(IMAGE):$(VERSION) and $(IMAGE):latest. It holds
# to dist's tree check (clean, and HEAD is tag v$(VERSION)). Without PUSH=1 the image is kept in
# Docker's own store (--load, which for two platforms needs the containerd image store, Docker
# Desktop's default); with PUSH=1 it is pushed to the registry IMAGE names, after
# `docker login ghcr.io`, and kept nowhere else.
IMAGE := ghcr.io/batondeck/batondeck-kanban
IMAGE_PLATFORMS := linux/amd64,linux/arm64
image:
	@$(RELEASE_CHECK) tree $(VERSION)
	docker buildx build --platform $(IMAGE_PLATFORMS) \
		--build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest \
		$(if $(filter 1,$(PUSH)),--push,--load) .

# check is the gate the pre-push hook runs. Its steps, in order: gofmt, go vet, staticcheck,
# THIRD_PARTY_NOTICES against go.sum, the release fixtures, the generated store code against its
# sources, web/dist against a fresh web build, the layering guard, the tests under the race
# detector, the store conformance suite once per engine, and the MCP contract.
check: fmt vet staticcheck notices-check release-check sqlc-check web-dist-check arch test conformance contract

# SQLC pins the generator, built from source at this version (minutes on a cold module cache).
SQLC := go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0

# sqlc-check fails when the committed generated code (internal/store/{sqlitedb,pgdb}, sqlc.yaml's
# outputs) does not match what the current queries and migrations produce. Until queries/ holds a
# .sql file there is nothing to generate, and it says so.
sqlc-check:
	@if ! find queries -name '*.sql' 2>/dev/null | grep -q .; then \
		echo "sqlc-check: no queries/*.sql yet"; exit 0; \
	fi; \
	tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
		cp -R internal/store/sqlitedb internal/store/pgdb "$$tmp/" \
			|| { echo "sqlc-check: the generated code is missing - run $(SQLC) generate and commit the result" >&2; exit 1; }; \
		$(SQLC) generate || { echo "sqlc-check: sqlc generate FAILED" >&2; exit 1; }; \
		if diff -r "$$tmp/sqlitedb" internal/store/sqlitedb >/dev/null \
		&& diff -r "$$tmp/pgdb" internal/store/pgdb >/dev/null; then \
			echo "sqlc-check: generated code matches its sources"; \
		else \
			echo "sqlc-check: generated code is STALE - run $(SQLC) generate and commit the result"; \
			diff -r "$$tmp/sqlitedb" internal/store/sqlitedb | head -20; \
			diff -r "$$tmp/pgdb" internal/store/pgdb | head -20; \
			exit 1; \
		fi

# web-dist-check builds web/ afresh and fails when the build differs from the committed web/dist
# by a byte: a changed or deleted file (against HEAD) or a file the commit does not hold (git's
# ignore rules apply, so a .DS_Store is not drift). The build writes web/dist in place, so a
# failure leaves the fresh build there to commit.
web-dist-check:
	@if [ ! -f web/package.json ]; then echo "web-dist-check: no web/package.json yet"; exit 0; fi; \
	cd web && npm ci && npm run build && cd .. && \
	if git diff --quiet HEAD -- web/dist && [ -z "$$(git ls-files --others --exclude-standard -- web/dist)" ]; then \
		echo "web-dist-check: web/dist matches a fresh build"; \
	else \
		echo "web-dist-check: web/dist is STALE - a fresh build changed it; commit what it wrote:"; \
		git status --short --untracked-files=all -- web/dist; \
		exit 1; \
	fi

# notices writes THIRD_PARTY_NOTICES: the licence texts of every Go module the shipped binary links
# on any of PLATFORMS, as their own sources hold them (scripts/notices.mjs says how; it is handed
# PLATFORMS, the one list). notices-check holds the file to PLATFORMS, to go.sum by hash and to the
# set of modules the binary links (`go list -deps` per platform, by count and hash): it is part of
# `check`. A dependency change, an import that links a module go.sum already held, or a change to
# PLATFORMS, that forgets `make notices` fails there.
notices:
	NOTICES_PLATFORMS="$(PLATFORMS)" node scripts/notices.mjs
notices-check:
	NOTICES_PLATFORMS="$(PLATFORMS)" node scripts/notices.mjs --check

fmt:
	@d=$$(gofmt -l .); if [ -n "$$d" ]; then echo "gofmt needed:"; echo "$$d"; exit 1; fi

vet:
	go vet ./...

# arch is the layering guard of ADR-0003 §C (internal/arch/layering_test.go). -count=1 so the gate's
# verdict always comes from a fresh `go list`: the test cache does notice edits to the tree, but it
# records only what the test process itself opens, so the gate does not rely on it.
.PHONY: arch
arch:
	go test -count=1 ./internal/arch/

# Naming the module's real trees keeps the run to this repository's code: ./... would reach into
# web/node_modules once the UI's packages are installed.
test:
	go test -race ./cmd/... ./internal/...

# conformance judges each store engine by internal/store/conformance, one run per engine, by name.
# The Postgres run needs BATONDECK_TEST_POSTGRES_DSN: unset, it skips and prints why; set and
# unreachable, it fails.
conformance:
	go test -race -count=1 -v -run '^TestSQLiteConformance$$' ./internal/store/conformance/
	go test -race -count=1 -v -run '^TestPostgresConformance$$' ./internal/store/conformance/

# contract holds contract/expected.json to canonical(tools.json + admin-tools.json) ⊖ deltas.json and
# runs the comparator's refusal controls (contract/contract_test.go). After a deliberate change to
# deltas.json: `go test ./contract/ -run TestExpectedIsTheExactDerivation -update`. tools.json and
# admin-tools.json come from conductor by `node scripts/contract-import.mjs <mcp.json> [--admin <dump>]`,
# which the gate cannot run: conductor is a private sibling.
contract:
	go test -race ./contract/

# clean removes what a build PRODUCES: the binary and dist/.
clean:
	rm -f $(BINARY)
	rm -rf dist
