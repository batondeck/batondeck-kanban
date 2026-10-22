/**
 * T-558 — presence from WAKE-BUS ATTACHMENT, not just tool calls.
 *
 * The bug these pin: `lastSeenAt` moves only on an MCP tool call, so an agent parked on its Pub/Sub
 * wake subscription — idle precisely because it is waiting to be handed work — went OFFLINE after
 * `presenceOfflineAfterMs` (75s) while being fully wakeable. Presence claimed the readiest agent in
 * the fleet was gone.
 *
 * Every case pairs the refusal with its positive control (rule 5), because "attached agents show
 * online" is only meaningful next to "unattached ones still don't".
 */
import { describe, it, expect } from 'vitest';
import { deriveAgentState, isSessionOnline, buildAgentList, isLiveEntry } from '../agentStatus';
import { uiSettings } from '../settings';
import type { AgentSession } from '../types';

const NOW = Date.parse('2026-09-08T12:00:00.000Z');
const ago = (ms: number): string => new Date(NOW - ms).toISOString();
const MIN = 60_000;

const row = (over: Partial<AgentSession> = {}): AgentSession => ({
  sessionKey: 'k',
  agentId: 'aid-1',
  agentName: 'worker-1',
  identityId: 'U-1',
  kind: 'service_account',
  firstSeenAt: ago(60 * MIN), // long past CONNECTING_WINDOW_MS, so it never confounds a state assert
  lastSeenAt: ago(60 * MIN), // STALE by default: these tests are about the other signal
  sessionCount: 1,
  requestCount: 50,
  toolCounts: {},
  ...over,
});

const NONE = new Set<string>();

describe('isSessionOnline — two independent liveness signals, each with its own window', () => {
  it('THE BUG: a bus-attached agent with a stale lastSeenAt is ONLINE — control: the same row unattached is not', () => {
    // Identical rows but for the stamp, so the assertion cannot be satisfied by anything else.
    expect(isSessionOnline(row({ wakeAttachedAt: ago(MIN) }), NOW)).toBe(true);
    expect(isSessionOnline(row(), NOW)).toBe(false);
  });

  it('ABSENT means NOT ATTACHED, never "attached long ago"', () => {
    expect(isSessionOnline(row({ wakeAttachedAt: undefined }), NOW)).toBe(false);
    // An unparseable stamp is treated the same way — fail closed, never present-by-accident.
    expect(isSessionOnline(row({ wakeAttachedAt: 'not-a-date' }), NOW)).toBe(false);
  });

  it('the wake window is 360s: a 5-minute-old mint still counts, a 7-minute-old one does not', () => {
    // 360s = one ~180s refresh cadence plus a whole spare, so ONE dropped refresh must not blink the
    // dot (5 min → still online) while a departed agent ages out inside its 300s credential life.
    expect(isSessionOnline(row({ wakeAttachedAt: ago(5 * MIN) }), NOW)).toBe(true);
    expect(isSessionOnline(row({ wakeAttachedAt: ago(7 * MIN) }), NOW)).toBe(false);
  });

  it('the tool-call signal is unchanged — this widens presence, it never narrows it', () => {
    const within = uiSettings().presenceOfflineAfterMs - 5_000;
    expect(isSessionOnline(row({ lastSeenAt: ago(within) }), NOW)).toBe(true);
    // …and it still works with no wake stamp at all, which is every row predating the field.
    expect(isSessionOnline(row({ lastSeenAt: ago(within), wakeAttachedAt: undefined }), NOW)).toBe(true);
  });
});

describe('deriveAgentState / isLiveEntry read the same rule', () => {
  it('an attached agent is available, not offline — control: unattached is offline', () => {
    expect(deriveAgentState(row({ wakeAttachedAt: ago(MIN) }), NONE, NOW)).toBe('available');
    expect(deriveAgentState(row(), NONE, NOW)).toBe('offline');
  });

  it('a live claim still outranks both signals', () => {
    // "busy" must win even with everything stale: lastSeenAt legitimately goes cold during a long task.
    expect(deriveAgentState(row(), new Set(['worker-1']), NOW)).toBe('busy');
  });

  it('isLiveEntry counts bus attachment as "around now" past the presenceWindow', () => {
    // 60 min stale is far outside presenceWindowMs (10 min), so only the stamp can carry this.
    const [attached] = buildAgentList([row({ wakeAttachedAt: ago(MIN) })], NONE, [], NOW);
    const [bare] = buildAgentList([row()], NONE, [], NOW);
    expect(isLiveEntry(attached!, NOW)).toBe(true);
    expect(isLiveEntry(bare!, NOW)).toBe(false);
  });

  it('collapsing siblings keeps the row that is actually live, not the one with the newer tool call', () => {
    // Two rows, one agent name. The bus-attached one is current even though the other called a tool
    // more recently; collapsing on lastSeenAt alone would keep the wrong row and show a stale dot.
    const listening = row({ agentId: 'aid-listening', lastSeenAt: ago(30 * MIN), wakeAttachedAt: ago(MIN) });
    const quiet = row({ agentId: 'aid-quiet', lastSeenAt: ago(20 * MIN) });
    const [entry] = buildAgentList([quiet, listening], NONE, [], NOW);
    expect(entry!.wakeAttachedAt).toBe(listening.wakeAttachedAt);
    expect(entry!.state).toBe('available');
  });
});

