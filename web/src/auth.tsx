import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import * as sessionApi from './api/session.js';
import { UI_DEFAULTS, hydrateUiSettings, type UiSettings } from './settings.js';

interface AuthState {
  identity: string | null;
  /**
   * DERIVED ALIAS, SURVIVING FOR ONE RELEASE (T-378). Equivalent to
   * `can(permissions, SUPERADMIN)`, computed at the BFF.
   *
   * It is kept so the migration off the boolean can be incremental across 16 files rather than a flag
   * day. **Do not delete it while finishing the source-side migration** — removing it turns an
   * incremental change into a simultaneous one, which is the shape that leaves an authorization
   * surface half-migrated.
   *
   * *** IT NOW HAS ZERO READERS IN THE SPA (T-378 tail).*** The routes, the nav and the three screens
   * that widen `/api/agents/sessions` all read `permissions` through `can()`. It is still carried here
   * deliberately, per the paragraph above; retiring it is a separate, one-line change whose
   * PRECONDITION IS THE BFF DROPPING THE FIELD from `/api/session`, not the SPA finishing.
   *
   * *** DO NOT "SIMPLIFY" IT INTO `can(permissions, SUPERADMIN)`. *** The BFF computes it from its own
   * oracle (`check_access` → the permission **or** unexpired break-glass). Deriving it here
   * would be the inversion the BFF explicitly refused: in production nobody holds the superadmin role,
   * so a derived boolean reports false for every admin alive.
   */
  isAdmin: boolean;
  /**
   * The workspace permissions this session holds.
   *
   * ABSENT until the BFF lands it, which is why every reader goes through `can()` and its `?? []`.
   * An absent array must read as "none" and never as "all" — see the header of `permissions.ts`.
   */
  permissions: readonly string[];
  /** Runtime subscriptions master switch (workspace/settings.subscriptionsEnabled). Off by default → no
   * usage limits and the Billing screen is hidden. An admin toggles it from the Admin portal (no redeploy). */
  subscriptionsEnabled: boolean;
  /** Effective UI runtime settings (defaults merged with workspace overrides from /api/session). */
  ui: UiSettings;
  /**
   * WHICH ORGANIZATION THIS SESSION ACTS IN, and what it is CALLED (T-392 / T-453).
   *
   * Both arrive on `/api/session`, which the SPA already loads at boot, so the identity menu renders
   * the organization with **no request of its own** — the owner's requirement, and the reason
   * `tenantName` exists on the wire at all. The live LIST is deliberately NOT here: it costs a WorkOS
   * read and belongs on `/api/tenants`, which only the switcher dialog hits.
   *
   * `tenantName` is `null` for any session minted before the `cn` cookie shipped, and for an
   * organization WorkOS has no name for. Renderers must fall back to `tenantId`, never to blank
   * (T-394). NOTHING may key on the name: it is user-supplied, editable and not unique.
   */
  tenantId: string | null;
  tenantName: string | null;
  /** Origin of the authenticated pull endpoint (the core), for composing a member's personal
   * marketplace URL in Settings. Null on gateways predating it. */
  pullBase: string | null;
  loading: boolean;
  /** Local sign-in; throws the BFF's ApiError on a refusal. The URL is not touched, so the route the
   * visitor asked for renders once the session loads. */
  login: (login: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  /** Re-fetch /api/session so context-derived UI (e.g. the Billing nav) reflects a runtime change like an
   * admin toggling subscriptions on/off — without a full page reload. */
  refresh: () => Promise<void>;
}

const Ctx = createContext<AuthState>({
  identity: null,
  isAdmin: false,
  permissions: [],
  subscriptionsEnabled: false,
  ui: UI_DEFAULTS,
  tenantId: null,
  tenantName: null,
  pullBase: null,
  loading: true,
  login: async () => {},
  logout: async () => {},
  refresh: async () => {},
});

export function AuthProvider({ children }: { children: ReactNode }) {
  const [identity, setIdentity] = useState<string | null>(null);
  const [isAdmin, setIsAdmin] = useState(false);
  const [permissions, setPermissions] = useState<readonly string[]>([]);
  const [subscriptionsEnabled, setSubscriptionsEnabled] = useState(false);
  const [ui, setUi] = useState<UiSettings>(UI_DEFAULTS);
  const [tenantId, setTenantId] = useState<string | null>(null);
  const [tenantName, setTenantName] = useState<string | null>(null);
  const [pullBase, setPullBase] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const qc = useQueryClient();

  const refresh = async (): Promise<void> => {
    try {
      const s = await sessionApi.session();
      setIdentity(s.identity);
      setIsAdmin(s.isAdmin);
      // `?? []` at the ONE place the wire is read. Absent means none (see permissions.ts).
      setPermissions(s.permissions ?? []);
      setSubscriptionsEnabled(s.subscriptionsEnabled);
      // `?? null` on both: absent and null mean the same thing here, and two spellings of absent is how
      // a fallback chain ends up with a branch nobody wrote.
      setTenantId(s.tenantId ?? null);
      setTenantName(s.tenantName ?? null);
      setPullBase(s.pullBase ?? null);
      // Hydrate the module store (for non-React readers like agentStatus) AND the context copy (re-render).
      setUi(hydrateUiSettings(s.uiSettings as Partial<UiSettings>));
    } catch {
      setIdentity(null);
    }
  };

  useEffect(() => {
    void refresh().finally(() => setLoading(false));
    // api/http.ts fires this when any call returns 401 mid-session (expired/revoked cookie): drop the
    // identity so the app falls back to the sign-in screen instead of silently failing every request.
    const onUnauthorized = (): void => setIdentity(null);
    window.addEventListener('batondeck:unauthorized', onUnauthorized);
    return () => window.removeEventListener('batondeck:unauthorized', onUnauthorized);
  }, []);

  // Both drop the query cache: the next account must not render the previous one's data.
  const login = async (loginName: string, password: string): Promise<void> => {
    await sessionApi.login(loginName, password);
    qc.clear();
    await refresh();
  };

  // A failed sign-out throws and keeps the identity: showing the sign-in card over a session the
  // server still holds would be a sign-out that did not happen. A 401 (already ended) is signed out
  // by http.ts's `batondeck:unauthorized`.
  const logout = async (): Promise<void> => {
    await sessionApi.logout();
    qc.clear();
    setIdentity(null);
  };

  return <Ctx.Provider value={{ identity, isAdmin, permissions, subscriptionsEnabled, ui, tenantId, tenantName, pullBase, loading, login, logout, refresh }}>{children}</Ctx.Provider>;
}

export const useAuth = (): AuthState => useContext(Ctx);
