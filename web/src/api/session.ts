// The session area (internal/api/session.go): who is signed in, signing in and signing out. The CSRF token is
// read from R0.12's bk_csrf cookie by http.ts, so neither call keeps it in module state any more.
import { ApiError, req } from './http.js';

/**
 * Sign in with a local account (R0.12's form login). The body is form-encoded because the handler
 * reads `login` and `password` as form fields; success is 204 and the session and CSRF cookies. A
 * refusal throws the BFF's `{code, message}` as an ApiError, whose message the form shows.
 *
 * Not `req`: that sends JSON, and its 401 fires `batondeck:unauthorized`, which means a session
 * ended, not a wrong password.
 */
export async function login(loginName: string, password: string): Promise<void> {
  const res = await fetch('/api/login', {
    method: 'POST',
    headers: { 'content-type': 'application/x-www-form-urlencoded' },
    credentials: 'include',
    body: new URLSearchParams({ login: loginName, password }).toString(),
  });
  if (res.ok) return;
  const j = (await res.json().catch(() => ({}))) as { code?: string; message?: string };
  throw new ApiError(res.status, j.code ?? 'INTERNAL', j.message ?? res.statusText);
}

/** Sign out: the BFF deletes the session row and expires both cookies (204). */
export async function logout(): Promise<void> {
  await req<void>('POST', '/api/logout');
}

// `tenantName` (T-453) is the organization's DISPLAY name, served from the session cookie jar so the
// identity menu can render it with no request of its own. OPTIONAL on the wire and `null` for any
// session minted before `cn` shipped — the renderer falls back to `tenantId`, never to blank.
export async function session(): Promise<{ identity: string; csrfToken: string | null; isAdmin: boolean; permissions?: string[]; subscriptionsEnabled: boolean; uiSettings: Record<string, number | boolean>; tenantId?: string | null; tenantName?: string | null; pullBase?: string | null }> {
  // `permissions` is OPTIONAL on the wire and stays optional (T-378): it is absent until the BFF
  // lands it, and optional-here forces every reader through `can()`'s `?? []` rather than letting a
  // non-null assertion invent an empty array somewhere less careful.
  const s = await req<{ identity: string; csrfToken: string | null; isAdmin?: boolean; permissions?: string[]; subscriptionsEnabled?: boolean; uiSettings?: Record<string, number | boolean>; tenantId?: string | null; tenantName?: string | null; pullBase?: string | null }>('GET', '/api/session');
  return { ...s, isAdmin: s.isAdmin ?? false, subscriptionsEnabled: s.subscriptionsEnabled ?? false, uiSettings: s.uiSettings ?? {} };
}

/** The unauthenticated server facts: whether the OAuth server runs, and whether no account exists yet. */
export async function publicConfig(): Promise<{ oauth: boolean; noUsers: boolean }> {
  const c = await req<{ oauth?: boolean; noUsers?: boolean }>('GET', '/api/public-config');
  return { oauth: c.oauth ?? false, noUsers: c.noUsers ?? false };
}
