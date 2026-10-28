// R1.14: the frames /api/stream sends that the SPA had no handler for. `resync` (and a rebuilt
// socket) must refetch the board, and a presence frame of device-directory entries must become
// session rows that the next frame replaces.
import { describe, it, expect, vi, afterEach } from 'vitest';
import * as streamApi from '../api/stream.js';
import { replaceDevicePresence } from '../presenceMerge.js';
import type { AgentSession } from '../types.js';

class FakeES {
  static instances: FakeES[] = [];
  readyState = 0;
  listeners = new Map<string, Array<(e: { data?: string }) => void>>();
  closed = false;
  constructor(public url: string) {
    FakeES.instances.push(this);
  }
  addEventListener(t: string, fn: (e: { data?: string }) => void): void {
    this.listeners.set(t, [...(this.listeners.get(t) ?? []), fn]);
  }
  close(): void {
    this.closed = true;
    this.readyState = 2;
  }
  emit(t: string, data?: unknown): void {
    for (const fn of this.listeners.get(t) ?? []) fn(data === undefined ? {} : { data: JSON.stringify(data) });
  }
}

afterEach(() => {
  FakeES.instances = [];
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('resync', () => {
  it('a resync frame calls onResync, and nothing else does on a first connect', () => {
    vi.stubGlobal('EventSource', FakeES);
    const onResync = vi.fn();
    const stop = streamApi.stream('p1', 'B-1', vi.fn(), undefined, undefined, undefined, onResync);
    const es = FakeES.instances[0]!;
    es.readyState = 1;
    es.emit('open');
    es.emit('ready', {});
    expect(onResync).not.toHaveBeenCalled();
    es.emit('resync', {});
    expect(onResync).toHaveBeenCalledTimes(1);
    stop();
  });

  it('a rebuilt socket calls onResync: it starts at "now" and no board frame comes until a change', () => {
    vi.useFakeTimers();
    vi.stubGlobal('EventSource', FakeES);
    const onResync = vi.fn();
    const stop = streamApi.stream('p1', 'B-1', vi.fn(), undefined, undefined, undefined, onResync);
    const first = FakeES.instances[0]!;
    first.readyState = 1;
    first.emit('open');
    first.readyState = 2;
    first.emit('error');
    vi.advanceTimersByTime(1000);
    const second = FakeES.instances[1]!;
    second.readyState = 1;
    second.emit('open');
    expect(onResync).toHaveBeenCalledTimes(1);
    stop();
  });
});

describe('presence frames from the device directory', () => {
  it('maps each entry to a device:<pid> session row whose last activity is the frame time', () => {
    const rows = streamApi.presenceSessions(
      { sessions: [{ pid: 7, role: 'worker', identityId: 'U-1', agentId: 'claude-1', adapter: 'claude-code', registeredAt: '2026-10-23T10:00:00Z' }] },
      '2026-10-23T12:00:00Z',
    );
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({
      sessionKey: 'device:7', agentId: 'claude-1', agentName: 'claude-1', identityId: 'U-1', clientName: 'claude-code',
      firstSeenAt: '2026-10-23T10:00:00Z', lastSeenAt: '2026-10-23T12:00:00Z',
    });
  });

  it('the stream hands the mapped rows to onPresence', () => {
    vi.stubGlobal('EventSource', FakeES);
    const onPresence = vi.fn();
    const stop = streamApi.stream('p1', 'B-1', vi.fn(), undefined, onPresence);
    FakeES.instances[0]!.emit('presence', { sessions: [{ pid: 9, role: 'master', identityId: 'U-1', agentId: 'm', registeredAt: '2026-10-23T10:00:00Z' }] });
    expect(onPresence).toHaveBeenCalledTimes(1);
    expect((onPresence.mock.calls[0]![0] as AgentSession[])[0]!.sessionKey).toBe('device:9');
    stop();
  });

  it('a frame replaces the device rows and keeps every other row', () => {
    const row = (sessionKey: string): AgentSession => ({
      sessionKey, agentId: sessionKey, agentName: sessionKey, identityId: 'U-1', kind: 'dev',
      firstSeenAt: '2026-10-23T10:00:00Z', lastSeenAt: '2026-10-23T10:00:00Z', sessionCount: 1, requestCount: 0, toolCounts: {},
    });
    const next = replaceDevicePresence([row('device:1'), row('U-1:agent')], [row('device:2')]);
    expect(next.map((s) => s.sessionKey).sort()).toEqual(['U-1:agent', 'device:2']);
  });
});
