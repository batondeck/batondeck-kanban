/**
 * P-257 (drift D9) — the online flag reads the device directory or the session's last activity.
 *
 * `wakeAttachedAt` was the old second signal, and nothing on the server writes it, so presence now
 * rests on two signals that are both real: a `device:<pid>` row (the device directory's presence frame
 * lists the live CLI processes, the whole list each time) and `lastSeenAt` (a session's last request).
 *
 * Every case pairs the refusal with its positive control, because "device rows show online" is only
 * meaningful next to "a stale MCP row does not".
 */
import { describe, it, expect } from 'vitest';
import { agentKey, busyAgentKeys, deriveAgentState, isSessionOnline, buildAgentList, isLiveEntry } from '../agentStatus';
import { uiSettings } from '../settings';
import type { AgentSession, Task } from '../types';

const NOW = Date.parse('2026-09-08T12:00:00.000Z');
const ago = (ms: number): string => new Date(NOW - ms).toISOString();
const MIN = 60_000;

const row = (over: Partial<AgentSession> = {}): AgentSession => ({
  sessionKey: 'U-1::worker-1',
  agentId: 'aid-1',
  agentName: 'worker-1',
  identityId: 'U-1',
  kind: 'api_token',
  firstSeenAt: ago(60 * MIN), // long past CONNECTING_WINDOW_MS, so it never confounds a state assert
  lastSeenAt: ago(60 * MIN), // STALE by default
  sessionCount: 1,
  requestCount: 50,
  toolCounts: {},
  ...over,
});
const device = (over: Partial<AgentSession> = {}): AgentSession => row({ sessionKey: 'device:42', kind: 'dev', ...over });

const NONE = new Set<string>();

describe('isSessionOnline — the device directory or the last request', () => {
  it('a device row is ONLINE however old its activity — control: the same row as an MCP session is not', () => {
    expect(isSessionOnline(device(), NOW)).toBe(true);
    expect(isSessionOnline(row(), NOW)).toBe(false);
  });

  it('an MCP session is online within presenceOfflineAfterMs of its last request, and offline after', () => {
    const t = uiSettings().presenceOfflineAfterMs;
    expect(isSessionOnline(row({ lastSeenAt: ago(t - 5_000) }), NOW)).toBe(true);
    expect(isSessionOnline(row({ lastSeenAt: ago(t + 5_000) }), NOW)).toBe(false);
  });

  it('an unparseable lastSeenAt is offline — fail closed, never present by accident', () => {
    expect(isSessionOnline(row({ lastSeenAt: 'not-a-date' }), NOW)).toBe(false);
  });

  it('wakeAttachedAt, which nothing writes, no longer makes a stale row online', () => {
    const stale = { ...row(), wakeAttachedAt: ago(MIN) } as AgentSession;
    expect(isSessionOnline(stale, NOW)).toBe(false);
  });
});

describe('deriveAgentState / isLiveEntry read the same rule', () => {
  it('a device row is available, not offline — control: a stale MCP row is offline', () => {
    expect(deriveAgentState(device(), NONE, NOW)).toBe('available');
    expect(deriveAgentState(row(), NONE, NOW)).toBe('offline');
  });

  it('a live claim still outranks both signals', () => {
    expect(deriveAgentState(row(), new Set([agentKey('U-1', 'worker-1')]), NOW)).toBe('busy');
  });

  it('isLiveEntry counts a device row as "around now" past the presenceWindow', () => {
    // 60 min stale is far outside presenceWindowMs (10 min), so only the device directory carries this.
    const [listed] = buildAgentList([device()], NONE, [], NOW);
    const [bare] = buildAgentList([row()], NONE, [], NOW);
    expect(listed!.device).toBe(true);
    expect(isLiveEntry(listed!, NOW)).toBe(true);
    expect(isLiveEntry(bare!, NOW)).toBe(false);
  });

  it('collapsing siblings keeps the device row over an MCP row with a newer request', () => {
    const listed = device({ lastSeenAt: ago(30 * MIN) });
    const mcp = row({ lastSeenAt: ago(20 * MIN) });
    for (const order of [[mcp, listed], [listed, mcp]]) {
      const [entry] = buildAgentList(order, NONE, [], NOW);
      expect(entry!.device).toBe(true);
      expect(entry!.state).toBe('available');
    }
  });

  it('of two MCP rows the newer request wins', () => {
    const older = row({ agentId: 'aid-old', lastSeenAt: ago(30 * MIN) });
    const newer = row({ agentId: 'aid-new', lastSeenAt: ago(5_000) });
    const [entry] = buildAgentList([newer, older], NONE, [], NOW);
    expect(entry!.lastSeenAt).toBe(newer.lastSeenAt);
    expect(entry!.state).toBe('available');
  });
});

/**
 * REVOCATION beats every liveness signal. A revoked row keeps the timestamps it had at revoke time,
 * and nothing else on the presence path reads `revokedAt` (`Admin.tsx` reads it for opacity only).
 */
