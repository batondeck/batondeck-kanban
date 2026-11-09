# Security policy

## Reporting a vulnerability

Please report a vulnerability privately, through GitHub's private vulnerability reporting on this
repository: the **Security** tab, then **Report a vulnerability**. Do not open a public issue, pull
request or discussion for it.

Include what you can of:

- the version (`batondeck-kanban version`) and the engine (SQLite or Postgres);
- what an attacker can do, and what they need first (a token, a project role, network access);
- the steps or a request that shows it.

You will get an answer on the report. A confirmed issue is fixed in a release, and the report is
published as a GitHub security advisory once that release is out, with credit to you unless you
ask otherwise.

## Supported versions

The latest release is the supported one. Fixes are not backported to older releases.

## Scope

In scope: the `batondeck-kanban` binary and image (its MCP server, REST API, web UI, OAuth
authorization server, on-call ingest at `/hooks/{id}`, CLI and storage), and the Claude Code plugin
under `plugin/`.

Out of scope: a deployment's own configuration (for example a `public_url` served without TLS, or a
reverse proxy that is not in `trusted_proxies`), and the security of the MCP clients that connect to
it.
