// The live area: /api/stream (internal/api/stream.go), one EventSource per tab shared by every screen.
import type { AgentSession } from '../types.js';

/**
 * The hardened SSE connector shared by every stream in the app.
 *
 * EXTRACTED, NOT COPIED. All of this behaviour was bought by T-47 and it is subtle enough that a second
 * hand-rolled stream would get it wrong: EventSource retries a dropped CONNECTION itself, but goes
 * permanently CLOSED on any non-2xx (a 5xx blip, a redeploy, an expired session) and never recovers —
 * and a socket that stays OPEN but stops carrying data never fires `error` at all, so it looks perfectly
 * healthy while live push is dead. Hence: reconnect with backoff on CLOSED, plus a stall watchdog armed
 * by every inbound byte and rescued by the server's named `ping` frames.
 *
 * `listeners` maps SSE event names to handlers; every one of them rearms the watchdog, because any frame
 * is proof the socket is still carrying data.
 */
function sseConnect(
  path: string,
  onLive: ((live: boolean) => void) | undefined,
  listeners: Record<string, (data: unknown) => void>,
  onReconnect?: () => void,
): () => void {
  const STALL_MS = 60_000; // server pings every 20s; tolerate two dropped ones before reconnecting
  let es: EventSource | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let stall: ReturnType<typeof setTimeout> | undefined;
  let delay = 1000;
  let stopped = false;
  let reconnected = false;
  // *** A CLOSED STREAM MAY BE A SIGNED-OUT ONE (P-282). *** Once the session ends the server closes the
  // stream and the browser's reconnect meets 401 (internal/api/stream.go, P-219), but EventSource hides
  // the status: CLOSED looks exactly like a 5xx blip, so the backoff below would retry every 30s forever
  // on a screen that still looks signed in. Ask /api/session, which answers 401 only when the session is
  // gone: then cancel the reconnect drop() armed and send the app to sign-in through the event a 401
  // from req() fires (http.ts). Any other answer, or none, leaves that reconnect as it was; it is armed
  // BEFORE the probe, so a slow probe never delays it.
  const probeSession = (src: EventSource): void => {
    let probe: Promise<Response>;
    try {
      probe = fetch('/api/session', { credentials: 'include' });
    } catch {
      return;
    }
    probe.then(
      (res) => {
        if (res.status !== 401 || stopped || es !== src) return;
        stopped = true;
        clearTimeout(timer);
        window.dispatchEvent(new Event('batondeck:unauthorized'));
      },
      () => {},
    );
  };
  const connect = (): void => {
    // *** A STREAM MUST NEVER TAKE DOWN THE TREE THAT OPENED IT. *** `NotificationsNavLink` mounts this
    // app-wide, so an EventSource that is absent (jsdom, an old or locked-down browser) or throws on
    // construction would crash the whole nav rather than costing live updates. Degrade to "not live" —
    // every caller already treats that as "fall back to fetching", which is a working app.
    let src: EventSource;
    try {
      if (typeof EventSource === 'undefined') throw new Error('EventSource unavailable');
      src = new EventSource(path, { withCredentials: true });
    } catch {
      onLive?.(false);
      return;
    }
    es = src;
    // Clearing `stall` here is load-bearing: a leaked watchdog would later close the stream that
    // REPLACED this one.
    const drop = (): void => {
      clearTimeout(stall);
      src.close();
      timer = setTimeout(connect, delay);
      // ponytail: KNOWN CEILING — fixed backoff, no jitter. Safe while a tab opens few streams; add
      // jitter if many tabs are ever expected to reconnect off one server restart.
      delay = Math.min(delay * 2, 30_000);
    };
    const alive = (): void => {
      clearTimeout(stall);
      stall = setTimeout(() => {
        if (stopped || es !== src) return; // stale watchdog from a superseded stream
        onLive?.(false);
        drop();
      }, STALL_MS);
    };
    src.addEventListener('open', () => {
      delay = 1000;
      alive();
      onLive?.(true);
      // A fresh stream starts at "now", so anything during the outage would sit unseen until the next
      // event. One catch-up on reconnect closes that gap.
      if (reconnected) onReconnect?.();
      reconnected = true;
    });
    src.addEventListener('ping', alive);
    for (const [name, fn] of Object.entries(listeners)) {
      src.addEventListener(name, (e) => {
        alive();
        try {
          fn(JSON.parse((e as MessageEvent<string>).data));
        } catch {
          /* a malformed frame must never kill the stream */
        }
      });
    }
    src.addEventListener('error', () => {
      if (stopped) return;
      onLive?.(false);
      // CONNECTING means the browser is already retrying — leave it alone, but keep the watchdog armed
      // so a retry that never lands is still rescued. CLOSED is terminal and ours to redo.
      if (src.readyState !== 2) return;
      drop();
      probeSession(src);
    });
  };
  connect();
  return () => {
    stopped = true;
    clearTimeout(timer);
    clearTimeout(stall);
    es?.close();
  };
}

