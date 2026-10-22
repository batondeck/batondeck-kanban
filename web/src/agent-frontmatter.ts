/**
 * Rendering a subagent's frontmatter block in the browser.
 *
 * WHY THIS IS DUPLICATED FROM `src/domain/agent-frontmatter.ts` AND NOT IMPORTED: `web/` is a
 * separate package with no dependency on the core and no path alias to it — the SPA talks to the
 * gateway over HTTP and shares no code by construction. The builder has to produce the exact bytes
 * it PUTs to the signed URL, so the renderer has to exist here.
 *
 * The duplication is PINNED, not trusted: `test/unit/agent-frontmatter-drift.test.ts` renders the
 * same configs through both copies and fails on any difference. Without that, the browser could
 * write an agent header the core's own gate refuses — and the author would see it fail at scan time
 * with no idea which of the two was wrong.
 */
export const AGENT_MODELS = ['sonnet', 'opus', 'haiku', 'fable', 'inherit'] as const;
export const AGENT_EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max'] as const;
export const AGENT_MEMORY = ['user', 'project', 'local'] as const;
export const AGENT_COLORS = [
  'red', 'blue', 'green', 'yellow', 'purple', 'orange', 'pink', 'cyan',
] as const;
/** Fields a PLUGIN subagent discards. Offered nowhere in the form — see the core module's note. */
export const IGNORED_IN_PLUGINS = ['permissionMode', 'hooks', 'mcpServers'] as const;

export interface AgentConfig {
  name: string;
  description: string;
  tools?: readonly string[];
  disallowedTools?: readonly string[];
  model?: string;
  maxTurns?: number;
  skills?: readonly string[];
  memory?: (typeof AGENT_MEMORY)[number];
  background?: boolean;
  effort?: (typeof AGENT_EFFORTS)[number];
  isolation?: 'worktree';
  color?: (typeof AGENT_COLORS)[number];
  initialPrompt?: string;
  cacheTtl?: '5m' | '1h';
}

const NAME_RE = /^[a-z][a-z0-9-]*$/;

export function qualifySkillRefs(skills: readonly string[], pluginName: string): string[] {
  return skills.map((s) => (s.includes(':') ? s : `${pluginName}:${s}`));
}

