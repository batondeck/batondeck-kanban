#!/bin/sh
# The plugin's two hooks. `start` (SessionStart) runs
#   batondeck-kanban session register --role "${BATONDECK_ROLE:-worker}" [--agent "$BATONDECK_AGENT"]
# and `end` (SessionEnd) runs `batondeck-kanban session unregister`. The binary reads BATONDECK_TOKEN
# and BATONDECK_CONFIG or BATONDECK_LISTEN from the environment claude was started in.
# Nothing reaches stdout (a SessionStart hook's stdout is added to the model's context), and the
# script exits 0 whatever happens, so a missing binary or a stopped server never breaks a session.
if ! command -v batondeck-kanban >/dev/null 2>&1; then
  echo "batondeck-kanban plugin: batondeck-kanban is not on PATH; this session is not registered" >&2
  exit 0
fi
case "${1:-}" in
  start)
    if [ -n "${BATONDECK_AGENT:-}" ]; then
      batondeck-kanban session register --role "${BATONDECK_ROLE:-worker}" --agent "$BATONDECK_AGENT" >&2
    else
      batondeck-kanban session register --role "${BATONDECK_ROLE:-worker}" >&2
    fi
    ;;
  end)
    batondeck-kanban session unregister >&2
    ;;
  *)
    echo "usage: session.sh start|end" >&2
    ;;
esac
exit 0