/**
 * ONE EventSource per tab, shared by every component that wants events.
 *
 * WHY A SINGLETON. A board page used to open TWO streams: `Board.tsx` opened a board-scoped one for
 * `updated`, and the app-wide nav badge opened `/api/stream` for presence + notifications. Both carried
 * presence and both ran their own 20s heartbeat, so the second connection was pure duplication — two
 * sockets, two keep-alives, two presence subscriptions on the server for one user looking at one page.
 *
 * Now every caller registers here and shares one socket. The board is part of the URL, so when it
 * changes (or the last board-interested subscriber leaves) the connection is rebuilt against the new
 * query — the only time a reconnect is needed.
 */
type StreamHandlers = {
  onUpdate?: () => void;
  /** Composed board snapshot pushed by the gateway (`event: board`) — board + tasks + feed in one
   *  frame, so the SPA writes it straight to the cache instead of refetching three endpoints. */
  onBoard?: (d: unknown) => void;
  onPresence?: (sessions: AgentSession[]) => void;
  onNotifications?: (d: unknown) => void;
  onLive?: (live: boolean) => void;
  /** The server could not replay what this tab missed (`event: resync`), or the socket was rebuilt
   *  and starts at "now": refetch the board, its tasks and its feed. */
  onResync?: () => void;
};
type Sub = { key: { projectId?: string; boardId?: string }; h: StreamHandlers };

const subs = new Set<Sub>();
let closeCurrent: (() => void) | undefined;
let currentUrl = '';
/**
 * The last `presence` payload the CURRENT socket carried, for a subscriber that joins it later. The
 * server sends presence once on open and then only on a change, and the nav badge opens the socket at
 * app mount, so a screen joining on the same URL (Agents with no board, the full task page) would
 * otherwise not see it until the next change. Cleared whenever the socket is replaced or goes down:
 * a rebuilt or reconnected socket sends its own on open.
 *
 * NOT `notifications`: the server sends those on open and per new row, but not when one is marked
 * read, so the last frame can be older than the reader's own mark-read, and every notifications
 * subscriber reads the list over REST first anyway.
 */
let lastPresence: unknown;

/** The board the shared socket should be scoped to: whichever subscriber named one. */
function desiredUrl(): string {
  for (const s of subs) {
    if (s.key.projectId && s.key.boardId) {
      return `/api/stream?projectId=${encodeURIComponent(s.key.projectId)}&boardId=${encodeURIComponent(s.key.boardId)}`;
    }
  }
  return '/api/stream';
}

function fanout<K extends keyof StreamHandlers>(name: K, apply: (h: StreamHandlers) => void): void {
  for (const s of [...subs]) {
    try {
      apply(s.h);
    } catch {
      /* one bad subscriber must not stop the others */
    }
  }
  void name;
}

function reconcile(): void {
  const want = subs.size === 0 ? '' : desiredUrl();
  if (want === currentUrl) return;
  closeCurrent?.();
  closeCurrent = undefined;
  currentUrl = want;
  lastPresence = undefined;
  if (!want) return;
  closeCurrent = sseConnect(
    want,
    (live) => {
      if (!live) lastPresence = undefined;
      fanout('onLive', (h) => h.onLive?.(live));
    },
    {
      updated: () => fanout('onUpdate', (h) => h.onUpdate?.()),
      board: (d) => fanout('onBoard', (h) => h.onBoard?.(d)),
      presence: (d) => {
        lastPresence = d;
        fanout('onPresence', (h) => h.onPresence?.(presenceSessions(d, new Date().toISOString())));
      },
      // The caller's list_notifications {limit:50}: on open, then per notification row (R2.3).
      notifications: (d) => fanout('onNotifications', (h) => h.onNotifications?.(d)),
      resync: () => fanout('onResync', (h) => h.onResync?.()),
    },
    () => {
      fanout('onUpdate', (h) => h.onUpdate?.());
      fanout('onResync', (h) => h.onResync?.());
    },
  );
}

/** One live CLI process as the device session directory lists it (internal/service DeviceSession). */
interface DeviceSession {
  pid: number;
  role: string;
  identityId: string;
  agentId: string;
  adapter?: string;
  registeredAt: string;
  /** The change-log id of the last doorbell written to the process, 0 for none (wake_sessions.last_cursor). */
  lastDeliveredCursor?: number;
}

