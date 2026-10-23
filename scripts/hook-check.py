#!/usr/bin/env python3
"""hook-check: the plugin's hooks, run as Claude Code runs them, write nothing to stdout and exit 0.

A SessionStart hook's stdout is added to the model's context, so `batondeck-kanban session register`
and `unregister` must keep stdout empty on every path, success included, and plugin/hooks/session.sh
must too. This builds the real binary, serves a scratch SQLite store on a Unix socket in a temporary
directory, mints a user and a token with the binary's own subcommands, and runs each case as a
subprocess, the binary directly (the argv the hook builds) and through the hook script.

usage: python3 scripts/hook-check.py   (needs go; no network; everything lives in a temp dir)
"""
import os
import subprocess
import sys
import tempfile
import time

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
HOOK = os.path.join(ROOT, "plugin", "hooks", "session.sh")


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
                ("hook start", ["sh", HOOK, "start"], {**tok, **hook_path, "BATONDECK_ROLE": "master"}, "registered as master"),
                ("hook start: agent", ["sh", HOOK, "start"], {**tok, **hook_path, "BATONDECK_AGENT": "hook-check"},
                 "agent hook-check"),
                ("hook end", ["sh", HOOK, "end"], {**tok, **hook_path}, "session unregister"),
                ("hook start: bad role", ["sh", HOOK, "start"], {**tok, **hook_path, "BATONDECK_ROLE": "boss"}, "usage"),
                ("hook start: binary not on PATH", ["sh", HOOK, "start"], {**tok, "PATH": "/usr/bin:/bin"}, "not on PATH"),
            ]
            for name, argv, extra, want in cases:
                r = run(argv, extra, stdin=b'{"session_id":"hook-check","hook_event_name":"SessionStart"}')
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
    print(f"hook-check: {len(cases)} cases, the binary and plugin/hooks/session.sh: stdout empty on every one: ok")
    return 0


if __name__ == "__main__":
    sys.exit(main())
