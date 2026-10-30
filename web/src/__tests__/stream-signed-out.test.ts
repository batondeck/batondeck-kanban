// P-282: after a logout (or an expired session) the server closes /api/stream and the browser's reconnect
// meets 401, which EventSource reports only as a CLOSED error. The connector must ask /api/session, and a
// 401 there must send the app to sign-in and end the reconnects; anything else must reconnect as before.
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

// Every stream a test opens is stopped here too, so a test that fails half-way leaves the shared socket
// module clean for the next one (a mutant fails only the test that names it).
const cleanups: Array<() => void> = [];

afterEach(() => {
  for (const c of cleanups.splice(0)) c();
  FakeES.instances = [];
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

/** A stream whose first socket opened and then went CLOSED, the way a 401 on the reconnect leaves it. */
function closedStream(session: () => Promise<Response>): { fetchMock: ReturnType<typeof vi.fn>; unauthorized: ReturnType<typeof vi.fn>; stop: () => void } {
  vi.useFakeTimers();
  vi.stubGlobal('EventSource', FakeES);
  const fetchMock = vi.fn(session);
  vi.stubGlobal('fetch', fetchMock);
  const unauthorized = vi.fn();
  window.addEventListener('batondeck:unauthorized', unauthorized);
  const stopStream = streamApi.stream('p1', 'B-1', () => {});
  const stop = (): void => {
    stopStream();
    window.removeEventListener('batondeck:unauthorized', unauthorized);
  };
  cleanups.push(stop);
  const first = FakeES.instances[0]!;
  first.readyState = 1;
  first.emit('open');
  first.readyState = 2;
  first.emit('error');
  return { fetchMock, unauthorized, stop };
}

const status = (code: number) => async () => ({ status: code, ok: code < 400 }) as Response;

describe('a CLOSED stream asks /api/session whether the session is gone', () => {
  it('401: the app is sent to sign-in once and the stream is not reconnected', async () => {
    const s = closedStream(status(401));
    expect(s.fetchMock).toHaveBeenCalledTimes(1);
    expect(s.fetchMock.mock.calls[0]![0]).toBe('/api/session');
    expect(s.fetchMock.mock.calls[0]![1]).toMatchObject({ credentials: 'include' });
    await vi.advanceTimersByTimeAsync(0); // the probe settles
    expect(s.unauthorized).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(FakeES.instances).toHaveLength(1); // no reconnect, not at 1s and not at the 30s cap
    expect(s.unauthorized).toHaveBeenCalledTimes(1);
    s.stop();
  });

  it('CONTROL 200: a live session reconnects after 1s and nothing is told to sign in', async () => {
    const s = closedStream(status(200));
    await vi.advanceTimersByTimeAsync(0);
    await vi.advanceTimersByTimeAsync(1000);
    expect(FakeES.instances).toHaveLength(2);
    expect(s.unauthorized).not.toHaveBeenCalled();
    s.stop();
  });

  it('CONTROL 5xx and a failed probe: both reconnect as before', async () => {
    for (const session of [status(503), async () => Promise.reject(new TypeError('network'))]) {
      const s = closedStream(session as () => Promise<Response>);
      await vi.advanceTimersByTimeAsync(1000);
      expect(FakeES.instances).toHaveLength(2);
      expect(s.unauthorized).not.toHaveBeenCalled();
      s.stop();
      FakeES.instances = [];
    }
  });

  it('a probe that never settles does not delay the reconnect', async () => {
    const s = closedStream(() => new Promise<Response>(() => {}));
    await vi.advanceTimersByTimeAsync(1000);
    expect(FakeES.instances).toHaveLength(2);
    s.stop();
  });

  it('a CONNECTING error (the browser retrying a dropped connection itself) sends no probe', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('EventSource', FakeES);
    const fetchMock = vi.fn(status(401));
    vi.stubGlobal('fetch', fetchMock);
    const stop = streamApi.stream('p1', 'B-1', () => {});
    cleanups.push(stop);
    const es = FakeES.instances[0]!;
    es.readyState = 1;
    es.emit('open');
    es.readyState = 0;
    es.emit('error');
    await vi.advanceTimersByTimeAsync(5000);
    expect(fetchMock).not.toHaveBeenCalled();
    stop();
  });

  it('a 401 that lands after stop() does nothing', async () => {
    let answer: (r: Response) => void = () => {};
    const s = closedStream(() => new Promise<Response>((r) => (answer = r)));
    const heard = vi.fn(); // stop() unhooks s.unauthorized, so listen apart from it
    window.addEventListener('batondeck:unauthorized', heard);
    cleanups.push(() => window.removeEventListener('batondeck:unauthorized', heard));
    s.stop();
    answer({ status: 401, ok: false } as Response);
    await vi.advanceTimersByTimeAsync(0);
    expect(heard).not.toHaveBeenCalled();
  });
});