/**
 * A presence frame lists this device's live processes for the viewer, the whole list each time. They
 * become AgentSession rows keyed `device:<pid>` (presenceMerge.ts replaces those rows per frame), and
 * `seenAt`, the frame's arrival, is each one's last activity: the directory's word that it is live now.
 */
export function presenceSessions(d: unknown, seenAt: string): AgentSession[] {
  const list = (d as { sessions?: DeviceSession[] } | null)?.sessions ?? [];
  return list.map((e) => ({
    sessionKey: `device:${e.pid}`,
    agentId: e.agentId,
    agentName: e.agentId,
    identityId: e.identityId,
    kind: 'dev',
    ...(e.adapter ? { clientName: e.adapter } : {}),
    firstSeenAt: e.registeredAt,
    lastSeenAt: seenAt,
    sessionCount: 1,
    requestCount: 0,
    toolCounts: {},
    lastDeliveredCursor: e.lastDeliveredCursor ?? 0,
  }));
}

/** Join the shared stream. Returns an unsubscribe that rebuilds the socket only if the scope changed. */
export function joinStream(key: { projectId?: string; boardId?: string }, h: StreamHandlers): () => void {
  const sub: Sub = { key, h };
  subs.add(sub);
  const before = currentUrl;
  reconcile();
  // Joined a socket that was already open: hand over the presence it carried (see `lastPresence`).
  // Next microtask, so the frame lands after the caller's effect has returned; and only while this
  // subscriber is still here and the frame is still the socket's last (a newer one was fanned out).
  if (currentUrl === before && lastPresence !== undefined && h.onPresence) {
    const replay = lastPresence;
    const onPresence = h.onPresence;
    queueMicrotask(() => {
      if (!subs.has(sub) || replay !== lastPresence) return;
      try {
        onPresence(presenceSessions(replay, new Date().toISOString()));
      } catch {
        /* a bad subscriber must not break the join */
      }
    });
  }
  return () => {
    subs.delete(sub);
    reconcile();
  };
}

/**
 * SSE stream of board updates, with reconnect. EventSource retries a dropped *connection* on its own,
 * but goes permanently CLOSED on any non-2xx response — a 5xx blip, a redeploy, a core cold start that
 * fails the /stream pre-flight `get_board`, an expired session. Nothing here listened for that, so one
 * bad reconnect silently killed live push until the user reloaded the page (T-47). Reconnect with
 * backoff, and report liveness so callers can poll faster while the stream is down.
 *
 * The other half: a socket that stays OPEN but stops carrying data (a proxy or Cloud Run dropping a
 * half-open connection without an RST) never fires `error`, so it looked perfectly healthy and live
 * push was dead with only the 30s poll behind it. The gateway already emits a keep-alive every 20s —
 * now as a named `ping` event rather than an SSE comment, which EventSource does not surface — so
 * silence past three of them is a stall: force the same reconnect an error would have.
 */
/**
 * Identity-keyed stream (Tier 2) — presence + notifications for screens with NO board open.
 *
 * `/agents` and `/notifications` are standalone routes, so the board stream is not mounted while a
 * user sits on them; and notifications are caller-scoped and cross-project, so no board is the right
 * key for them even in principle. Shares `sseConnect` with the board stream so the T-47 reconnect and
 * stall-watchdog behaviour cannot drift between the two.
 */
/** Presence + notifications for screens with no board open. Shares the one socket (`joinStream`). */
export function userStream(handlers: { onPresence?: (sessions: AgentSession[]) => void; onNotifications?: (d: unknown) => void; onLive?: (live: boolean) => void }): () => void {
  return joinStream({}, handlers);
}

/**
 * Board updates + presence. Shares the SAME socket as `userStream` — naming a board just scopes the
 * shared connection, it does not open a second one.
 */
export function stream(
  projectId: string,
  boardId: string,
  onUpdate: () => void,
  onLive?: (live: boolean) => void,
  onPresence?: (sessions: AgentSession[]) => void,
  /** The composed snapshot. When present the caller writes it to the cache and the `onUpdate`
   *  refetch becomes redundant for THIS board — see Board.tsx. */
  onBoard?: (d: unknown) => void,
  onResync?: () => void,
): () => void {
  return joinStream({ projectId, boardId }, { onUpdate, onLive, onPresence, ...(onBoard ? { onBoard } : {}), ...(onResync ? { onResync } : {}) });
}
