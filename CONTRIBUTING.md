# Contributing

Thank you for helping. Issues and pull requests are welcome; for a security problem, see
[SECURITY.md](SECURITY.md) instead of opening an issue.

## Before you start

For anything larger than a small fix, open an issue first and say what you want to change and why,
so the approach can be agreed before you spend time on it.

## What you need

- Go (the version in `go.mod`'s `toolchain` line; `GOTOOLCHAIN=auto` fetches it).
- Node.js 22 (what CI uses) and npm, for the web UI under `web/`.
- Docker, for the Postgres half of the tests: `make check` starts a Postgres 16 container.

## Build and test

```
make hooks    # once per clone: the pre-commit and pre-push hooks under githooks/
make build    # the binary, with the web UI embedded from web/dist
make check    # everything a pull request must pass, on SQLite and on Postgres
```

`make check` is the gate: the pre-push hook runs it, and CI runs it again on every pull request,
together with the end-to-end suites (`make e2e` for the MCP tools, `make e2e-ui` for the web UI in
Chromium). Run what you changed first; for example `go test ./internal/service/...`, or
`cd web && npm test`.

If you change anything under `web/src`, rebuild the embedded bundle with `cd web && npm run build`
and commit `web/dist` with your change: `make web-dist-check` refuses a bundle that does not match
its sources.

## Pull requests

- One change per pull request, with a description of what it changes and how you checked it.
- New behaviour comes with a test that fails without it.
- Add a line to `CHANGELOG.md` under `## [Unreleased]` for anything a user would notice.
- Keep secrets out of commits: the pre-commit hook scans what you stage for credentials.

## Licence

By contributing, you agree that your contribution is licensed under the Apache License, Version
2.0, as the rest of this repository is (see `LICENSE`, section 5).
