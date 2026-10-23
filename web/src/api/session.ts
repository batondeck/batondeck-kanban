// The session area (internal/api/session.go): who is signed in, and signing out. The CSRF token is
// read from R0.12's bk_csrf cookie by http.ts, so neither call keeps it in module state any more.
import { req } from './http.js';

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
export async function logout(): Promise<void> {
  const { logoutUrl } = await req<{ ok: boolean; logoutUrl: string | null }>('POST', '/auth/logout');
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
