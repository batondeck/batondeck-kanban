#!/usr/bin/env python3
"""Refuse credentials in the INDEX, before they become a commit.

WHY. Every other guard in this repo protects correctness; this one protects the one mistake that
cannot be fixed by a follow-up commit. A secret pushed to a branch is disclosed the moment it leaves
the machine: `git revert` does not remove it, `--amend` does not remove it after a push, and a
private repo does not make it private (it is in every clone, every fork, every CI log that ever
checked out that ref). The remedy is always the same and always expensive — rotate the credential,
then rewrite history. So the check runs at the ONE moment the cost is zero: before the commit exists.

What it guards is the accident: a debug print, a pasted curl, a fixture with a real token in it, a
doc example someone filled in with the live value.

INDEX, NOT WORKING TREE. Every path is read with `git show :<path>`, so what
is scanned is exactly what the commit will contain. A working-tree scan passes on the very commit
that introduces the secret whenever the file was edited after staging.

HIGH CONFIDENCE ONLY, AND THAT IS A DESIGN DECISION. A noisy secret scanner is bypassed within a
week, and a bypassed guard is worse than no guard because it also carries the belief that something
is watching. So the patterns below match credential FORMATS that are unambiguous on sight — a PEM
private key header, a Google service-account JSON with a real key body, a provider token with a
distinctive prefix. Deliberately NOT here: generic high-entropy strings (every hash, UUID, lockfile
integrity and base64 asset trips them), and `password = "..."` (fixtures and docs are full of them).
Those belong in a periodic external scan, not in a hook that must stay in the low seconds and must
never cry wolf.

MARKER. Put `secret-scan-ok: <reason>` on the offending line or the line immediately above it. It is
for documented FAKES: a test fixture, a worked example in a doc, a format illustration. The reason is
required: a marker with nothing after the colon excuses nothing and fails. If the value is real, the
marker is the wrong tool — rotate it and keep it out of git.

FIVE WAYS THIS FAILS, and the last four are what keep it honest:
  1. an unmarked credential in a staged file  — the thing being guarded;
  2. a marker with no reason                  — an exemption nobody can review;
  3. a marker with no match on or under it    — the exemption rotted and now silently covers
                                                whatever moved into its place;
  4. `--selftest` failing                     — the patterns no longer catch their own examples;
  5. nothing scanned when files ARE staged    — a check matching nothing is DARK, not passing.

Run standalone over the whole tree with `--all` (audits history-in-HEAD, not just the index), and
prove it still works with `--selftest`.
"""
from __future__ import annotations

import re
import subprocess
import sys

MARKER = "secret-scan-ok:"

