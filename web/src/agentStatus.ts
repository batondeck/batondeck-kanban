/**
 * Live agent presence/status, derived from agent sessions (list_agent_sessions) + the board's
 * active claims. Four states with the canonical colors:
 *   available (green)  — connected and idle, ready to be assigned work
 *   busy      (yellow) — holding an active lease (working a task)
 *   offline   (red)    — no recent activity (session went stale)
 *   connecting(blue)   — a session just appeared and is still spinning up
 *
 * Liveness has TWO inputs (P-257, drift D9):
 *   - a `device:<pid>` row — the device session directory's presence frame lists the live CLI processes
 *     on this machine, the whole list each time (api/stream.ts), so a row it lists IS live, however long
 *     ago it registered; a process that leaves the directory leaves the list (presenceMerge.ts).
 *   - `lastSeenAt` — any other session's last MCP request, fresh within `presenceOfflineAfterMs`.
 * "busy" comes from a task whose live claim names the agent, and still outranks both. A process waiting
 * for work makes no requests, which is why the device directory and not a request count says it is there.
 */
import type { AgentSession, Task } from './types.js';
import { toolKey, type ToolKey } from './ToolLogo.js';
import { uiSettings } from './settings.js';

export type AgentState = 'available' | 'busy' | 'offline' | 'connecting';

/**
 * Display-safe agent name. Historical session rows can hold an unexpanded placeholder — a literal
 * "${BATONDECK_AGENT}", "{{agent}}", "%AGENT%" — or a blank; the core blocks NEW ones but old rows
 * persist in Firestore. Fall back to the client name (or "agent") so the UI never shows template goo.
 */
export function displayAgentName(name: string | undefined, clientName?: string): string {
  const n = name?.trim() ?? '';
  if (!n || n.includes('${') || /^\{\{.*\}\}$/.test(n) || /^%.+%$/.test(n)) return clientName || 'agent';
  return n;
}

export const STATE_META: Record<AgentState, { color: string; label: string }> = {
  available: { color: 'var(--done)', label: 'Available' }, // green
  busy: { color: 'var(--progress)', label: 'Busy' }, // yellow / amber
  offline: { color: 'var(--blocked)', label: 'Offline' }, // red
  connecting: { color: 'var(--ready)', label: 'Connecting' }, // blue
};

// A session just created and with at most two requests shows "connecting" for this long. How long an
// MCP session with no request stays online is admin-tunable (uiSettings().presenceOfflineAfterMs).
const CONNECTING_WINDOW_MS = 12_000;

/** Is this a row of the device session directory's live list (api/stream.ts presenceSessions)? */
function isDeviceRow(s: Pick<AgentSession, 'sessionKey'>): boolean {
  return s.sessionKey.startsWith('device:');
}

/** The session's last activity, as a time; NaN when it has none. */
function livenessAt(s: Pick<AgentSession, 'lastSeenAt'>): number {
  return Date.parse(s.lastSeenAt);
}

export interface AgentEntry {
  name: string;
  state: AgentState;
  lastSeenAt: string;
  /** The entry is a row of the device directory's live list, so `isLiveEntry` counts it live. */
  device?: boolean;
  /** Carried because `isLiveEntry` does NOT consult `state`, it re-checks liveness itself — so without
   * this a revoked device row would pass through it even after `deriveAgentState` has called it
   * offline, and `AgentSelect` would offer it as an assignment target. */
  revokedAt?: string;
  identityId?: string;
  tool: ToolKey | null;
}

/** Agents holding a live (non-expired) claim on any of these tasks — the "busy" set for presence dots. */
export function busyAgentNames(tasks: Task[]): Set<string> {
  const now = Date.now();
  const s = new Set<string>();
  for (const t of tasks) {
    const c = t.claim;
    if (c?.agent && (!c.expiresAt || new Date(c.expiresAt).getTime() > now)) s.add(c.agent);
  }
  return s;
}

/**
 * *** THE ONE ONLINE RULE. *** Both presence surfaces route through this: `deriveAgentState` below
 * (the dots) and the Agents screen's "N online" count and per-row badge. Add a signal HERE.
 *
 * Online is a row of the device directory's live list, or a session whose last request is within
 * `presenceOfflineAfterMs`. Either alone is sufficient evidence of life.
 */
export function isSessionOnline(
  s: Pick<AgentSession, 'sessionKey' | 'lastSeenAt' | 'revokedAt'>,
  now: number,
): boolean {
  // *** REVOKED OUTRANKS EVERY LIVENESS SIGNAL. *** A disconnected session's row still carries the
  // timestamps it had at revoke time, and nothing else on the presence path looks at `revokedAt`
  // (`Admin.tsx` reads it, for opacity only), so without this a disconnected agent stayed in the Board
  // rail and the AgentSelect menu until its stale timestamps aged out.
  if (s.revokedAt) return false;
  if (isDeviceRow(s)) return true;
  const sinceSeen = now - livenessAt(s);
  return !Number.isNaN(sinceSeen) && sinceSeen <= uiSettings().presenceOfflineAfterMs;
}

