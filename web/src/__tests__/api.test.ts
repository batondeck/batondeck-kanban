/** api client behavior: error mapping and the 401 → batondeck:unauthorized signal. */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { api, ApiError } from '../api.js';

function jsonResponse(status: number, body: unknown): Response {
  return { ok: status < 400, status, json: async () => body } as Response;
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('api error handling', () => {
  it('maps a non-ok response to an ApiError with the gateway code', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(409, { code: 'STALE', message: 'version mismatch' })));
    await expect(api.getTask('p1', 'T-1')).rejects.toMatchObject({ code: 'STALE', status: 409, message: 'version mismatch' });
  });

  it('dispatches batondeck:unauthorized on a 401 from any API call', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(401, { code: 'UNAUTHENTICATED', message: 'no session' })));
    const heard = vi.fn();
    window.addEventListener('batondeck:unauthorized', heard);
    try {
      await expect(api.getTask('p1', 'T-1')).rejects.toBeInstanceOf(ApiError);
      expect(heard).toHaveBeenCalledTimes(1);
    } finally {
      window.removeEventListener('batondeck:unauthorized', heard);
    }
  });

  it('does NOT dispatch the signal for the /api/session signed-out probe', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(401, { code: 'UNAUTHENTICATED', message: 'no session' })));
    const heard = vi.fn();
    window.addEventListener('batondeck:unauthorized', heard);
    try {
      await expect(api.session()).rejects.toBeInstanceOf(ApiError);
      expect(heard).not.toHaveBeenCalled();
    } finally {
      window.removeEventListener('batondeck:unauthorized', heard);
    }
  });
});

/**
 * T-47: a non-2xx on /stream (5xx blip, redeploy, expired session) puts EventSource in CLOSED for good.
 * api.stream must notice and reconnect, and must report liveness so the board can poll faster meanwhile.
 */
describe('api.stream reconnect', () => {
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

  afterEach(() => {
    FakeES.instances = [];
    vi.useRealTimers();
  });

  it('reconnects after a terminal (CLOSED) stream error and tracks liveness', () => {
    vi.useFakeTimers();
    vi.stubGlobal('EventSource', FakeES);
    const live: boolean[] = [];
    const updates = vi.fn();
    const stop = api.stream('p1', 'B-1', updates, (l) => live.push(l));

    const first = FakeES.instances[0]!;
    first.readyState = 1;
    first.emit('open');
    expect(live).toEqual([true]);
    expect(updates).not.toHaveBeenCalled(); // first connect: the queries just loaded

    // server answered the reconnect with a non-2xx → terminal CLOSED
    first.readyState = 2;
    first.emit('error');
    expect(live).toEqual([true, false]);
    expect(FakeES.instances).toHaveLength(1); // backoff, not a hot loop
    // exactly one pending timer — the retry. The dead stream's stall watchdog must have been cancelled
    // with it, or it would fire later and close the stream that replaced this one.
    expect(vi.getTimerCount()).toBe(1);

    vi.advanceTimersByTime(1000);
    expect(FakeES.instances).toHaveLength(2);
    const second = FakeES.instances[1]!;
    second.readyState = 1;
    second.emit('open');
    expect(live).toEqual([true, false, true]);
    expect(updates).toHaveBeenCalledTimes(1); // refetch once on reconnect: catch up on the outage gap

    stop();
    expect(second.closed).toBe(true);
  });

  it('leaves a CONNECTING stream to the browser and reconnects nothing after stop()', () => {
    vi.useFakeTimers();
    vi.stubGlobal('EventSource', FakeES);
    const stop = api.stream('p1', 'B-1', () => {});
    const es = FakeES.instances[0]!;
    es.readyState = 0; // browser is retrying the dropped connection itself
    es.emit('error');
    vi.advanceTimersByTime(60_000);
    expect(FakeES.instances).toHaveLength(1);

    stop();
    es.readyState = 2;
    es.emit('error');
    vi.advanceTimersByTime(60_000);
    expect(FakeES.instances).toHaveLength(1);
  });

  // A stream that stays OPEN but stops carrying data never fires `error`. The gateway's 20s keep-alive
  // is the only proof the socket is alive, so silence past three of them must force a reconnect.
  it('reconnects a stalled-but-open stream, and does NOT reconnect one that keeps pinging', () => {
    vi.useFakeTimers();
    vi.stubGlobal('EventSource', FakeES);
    const live: boolean[] = [];
    const stop = api.stream('p1', 'B-1', () => {}, (l) => live.push(l));
    const first = FakeES.instances[0]!;
    first.readyState = 1;
    first.emit('open');

    // positive control: pings keep arriving → nothing is reconnected, liveness stays true
    for (let i = 0; i < 5; i++) {
      vi.advanceTimersByTime(20_000);
      first.emit('ping');
    }
    expect(FakeES.instances).toHaveLength(1);
    expect(live).toEqual([true]);

    // refusal: the pings stop while the socket stays OPEN
    vi.advanceTimersByTime(60_000);
    expect(live).toEqual([true, false]);
    expect(first.closed).toBe(true);
    // exactly one pending timer — the retry. A second would be this dead stream's watchdog surviving
    // to close the stream that replaces it.
    expect(vi.getTimerCount()).toBe(1);
    vi.advanceTimersByTime(1000);
    expect(FakeES.instances).toHaveLength(2);

    // the superseded stream's watchdog must not close the one that replaced it
    const second = FakeES.instances[1]!;
    second.readyState = 1;
    second.emit('open');
    vi.advanceTimersByTime(120_000);
    stop();
    expect(FakeES.instances.filter((i) => !i.closed)).toHaveLength(0);
  });

  // stop() must leave NOTHING pending. Asserting "no reconnect happened" is not enough — the `stopped`
  // flag alone satisfies that while a 60s watchdog timer keeps the closure alive past unmount. Count
  // the timers instead, which is the only assertion that can tell the two apart.
  it('stop() leaves no pending timer (neither the retry nor the stall watchdog)', () => {
    vi.useFakeTimers();
    vi.stubGlobal('EventSource', FakeES);
    const stop = api.stream('p1', 'B-1', () => {});
    const first = FakeES.instances[0]!;
    first.readyState = 1;
    first.emit('open');
    expect(vi.getTimerCount()).toBe(1); // the stall watchdog is armed while connected
    stop();
    expect(vi.getTimerCount()).toBe(0);
    vi.advanceTimersByTime(300_000);
    expect(FakeES.instances).toHaveLength(1);
  });
});

