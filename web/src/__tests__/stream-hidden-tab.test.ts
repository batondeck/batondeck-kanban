// api-http#5: the server speaks HTTP/1.1 on the documented plain-HTTP setup, where a browser holds at
// most six connections per host, and every tab held one /api/stream for as long as it lived. The
// sixth tab's stream took the last connection and every fetch in every tab then waited. A tab hidden
// for longer than a short grace now closes its stream, and reopens it when shown, refetching what it
// may have missed; a tab opened in the background opens none until it is shown.
import { describe, it, expect, vi, afterEach } from 'vitest';
import * as streamApi from '../api/stream.js';

class FakeES {
  static instances: FakeES[] = [];
  readyState = 0;
  listeners = new Map<string, Array<() => void>>();
  closed = false;
  constructor(public url: string) {
    FakeES.instances.push(this);
  }
  addEventListener(t: string, fn: () => void): void {
    this.listeners.set(t, [...(this.listeners.get(t) ?? []), fn]);
  }
  close(): void {
    this.closed = true;
    this.readyState = 2;
  }
  emit(t: string): void {
    for (const fn of this.listeners.get(t) ?? []) fn();
  }
}

let visibility: DocumentVisibilityState = 'visible';
Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => visibility });
const show = (v: DocumentVisibilityState): void => {
  visibility = v;
  document.dispatchEvent(new Event('visibilitychange'));
};

const cleanups: Array<() => void> = [];
afterEach(() => {
  for (const c of cleanups.splice(0)) c();
  show('visible');
  FakeES.instances = [];
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function open(onResync = vi.fn()): { onResync: ReturnType<typeof vi.fn> } {
  vi.useFakeTimers();
  vi.stubGlobal('EventSource', FakeES);
  vi.stubGlobal('fetch', vi.fn(async () => ({ status: 200, ok: true }) as Response));
  cleanups.push(streamApi.stream('p1', 'B-1', () => {}, undefined, undefined, undefined, onResync));
  return { onResync };
}

const live = (): FakeES[] => FakeES.instances.filter((e) => !e.closed);

describe('a hidden tab gives its stream back', () => {
  it('hidden past the grace: the stream closes; shown again: it reopens on the same board and refetches', async () => {
    const { onResync } = open();
    const first = FakeES.instances[0]!;
    first.readyState = 1;
    first.emit('open');
    expect(onResync).not.toHaveBeenCalled();
    show('hidden');
    await vi.advanceTimersByTimeAsync(streamApi.HIDDEN_STREAM_GRACE_MS);
    expect(first.closed).toBe(true);
    expect(live()).toHaveLength(0);
    await vi.advanceTimersByTimeAsync(120_000);
    expect(FakeES.instances).toHaveLength(1); // nothing reconnects while hidden
    show('visible');
    expect(live()).toHaveLength(1);
    const second = live()[0]!;
    expect(second.url).toBe(first.url);
    second.readyState = 1;
    second.emit('open');
    expect(onResync).toHaveBeenCalledTimes(1);
  });

  it('CONTROL: hidden for less than the grace, the stream stays', async () => {
    open();
    const first = FakeES.instances[0]!;
    first.readyState = 1;
    first.emit('open');
    show('hidden');
    await vi.advanceTimersByTimeAsync(streamApi.HIDDEN_STREAM_GRACE_MS - 1);
    show('visible');
    await vi.advanceTimersByTimeAsync(streamApi.HIDDEN_STREAM_GRACE_MS * 2);
    expect(first.closed).toBe(false);
    expect(FakeES.instances).toHaveLength(1);
  });

  it('a tab opened in the background opens no stream until it is shown', async () => {
    show('hidden');
    const { onResync } = open();
    expect(FakeES.instances).toHaveLength(0);
    show('visible');
    expect(live()).toHaveLength(1);
    live()[0]!.readyState = 1;
    live()[0]!.emit('open');
    expect(onResync).toHaveBeenCalledTimes(1);
  });
});