export function deriveAgentState(s: AgentSession, busyNames: Set<string>, now: number): AgentState {
  // An active lease is definitive: the agent is working even if it isn't currently polling (lastSeenAt
  // can legitimately go stale while a long task runs), so "busy" wins over the staleness check.
  // Before `busy`, deliberately: a revoked agent may still hold a live claim (the lease outlives the
  // disconnect), and rendering that as "working" invites waiting for a result that cannot arrive.
  if (s.revokedAt) return 'offline';
  if (busyNames.has(s.agentName)) return 'busy';
  if (!isSessionOnline(s, now)) return 'offline';
  const sinceFirst = now - Date.parse(s.firstSeenAt);
  if (!Number.isNaN(sinceFirst) && sinceFirst < CONNECTING_WINDOW_MS && s.requestCount <= 2) return 'connecting';
  return 'available';
}

const ORDER: Record<AgentState, number> = { available: 0, connecting: 1, busy: 2, offline: 3 };
export function stateOrder(s: AgentState): number {
  return ORDER[s];
}

/**
 * Collapse sessions (keyed by identity+agentName) to one entry per agent name, keeping the freshest,
 * and fold in any extra names already referenced on the board (assignees) so you can still see/route
 * to an agent that isn't currently connected (shown offline, unless it holds a live claim).
 */
export function buildAgentList(
  sessions: AgentSession[],
  busyNames: Set<string>,
  extraNames: string[],
  now: number,
): AgentEntry[] {
  const byName = new Map<string, AgentEntry>();
  for (const s of sessions) {
    if (!s.agentName) continue;
    const prev = byName.get(s.agentName);
    // A device row (listed live by the directory) outranks an MCP row of the same name, then the
    // newer activity wins.
    //
    // REVOCATION IS THE FIRST KEY, because reconnecting mints a NEW session and leaves the old one
    // revoked beside it: on `livenessAt` alone the dead row can win (it may carry newer timestamps than
    // a freshly-created one) and the agent renders offline while it is connected and working.
    if (prev && !prev.revokedAt !== !s.revokedAt) {
      if (!prev.revokedAt) continue; // keep the live one
    } else if (prev && (prev.device ?? false) !== isDeviceRow(s)) {
      if (prev.device) continue; // keep the one the directory lists
    } else if (prev && livenessAt(prev) >= livenessAt(s)) continue;
    byName.set(s.agentName, {
      name: s.agentName,
      state: deriveAgentState(s, busyNames, now),
      lastSeenAt: s.lastSeenAt,
      ...(isDeviceRow(s) ? { device: true } : {}),
      revokedAt: s.revokedAt,
      identityId: s.identityId,
      tool: toolKey(s.agentName, s.clientName),
    });
  }
  for (const n of extraNames) {
    if (n && !byName.has(n)) byName.set(n, { name: n, state: busyNames.has(n) ? 'busy' : 'offline', lastSeenAt: '', tool: toolKey(n) });
  }
  return [...byName.values()].sort((a, b) => stateOrder(a.state) - stateOrder(b.state) || a.name.localeCompare(b.name));
}

/**
 * agentName → tool logo for feed rows that only know the actor's display name. Name-prefix detection
 * (claude-*, cursor-*) still happens inside Avatar; this map adds the client-derived fallback (e.g.
 * "worthy-gayal" connected via claude-code). Newest session wins. NOTE: coverage depends on the sessions
 * passed in — list_agent_sessions is identity-scoped unless the (admin-only) all:true view is used, so
 * non-admin viewers only get logos for agents that connected under their own identity.
 */
export function toolByAgent(sessions: AgentSession[]): Map<string, ToolKey> {
  const newest = new Map<string, AgentSession>();
  for (const s of sessions) {
    if (!s.agentName || !s.clientName) continue;
    const prev = newest.get(s.agentName);
    if (!prev || Date.parse(s.lastSeenAt) > Date.parse(prev.lastSeenAt)) newest.set(s.agentName, s);
  }
  const out = new Map<string, ToolKey>();
  for (const [name, s] of newest) {
    const t = toolKey(name, s.clientName);
    if (t) out.set(name, t);
  }
  return out;
}

/**
 * "Who's around now" filter for presence/pickers: an agent is live if it holds a lease (busy) or was
 * seen within the window. Without this, presence surfaces the entire all-time session history — dozens
 * of dead generated-name sessions — which buries any actually-active agent.
 */
export function isLiveEntry(a: AgentEntry, now: number): boolean {
  if (a.revokedAt) return false; // disconnected: not a target
  if (a.state === 'busy') return true;
  if (a.device) return true; // the device directory lists it live now
  if (!a.lastSeenAt) return false;
  const seen = Date.parse(a.lastSeenAt);
  return !Number.isNaN(seen) && now - seen < uiSettings().presenceWindowMs;
}
