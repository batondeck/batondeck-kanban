/** Typed client for the BatonDeck gateway. Cookies carry the session; CSRF token on writes. */
import type {
  AccessRequest,
  ActivityEvent,
  AgentSession,
  AssetRef,
  Attachment,
  Audience,
  BillingStatus,
  Board,
  ChronicleBlockInput,
  ChronicleConflicts,
  ChroniclePage,
  ChroniclePageKind,
  ChronicleVersion,
  ChronicleVersionMeta,
  Comment,
  ContextItem,
  CreateIntegrationBody,
  Delivery,
  DependencyEdge,
  FollowUp,
  GitDestination,
  Integration,
  IntegrationPatchBody,
  McpConnection,
  MemoryEntry,
  MySettings,
  Notification,
  OncallSource,
  OpsSummary,
  OrgSurfaceProfile,
  Plugin,
  PluginFile,
  PluginTarget,
  PluginVersion,
  Project,
  RegistryAssetContent,
  RegistryAssetRow,
  RegistryAssetType,
  RegistryConnectionPreset,
  RegistryScope,
  RunView,
  Sprint,
  SprintStatus,
  Task,
} from './types.js';

export class ApiError extends Error {
  code: string;
  status: number;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

let csrfToken: string | null = null;

/**
 * Tasks THIS tab just wrote to. The board flashes a ring on a card whose `version` moves under you
 * (T-51) — but your own edit bumps the version too, so without this the board would flash your own
 * work back at you and the signal would stop meaning "someone else touched this" (CONTRIBUTING §9).
 *
 * Marked HERE rather than at each mutation site because this is the choke point every write already
 * goes through — the drawer's `run()`, the optimistic hooks in mutations.ts, comments, follow-ups and
 * runs alike. A per-caller mark is a list that the next caller forgets to join.
 */
const localEdits = new Map<string, number>();
const LOCAL_EDIT_MS = 5000;
export function markLocalEdit(taskId: string): void {
  localEdits.set(taskId, Date.now());
  // ponytail: KNOWN CEILING — prune on write, so an entry outlives its TTL until the NEXT write. Fine:
  // the map only ever holds tasks touched in the last few seconds. Upgrade path: if a session can go
  // idle holding many stale entries (bulk edit then walk away), sweep on read too.
  for (const [id, t] of localEdits) if (Date.now() - t > LOCAL_EDIT_MS) localEdits.delete(id);
}
export function isLocalEdit(taskId: string): boolean {
  const t = localEdits.get(taskId);
  return t !== undefined && Date.now() - t < LOCAL_EDIT_MS;
}

/** Workspace runtime flags. `ui` and `analytics` hold stored OVERRIDES only — defaults live in the consumer. */
export interface AdminFlags {
  subscriptionsEnabled: boolean;
  ui: Record<string, number | boolean>;
  analytics: Record<string, number>;
}

/**
 * The analytics service's effective config. `perMin`/`maxQueryDays` are what it is enforcing right now
 * (seed, or the admin's override); `retentionDays` is the deploy-time storage ceiling that
 * `maxQueryDays` may narrow within but can never exceed — BigQuery has deleted anything older.
 */
export interface AnalyticsLimit {
  perMin: number;
  enabled: boolean;
  perInstance: boolean;
  maxQueryDays: number;
  retentionDays: number;
  overridden: boolean;
}

/** One CLI token as a read path returns it — no value, by construction (T-457). */
export interface CliTokenRow {
  id: string;
  label: string;
  createdAt: string;
  expiresAt: string;
  lastUsedAt: string | null;
  revokedAt: string | null;
  status: 'live' | 'expired' | 'revoked';
}

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  // Any non-GET aimed at a specific task is this tab editing it. (`…/boards/<id>/tasks` — a create —
  // has nothing after `/tasks`, so it doesn't match: a brand-new card is seeded, never flashed.)
  if (method !== 'GET') {
    const m = /\/tasks\/([^/?]+)/.exec(path);
    if (m?.[1]) markLocalEdit(decodeURIComponent(m[1]));
  }
  const headers: Record<string, string> = {};
  if (body !== undefined) headers['content-type'] = 'application/json';
  if (method !== 'GET' && csrfToken) headers['x-csrf-token'] = csrfToken;
  const res = await fetch(path, {
    method,
    headers,
    credentials: 'include',
    ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
  });
  if (res.status === 204) return undefined as T;
  if (!res.ok) {
    // A 401 mid-session means the cookie expired/was revoked — tell the auth layer so the app returns
    // to the sign-in screen instead of every poll failing silently forever. /api/session is exempt:
    // its 401 is the normal signed-out probe on boot, already handled by AuthProvider.refresh().
    if (res.status === 401 && path !== '/api/session') window.dispatchEvent(new Event('batondeck:unauthorized'));
    const j = (await res.json().catch(() => ({}))) as { code?: string; message?: string };
    throw new ApiError(res.status, j.code ?? 'INTERNAL', j.message ?? res.statusText);
  }
  return (await res.json()) as T;
}

const P = (projectId: string): string => `/api/projects/${projectId}`;
/** On-call routes are project-scoped but NOT under `/api/projects` — they carry their own prefix so
 * the permission-coverage exemption can name one subtree (`/api/oncall`) rather than a fragment of
 * the projects tree. */
const ON = (projectId: string): string => `/api/oncall/${projectId}`;
const ST = '/api/studio';
const SP = (pluginId: string): string => `${ST}/plugins/${encodeURIComponent(pluginId)}`;

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
};
type Sub = { key: { projectId?: string; boardId?: string }; h: StreamHandlers };

const subs = new Set<Sub>();
let closeCurrent: (() => void) | undefined;
let currentUrl = '';

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
  if (!want) return;
  closeCurrent = sseConnect(
    want,
    (live) => fanout('onLive', (h) => h.onLive?.(live)),
    {
      updated: () => fanout('onUpdate', (h) => h.onUpdate?.()),
      board: (d) => fanout('onBoard', (h) => h.onBoard?.(d)),
      presence: (d) => fanout('onPresence', (h) => h.onPresence?.((d as { sessions: AgentSession[] }).sessions)),
      notifications: (d) => fanout('onNotifications', (h) => h.onNotifications?.(d)),
    },
    () => fanout('onUpdate', (h) => h.onUpdate?.()),
  );
}

