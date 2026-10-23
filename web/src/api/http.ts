// The request helper every area file shares: req, P and ON, the error type, and the local-edit marks
// (R1.14 moved them here from api.ts, which is gone). The CSRF token is read from R0.12's bk_csrf
// cookie on every write.
export class ApiError extends Error {
  code: string;
  status: number;
  constructor(status: number, code: string, message: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

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

const csrfToken = (): string | null => {
  const m = /(?:^|;\s*)bk_csrf=([^;]*)/.exec(document.cookie);
  return m?.[1] ? decodeURIComponent(m[1]) : null;
};

export async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  // Any non-GET aimed at a specific task is this tab editing it. (`…/boards/<id>/tasks` — a create —
  // has nothing after `/tasks`, so it doesn't match: a brand-new card is seeded, never flashed.)
  if (method !== 'GET') {
    const m = /\/tasks\/([^/?]+)/.exec(path);
    if (m?.[1]) markLocalEdit(decodeURIComponent(m[1]));
  }
  const headers: Record<string, string> = {};
  if (body !== undefined) headers['content-type'] = 'application/json';
  const csrf = csrfToken();
  if (method !== 'GET' && csrf) headers['x-csrf-token'] = csrf;
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

export const P = (projectId: string): string => `/api/projects/${projectId}`;
export const ON = (projectId: string): string => `/api/oncall/${projectId}`;
