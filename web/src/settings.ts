/**
 * Admin-tunable UI runtime settings. Defaults live HERE (the single source); the workspace stores only
 * overrides (workspace/settings.ui via get/set_feature_flags), delivered on /api/session and hydrated
 * once at boot. Non-React code (agentStatus) reads the module store synchronously via uiSettings();
 * React code re-renders through the AuthContext copy. Admins edit these in /admin — changes apply on
 * each client's next session load (~page load / auth refresh), like the core's ~60s flag cache.
 */
export interface UiSettings {
  /** Results shown per PAGE of ticket search ("Show more" pages through the rest; the fetch always uses the server cap 50). */
  searchK: number;
  /** Debounce on the /activity search input. */
  searchDebounceMs: number;
  /** Page size for board task lists (list_tasks cap 200). */
  taskPageLimit: number;
  /** Rows in the Analytics "Recent Activity" strip (the board feed serves at most 50 events). */
  recentActivityLimit: number;
  /** Sessions fetched for presence/client-logo lookups (list_agent_sessions cap 200). */
  sessionsLimit: number;
  /** An MCP session with no request for this long is shown offline; a device-directory row is online while listed. */
  presenceOfflineAfterMs: number;
  /** "Who's around now" window for presence lists/pickers. */
  presenceWindowMs: number;
  /** Fetch link-card icons from the Google favicon proxy (off → generic icon, no external request). */
  linkFavicons: boolean;
}

export const UI_DEFAULTS: UiSettings = {
  searchK: 50,
  searchDebounceMs: 300,
  taskPageLimit: 200,
  recentActivityLimit: 12,
  sessionsLimit: 100,
  presenceOfflineAfterMs: 75000,
  presenceWindowMs: 600000,
  linkFavicons: true,
};

let current: UiSettings = UI_DEFAULTS;

/** The effective settings (defaults merged with workspace overrides). Safe before hydration: defaults. */
export function uiSettings(): UiSettings {
  return current;
}

/** Merge workspace overrides over the defaults. Called by AuthProvider on every session load. */
export function hydrateUiSettings(overrides: Partial<UiSettings> | undefined): UiSettings {
  const merged = { ...UI_DEFAULTS, ...(overrides ?? {}) };
  // `list_tasks` refuses a `limit` over 200 (internal/service/tasks.go listMaxLimit), and the drawer,
  // the Activity log and the Agents screen send this page size in one request: past 200 each would fail.
  const pageLimit = Math.floor(Number(merged.taskPageLimit));
  merged.taskPageLimit = Number.isFinite(pageLimit) ? Math.min(200, Math.max(1, pageLimit)) : UI_DEFAULTS.taskPageLimit;
  current = merged;
  return current;
}
