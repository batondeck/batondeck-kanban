#!/usr/bin/env python3
"""skill-lint: every instruction plugin/ ships must be followable against this repository.

Checks, over the instruction files (plugin/skills/*/SKILL.md, plugin/commands/*.md, plugin/README.md,
plugin/SECURITY.md) unless a check says otherwise:
  1. each SKILL.md has frontmatter with a non-empty name and description; each command a description;
  2. every script a file names (scripts/<x>.sh|.py, hooks/<x>.sh) exists in plugin/, hooks.json included;
  3. every tool a file names exists in contract/expected.json. A tool name is any lowercase snake_case
     word (a letter, then letters and digits, with at least one `_` segment) that is not part of a
     path or a longer identifier, in any form: backticked, a call, a fenced block or plain prose. The
     only snake_case words allowed that are not tools are the contract's enum values and the server's
     prompts (NOT_TOOLS);
  4. no file under plugin/ but TRIM.md names a conductor tool this repository dropped (in
     contract/tools.json or admin-tools.json, not in expected.json), in any form;
  5. no file under plugin/ but TRIM.md names a retired term (the six-state names, the dropped wake
     paths) — TRIM.md is the record of what was dropped, so it is the one file that names them;
  6. plugin/hooks/hooks.json registers exactly SessionStart and SessionEnd (ADR-0004);
  7. every field a file writes in backticks, as a lone camelCase word (`downloadUrl`) or as a
     camelCase key (`blockedReason: "poison"`, `{ projectId, ... }` is not one), is a property somewhere
     in contract/expected.json, or one of NOT_FIELDS: an instruction must not send an agent looking for
     a field no tool carries.
It also WARNS, without failing, about every tool the files name that is in expected.json but not
registered by the server yet (the mcp.Tool(s, "<area>", "<name>" calls in internal/mcp/tools/*.go).
Python 3 standard library only. Exit 1 with one line per problem.
"""
import glob
import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PLUGIN = os.path.join(ROOT, "plugin")
EXEMPT = {os.path.join(PLUGIN, "TRIM.md")}

# Backticked snake_case words that are not tool names: the server's MCP prompts
# (internal/mcp/prompts.go). A name listed here is never checked against expected.json. The enum
# values of contract/expected.json (a memory scope such as agent_global) are not tools either.
NOT_TOOLS = {"pick_up_next_task", "triage_inbox", "summarize_for_handoff", "decompose_into_subtasks"}

# Backticked camelCase fields that are not tool properties, each with where it does live.
NOT_FIELDS = {
    "boardIds",             # the socket doorbell frame's (ADR-0004, amended 2026-10-28), not a tool's
    "crossSessionInbound",  # a Claude Code setting (--settings), not the server's
}

# The words ADR-0004 and ADR-0005 retired (R3.4 AC2), case-sensitive as that grep is.
RETIRED = re.compile(r"BACKLOG|READY|CANCELLED|DEAD_LETTER|requeue_task|get_transitions|Pub/Sub|channel"
                     r"|asyncRewake|stop-gate|wake-wait")


def rel(p):
    return os.path.relpath(p, ROOT)


def enum_values(node):
    if isinstance(node, dict):
        out = {v for v in node.get("enum", []) if isinstance(v, str)}
        for v in node.values():
            out |= enum_values(v)
        return out
    if isinstance(node, list):
        return set().union(*map(enum_values, node)) if node else set()
    return set()


def property_names(node):
    """Every property name anywhere in a contract node: the fields its tools take and return."""
    if isinstance(node, dict):
        out = set(node.get("properties", {}) or {})
        for v in node.values():
            out |= property_names(v)
        return out
    if isinstance(node, list):
        return set().union(*map(property_names, node)) if node else set()
    return set()


def tool_names(path):
    with open(path) as f:
        tools = json.load(f)["tools"]
    return set(tools) if isinstance(tools, dict) else {t["name"] for t in tools}