/**
 * A stream must never take down the tree that opened it.
 *
 * `NotificationsNavLink` mounts `api.userStream` app-wide, so an EventSource that is absent or throws on
 * construction would crash the entire nav — not merely cost live updates. That is exactly what happened
 * when the app-wide stream landed: two unrelated shell tests started failing with "An error occurred in
 * the <NotificationsNavLink> component". The fix belongs in the connector, not in the tests.
 */
describe('sseConnect degrades instead of crashing', () => {
  it('EventSource missing entirely: reports not-live and returns a working unsubscribe', () => {
    vi.stubGlobal('EventSource', undefined);
    const live: boolean[] = [];
    const off = api.userStream({ onLive: (l) => void live.push(l) });
    expect(live, 'callers must be told the stream is down so they can fall back').toEqual([false]);
    expect(() => off()).not.toThrow();
    vi.unstubAllGlobals();
  });

  it('EventSource throws on construction: same degradation, no exception escapes', () => {
    vi.stubGlobal('EventSource', function () {
      throw new Error('blocked by policy');
    });
    const live: boolean[] = [];
    let off: (() => void) | undefined;
    expect(() => (off = api.userStream({ onLive: (l) => void live.push(l) }))).not.toThrow();
    expect(live).toEqual([false]);
    off?.(); // MUST unsubscribe: the stream is a module-level singleton, so a leak here makes every
             // later test in this file order-dependent. That is exactly how the one-socket tests below
             // first failed.
    vi.unstubAllGlobals();
  });
});

/**
 * ONE SOCKET PER TAB — the property that collapsed the board page from two streams to one.
 *
 * A board page used to open TWO EventSources: `Board.tsx` for `updated`, and the app-wide nav badge for
 * presence + notifications. Both carried presence and both ran their own 20s heartbeat, so the second
 * was pure duplication — two sockets and two server-side presence subscriptions for one user on one
 * page. Reported from a real staging session, not caught by any test, because nothing here had ever
 * counted connections.
 */