# (name, regex, an example that MUST match — the selftest asserts each one).
PATTERNS: list[tuple[str, re.Pattern[str], str]] = [
    (
        "private key block",
        re.compile(r"-----BEGIN (?:RSA |EC |OPENSSH |PGP |DSA )?PRIVATE KEY-----"),
        "-----BEGIN RSA PRIVATE KEY-----",
    ),
    (
        "GCP service-account key",
        re.compile(r'"type"\s*:\s*"service_account".*?"private_key"\s*:\s*"-----BEGIN', re.S),
        '{"type": "service_account", "private_key": "-----BEGIN PRIVATE KEY-----"}',
    ),
    (
        "AWS access key id",
        re.compile(r"\b(?:AKIA|ASIA)[0-9A-Z]{16}\b"),
        "AKIAIOSFODNN7EXAMPLE",
    ),
    (
        "GitHub token",
        re.compile(r"\bgh[pousr]_[A-Za-z0-9]{36,}\b"),
        "ghp_" + "a" * 36,
    ),
    (
        "Slack token",
        re.compile(r"\bxox[abposr]-[A-Za-z0-9-]{10,}\b"),
        "xoxb-1234567890-abcdefghij",
    ),
    (
        "Stripe secret key",
        re.compile(r"\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,}\b"),
        "sk_live_" + "a" * 24,
    ),
    (
        "Google API key",
        re.compile(r"\bAIza[0-9A-Za-z_\-]{35}\b"),
        "AIza" + "b" * 35,
    ),
    (
        "OpenAI / Anthropic style key",
        re.compile(r"\b(?:sk-ant-|sk-proj-)[A-Za-z0-9_\-]{20,}\b"),
        "sk-ant-" + "c" * 24,
    ),
    (
        "ElevenLabs key",
        re.compile(r"\bsk_[0-9a-f]{48}\b"),
        "sk_" + "0" * 48,
    ),
    (
        "Cloudflare API token assignment",
        re.compile(r"\bCF_API_TOKEN\s*=\s*['\"]?[A-Za-z0-9_\-]{30,}"),
        "CF_API_TOKEN=" + "d" * 40,
    ),
    (
        # A BatonDeck CLI token: the credential a human copies out of a browser and pastes into a
        # shell is the one most likely to land in a committed script or doc. Length floor 32 keeps
        # it high-confidence.
        "BatonDeck CLI token",
        re.compile(r"\bbd_[A-Za-z0-9_\-]{32,}\b"),
        "bd_" + "e" * 32,
    ),
    (
        "JWT with a payload",
        re.compile(r"\beyJ[A-Za-z0-9_\-]{10,}\.eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\b"),
        "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk",
    ),
]

# Paths that are allowed to carry credential-SHAPED strings: this file (it holds the examples above)
# and lockfiles (integrity hashes are not credentials but do trip loose patterns if any are added).
SKIP = re.compile(r"(?:^|/)(?:package-lock\.json|Cargo\.lock|scripts/check-secrets\.py)$")


def staged_paths() -> list[str]:
    out = subprocess.run(
        ["git", "diff", "--cached", "--name-only", "--diff-filter=ACMR"],
        capture_output=True, text=True, check=True,
    ).stdout
    return [p for p in out.splitlines() if p and not SKIP.search(p)]


def tracked_paths() -> list[str]:
    out = subprocess.run(["git", "ls-files"], capture_output=True, text=True, check=True).stdout
    return [p for p in out.splitlines() if p and not SKIP.search(p)]


def read(path: str, from_index: bool) -> str | None:
    """The INDEX copy when committing (rule 3); the HEAD/worktree copy for a whole-tree audit."""
    ref = f":{path}" if from_index else f"HEAD:{path}"
    r = subprocess.run(["git", "show", ref], capture_output=True, check=False)
    if r.returncode != 0:
        return None
    try:
        return r.stdout.decode("utf-8")
    except UnicodeDecodeError:
        return None  # binary — nothing to scan


def has_reason(line: str) -> bool:
    """A marker's reason: some letter or digit after the colon, a comment closer not counting."""
    after = line.split(MARKER, 1)[1].replace("-->", "").replace("*/", "")
    return re.search(r"[A-Za-z0-9]", after) is not None


def scan(path: str, body: str) -> tuple[list[str], int]:
    """Return (findings, markers_that_excused_something)."""
    lines = body.splitlines()
    findings: list[str] = []
    live_markers = 0
    for i, line in enumerate(lines):
        for name, pat, _ in PATTERNS:
            if not pat.search(line):
                continue
            marked = line if MARKER in line else (lines[i - 1] if i > 0 and MARKER in lines[i - 1] else None)
            if marked is None:
                findings.append(f"{path}:{i + 1}: {name}")
            elif not has_reason(marked):
                findings.append(f"{path}:{i + 1}: {name} — `{MARKER}` with no reason excuses nothing")
            else:
                live_markers += 1
            break
    return findings, live_markers


