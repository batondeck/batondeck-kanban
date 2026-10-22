import { describe, it, expect } from 'vitest';
import { mergePresence } from '../presenceMerge';
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
