# Changelog

Notable changes to batondeck-kanban. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project will use
[semantic versioning](https://semver.org/spec/v2.0.0.html) from its first release.

## [Unreleased]

### Added

- `internal/domain`: the six ticket statuses and the fixed transition table (same-status moves
  legal; `CANCELLED` and `DEAD_LETTER` refused as `VALIDATION`), with allowed-next and shortest-path
  guidance on `INVALID_TRANSITION`; the error model (13 codes, `STALE`, `RATE_LIMITED` and `INTERNAL`
  retryable); the move decision (WIP limit on entering IN_PROGRESS, a promotion with open blockers
  landing on BLOCKED, reorder within a column); order keys ported from fractional-indexing 4.0.0;
  dependency cycle detection (`CYCLE_DETECTED`). Pure functions, not yet called by any tool.
- The skeleton, copied from hdtp-gateway and stripped of HDTP: the Go module
  `github.com/batondeck/batondeck-kanban`; the `batondeck-kanban` binary with `serve`, `healthcheck`
  and `version` (`migrate`, `token`, `user` and `doctor` exit 2, not implemented in this release);
  `serve` answers `GET /healthz` with the binary and its version and serves the embedded web UI
  placeholder; config from `BATONDECK_*` > a JSON file > defaults, holding `database.url`,
  `database.synchronous`, `listen` and `public_url`; the data-dir lock, admin socket and keyring;
  the SQLite and Postgres engines with goose's migration machinery, no migration yet and no
  area; the Makefile gate
  (`make check`: fmt vet staticcheck notices-check release-check test), the githooks, the
  distroless Dockerfile, THIRD_PARTY_NOTICES and RELEASING.md.
