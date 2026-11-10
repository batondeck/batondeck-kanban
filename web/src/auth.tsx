import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import * as sessionApi from './api/session.js';
import { UI_DEFAULTS, hydrateUiSettings, type UiSettings } from './settings.js';

interface AuthState {
  identity: string | null;
  /**
   * The signed-in account's display name, else its login (`/api/session` `displayName`, P-314).
   * `identity` is an opaque `U-<hex>` account id since the identity cutover; this is what a person
   * reads. Null while loading or when the server has no account row for the identity.
   */
  displayName: string | null;
  /**
   * Is this caller a workspace admin? `/api/session` (`internal/api/session.go`) sets it from the
   * caller's `WorkspaceAdmin` and, from the same fact, puts `batondeck:superadmin` and
   * `batondeck:oncall` in `permissions`, so the two always agree. The routes, the nav and the screens
   * that widen `/api/agents/sessions` read `permissions` through `can()`, the one oracle (see
   * `permissions.ts`). Two readers remain: `useMayReadIntegrations` (displayNames.ts), which gates the
   * integrations read, and the Activity log, which shows the audit log to an admin.
   */
  isAdmin: boolean;
  /**
   * The workspace permissions this session holds. The server always sends the array; every reader
   * still goes through `can()` and its `?? []`, so an absent array reads as "none" and never as
   * "all" — see the header of `permissions.ts`.
   */
  permissions: readonly string[];
  /** On the wire for the SPA's older readers: this server has no billing or usage limits, and always
   *  answers false. Nothing reads it. */
  subscriptionsEnabled: boolean;
  /** Effective UI runtime settings (defaults merged with workspace overrides from /api/session). */
  ui: UiSettings;
  loading: boolean;
  /** Local sign-in; throws the BFF's ApiError on a refusal. The URL is not touched, so the route the
   * visitor asked for renders once the session loads. */
  login: (login: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  /** Re-fetch /api/session so context-derived UI (the display name, the UI settings) reflects a
   * runtime change without a full page reload. */
  refresh: () => Promise<void>;
}

const Ctx = createContext<AuthState>({
  identity: null,
  displayName: null,
  isAdmin: false,
  permissions: [],
  subscriptionsEnabled: false,
  ui: UI_DEFAULTS,
  loading: true,
  login: async () => {},
  logout: async () => {},
  refresh: async () => {},
});

export function AuthProvider({ children }: { children: ReactNode }) {
  const [identity, setIdentity] = useState<string | null>(null);
  const [displayName, setDisplayName] = useState<string | null>(null);
  const [isAdmin, setIsAdmin] = useState(false);
  const [permissions, setPermissions] = useState<readonly string[]>([]);
  const [subscriptionsEnabled, setSubscriptionsEnabled] = useState(false);
  const [ui, setUi] = useState<UiSettings>(UI_DEFAULTS);
  const [loading, setLoading] = useState(true);
  const qc = useQueryClient();

  const refresh = async (): Promise<void> => {
    try {
      const s = await sessionApi.session();
      setIdentity(s.identity);
      setDisplayName(s.displayName ?? null);
      setIsAdmin(s.isAdmin);
      // `?? []` at the ONE place the wire is read. Absent means none (see permissions.ts).
      setPermissions(s.permissions ?? []);
      setSubscriptionsEnabled(s.subscriptionsEnabled);
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

  return <Ctx.Provider value={{ identity, displayName, isAdmin, permissions, subscriptionsEnabled, ui, loading, login, logout, refresh }}>{children}</Ctx.Provider>;
}

export const useAuth = (): AuthState => useContext(Ctx);