describe('the shared stream opens exactly one EventSource', () => {
  /** Tracks how many sockets are OPEN, which is the property that matters — a rescope closes the old
   *  one, so counting constructions would just count reconnects. */
  class Counting {
    static open = 0;
    static urls: string[] = [];
    readyState = 1;
    constructor(public url: string) {
      Counting.open += 1;
      Counting.urls.push(url);
    }
    addEventListener(): void {}
    close(): void {
      Counting.open -= 1;
    }
  }
  const reset = () => {
    Counting.open = 0;
    Counting.urls = [];
    vi.stubGlobal('EventSource', Counting);
  };

  it('a board subscriber AND the app-wide nav share ONE open socket, scoped to the board', () => {
    reset();
    const offNav = api.userStream({});
    const offBoard = api.stream('P-1', 'B-1', () => {});
    expect(Counting.open, `urls: ${JSON.stringify(Counting.urls)}`).toBe(1);
    expect(Counting.urls.at(-1), 'the live socket must be scoped to the board').toContain('boardId=B-1');
    offBoard();
    offNav();
    vi.unstubAllGlobals();
  });

  it('leaving the board rescopes rather than stacking — still ONE open socket', () => {
    reset();
    const offNav = api.userStream({});
    const offBoard = api.stream('P-1', 'B-1', () => {});
    offBoard(); // navigate away from the board
    expect(Counting.open).toBe(1);
    expect(Counting.urls.at(-1), 'must fall back to the unscoped stream').toBe('/api/stream');
    offNav();
    vi.unstubAllGlobals();
  });

  it('the last subscriber leaving closes the socket — an idle tab holds no connection', () => {
    reset();
    const off = api.userStream({});
    expect(Counting.open).toBe(1);
    off();
    expect(Counting.open, 'a tab with nothing mounted must hold no socket').toBe(0);
    vi.unstubAllGlobals();
  });
});

/**
 * SIGN-OUT LEAVES OUR ORIGIN (T-452).
 *
 * *** THIS IS THE LAST HOP, AND IT IS THE ONE NO SERVER TEST CAN SEE. *** The BFF suite proves
 * `/auth/logout` EMITS a URL naming this session; nothing there proves anyone goes to it. Delete the
 * navigation in `api.logout` and every server-side test stays green while the browser never leaves our
 * origin, the AuthKit session survives, and the defect is fully restored — precisely the "green guard
 * over a deleted line" shape CONTRIBUTING rule 12 is about.
 *
 * Driven through the REAL `api.logout` with `fetch` stubbed, never through a mocked `api`: mocking the
 * module under test would assert the interface against itself.
 */
describe('api.logout navigates to the IdP logout URL', () => {
  const LOGOUT_URL = 'https://api.workos.com/user_management/sessions/logout?session_id=session_01X';

  const stubbedLocation = (logoutUrl: string | null): ReturnType<typeof vi.fn> => {
    const assign = vi.fn();
    vi.stubGlobal('location', { assign, search: '', href: '' });
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(200, { ok: true, logoutUrl })));
    return assign;
  };

  it('THE LAST HOP: a logout URL in the body becomes a top-level navigation', async () => {
    const assign = stubbedLocation(LOGOUT_URL);
    // NOT awaited — since T-490 a logout with a URL never resolves (the navigation is the sign-out,
    // and resolving is what let SignIn's auto-redirect abort it). The race rows below pin that.
    void api.logout();
    for (let i = 0; i < 20; i++) await Promise.resolve();
    expect(assign, 'the browser never left our origin — the AuthKit session would survive').toHaveBeenCalledWith(
      LOGOUT_URL,
    );
  });

  /**
   * NEGATIVE CONTROL (rule 5). `/auth/demo`, and any session minted before `cws` existed, answer
   * `logoutUrl: null` — those must sign out locally with no redirect at all. A version of the line
   * above that navigated unconditionally would send them to `undefined`.
   */
  it('NEGATIVE CONTROL: a null logout URL navigates nowhere', async () => {
    const assign = stubbedLocation(null);
    await api.logout();
    expect(assign).not.toHaveBeenCalled();
  });
});

describe('logout wins the race (T-490)', () => {
  /**
   * The IdP-logout navigation must be the LAST thing logout does: a resolved promise hands control
   * back to auth.tsx, which drops identity, renders SignIn, and SignIn's auto-redirect REPLACES the
   * in-flight navigation. Measured live 2026-08-20: IdP logout ERR_ABORTED, silent re-auth, Sign out
   * a no-op. So with a logoutUrl the promise must stay PENDING after location.assign.
   */
  it('with a logoutUrl: assigns the navigation and NEVER resolves', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(200, { ok: true, logoutUrl: 'https://idp.example/logout?session_id=s1' })));
    const assign = vi.fn();
    vi.stubGlobal('location', { assign, search: '', href: '' });
    let resolved = false;
    void api.logout().then(() => { resolved = true; });
    // Drain microtasks well past the fetch + assign.
    for (let i = 0; i < 20; i++) await Promise.resolve();
    expect(assign).toHaveBeenCalledWith('https://idp.example/logout?session_id=s1');
    expect(resolved, 'logout resolved after assign — the SignIn auto-redirect can now abort the IdP logout').toBe(false);
  });

  it('POSITIVE CONTROL: without a logoutUrl (demo session) logout resolves and navigates nowhere', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(200, { ok: true, logoutUrl: null })));
    const assign = vi.fn();
    vi.stubGlobal('location', { assign, search: '', href: '' });
    await api.logout();
    expect(assign).not.toHaveBeenCalled();
  });
});
