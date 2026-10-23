// The area files' request helper (R1.12): a copy of api.ts's req, P and ON, the CSRF token read from
// R0.12's bk_csrf cookie instead of api.ts's module state. R1.14 moves req here and drops api.ts's.
import { ApiError, markLocalEdit } from '../api.js';

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