function yamlScalar(v: string): string {
  // Quote anything YAML would reinterpret. A `: ` opens a mapping; so does a TRAILING colon
  // ("Use this for the following:" parses as a nested compact mapping and the repo's own yaml
  // package refuses it) — an earlier version of this comment claimed only colon-space was dangerous,
  // and the unquoted trailing colon shipped agents that failed to load. A mid-word colon stays
  // unquoted deliberately: qualified refs (`acme:review`) are written bare throughout the real
  // corpus. Leading letter (never a YAML number) and the boolean/null words are excluded too.
  const plain =
    /^[A-Za-z][\w .,()/':-]*$/.test(v) &&
    !v.includes(': ') &&
    !v.endsWith(':') &&
    !/^(?:true|false|yes|no|on|off|null)$/i.test(v);
  return plain ? v : JSON.stringify(v);
}

function yamlList(items: readonly string[]): string {
  return `[${items.map((i) => yamlScalar(i)).join(', ')}]`;
}

/** Empty string when the config is not yet renderable, so the preview can show why. */
export function agentConfigError(cfg: AgentConfig): string {
  if (!NAME_RE.test(cfg.name)) return 'Name must be lowercase letters, digits and hyphens.';
  if (cfg.description.trim() === '') return 'Description is required — routing reads it to decide whether to delegate here.';
  if (cfg.maxTurns !== undefined && (!Number.isInteger(cfg.maxTurns) || cfg.maxTurns < 1)) {
    return 'Max turns must be a positive whole number.';
  }
  return '';
}

export function renderAgentFile(cfg: AgentConfig, pluginName: string, body: string): string {
  const err = agentConfigError(cfg);
  if (err !== '') throw new Error(err);
  const lines: string[] = ['---', `name: ${cfg.name}`, `description: ${yamlScalar(cfg.description)}`];
  if (cfg.tools?.length) lines.push(`tools: ${yamlList(cfg.tools)}`);
  if (cfg.disallowedTools?.length) lines.push(`disallowedTools: ${yamlList(cfg.disallowedTools)}`);
  if (cfg.model !== undefined) lines.push(`model: ${cfg.model}`);
  if (cfg.skills?.length) lines.push(`skills: ${yamlList(qualifySkillRefs(cfg.skills, pluginName))}`);
  if (cfg.memory !== undefined) lines.push(`memory: ${cfg.memory}`);
  if (cfg.effort !== undefined) lines.push(`effort: ${cfg.effort}`);
  if (cfg.maxTurns !== undefined) lines.push(`maxTurns: ${String(cfg.maxTurns)}`);
  if (cfg.isolation !== undefined) lines.push(`isolation: ${cfg.isolation}`);
  if (cfg.background === true) lines.push('background: true');
  if (cfg.color !== undefined) lines.push(`color: ${cfg.color}`);
  if (cfg.initialPrompt !== undefined) lines.push(`initialPrompt: ${yamlScalar(cfg.initialPrompt)}`);
  if (cfg.cacheTtl !== undefined) lines.push(`experimental:\n  cacheTtl: ${cfg.cacheTtl}`);
  lines.push('---', '');
  return `${lines.join('\n')}\n${body.trim()}\n`;
}

/**
 * The inverse of `renderAgentFile` — what lets the composer OPEN an agent instead of only writing
 * one. Round-tripped in `agent-frontmatter.test.ts`: render → parse → render is byte-identical, so a
 * file this builder wrote can always be reopened by it.
 *
 * DELIBERATELY NARROW, and it refuses rather than guesses. This reads the subset `renderAgentFile`
 * emits — one `key: value` per line, flow lists, JSON-quoted scalars — and nothing else. A file with
 * block lists, anchors, multi-line scalars or a key this builder does not write is REJECTED with the
 * reason, so it opens in the raw editor instead. A parser that silently dropped what it did not
 * understand would be worse than none: the author would save back a file with fields deleted.
 *
 * `skills` come back QUALIFIED (`plugin:skill`) because that is what is on disk; the caller strips
 * its own prefix, since `qualifySkillRefs` puts it back on save.
 */
export function parseAgentFile(
  text: string,
): { cfg: AgentConfig; body: string; skills: string[] } | { error: string } {
  const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/.exec(text);
  if (!m) return { error: 'no frontmatter block — the file does not start with ---' };
  const [, head, rest] = m;
  const cfg: Record<string, unknown> = {};
  let skills: string[] = [];
  for (const raw of head!.split(/\r?\n/)) {
    const line = raw.trimEnd();
    if (line === '') continue;
    if (/^\s/.test(line)) {
      // The one nested key `renderAgentFile` emits is `experimental.cacheTtl`.
      const nested = /^\s+cacheTtl:\s*(.+)$/.exec(line);
      if (nested) { cfg['cacheTtl'] = unscalar(nested[1]!); continue; }
      return { error: `indented YAML this builder does not write: ${line.trim()}` };
    }
    const kv = /^([A-Za-z][\w]*):\s*(.*)$/.exec(line);
    if (!kv) return { error: `not a simple key: value line: ${line}` };
    const [, key, valRaw] = kv;
    const val = valRaw!.trim();
    if (key === 'experimental') { if (val !== '') return { error: 'experimental must be a block' }; continue; }
    if (val.startsWith('[')) {
      if (!val.endsWith(']')) return { error: `unterminated list for ${key!}` };
      const items = splitFlowList(val.slice(1, -1));
      if (key === 'skills') skills = items;
      else cfg[key!] = items;
      continue;
    }
    cfg[key!] = key === 'maxTurns' ? Number(unscalar(val)) : unscalar(val);
  }
  if (typeof cfg['name'] !== 'string' || cfg['name'] === '') return { error: 'no name' };
  if (typeof cfg['description'] !== 'string') return { error: 'no description' };
  return { cfg: cfg as unknown as AgentConfig, body: (rest ?? '').trim(), skills };
}

/** Reverse of `yamlScalar`: JSON-quoted round-trips through JSON.parse, everything else is literal. */
function unscalar(v: string): string {
  if (v.startsWith('"')) {
    try {
      const out: unknown = JSON.parse(v);
      if (typeof out === 'string') return out;
    } catch {
      /* fall through to the literal */
    }
  }
  if (v === 'true') return 'true';
  return v;
}

/** Split a flow list on commas that are not inside a quoted scalar. */
function splitFlowList(inner: string): string[] {
  const out: string[] = [];
  let cur = '';
  let quoted = false;
  for (let i = 0; i < inner.length; i++) {
    const ch = inner[i]!;
    if (ch === '"' && inner[i - 1] !== '\\') quoted = !quoted;
    if (ch === ',' && !quoted) { out.push(cur.trim()); cur = ''; continue; }
    cur += ch;
  }
  if (cur.trim() !== '') out.push(cur.trim());
  return out.filter((x) => x !== '').map((x) => unscalar(x));
}
