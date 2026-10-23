#!/usr/bin/env bash
# One MCP tool call against a batondeck-kanban server, from a shell: prints the tool's structured
# result as JSON and exits 0, or prints the error and exits 1. Each run opens its own MCP session.
#
#   scripts/mcp.sh <tool> '<json arguments>'
#   scripts/mcp.sh list_projects '{}'
#
# Environment:
#   BATONDECK_TOKEN  required: a CLI token, minted with `batondeck-kanban token create --user LOGIN`
#   BATONDECK_URL    the server's base URL (default http://localhost:8080; the MCP endpoint is /mcp)
set -euo pipefail

tool="${1:?usage: mcp.sh <tool> '<json arguments>'}"
args="${2:-}"; [ -n "${args}" ] || args='{}'
url="${BATONDECK_URL:-http://localhost:8080}/mcp"
[ -n "${BATONDECK_TOKEN:-}" ] || {
  echo "mcp.sh: BATONDECK_TOKEN is not set; mint one with: batondeck-kanban token create --user LOGIN" >&2
  exit 1
}
hdr=(-H "Authorization: Bearer ${BATONDECK_TOKEN}" -H "Content-Type: application/json"
     -H "Accept: application/json, text/event-stream")

# initialize, then read the session id the server answers with (a stateless server sends none).
sid="$(curl -sS -D - -o /dev/null -X POST "${url}" "${hdr[@]}" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"mcp.sh","version":"1"}}}' \
  | tr -d '\r' | awk -F': ' 'tolower($1)=="mcp-session-id"{print $2}')"
[ -z "${sid}" ] || hdr+=(-H "Mcp-Session-Id: ${sid}")
curl -sS -o /dev/null -X POST "${url}" "${hdr[@]}" -d '{"jsonrpc":"2.0","method":"notifications/initialized"}'

req="$(python3 -c 'import json,sys; print(json.dumps({"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":sys.argv[1],"arguments":json.loads(sys.argv[2])}}))' "${tool}" "${args}")"
curl -sS -N --max-time 120 -X POST "${url}" "${hdr[@]}" -d "${req}" | python3 -c '
import json, sys
raw = sys.stdin.read()
msgs = [l[6:] for l in raw.splitlines() if l.startswith("data: ")] or [raw]
for m in msgs:
    try:
        d = json.loads(m)
    except ValueError:
        continue
    if "error" in d:
        print("RPC_ERROR:", json.dumps(d["error"])); sys.exit(1)
    if "result" in d:
        r = d["result"]
        if r.get("isError"):
            print("TOOL_ERROR:", r["content"][0]["text"]); sys.exit(1)
        print(json.dumps(r.get("structuredContent", r), indent=2)); sys.exit(0)
print("TRANSPORT_ERROR:", raw.strip()[:400]); sys.exit(1)'