/** Join the shared stream. Returns an unsubscribe that rebuilds the socket only if the scope changed. */
export function joinStream(key: { projectId?: string; boardId?: string }, h: StreamHandlers): () => void {
  const sub: Sub = { key, h };
  subs.add(sub);
  reconcile();
  return () => {
    subs.delete(sub);
    reconcile();
  };
}

export const api = {
  /**
   * Sign out — and then LEAVE, because the redirect is the sign-out (T-452).
   *
   * The BFF clears our cookies and answers with WorkOS's logout URL. Hitting that URL is the only
   * thing that ends the AuthKit session, and it has to be a top-level NAVIGATION: this call is a
   * `fetch`, so a 302 would be followed by the fetch and the browser would never leave our origin —
   * which is exactly the shape of the original defect, a sign-out that returns 200 and leaves the
   * identity reconstitutable on the next `/login`.
   *
   * `null` (no IdP session — `/auth/demo`, or a session minted before `cws` existed) is not a
   * failure: the cookies are already gone, so the caller's own state update signs the user out
   * locally, exactly as it did before this change.
   */
  async logout(): Promise<void> {
    const { logoutUrl } = await req<{ ok: boolean; logoutUrl: string | null }>('POST', '/auth/logout');
    csrfToken = null;
    if (logoutUrl) {
      window.location.assign(logoutUrl);
      // NEVER RESOLVE past the assign: the navigation IS the sign-out (T-452), and a resolved promise
      // hands control back to the caller, which drops identity, renders SignIn, and SignIn's
      // auto-redirect REPLACES this still-in-flight navigation. Measured live 2026-08-20 (T-490): the
      // IdP logout died ERR_ABORTED, the surviving AuthKit session silently re-authenticated, and
      // Sign out landed the user back on /projects as the same account. If the navigation itself
      // fails, the next API call's 401 fires `batondeck:unauthorized` and recovery is the ordinary
      // signed-out path — strictly better than resolving into the race.
      await new Promise<never>(() => {});
    }
  },
  // `tenantName` (T-453) is the organization's DISPLAY name, served from the session cookie jar so the
  // identity menu can render it with no request of its own. OPTIONAL on the wire and `null` for any
  // session minted before `cn` shipped — the renderer falls back to `tenantId`, never to blank.
  async session(): Promise<{ identity: string; csrfToken: string | null; isAdmin: boolean; permissions?: string[]; subscriptionsEnabled: boolean; uiSettings: Record<string, number | boolean>; tenantId?: string | null; tenantName?: string | null; pullBase?: string | null }> {
    // `permissions` is OPTIONAL on the wire and stays optional (T-378): it is absent until the BFF
    // lands it, and optional-here forces every reader through `can()`'s `?? []` rather than letting a
    // non-null assertion invent an empty array somewhere less careful.
    const s = await req<{ identity: string; csrfToken: string | null; isAdmin?: boolean; permissions?: string[]; subscriptionsEnabled?: boolean; uiSettings?: Record<string, number | boolean>; tenantId?: string | null; tenantName?: string | null; pullBase?: string | null }>('GET', '/api/session');
    csrfToken = s.csrfToken;
    return { ...s, isAdmin: s.isAdmin ?? false, subscriptionsEnabled: s.subscriptionsEnabled ?? false, uiSettings: s.uiSettings ?? {} };
  },

  /**
   * TENANT SELECTION (T-392). Hit ONLY when the switcher opens — it costs a live WorkOS read, which is
   * why it is not folded into `/api/session` (every page load).
   *
   * `degraded: true` means the session predates the `cw` cookie; the caller must render "no switcher"
   * rather than "no organizations", because the two are not the same claim.
   */
  listTenants: () =>
    req<{
      current: string | null;
      // `name` is the organization's HUMAN name and may be null — render the id then, never blank.
      tenants: { tenantId: string; name: string | null; role: string | null }[];
      degraded: boolean;
    }>('GET', '/api/tenants'),
  /** Switch the acting organization. The server validates membership; a refusal here is authoritative. */
  selectTenant: (tenantId: string) => req<{ tenantId: string }>('POST', '/api/tenants/select', { tenantId }),
  /**
   * Invite a teammate into the CURRENT organization (T-389). The organization is NOT a parameter — the
   * server takes it from the session and re-validates it, so this cannot be pointed at someone else's
   * tenant. A 403 here is authoritative: the SPA's own check is a courtesy, not a control.
   */
  inviteTeammate: (email: string) => req<{ ok: true }>('POST', '/api/tenants/invitations', { email }),
  /**
   * The CURRENT organization's outstanding invitations (T-396). The organization is NOT a parameter —
   * it comes from the session — so this cannot be pointed at another tenant's invitees. Only `pending`
   * rows come back, which is how the UI answers "did they accept?" without a second endpoint.
   */
  listInvitations: () =>
    req<{ invitations: { id: string; email: string; state: 'pending'; createdAt: string; expiresAt: string }[] }>(
      'GET',
      '/api/tenants/invitations',
    ),
  /** Resend / revoke one invitation. 404 means it is not a pending invitation of THIS organization. */
  resendInvitation: (id: string) =>
    req<{ ok: true }>('POST', `/api/tenants/invitations/${encodeURIComponent(id)}/resend`),
  revokeInvitation: (id: string) =>
    req<{ ok: true }>('POST', `/api/tenants/invitations/${encodeURIComponent(id)}/revoke`),

  /**
   * SELF-SERVE ONBOARDING (T-387). Reached with NO SESSION — the caller holds only the short-lived
   * `co` ticket the sign-in callback minted, because T-270 refuses to mint a session for a user with
   * no organization and this flow does not re-open that.
   *
   * `csrfToken` is captured into the module-level `csrfToken` exactly as `session()` does, which is
   * what lets the POST below go through the ordinary double-submit path instead of an exemption.
   */
  async onboarding(): Promise<{ email: string; csrfToken: string | null }> {
    const s = await req<{ email: string; csrfToken: string | null }>('GET', '/api/onboarding');
    csrfToken = s.csrfToken;
    return s;
  },
  /** Create the workspace and, on success, the real session. The server sets the cookies. */
  createWorkspace: (name: string) => req<{ tenantId: string }>('POST', '/api/onboarding/organization', { name }),

  // projects
  // `members` / `memberDisplay` / `color` are what the Projects CARD renders — carried here so the grid
  // does not fan out one `getProject` per project. NULL for a workspace admin listing projects they are
  // not a member of (`all:true`), which is an authorization gate, not an oversight: treat null as "no
  // detail available", never as "this project has no members".
  listProjects: () =>
    req<{
      projects: Array<{
        projectId: string;
        name: string;
        role: string;
        members?: Record<string, string> | null;
        memberDisplay?: Record<string, string> | null;
        color?: string | null;
      }>;
    }>('GET', '/api/projects'),
  createProject: (name: string) => req<{ projectId: string }>('POST', '/api/projects', { name }),
  // `memberDisplay` is derived server-side per read (T-319), so it is a SIBLING of `project`, never a
  // field on it. A member absent from the map has no better label than its id — render the id.
  getProject: (projectId: string) =>
    req<{ project: Project; memberDisplay?: Record<string, string> }>('GET', P(projectId)),
  addMember: (projectId: string, identityId: string, role: string) =>
    req<{ members: Record<string, string> }>('POST', `${P(projectId)}/members`, { identityId, role }),
  removeMember: (projectId: string, identityId: string) =>
    req<{ ok: true }>('DELETE', `${P(projectId)}/members/${encodeURIComponent(identityId)}`),
  /** T-201: the signed-in user's OWN preferences. No identity parameter exists — by design. */
  getMySettings: () => req<MySettings>('GET', '/api/me/settings'),
  setMySettings: (patch: { maxTicketTokens?: number | null; tokenPolicy?: string | null; starredAssets?: string[] }) =>
    req<MySettings>('PATCH', '/api/me/settings', patch),

  // CLI tokens (T-457). There is deliberately NO `getCliToken` — the value is returned by the mint and
  // nowhere else, so a compromised session cannot walk the list to harvest secrets.
  listCliTokens: () => req<{ tokens: CliTokenRow[] }>('GET', '/api/me/cli-tokens'),
  createCliToken: (body: { label: string; ttlSeconds?: number }) =>
    req<CliTokenRow & { token: string; shownOnce: true }>('POST', '/api/me/cli-tokens', body),
  getMarketplaceKey: () =>
    req<{ key: string | null; createdAt: string | null; rotated: boolean; lastUsedAt: string | null; useCount: number; recentUses: Array<{ ts: string; kind: 'catalog' | 'archive'; pluginName?: string; semver?: string; ip?: string; userAgent?: string }> }>(
      'GET',
      '/api/me/marketplace-key',
    ),
  createMarketplaceKey: () =>
    req<{ key: string; rotated: boolean }>('POST', '/api/me/marketplace-key', {}),
  rotateMarketplaceKey: () =>
    req<{ key: string; rotated: boolean }>('POST', '/api/me/marketplace-key/rotate', {}),
  revokeCliToken: (id: string) =>
    req<{ revoked: boolean; token: CliTokenRow | null }>(
      'POST',
      `/api/me/cli-tokens/${encodeURIComponent(id)}/revoke`,
    ),
  updateSettings: (projectId: string, patch: Record<string, unknown>) =>
    req<{ settings: Project['settings'] }>('PATCH', `${P(projectId)}/settings`, patch),

  // chronicle (CHS-4) — the versioned, editable decision record
  /**
   * PAGED. `nextCursor` present means MORE RECORDS EXIST; absent means the list is complete.
   *
   * The distinction is the whole point: Chronicle's pitch is checkable evidence, so an index that
   * silently stops at one page is a FALSE ABSENCE claim — a reader concludes the decision was never
   * recorded. The caller must surface "partial" rather than treat a page as the whole.
   */
  listChroniclePages: (projectId: string, cursor?: string) =>
    req<{ pages: ChroniclePage[]; nextCursor?: string }>(
      'GET',
      `${P(projectId)}/chronicle${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`,
    ),
  getChroniclePage: (projectId: string, pageId: string) =>
    req<{
      page: ChroniclePage | null;
      head: ChronicleVersion | null;
      drifted: string[];
      /** The persisted sweep-conflict state with THEIRS attached (T-298); null when none stands. */
      conflicts?: ChronicleConflicts | null;
    }>(
      'GET',
      `${P(projectId)}/chronicle/${pageId}`,
    ),
  /**
   * PAGED like the index, for the same reason (see `listChroniclePages`): a history that silently
   * ends at 50 claims "there is no earlier version" — false, and believed. `nextCursor` present
   * means OLDER VERSIONS EXIST; absent means the history is complete.
   */
  listChronicleVersions: (projectId: string, pageId: string, cursor?: string) =>
    req<{ versions: ChronicleVersionMeta[]; nextCursor?: string }>(
      'GET',
      `${P(projectId)}/chronicle/${pageId}/versions${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ''}`,
    ),
  getChronicleVersion: (projectId: string, pageId: string, versionId: string) =>
    req<{ version: ChronicleVersion | null }>(
      'GET',
      `${P(projectId)}/chronicle/${pageId}/versions/${versionId}`,
    ),
  editChroniclePage: (
    projectId: string,
    pageId: string,
    expectedParentVersionId: string,
    blocks: ChronicleBlockInput[],
  ) =>
    req<{ versionId: string; drifted: string[] }>('POST', `${P(projectId)}/chronicle/${pageId}/edit`, {
      expectedParentVersionId,
      blocks,
    }),
  revertChroniclePage: (projectId: string, pageId: string, versionId: string) =>
    req<{ versionId: string }>('POST', `${P(projectId)}/chronicle/${pageId}/revert`, { versionId }),
  /**
   * Start a record by hand. `origin` is deliberately absent from the block shape here — the server
   * discards it anyway (only a sweep may assert that text was derived), so offering the field would
   * be inviting a caller to believe something it does not do.
   */
  createChroniclePage: (
    projectId: string,
    input: {
      kind: ChroniclePageKind;
      slug: string;
      title: string;
      blocks: Array<{ id: string; text: string }>;
      /** Minted ONCE per form instance, so a double-submit or retried POST replays the create
       *  instead of bouncing off the taken-slug refusal. */
      idempotencyKey?: string;
    },
  ) => req<{ pageId: string; versionId: string }>('POST', `${P(projectId)}/chronicle`, input),
  /**
   * IRREVERSIBLE — the page, every version and every stored body. Not a new version like everything
   * else. `expectedHeadVersionId` pins the head the confirmation displayed: a version landed
   * mid-confirm turns the destroy into a STALE instead of taking work the confirmer never saw.
   */
  deleteChroniclePage: (projectId: string, pageId: string, expectedHeadVersionId?: string) =>
    req<{ ok: true }>(
      'DELETE',
      `${P(projectId)}/chronicle/${pageId}${
        expectedHeadVersionId ? `?expectedHeadVersionId=${encodeURIComponent(expectedHeadVersionId)}` : ''
      }`,
    ),

  // boards
  listBoards: (projectId: string) => req<{ boards: Board[] }>('GET', `${P(projectId)}/boards`),
  createBoard: (projectId: string, name: string) => req<{ board: Board }>('POST', `${P(projectId)}/boards`, { name }),
  getBoard: (projectId: string, boardId: string) =>
    req<{ board: Board; taskCounts: Record<string, number> }>('GET', `${P(projectId)}/boards/${boardId}`),
  addColumn: (projectId: string, boardId: string, name: string, status: string, wipLimit?: number | null) =>
    req<{ column: unknown }>('POST', `${P(projectId)}/boards/${boardId}/columns`, { name, status, wipLimit }),
  updateColumn: (projectId: string, boardId: string, columnId: string, patch: Record<string, unknown>) =>
    req<{ board: Board }>('PATCH', `${P(projectId)}/boards/${boardId}/columns/${columnId}`, patch),
  removeColumn: (projectId: string, boardId: string, columnId: string) =>
    req<{ board: Board }>('DELETE', `${P(projectId)}/boards/${boardId}/columns/${columnId}`),

  // sprints (T-257). DEFINITION is agent-driven — create_sprint and set_sprint stay MCP-only, because
  // deciding what an objective contains is the planning act, not a click. What the SPA owns is the
  // LIFECYCLE: approve (the proposal task, through the ordinary REVIEW→DONE control) and then the
  // status changes below. T-330: activation had no button anywhere, so the ceremony was half
  // built — a human could approve and then had no way to start the thing they had approved.
  listSprints: (projectId: string, boardId: string) =>
    req<{ sprints: Sprint[] }>('GET', `${P(projectId)}/sprints?boardId=${encodeURIComponent(boardId)}`),
  /**
   * PROPOSED→ACTIVE|ABANDONED, ACTIVE→CLOSED|ABANDONED. `version` is the sprint's, not a task's.
   *
   * The core owns every rule this can hit — admin role, the bound-and-DONE proposal task, the
   * self-approval rung, the fixed transition table — and returns `warnings` rather than throwing on
   * the soft ones (closing with non-terminal members; approving your own proposal under 'warn').
   * The caller MUST surface those: a warning the UI swallows is the same as no warning.
   */
  updateSprint: (
    projectId: string,
    sprintId: string,
    version: number,
    patch: { status?: SprintStatus; name?: string; goal?: string; acceptance?: string },
  ) =>
    req<{ sprint: Sprint; warnings?: string[] }>('POST', `${P(projectId)}/sprints/${sprintId}`, { version, patch }),

  // tasks
  listTasks: (projectId: string, boardId: string, q: Record<string, string> = {}) =>
    req<{ tasks: Task[]; nextCursor?: string }>(
      'GET',
      `${P(projectId)}/boards/${boardId}/tasks?${new URLSearchParams(q).toString()}`,
    ),
  createTask: (projectId: string, boardId: string, body: Record<string, unknown>) =>
    req<{ task: Task }>('POST', `${P(projectId)}/boards/${boardId}/tasks`, body),
  getTask: (projectId: string, taskId: string) => req<{ task: Task }>('GET', `${P(projectId)}/tasks/${taskId}`),
  updateTask: (projectId: string, taskId: string, version: number, patch: Record<string, unknown>) =>
    req<{ task: Task }>('PATCH', `${P(projectId)}/tasks/${taskId}`, { version, patch }),
  moveTask: (projectId: string, taskId: string, version: number, args: Record<string, unknown>) =>
    req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/move`, { version, ...args }),
  claimTask: (projectId: string, taskId: string) =>
    req<{ leaseId: string; task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/claim`, {}),
  releaseTask: (projectId: string, taskId: string, leaseId: string) =>
    req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/release`, { leaseId }),
  completeTask: (projectId: string, taskId: string, leaseId: string) =>
    req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/complete`, { leaseId }),
  blockTask: (projectId: string, taskId: string, leaseId: string, reason: string) =>
    req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/block`, { leaseId, reason }),
  handoffTask: (projectId: string, taskId: string, leaseId: string, toAgent: string, memoryNote: string) =>
    req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/handoff`, { leaseId, toAgent, memoryNote }),
  setSummary: (projectId: string, taskId: string, version: number, summary: string) =>
    req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/summary`, { version, summary }),
  taskContext: (projectId: string, taskId: string) =>
    req<{ summary: string; fields: Record<string, unknown>; items: ContextItem[]; deps: { blockedBy: string[]; blocks: string[]; edges: DependencyEdge[] }; attachments?: Attachment[]; memory: MemoryEntry[]; openFollowUps?: FollowUp[]; openFollowUpCount?: number; nextCursor?: string }>(
      'GET',
      `${P(projectId)}/tasks/${taskId}/context`,
    ),
  addContextItem: (projectId: string, taskId: string, kind: string, body: string) =>
    req<{ item: ContextItem }>('POST', `${P(projectId)}/tasks/${taskId}/context-items`, { kind, body }),
  listComments: (projectId: string, taskId: string) =>
    req<{ comments: Comment[]; nextCursor?: string }>('GET', `${P(projectId)}/tasks/${taskId}/comments`),
  addComment: (projectId: string, taskId: string, body: string, parentCommentId?: string) =>
    req<{ comment: Comment }>('POST', `${P(projectId)}/tasks/${taskId}/comments`, {
      body,
      ...(parentCommentId ? { parentCommentId } : {}),
    }),

  // follow-ups (directed directives to the task's current holder; reopen-capable)
  listFollowUps: (projectId: string, taskId: string, openOnly = false) =>
    req<{ followUps: FollowUp[]; nextCursor?: string }>(
      'GET',
      `${P(projectId)}/tasks/${taskId}/follow-ups${openOnly ? '?openOnly=true' : ''}`,
    ),
  addFollowUp: (projectId: string, taskId: string, body: string, reopen: boolean) =>
    req<{ followUp: FollowUp; task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/follow-ups`, { body, reopen }),
  ackFollowUp: (projectId: string, taskId: string, followUpId: string) =>
    req<{ followUp: FollowUp }>('POST', `${P(projectId)}/tasks/${taskId}/follow-ups/${followUpId}/ack`),

  // runs (race a task across N agents; pick the winner)
  listRuns: (projectId: string, taskId: string) =>
    req<{ runs: RunView[] }>('GET', `${P(projectId)}/tasks/${taskId}/runs`),
  startRuns: (projectId: string, taskId: string, version: number, opts: { count?: number; agents?: string[]; brief?: string }) =>
    req<{ task: Task; runs: Task[] }>('POST', `${P(projectId)}/tasks/${taskId}/runs`, { version, ...opts }),
  openRun: (projectId: string, taskId: string, opts: { agent?: string; brief?: string }) =>
    req<{ run: Task }>('POST', `${P(projectId)}/tasks/${taskId}/runs/open`, opts),
  pickRun: (projectId: string, taskId: string, runTaskId: string, version: number) =>
    req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/runs/pick`, { runTaskId, version }),
  cancelRuns: (projectId: string, taskId: string, version: number) =>
    req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/runs/cancel`, { version }),

  // notifications (caller-scoped, cross-project)
  getNotifications: () => req<{ notifications: Notification[]; unread: number; nextCursor?: string }>('GET', '/api/notifications'),
  markNotificationsRead: (opts: { ids?: string[]; all?: boolean }) =>
    req<{ updated: number }>('POST', '/api/notifications/read', opts),

  // subtasks / memory / attachments / deps
  listSubtasks: (projectId: string, taskId: string) => req<{ tasks: Task[] }>('GET', `${P(projectId)}/tasks/${taskId}/subtasks`),
  addSubtask: (projectId: string, taskId: string, title: string) =>
    req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/subtasks`, { title }),
  readMemory: (projectId: string, taskId: string) => req<{ entries: MemoryEntry[] }>('GET', `${P(projectId)}/tasks/${taskId}/memory`),
  writeMemory: (projectId: string, taskId: string, scope: string, key: string, value: unknown) =>
    req<{ entryId: string }>('POST', `${P(projectId)}/tasks/${taskId}/memory`, { scope, key, value }),
  listAttachments: (projectId: string, taskId: string) =>
    req<{ attachments: Attachment[] }>('GET', `${P(projectId)}/tasks/${taskId}/attachments`),
  createAttachment: (projectId: string, taskId: string, body: { fileName: string; mimeType: string; kind: string; bytes: number }) =>
    req<{ attachmentId: string; uploadUrl: string; expiresAt: string }>('POST', `${P(projectId)}/tasks/${taskId}/attachments`, body),
  removeDependency: (projectId: string, edgeId: string) =>
    req<{ ok: true }>('DELETE', `${P(projectId)}/dependencies/${edgeId}`),
  addDependency: (projectId: string, fromTaskId: string, toTaskId: string, type = 'blocks') => {
    // Not a /tasks/<id> path, but it bumps BOTH endpoints' versions — mark them so neither flashes.
    markLocalEdit(fromTaskId);
    markLocalEdit(toTaskId);
    return req<{ edge: unknown }>('POST', `${P(projectId)}/dependencies`, { fromTaskId, toTaskId, type });
  },

  // feed + search
  feed: (projectId: string, boardId: string) => req<{ events: ActivityEvent[] }>('GET', `${P(projectId)}/boards/${boardId}/feed`),
  overview: (projectId: string) =>
    req<{ project: Project; memberDisplay?: Record<string, string>; boards: Board[]; perBoard: Array<{ board: Board; tasks: Task[]; feed: { events: ActivityEvent[] } }> }>(
      'GET',
      `${P(projectId)}/overview`,
    ),
  adminAccessRequests: () => req<{ requests: AccessRequest[] }>('GET', '/api/admin/access-requests'),
  adminDecideAccess: (email: string, decision: 'approved' | 'denied') =>
    req<{ request: AccessRequest }>('POST', '/api/admin/access-requests/decide', { email, decision }),
  /** `ownerDisplay`: owner's name, else email, derived at read; null = core had no better label than the id. */
  adminProjects: () =>
    req<{
      projects: Array<{ projectId: string; name: string; role: string; owner: string | null; ownerDisplay: string | null }>;
    }>('GET', '/api/admin/projects'),
  adminDeleteProject: (projectId: string) =>
    req<{ ok: true }>('DELETE', `/api/admin/projects/${encodeURIComponent(projectId)}`),
  adminSessions: () => req<{ sessions: AgentSession[] }>('GET', '/api/admin/sessions'),
  // `wakeRevokeFailed` / `wakeRevoked` are the core's ONLY signal that a revoked agent's Pub/Sub
  // subscription survived the delete (there is no retry and no reconciler). The gateway forwards the
  // tool result verbatim; typing them away here is what made the row grey out as though it had worked
  // (T-83/D6) — Admin.tsx renders both.
  adminPruneSessions: (olderThanMinutes?: number) =>
    req<{ pruned: number; wakeRevokeFailed?: number }>('POST', '/api/admin/sessions/prune', olderThanMinutes ? { olderThanMinutes } : {}),
  adminDisconnectAgent: (identityId: string, agentId: string) =>
    req<{ disconnected: boolean; wakeRevoked?: boolean }>('POST', '/api/admin/sessions/disconnect', { identityId, agentId }),
  /**
   * Disconnect one of YOUR OWN sessions. Distinct endpoint from `adminDisconnectAgent`, and the core
   * enforces owner-or-admin on it — so a member can end their own agent without the admin console.
   *
   * The gateway route has existed since the sessions work; the SPA only ever wired the ADMIN one, which
   * is why "Manage sessions" pointed at /admin and non-admins had no way to stop a session at all.
   */
  disconnectOwnSession: (identityId: string, agentId: string) =>
    req<{ disconnected: boolean; wakeRevoked?: boolean }>('POST', '/api/agents/sessions/disconnect', { identityId, agentId }),
  // --- runtime feature flags (admin-gated): subscriptions master switch, UI tunables, analytics limits ---
  adminFeatureFlags: () => req<AdminFlags>('GET', '/api/admin/feature-flags'),
  adminSetFeatureFlags: (patch: {
    subscriptionsEnabled?: boolean;
    ui?: Record<string, number | boolean | null>;
    // `null` unsets, reverting that knob to the analytics service's deploy-time seed.
    analytics?: Record<string, number | null>;
  }) => req<AdminFlags>('POST', '/api/admin/feature-flags', patch),
  /** The analytics service's EFFECTIVE config — what is enforced now, seeds included (see `/limits`). */
  adminAnalyticsLimit: () => req<AnalyticsLimit>('GET', '/api/admin/analytics-limit'),
  // --- billing / subscriptions (admin-gated) ---
  billingStatus: () => req<BillingStatus>('GET', '/api/billing/status'),
  billingCheckout: (planId: string) => req<{ url: string }>('POST', '/api/billing/checkout', { planId }),
  billingPortal: () => req<{ url: string }>('POST', '/api/billing/portal'),
  // all=true (workspace-wide, core-enforced admin) lets admin viewers resolve client logos for every agent.
  agentSessions: (limit?: number, all?: boolean) =>
    req<{ sessions: AgentSession[] }>(
      'GET',
      `/api/agents/sessions?${new URLSearchParams({ ...(limit ? { limit: String(limit) } : {}), ...(all ? { all: 'true' } : {}) }).toString()}`,
    ),
  analyticsOperations: (projectId: string, days = 7) =>
    req<OpsSummary>('GET', `/api/analytics/operations?projectId=${encodeURIComponent(projectId)}&days=${days}`),
  // --- on-call integrations (spec §9/§10) ---
  /** The source catalog, SERVED rather than mirrored — see the route's docblock in the gateway. */
  oncallSources: () => req<{ sources: OncallSource[] }>('GET', '/api/oncall/sources'),

  // ---- plugin studio (S3) ----
  // Every one of these is org-scoped by the caller's TENANT, so none takes a projectId. The core
  // refuses a caller without the org-admin permission, which is what actually gates the screens —
  // `ROUTE_PERMISSIONS['/studio']` only hides the door.
  studioPlugins: () => req<{ plugins: Plugin[] }>('GET', `${ST}/plugins`),
  studioDeletePlugin: (pluginId: string, confirmName: string) =>
    req<{ deleted: { pluginId: string; name: string; branchesDeleted: string[]; tagsDeleted: number; objectsDeleted: number } }>(
      'DELETE',
      SP(pluginId),
      { confirmName },
    ),
  studioCreatePlugin: (body: {
    name: string;
    description?: string;
    audiences?: string[];
    targets?: PluginTarget[];
    idempotencyKey?: string;
  }) => req<{ plugin: Plugin }>('POST', `${ST}/plugins`, body),
  /** Read ONE draft file's text — what lets an editor open a file instead of retyping it. */
  studioGetFile: (pluginId: string, path: string, versionId?: string) =>
    req<{ file: { path: string; size: number; contentRef: string }; content: string }>(
      'GET',
      `${SP(pluginId)}/file?path=${encodeURIComponent(path)}${versionId !== undefined ? `&versionId=${encodeURIComponent(versionId)}` : ''}`,
    ),
  /** Remove one file from the open draft — entry and bytes. */
  studioRemoveFile: (pluginId: string, path: string) =>
    req<{ removed: { path: string }; draft: PluginVersion; bytesDeleted: boolean }>(
      'DELETE',
      `${SP(pluginId)}/file`,
      { path },
    ),
  studioVersions: (pluginId: string) =>
    req<{ versions: PluginVersion[] }>('GET', `${SP(pluginId)}/versions`),
  studioFinalizeVersion: (pluginId: string, semver: string) =>
    req<{ version: PluginVersion }>('POST', `${SP(pluginId)}/versions`, { semver }),
  studioVersion: (pluginId: string, versionId: string) =>
    req<{ version: PluginVersion; paths: string[] }>('GET', `${SP(pluginId)}/versions/${encodeURIComponent(versionId)}`),
  studioDiscardVersion: (pluginId: string, versionId: string) =>
    req<{ discarded: { versionId: string; state: string; objectsDeleted: number } }>(
      'DELETE',
      `${SP(pluginId)}/versions/${encodeURIComponent(versionId)}`,
    ),
  studioApproveVersion: (pluginId: string, versionId: string) =>
    req<{ version: PluginVersion }>(
      'POST',
      `${SP(pluginId)}/versions/${encodeURIComponent(versionId)}/approve`,
    ),
  studioYankVersion: (pluginId: string, versionId: string, reason: string) =>
    req<{ version: PluginVersion }>(
      'POST',
      `${SP(pluginId)}/versions/${encodeURIComponent(versionId)}/yank`,
      { reason },
    ),
  studioPublishVersion: (pluginId: string, versionId: string, destinationId: string) =>
    req<{ version: PluginVersion }>(
      'POST',
      `${SP(pluginId)}/versions/${encodeURIComponent(versionId)}/publish`,
      { destinationId },
    ),
  // Returns `upload` ONLY when the deployment has an object store. Absent means the bytes cannot be
  // sent and the version cannot be completed — say so rather than dereferencing it.
  studioAddFile: (pluginId: string, body: { path: string; size: number }) =>
    req<{ file: PluginFile; draft: PluginVersion; upload?: { url: string; expiresAt: string } }>(
      'POST',
      `${SP(pluginId)}/files`,
      body,
    ),
  studioSelectAsset: (
    pluginId: string,
    body: { scope: RegistryScope; type: RegistryAssetType; assetId: string; semver?: string; deselect?: boolean },
  ) => req<{ draft: { id: string; state: string; assetRefs: AssetRef[] } }>('POST', `${SP(pluginId)}/assets`, body),
  studioSetPluginMcp: (pluginId: string, names: string[]) =>
    req<{ draft: PluginVersion }>('PUT', `${SP(pluginId)}/mcp`, { names }),
  studioDestinations: () =>
    req<{ destinations: GitDestination[]; pull?: { base: string } }>('GET', `${ST}/destinations`),
  studioAddDestination: (body: {
    tier: 'customer-remote' | 'managed-branch';
    branch: string;
    remoteUrl?: string;
    credential?: string;
    audiences?: string[];
  }) => req<{ destination: GitDestination }>('POST', `${ST}/destinations`, body),
  studioConnections: () => req<{ connections: McpConnection[] }>('GET', `${ST}/connections`),
  studioAddConnection: (body: {
    name: string;
    url?: string;
    mode?: 'direct' | 'proxied';
    authMode?: 'none' | 'bearer' | 'header';
    authHeader?: string;
    secret?: string;
    /** A custom server block: name + JSON is the whole contract. */
    configJson?: string;
  }) => req<{ connection: McpConnection }>('POST', `${ST}/connections`, body),
  studioRotateConnectionSecret: (connectionId: string, body: { secret: string; revokePrevious?: boolean }) =>
    req<{ connection: McpConnection }>(
      'POST',
      `${ST}/connections/${encodeURIComponent(connectionId)}/rotate-secret`,
      body,
    ),
  studioAudiences: () => req<{ audiences: Audience[] }>('GET', `${ST}/audiences`),
  studioCreateAudience: (body: { name: string; members?: string[]; groupBindings?: string[] }) =>
    req<{ audience: Audience }>('POST', `${ST}/audiences`, body),
  studioUpdateAudienceMembership: (
    audienceId: string,
    body: { addMembers?: string[]; removeMembers?: string[]; addGroups?: string[]; removeGroups?: string[] },
  ) =>
    req<{ audience: Audience }>(
      'POST',
      `${ST}/audiences/${encodeURIComponent(audienceId)}/membership`,
      body,
    ),
  studioSurfaceProfile: () => req<{ profile: OrgSurfaceProfile }>('GET', `${ST}/surface-profile`),
  studioSetSurfaceProfile: (mode: 'full' | 'router') =>
    req<{ profile: OrgSurfaceProfile }>('POST', `${ST}/surface-profile`, { mode }),
  studioKnowledge: (limit?: number) =>
    req<{ knowledgeProjectId: string; pages: { id: string; title: string; kind: string }[] }>(
      'GET',
      limit === undefined ? `${ST}/knowledge` : `${ST}/knowledge?limit=${String(limit)}`,
    ),
  studioSetKnowledgeSource: (knowledgeProjectId: string | null) =>
    req<{ knowledgeProjectId: string | null }>('POST', `${ST}/knowledge`, { knowledgeProjectId }),
  studioRegistryAssets: (q?: { type?: RegistryAssetType; scope?: RegistryScope }) => {
    const p = new URLSearchParams();
    if (q?.type) p.set('type', q.type);
    if (q?.scope) p.set('scope', q.scope);
    const qs = p.toString();
    return req<{ assets: RegistryAssetRow[] }>('GET', qs ? `${ST}/registry/assets?${qs}` : `${ST}/registry/assets`);
  },
  studioCreateRegistryAsset: (body: {
    assetId: string;
    type: RegistryAssetType;
    name: string;
    description: string;
    idempotencyKey?: string;
  }) => req<{ asset: RegistryAssetRow }>('POST', `${ST}/registry/assets`, body),
  studioRegistryAsset: (assetId: string, q: { scope: RegistryScope; type: RegistryAssetType; semver?: string }) => {
    const p = new URLSearchParams({ scope: q.scope, type: q.type });
    if (q.semver) p.set('semver', q.semver);
    // FLAT, matching the tool's own outputSchema — this was typed `{ asset: … }` from day one and
    // nothing noticed because no screen called it; the FIRST live click on Details crashed on the
    // phantom wrapper. Types are erased at runtime: only a caller can catch this class.
    return req<{
      scope: RegistryScope;
      type: RegistryAssetType;
      assetId: string;
      semver: string;
      files: { path: string; size: number; content: string }[];
    }>(
      'GET',
      `${ST}/registry/assets/${encodeURIComponent(assetId)}?${p.toString()}`,
    );
  },
  studioPublishAssetVersion: (
    assetId: string,
    body: { semver: string; files: { path: string; content: string }[]; connection?: RegistryConnectionPreset },
  ) => req<{ asset: RegistryAssetContent }>('POST', `${ST}/registry/assets/${encodeURIComponent(assetId)}/versions`, body),
  listIntegrations: (projectId: string, includeDeleted?: boolean) =>
    req<{ integrations: Integration[] }>(
      'GET',
      `${ON(projectId)}/integrations${includeDeleted ? '?includeDeleted=true' : ''}`,
    ),
  /**
   * Tombstone an integration. The endpoint stops answering; the row stays so an old incident's
   * delivery log is still readable. `version` rides the query string because a DELETE has no body —
   * it is still enforced (STALE on mismatch).
   */
  deleteIntegration: (projectId: string, integrationId: string, version: number) =>
    req<{ integration: Integration; deleted: true }>(
      'DELETE',
      `${ON(projectId)}/integrations/${integrationId}?version=${version}`,
    ),
  createIntegration: (projectId: string, body: CreateIntegrationBody) =>
    req<{ integration: Integration; secret?: string; shownOnce?: true }>(
      'POST',
      `${ON(projectId)}/integrations`,
      body,
    ),
  updateIntegration: (projectId: string, integrationId: string, version: number, patch: IntegrationPatchBody) =>
    req<{ integration: Integration }>('POST', `${ON(projectId)}/integrations/${integrationId}`, {
      version,
      patch,
    }),
  /**
   * `externalSecret` is REQUIRED for a source that issues its own (PagerDuty) — the core refuses the
   * rotation otherwise, because a minted secret is one the source will never send.
   */
  rotateIntegrationSecret: (
    projectId: string,
    integrationId: string,
    version: number,
    opts?: { revokePrevious?: boolean; externalSecret?: string },
  ) =>
    req<{ integration: Integration; secret?: string; shownOnce?: true }>(
      'POST',
      `${ON(projectId)}/integrations/${integrationId}/rotate-secret`,
      {
        version,
        ...(opts?.revokePrevious ? { revokePrevious: true } : {}),
        ...(opts?.externalSecret ? { externalSecret: opts.externalSecret } : {}),
      },
    ),
  sendTestEvent: (projectId: string, integrationId: string, title?: string) =>
    req<{ result: string; taskId: string | null; deliveryId: string; doorbell: string }>(
      'POST',
      `${ON(projectId)}/integrations/${integrationId}/test`,
      title ? { title } : {},
    ),
  /** `nextCursor` present means MORE ROWS EXIST; absent means the list is complete. */
  listDeliveries: (projectId: string, integrationId: string, cursor?: string, limit = 25) =>
    req<{ deliveries: Delivery[]; nextCursor?: string }>(
      'GET',
      `${ON(projectId)}/integrations/${integrationId}/deliveries?limit=${limit}` +
        (cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''),
    ),
  replayDelivery: (projectId: string, integrationId: string, deliveryId: string) =>
    req<{ result: string; taskId?: string | null; newDeliveryId: string }>(
      'POST',
      `${ON(projectId)}/integrations/${integrationId}/deliveries/${encodeURIComponent(deliveryId)}/replay`,
    ),
  /** The raw payload of a REFUSED delivery. A real navigation, never fetch-and-render: the response
   * is an attachment of unauthenticated attacker-controlled bytes (§8.3). Retained for the download
   * route, which still serves them as octet-stream + attachment + nosniff. */
  rawDeliveryUrl: (projectId: string, integrationId: string, deliveryId: string) =>
    `${ON(projectId)}/integrations/${integrationId}/deliveries/${encodeURIComponent(deliveryId)}/raw`,
  /**
   * The same bytes, as TEXT, for the in-app viewer.
   *
   * Decoded here rather than rendered by the browser: the caller puts it in a React text node, which
   * escapes it, so hostile bytes cannot become markup. Rejects when the payload has aged out — the
   * log entry survives 30 days and the body only 7, and the viewer explains that rather than showing
   * an empty box.
   */
  readDeliveryRaw: async (projectId: string, integrationId: string, deliveryId: string): Promise<{ payload: string }> => {
    const res = await fetch(
      `${ON(projectId)}/integrations/${integrationId}/deliveries/${encodeURIComponent(deliveryId)}/raw`,
      { credentials: 'include' },
    );
    if (!res.ok) throw new Error(`payload unavailable (${res.status})`);
    return { payload: await res.text() };
  },
  search: (projectId: string, query: string, k?: number) =>
    req<{ results: Array<{ task: Task; score: number }> }>(
      'GET',
      `${P(projectId)}/search?${new URLSearchParams({ query, ...(k ? { k: String(k) } : {}) }).toString()}`,
    ),

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
  userStream(handlers: { onPresence?: (sessions: AgentSession[]) => void; onNotifications?: (d: unknown) => void; onLive?: (live: boolean) => void }): () => void {
    return joinStream({}, handlers);
  },

  /**
   * Board updates + presence. Shares the SAME socket as `userStream` — naming a board just scopes the
   * shared connection, it does not open a second one.
   */
  stream(
    projectId: string,
    boardId: string,
    onUpdate: () => void,
    onLive?: (live: boolean) => void,
    onPresence?: (sessions: AgentSession[]) => void,
    /** The composed snapshot. When present the caller writes it to the cache and the `onUpdate`
     *  refetch becomes redundant for THIS board — see Board.tsx. */
    onBoard?: (d: unknown) => void,
  ): () => void {
    return joinStream({ projectId, boardId }, { onUpdate, onLive, onPresence, ...(onBoard ? { onBoard } : {}) });
  },
};

/**
 * The name a project's FIRST board gets when one is auto-created.
 *
 * It used to be the bare string "Board", at both call sites that auto-create. Reported from staging:
 * *"both projects have `Board` named board, not sure what it is"* — correct, and the switcher shows
 * only `board.name`, so two projects were indistinguishable by the one field on screen.
 *
 * Named after its project instead. The fallback keeps the old string for the case where the project
 * name is not to hand, which is better than inventing one.
 *
 * THIS FIXES NEW PROJECTS ONLY. Boards already called "Board" keep that name — renaming other
 * people's boards is not a display fix's business. Rename them from the board settings.
 */
export function defaultBoardName(projectName?: string | null): string {
  const n = (projectName ?? '').trim();
  return n ? `${n} board` : 'Board';
}
