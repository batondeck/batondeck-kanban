#!/usr/bin/env python3
"""hook-check: the plugin's hooks, run as Claude Code runs them, write nothing to stdout and exit 0.

A SessionStart hook's stdout is added to the model's context, so `batondeck-kanban session register`
and `unregister` must keep stdout empty on every path, success included, and plugin/hooks/session.sh
must too. This builds the real binary, serves a scratch SQLite store on a Unix socket in a temporary
directory, mints a user and a token with the binary's own subcommands, and runs each case as a
subprocess: the binary directly (the argv the hook builds), then EVERY command plugin/hooks/hooks.json
declares, as Claude Code runs it (`sh -c` with CLAUDE_PLUGIN_ROOT set), under each scenario of its
event. A declared command whose script does not exist, that writes to stdout or exits non-zero fails.

usage: python3 scripts/hook-check.py   (needs go; no network; everything lives in a temp dir)
"""
import json
import os
import shlex
import subprocess
import sys
import tempfile
import time

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PLUGIN = os.path.join(ROOT, "plugin")
HOOKS_JSON = os.path.join(PLUGIN, "hooks", "hooks.json")

# The scenarios each declared command of an event is run under: (name, environment, a stderr substring
# that proves the path ran). An event with no scenarios here is refused: nothing would check it.
SCENARIOS = {
    "SessionStart": [
        ("worker", {}, "registered as worker"),
        ("master", {"BATONDECK_ROLE": "master"}, "registered as master"),
        ("agent", {"BATONDECK_AGENT": "hook-check"}, "agent hook-check"),
        ("bad role", {"BATONDECK_ROLE": "boss"}, "usage"),
        ("binary not on PATH", {"PATH": "/usr/bin:/bin"}, "not on PATH"),
    ],
    "SessionEnd": [
        ("unregister", {}, "session unregister"),
        ("binary not on PATH", {"PATH": "/usr/bin:/bin"}, "not on PATH"),
    ],
}


def declared_hooks():
    """Every (event, hook) plugin/hooks/hooks.json declares."""
    with open(HOOKS_JSON) as f:
        events = json.load(f)["hooks"]
    return [(event, h) for event, groups in events.items() for g in groups for h in g.get("hooks", [])]


