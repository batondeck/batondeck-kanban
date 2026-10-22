/**
 * Live agent presence/status, derived from agent sessions (list_agent_sessions) + the board's
 * active claims. Four states with the canonical colors:
 *   available (green)  — connected and idle, ready to be assigned work
 *   busy      (yellow) — holding an active lease (working a task)
 *   offline   (red)    — no recent activity (session went stale)
 *   connecting(blue)   — a session just appeared and is still spinning up
 *
 * Liveness has TWO independent inputs and neither subsumes the other:
 *   - `lastSeenAt`     — moves on an MCP TOOL CALL (including the worker's ~50s long-poll).
 *   - `wakeAttachedAt` — moves when the agent MINTS WAKE CREDENTIALS, i.e. is attached to the Pub/Sub
 *                        doorbell bus (T-558). Its own ~3-minute cadence, hence its own window below.
 * "busy" comes from a task whose live claim names the agent, and still outranks both.
 *
 * *** THIS BLOCK SAID "No new backend surface" UNTIL T-558, AND THAT LINE WAS THE BUG. *** Deriving
 * presence from `lastSeenAt` alone is only correct if every live agent makes tool calls. An agent
 * parked on its wake subscription makes NONE — it is idle precisely because it is waiting to be given
 * work — so the readiest agent in the fleet rendered OFFLINE after `presenceOfflineAfterMs` (75s).
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

// A worker idles by long-polling wait_for_task (~50s/cycle), so lastSeenAt refreshes at least that
// often while it's alive and waiting; allow a cycle + buffer before calling it offline. The threshold
// is admin-tunable (uiSettings().presenceOfflineAfterMs).
const CONNECTING_WINDOW_MS = 12_000;

/**
 * How long a wake-credential mint vouches for an agent. NOT admin-tunable, deliberately: unlike
 * `presenceOfflineAfterMs` this is not a taste threshold, it is derived from two constants on the
 * credential path — `WAKE_TTL_SECS` (300, `wake/src/lib.rs`) and the listener's `REFRESH_SLACK` (120,
 * `skill/wake-listener/`). An attached agent held a stream for its token's life, so this is one cadence
 * plus a whole spare one: a single dropped refresh does not blink the dot, and a departed agent still
 * ages out inside the 300s its credential remains valid. Retune it only when those constants move.
 */
const WAKE_ATTACHED_WINDOW_MS = 360_000;

/** Was this session attached to the doorbell bus recently enough to count as present? */
function wakeAttached(s: Pick<AgentSession, 'wakeAttachedAt'>, now: number): boolean {
  if (!s.wakeAttachedAt) return false; // absent = not attached, NEVER "attached long ago"
  const t = Date.parse(s.wakeAttachedAt);
  return !Number.isNaN(t) && now - t < WAKE_ATTACHED_WINDOW_MS;
}

/**
 * The freshest evidence this session was alive, by EITHER route. Used only to pick between sibling
 * rows of the same agent name — a row idling on the bus is more current than one whose last tool call
 * is older, and collapsing on `lastSeenAt` alone would discard the live one and show its dot as stale.
 */
function livenessAt(s: Pick<AgentSession, 'lastSeenAt' | 'wakeAttachedAt'>): number {
  const seen = Date.parse(s.lastSeenAt);
  const att = s.wakeAttachedAt ? Date.parse(s.wakeAttachedAt) : Number.NaN;
  if (Number.isNaN(seen)) return att;
  if (Number.isNaN(att)) return seen;
  return Math.max(seen, att);
}

export interface AgentEntry {
  name: string;
  state: AgentState;
  lastSeenAt: string;
  /** T-558 — carried so `isLiveEntry` can see bus attachment too, not just tool calls. */
  wakeAttachedAt?: string;
  /** Carried for the same reason, one signal over: `isLiveEntry` does NOT consult `state`, it re-checks
   * attachment itself — so without this a revoked row passes it through the wake branch even after
   * `deriveAgentState` has called it offline, and `AgentSelect` offers it as an assignment target. */
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
 * (the dots) and the Agents screen's "N online" count and per-row badge. They were two separate
 * expressions of the same threshold, under a comment promising they agreed — so T-558 had to be fixed
 * in both, and the next change to either would have silently split them. Add a signal HERE.
 *
 * OFFLINE requires BOTH signals to be stale. Either alone is sufficient evidence of life, and they have
 * different cadences — a tool call every ~50s while working, a credential mint every ~180s while merely
 * listening — so each keeps its own window rather than being blended into one threshold.
 */
export function isSessionOnline(
  s: Pick<AgentSession, 'lastSeenAt' | 'wakeAttachedAt' | 'revokedAt'>,
  now: number,
): boolean {
  // *** REVOKED OUTRANKS EVERY LIVENESS SIGNAL. *** `disconnect_agent` DELETES the Pub/Sub
  // subscription (spec §4 C4), so a revoked row is not merely stale — its doorbell no longer exists
  // and assigning to it rings nothing. The row still carries the timestamps it had at revoke time, and
  // nothing else on the presence path looks at `revokedAt` (`Admin.tsx` reads it, for opacity only),
  // so without this a disconnected agent stayed in the Board rail and the AgentSelect menu until its
  // stale timestamps aged out. That was already true of `lastSeenAt` for ~75s; adding the 360s wake
  // window widened the same hole ~5x, which is what made it worth closing here.
  if (s.revokedAt) return false;
  const sinceSeen = now - Date.parse(s.lastSeenAt);
  if (!Number.isNaN(sinceSeen) && sinceSeen <= uiSettings().presenceOfflineAfterMs) return true;
  return wakeAttached(s, now);
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
    // Compare on `livenessAt`, not `lastSeenAt`: of two rows for one agent name, the one attached to
    // the bus is the current one even if the other made a tool call more recently.
    //
    // REVOCATION IS THE FIRST KEY, because reconnecting mints a NEW session and leaves the old one
    // revoked beside it: on `livenessAt` alone the dead row can win (it may carry newer timestamps than
    // a freshly-created one) and the agent renders offline while it is connected and working.
    if (prev && !prev.revokedAt !== !s.revokedAt) {
      if (!prev.revokedAt) continue; // keep the live one
    } else if (prev && livenessAt(prev) >= livenessAt(s)) continue;
    byName.set(s.agentName, {
      name: s.agentName,
      state: deriveAgentState(s, busyNames, now),
      lastSeenAt: s.lastSeenAt,
      wakeAttachedAt: s.wakeAttachedAt,
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
  if (a.revokedAt) return false; // disconnected: its subscription is deleted, so it is not a target
  if (a.state === 'busy') return true;
  if (wakeAttached(a, now)) return true; // T-558: attached to the bus IS "around now"
  if (!a.lastSeenAt) return false;
  const seen = Date.parse(a.lastSeenAt);
  return !Number.isNaN(seen) && now - seen < uiSettings().presenceWindowMs;
}