describe('a revoked session is never present, however live its signals', () => {
  const revoked = (over: Partial<AgentSession> = {}): AgentSession =>
    row({ revokedAt: ago(MIN), revokedBy: 'admin@x.test', ...over });

  it('REFUSAL: revoked + a fresh lastSeenAt is offline — CONTROL: the identical row without revokedAt is online', () => {
    const fresh = { lastSeenAt: ago(5_000) };
    expect(isSessionOnline(revoked(fresh), NOW)).toBe(false);
    expect(isSessionOnline(row(fresh), NOW)).toBe(true);
    expect(deriveAgentState(revoked(fresh), NONE, NOW)).toBe('offline');
    expect(deriveAgentState(row(fresh), NONE, NOW)).toBe('available');
  });

  it('REFUSAL: a revoked device row is offline too', () => {
    expect(isSessionOnline(revoked({ sessionKey: 'device:42' }), NOW)).toBe(false);
    expect(isSessionOnline(device(), NOW)).toBe(true); // control
  });

  it('revoked outranks BUSY: a disconnected agent holding a live claim is not "working"', () => {
    expect(deriveAgentState(revoked(), new Set([agentKey('U-1', 'worker-1')]), NOW)).toBe('offline');
    expect(deriveAgentState(row(), new Set([agentKey('U-1', 'worker-1')]), NOW)).toBe('busy'); // control
  });

  it('THE ASSIGNMENT SURFACE: isLiveEntry refuses it — it does not consult state, so it needs its own check', () => {
    const [dead] = buildAgentList([revoked({ sessionKey: 'device:42' })], NONE, [], NOW);
    const [alive] = buildAgentList([device()], NONE, [], NOW);
    expect(dead!.revokedAt).toBeDefined(); // it must be CARRIED, or the check below cannot see it
    expect(isLiveEntry(dead!, NOW)).toBe(false);
    expect(isLiveEntry(alive!, NOW)).toBe(true); // control
  });

  it('a RECONNECT wins over its own revoked predecessor, whatever the timestamps say', () => {
    const dead = revoked({ agentId: 'old', lastSeenAt: ago(5_000) });
    const live = row({ agentId: 'new', lastSeenAt: ago(20_000) });
    const [entry] = buildAgentList([dead, live], NONE, [], NOW);
    expect(entry!.revokedAt).toBeUndefined();
    expect(entry!.state).toBe('available');
  });
});

/**
 * P-335 — an agent is its identity plus its name. Every user's default-label session is called
 * `claude-code`, so a name alone would let bob's claim mark alice's own idle session busy, and fold
 * two users' agents into one entry.
 */
describe('the busy set and the agent list key an agent by identity plus name', () => {
  const claimed = (owner: string, agent: string): Task =>
    ({ id: `T-${owner}`, claim: { owner, agent, leaseId: 'L-1', expiresAt: new Date(Date.now() + 10 * MIN).toISOString() } }) as unknown as Task;
  const alice = row({ sessionKey: 'U-alice::claude-code', agentId: 'aid-a', agentName: 'claude-code', identityId: 'U-alice', lastSeenAt: ago(5_000) });
  const bob = row({ sessionKey: 'U-bob::claude-code', agentId: 'aid-b', agentName: 'claude-code', identityId: 'U-bob', lastSeenAt: ago(5_000) });

  it("REFUSAL: bob's live claude-code claim does not mark alice's claude-code busy — CONTROL: bob's own session is busy", () => {
    const busy = busyAgentKeys([claimed('U-bob', 'claude-code')]);
    expect(deriveAgentState(alice, busy, NOW)).toBe('available');
    expect(deriveAgentState(bob, busy, NOW)).toBe('busy');
  });

  it("two identities' same-named sessions are two entries, each with its own state", () => {
    const list = buildAgentList([alice, bob], busyAgentKeys([claimed('U-bob', 'claude-code')]), [], NOW);
    expect(list.map((e) => [e.identityId, e.name, e.state])).toEqual([
      ['U-alice', 'claude-code', 'available'],
      ['U-bob', 'claude-code', 'busy'],
    ]);
  });

  it('a board name with no session is one entry, busy when any live claim names it — control: offline when none does', () => {
    const named = buildAgentList([], busyAgentKeys([claimed('U-bob', 'reviewer')]), ['reviewer', 'reviewer'], NOW);
    expect(named.map((e) => [e.name, e.state])).toEqual([['reviewer', 'busy']]);
    const idle = buildAgentList([], busyAgentKeys([]), ['reviewer'], NOW);
    expect(idle.map((e) => [e.name, e.state])).toEqual([['reviewer', 'offline']]);
    const withSession = buildAgentList([alice], busyAgentKeys([]), ['claude-code'], NOW);
    expect(withSession).toHaveLength(1); // a name a session already carries is not added again
  });
});
