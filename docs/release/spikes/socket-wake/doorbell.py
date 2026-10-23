#!/usr/bin/env python3
"""R3.1 spike writer: ring one Claude Code session's inbox socket. Scratch, not product code.

Usage: doorbell.py <pid> <content> [--priority next|now|later] [--no-auth]

The target is named by PID, never by path: the socket path, sessionId and auth key come from
that session's own registry entry, ~/.claude/sessions/<pid>.json and <pid>.<sha256(path)>.key.
The token is read from the key file and never printed. Run it only against a session you started.
"""
import argparse, hashlib, json, os, socket, time

ap = argparse.ArgumentParser()
ap.add_argument("pid", type=int)
ap.add_argument("content")
ap.add_argument("--priority", choices=["next", "now", "later"])
ap.add_argument("--no-auth", action="store_true")
a = ap.parse_args()

entry = json.load(open(os.path.expanduser(f"~/.claude/sessions/{a.pid}.json")))
path = entry["messagingSocketPath"]
os.kill(a.pid, 0)  # a SIGKILLed session leaves its socket file behind; refuse a dead pid

frames = []
if not a.no_auth:
    digest = hashlib.sha256(path.encode()).hexdigest()
    key = os.path.expanduser(f"~/.claude/sessions/{a.pid}.{digest}.key")
    if os.path.exists(key):
        frames.append({"type": "auth", "token": json.load(open(key))["peerToken"]})
msg = {"type": "user", "session_id": entry["sessionId"], "message": {"role": "user", "content": a.content}}
if a.priority:
    msg["priority"] = a.priority
frames.append(msg)

s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.connect(path)
for f in frames:
    raw = (json.dumps(f, separators=(",", ":")) + "\n").encode()
    t = time.time_ns()
    s.sendall(raw)
    if f["type"] == "auth":
        print(f"WRITE t_ns={t} auth line ({len(raw)} bytes, token withheld)")
    else:
        print(f"WRITE t_ns={t} {raw.decode().rstrip()}\n  hex={raw.hex()}")
s.shutdown(socket.SHUT_WR)
s.settimeout(2.0)
try:
    reply = s.recv(65536)
except socket.timeout:
    reply = b""
print(f"RESPONSE {len(reply)} bytes")  # the inbox never answers on the connection