/**
 * REVOCATION beats every liveness signal.
 *
 * `disconnect_agent` DELETES the Pub/Sub subscription (spec §4 C4), so a revoked row's doorbell does
 * not exist — assigning to it rings nothing. The row keeps the timestamps it had at revoke time, and
 * before this nothing on the presence path read `revokedAt` (`Admin.tsx` reads it for opacity only).
 *
 * SCALE OF THE HOLE, because it is what justifies fixing it here: with `lastSeenAt` alone a
 * disconnected agent lingered in the Board rail and the AgentSelect menu for ~75s. Adding the 360s
 * wake window widened that to ~5x — so this suite's own feature is what made it worth closing.
 */
describe('a revoked session is never present, however live its timestamps', () => {
  const revoked = (over: Partial<AgentSession> = {}): AgentSession =>
    row({ revokedAt: ago(MIN), revokedBy: 'admin@x.test', ...over });

  it('REFUSAL: revoked + a fresh wake stamp is offline — CONTROL: the identical row without revokedAt is available', () => {
    // The two rows differ in exactly one field, so nothing else can be producing the result.
    expect(isSessionOnline(revoked({ wakeAttachedAt: ago(MIN) }), NOW)).toBe(false);
    expect(isSessionOnline(row({ wakeAttachedAt: ago(MIN) }), NOW)).toBe(true);
    expect(deriveAgentState(revoked({ wakeAttachedAt: ago(MIN) }), NONE, NOW)).toBe('offline');
    expect(deriveAgentState(row({ wakeAttachedAt: ago(MIN) }), NONE, NOW)).toBe('available');
  });

  it('REFUSAL: revoked + a fresh lastSeenAt is offline too — the hole predates the wake window', () => {
    const fresh = { lastSeenAt: ago(5_000) };
    expect(isSessionOnline(revoked(fresh), NOW)).toBe(false);
    expect(isSessionOnline(row(fresh), NOW)).toBe(true); // control
  });

  it('revoked outranks BUSY: a disconnected agent holding a live claim is not "working"', () => {
    // The lease outlives the disconnect, so this row really does appear in busyNames. Rendering it
    // busy invites waiting for a result that cannot arrive — its subscription is gone.
    expect(deriveAgentState(revoked(), new Set(['worker-1']), NOW)).toBe('offline');
    expect(deriveAgentState(row(), new Set(['worker-1']), NOW)).toBe('busy'); // control
  });

  it('THE ASSIGNMENT SURFACE: isLiveEntry refuses it — it does not consult state, so it needs its own check', () => {
    // AgentSelect filters on isLiveEntry ALONE. Without revokedAt on AgentEntry a revoked row passes
    // through the wake branch even after deriveAgentState called it offline, and lands in the menu.
    const [dead] = buildAgentList([revoked({ wakeAttachedAt: ago(MIN) })], NONE, [], NOW);
    const [alive] = buildAgentList([row({ wakeAttachedAt: ago(MIN) })], NONE, [], NOW);
    expect(dead!.revokedAt).toBeDefined(); // it must be CARRIED, or the check below cannot see it
    expect(isLiveEntry(dead!, NOW)).toBe(false);
    expect(isLiveEntry(alive!, NOW)).toBe(true); // control
  });

  it('a RECONNECT wins over its own revoked predecessor, whatever the timestamps say', () => {
    // Reconnecting mints a NEW session and leaves the old one revoked beside it, same agentName. The
    // dead row deliberately carries the FRESHER stamp here: on liveness alone it would win the collapse
    // and the agent would render offline while connected and working.
    const dead = revoked({ agentId: 'old', wakeAttachedAt: ago(MIN) });
    const live = row({ agentId: 'new', wakeAttachedAt: ago(3 * MIN) });
    const [entry] = buildAgentList([dead, live], NONE, [], NOW);
    expect(entry!.revokedAt).toBeUndefined();
    expect(entry!.state).toBe('available');
  });
});
