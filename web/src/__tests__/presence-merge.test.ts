import { describe, it, expect } from 'vitest';
import { keepCachedDevices, mergePresence } from '../presenceMerge';
import type { AgentSession } from '../types';

const row = (sessionKey: string, lastSeenAt: string, requestCount = 1): AgentSession => ({
  sessionKey,
  agentId: sessionKey,
  agentName: sessionKey,
  identityId: 'U-1',
  kind: 'service_account',
  firstSeenAt: lastSeenAt,
  lastSeenAt,
  sessionCount: 1,
  requestCount,
  toolCounts: {},
});

describe('mergePresence — a 20-row live frame must not shrink the 100-row list', () => {
  it('keeps rows absent from the frame and upserts the ones it names, newest first', () => {
    const prev = [row('old-1', '2026-09-01T00:00:00Z'), row('live-1', '2026-09-06T10:00:00Z', 5)];
    const frame = [row('live-1', '2026-09-06T10:05:00Z', 6), row('live-2', '2026-09-06T10:04:00Z')];
    const merged = mergePresence(prev, frame);
    expect(merged.map((s) => s.sessionKey)).toEqual(['live-1', 'live-2', 'old-1']);
    // The frame's copy wins for a row both hold (the counter moved on).
    expect(merged[0]!.requestCount).toBe(6);
  });

  it('POSITIVE CONTROL: with nothing cached the frame IS the list', () => {
    const frame = [row('a', '2026-09-06T10:00:00Z'), row('b', '2026-09-06T11:00:00Z')];
    expect(mergePresence(undefined, frame).map((s) => s.sessionKey)).toEqual(['b', 'a']);
  });
});

describe('keepCachedDevices — a list answer keeps the cached device rows and nothing else (P-197)', () => {
  it('a session the list no longer answers goes; a cached device: row stays; the answer wins its own rows', () => {
    const cached = [row('S-gone', '2026-10-30T10:00:00Z'), row('device:4242', '2026-10-30T10:01:00Z'), row('S-live', '2026-10-30T09:00:00Z', 1)];
    const fresh = [row('S-live', '2026-10-30T10:02:00Z', 7)];
    const out = keepCachedDevices(fresh, cached);
    expect(out.map((s) => s.sessionKey)).toEqual(['S-live', 'device:4242']);
    expect(out[0]!.requestCount).toBe(7);
  });

  it('an empty answer with an MCP and a device row cached: only the device row', () => {
    expect(keepCachedDevices([], [row('S-mcp', '2026-10-30T10:00:00Z'), row('device:1', '2026-10-30T10:00:00Z')]).map((s) => s.sessionKey)).toEqual(['device:1']);
  });

  it('POSITIVE CONTROL: nothing cached, the answer is the list', () => {
    expect(keepCachedDevices([row('a', '2026-10-30T10:00:00Z')], undefined).map((s) => s.sessionKey)).toEqual(['a']);
  });
});