def main():
    problems = []
    with tempfile.TemporaryDirectory(prefix="bk-hook-check-", dir="/tmp") as tmp:
        bindir = os.path.join(tmp, "bin")
        binary = os.path.join(bindir, "batondeck-kanban")
        subprocess.run(["go", "build", "-o", binary, "./cmd/batondeck-kanban"], cwd=ROOT, check=True)
        sock = os.path.join(tmp, "sock", "bk.sock")  # under /tmp: a Unix socket path is at most 104 bytes on darwin
        # No CLAUDE_CODE_MESSAGING_SOCKET, and a registry of our own: run from inside a Claude Code
        # session, the client would otherwise register that real session with this scratch server.
        env = {k: v for k, v in os.environ.items()
               if not k.startswith(("BATONDECK_", "CLAUDE_CODE_MESSAGING_"))}
        env.update(CLAUDE_CONFIG_DIR=os.path.join(tmp, "claude"),
                   BATONDECK_DATABASE_URL=os.path.join(tmp, "db", "bk.sqlite"),
                   BATONDECK_LISTEN="unix://" + sock)

        def run(argv, extra=None, drop=(), stdin=b""):
            e = dict(env, **(extra or {}))
            for k in drop:
                e.pop(k, None)
            return subprocess.run(argv, env=e, input=stdin, capture_output=True, timeout=30)

        r = run([binary, "migrate"])
        if r.returncode != 0:
            print(f"hook-check: migrate failed: {r.stderr.decode().strip()}", file=sys.stderr)
            return 1
        r = run([binary, "user", "add", "hook-check@example.test"], stdin=b"hook-check-password\n")
        if r.returncode != 0:
            print(f"hook-check: user add failed: {r.stderr.decode().strip()}", file=sys.stderr)
            return 1
        r = run([binary, "token", "create", "--user", "hook-check@example.test", "--ttl", "1h"])
        token = r.stdout.decode().strip()
        if r.returncode != 0 or not token:
            print(f"hook-check: token create failed: {r.stderr.decode().strip()}", file=sys.stderr)
            return 1

        serve = subprocess.Popen([binary, "serve"], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            for _ in range(100):
                if os.path.exists(sock):
                    break
                time.sleep(0.1)
            else:
                print("hook-check: the scratch server never bound its socket", file=sys.stderr)
                return 1
            tok = {"BATONDECK_TOKEN": token}
            hook_path = {"PATH": bindir + os.pathsep + env.get("PATH", "")}
            down = {"BATONDECK_LISTEN": "unix://" + os.path.join(tmp, "nowhere.sock"), **tok}
            cases = [
                # (name, argv, env, a stderr substring that proves the path ran)
                ("register: no token", [binary, "session", "register", "--role", "worker"], {}, "BATONDECK_TOKEN is not set"),
                ("register: no unix listen", [binary, "session", "register", "--role", "worker"],
                 {"BATONDECK_LISTEN": "tcp://127.0.0.1:1", **tok}, "no unix:// entry"),
                ("register: server down", [binary, "session", "register", "--role", "worker"], down, "the server on"),
                ("register: refused (bad token)", [binary, "session", "register", "--role", "master"],
                 {"BATONDECK_TOKEN": "bd_not-a-token"}, "refused"),
                ("register: worker", [binary, "session", "register", "--role", "worker", "--agent", "hook-check"], tok,
                 "registered as worker"),
                ("unregister", [binary, "session", "unregister"], tok, "removed"),
                ("unregister: nothing registered", [binary, "session", "unregister"], tok, "refused"),
            ]
            hooks = declared_hooks()
            for event in SCENARIOS:
                if not any(e == event for e, _ in hooks):
                    problems.append(f"hooks.json declares no command for {event}")
            for event, h in hooks:
                command = h.get("command", "")
                if h.get("type") != "command" or not command:
                    problems.append(f"hooks.json {event}: a hook that is not a command: {h!r}")
                    continue
                if event not in SCENARIOS:
                    problems.append(f"hooks.json {event}: {command!r} is on an event hook-check has no scenario for")
                    continue
                script = shlex.split(command.replace("${CLAUDE_PLUGIN_ROOT}", PLUGIN))[0]
                if "/" in script and not os.path.isfile(script):
                    problems.append(f"hooks.json {event}: {command!r} runs {os.path.relpath(script, ROOT)}, which does not exist")
                for name, extra, want in SCENARIOS[event]:
                    cases.append((f"{event} {command!r}: {name}", ["sh", "-c", command],
                                  {"CLAUDE_PLUGIN_ROOT": PLUGIN, **tok, **hook_path, **extra}, want, event))
            for case in cases:
                name, argv, extra, want = case[:4]
                event = case[4] if len(case) > 4 else "SessionStart"
                payload = json.dumps({"session_id": "hook-check", "hook_event_name": event}).encode()
                r = run(argv, extra, stdin=payload)
                err = r.stderr.decode(errors="replace").strip()
                if r.stdout:
                    problems.append(f"{name}: wrote {len(r.stdout)} bytes to stdout: {r.stdout[:200]!r}")
                if r.returncode != 0 and argv[0] == "sh":
                    problems.append(f"{name}: the hook exited {r.returncode}; a hook must always exit 0")
                if r.returncode not in (0, 2) and argv[0] == binary:
                    problems.append(f"{name}: exited {r.returncode}")
                if want not in err:
                    problems.append(f"{name}: stderr {err!r} lacks {want!r}, so the case did not run the path it names")
        finally:
            serve.terminate()
            serve.wait(timeout=10)

    if problems:
        print("hook-check FAILED:", file=sys.stderr)
        for p in problems:
            print(f"  - {p}", file=sys.stderr)
        return 1
    print(f"hook-check: {len(cases)} cases, the binary and the {len(hooks)} commands plugin/hooks/hooks.json declares: "
          "stdout empty on every one: ok")
    return 0


if __name__ == "__main__":
    sys.exit(main())