def main():
    problems = []
    expected = tool_names(os.path.join(ROOT, "contract", "expected.json"))
    not_tools = NOT_TOOLS | enum_values(json.load(open(os.path.join(ROOT, "contract", "expected.json"))))
    conductor = tool_names(os.path.join(ROOT, "contract", "tools.json")) | tool_names(
        os.path.join(ROOT, "contract", "admin-tools.json"))
    dropped = conductor - expected

    skills = sorted(glob.glob(os.path.join(PLUGIN, "skills", "*", "SKILL.md")))
    commands = sorted(glob.glob(os.path.join(PLUGIN, "commands", "*.md")))
    docs = skills + commands + [p for p in (os.path.join(PLUGIN, "README.md"), os.path.join(PLUGIN, "SECURITY.md"))
                                if os.path.isfile(p)]
    if not skills:
        problems.append("plugin/skills/*/SKILL.md: no skill found, nothing to lint")

    # 1. frontmatter
    for d in skills + commands:
        text = open(d).read()
        parts = text.split("---", 2)
        if not text.startswith("---") or len(parts) < 3:
            problems.append(f"{rel(d)}: no frontmatter")
            continue
        keys = ("name", "description") if d in skills else ("description",)
        for k in keys:
            m = re.search(rf"^{k}:[ \t]*(.*)$", parts[1], re.M)
            if not m or not m.group(1).strip().strip('"'):
                problems.append(f"{rel(d)}: frontmatter `{k}` missing or empty")

    # 2. scripts, in the instruction files and in hooks.json
    ref = re.compile(r"(?:\$\{CLAUDE_PLUGIN_ROOT\}/)?((?:[A-Za-z0-9_.-]+/)*(?:scripts|hooks)/[A-Za-z0-9_.-]+\.(?:sh|py))")
    for d in docs + [os.path.join(PLUGIN, "hooks", "hooks.json")]:
        base = os.path.dirname(d)
        for n, line in enumerate(open(d).read().splitlines(), 1):
            for m in ref.finditer(line):
                p = m.group(1)
                if not any(os.path.isfile(os.path.join(b, p)) for b in (PLUGIN, base)):
                    problems.append(f"{rel(d)}:{n}: names `{p}`, which is not in plugin/")

    # 3. every snake_case word is a tool, in any form (backticks, a call, a fence, prose)
    snake = re.compile(r"(?<![A-Za-z0-9_./$-])[a-z][a-z0-9]*(?:_[a-z0-9]+)+(?![A-Za-z0-9_])")
    named = set()
    for d in docs:
        for n, line in enumerate(open(d).read().splitlines(), 1):
            for m in snake.finditer(line):
                name = m.group(0)
                if name in not_tools:
                    continue
                if name in expected:
                    named.add(name)
                else:
                    problems.append(f"{rel(d)}:{n}: names the tool `{name}`, which is not in contract/expected.json")

    # the warning: named tools the server does not register yet
    reg = re.compile(r'mcp\.Tool(?:\[[^\]]*\])?\(s, "[a-z]+", "([a-z_]+)"')
    registered = set()
    for g in glob.glob(os.path.join(ROOT, "internal", "mcp", "tools", "*.go")):
        if not g.endswith("_test.go"):
            registered |= set(reg.findall(open(g).read()))
    if not registered:
        problems.append("internal/mcp/tools/*.go: no mcp.Tool registration found, so the unregistered-tool warning has no source")
    unregistered = sorted(named - registered)

    # 4 and 5: every file under plugin/ except TRIM.md
    word = re.compile(r"\b(" + "|".join(sorted(map(re.escape, dropped), key=len, reverse=True)) + r")\b") if dropped else None
    scanned = 0
    for dirpath, _, files in os.walk(PLUGIN):
        for fn in sorted(files):
            p = os.path.join(dirpath, fn)
            if p in EXEMPT:
                continue
            try:
                lines = open(p, encoding="utf-8").read().splitlines()
            except (UnicodeDecodeError, OSError):
                continue
            scanned += 1
            for n, line in enumerate(lines, 1):
                for m in (word.finditer(line) if word else ()):
                    problems.append(f"{rel(p)}:{n}: names `{m.group(1)}`, a conductor tool this repository dropped")
                for m in RETIRED.finditer(line):
                    problems.append(f"{rel(p)}:{n}: names `{m.group(0)}`, retired by ADR-0004/ADR-0005")

    # 7. backticked fields are contract properties
    props = property_names(json.load(open(os.path.join(ROOT, "contract", "expected.json"))))
    span = re.compile(r"`([^`\n]+)`")
    key = re.compile(r"\b([a-z]+[A-Z][A-Za-z0-9]*)\s*:")
    lone = re.compile(r"[a-z]+[A-Z][A-Za-z0-9]*")
    for d in docs:
        for n, line in enumerate(open(d).read().splitlines(), 1):
            for m in span.finditer(line):
                code = m.group(1)
                fields = set(key.findall(code))
                if lone.fullmatch(code):
                    fields.add(code)
                for f in sorted(fields - props - NOT_FIELDS):
                    problems.append(f"{rel(d)}:{n}: names the field `{f}`, which no tool of contract/expected.json takes or returns")

    # 6. the hook surface
    hooks_json = os.path.join(PLUGIN, "hooks", "hooks.json")
    try:
        events = sorted(json.load(open(hooks_json))["hooks"])
    except (OSError, ValueError, KeyError) as e:
        problems.append(f"{rel(hooks_json)}: unreadable ({e})")
    else:
        if events != ["SessionEnd", "SessionStart"]:
            problems.append(f"{rel(hooks_json)}: registers {events}; exactly SessionStart and SessionEnd are allowed (ADR-0004)")

    if problems:
        print("skill-lint FAILED:", file=sys.stderr)
        for p in problems:
            print(f"  - {p}", file=sys.stderr)
        return 1
    if registered and unregistered:
        print(f"skill-lint: WARNING: the plugin names {len(unregistered)} tools that are in contract/expected.json "
              f"but not registered by the server yet ({len(registered)} registered): {' '.join(unregistered)}")
    print(f"skill-lint: {len(docs)} instruction files, {scanned} plugin files, {len(expected)} tools in "
          f"contract/expected.json, {len(dropped)} dropped conductor tools: ok")
    return 0


if __name__ == "__main__":
    sys.exit(main())