def rotted_markers(path: str, body: str) -> list[str]:
    """Markers that no longer excuse anything.

    NOT applied to prose. `.md` files DESCRIBE the marker, and a rule that cannot be documented
    without tripping itself is a broken rule, not a finding. Documentation may still *use* a marker
    (a doc example carrying a fake token) and it is still honoured for excusing, reason required;
    only rot-detection is skipped, because the rot rule exists to stop an exemption outliving the
    code beneath it, and prose has no code beneath it.
    """
    if path.endswith(".md"):
        return []
    lines = body.splitlines()
    out = []
    for i, line in enumerate(lines):
        if MARKER not in line:
            continue
        here = any(p.search(line) for _, p, _ in PATTERNS)
        below = i + 1 < len(lines) and any(p.search(lines[i + 1]) for _, p, _ in PATTERNS)
        if not here and not below:
            out.append(f"{path}:{i + 1}: `{MARKER}` excuses nothing — delete it")
    return out


def selftest() -> int:
    """Rule 5: a refusal test AND a positive control, for every pattern."""
    bad = 0
    for name, pat, example in PATTERNS:
        if not pat.search(example):
            print(f"selftest FAIL: {name} no longer matches its own example", file=sys.stderr)
            bad += 1
    # Positive control: ordinary code must not trip anything.
    clean = (
        "const token = process.env.BATONDECK_TOKEN;\n"
        "// see docs/release/plan-2026-10-22.md\n"
        "password = 'hunter2'  # a fixture, deliberately not matched\n"
        "sha256 = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855'\n"
    )
    hits, _ = scan("<control>", clean)
    if hits:
        print(f"selftest FAIL: clean code tripped {hits}", file=sys.stderr)
        bad += 1
    # Refusal control: the marker must actually excuse a real match.
    excused = f"key = 'AKIAIOSFODNN7EXAMPLE'  # {MARKER} illustration only\n"
    hits, markers = scan("<excused>", excused)
    if hits or markers != 1:
        print(f"selftest FAIL: marker did not excuse (hits={hits}, markers={markers})", file=sys.stderr)
        bad += 1
    # Refusal control: a marker with no reason must not excuse.
    for bare in (f"key = 'AKIAIOSFODNN7EXAMPLE'  # {MARKER}\n", f"<!-- {MARKER} -->\nAKIAIOSFODNN7EXAMPLE\n"):
        hits, markers = scan("<bare>", bare)
        if not hits or markers:
            print(f"selftest FAIL: a marker with no reason excused (hits={hits})", file=sys.stderr)
            bad += 1
    print("selftest ok" if not bad else f"selftest FAILED ({bad})")
    return 1 if bad else 0


def main() -> int:
    if "--selftest" in sys.argv:
        return selftest()
    whole_tree = "--all" in sys.argv
    paths = tracked_paths() if whole_tree else staged_paths()
    if not paths:
        return 0  # nothing staged is a legitimate no-op; `--all` on an empty repo is not our problem.

    findings: list[str] = []
    rotted: list[str] = []
    scanned = 0
    for p in paths:
        body = read(p, from_index=not whole_tree)
        if body is None:
            continue
        scanned += 1
        hits, _ = scan(p, body)
        findings += hits
        rotted += rotted_markers(p, body)

    if scanned == 0:
        print(
            f"check-secrets: {len(paths)} path(s) selected but NONE could be read — the check is "
            "dark, not passing.",
            file=sys.stderr,
        )
        return 1

    for line in findings + rotted:
        print(f"  {line}", file=sys.stderr)
    if findings:
        print(
            f"\ncheck-secrets FAILED: {len(findings)} credential(s) in the index.\n"
            "  Real value?  Rotate it FIRST (it is already on this machine's disk), then keep it out of git.\n"
            f"  Fake value?  Put `{MARKER} <reason>` on the line or the line above it.\n"
            "  Do NOT bypass the hook — a secret in a commit is disclosed even if you amend.",
            file=sys.stderr,
        )
    if rotted and not findings:
        print(f"\ncheck-secrets FAILED: {len(rotted)} stale `{MARKER}` marker(s).", file=sys.stderr)
    return 1 if (findings or rotted) else 0


if __name__ == "__main__":
    sys.exit(main())
