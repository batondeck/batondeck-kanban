/**
 * UI runtime settings. Defaults live HERE (the single source). `/api/session` carries a `uiSettings`
 * object of overrides, hydrated on every session load; this server always sends it empty, so the
 * defaults below are what every client runs with. Non-React code (agentStatus) reads the module store
 * synchronously via uiSettings(); React code re-renders through the AuthContext copy.
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
}

export const UI_DEFAULTS: UiSettings = {
  searchK: 50,
  searchDebounceMs: 300,
  taskPageLimit: 200,
  recentActivityLimit: 12,
  sessionsLimit: 100,
  presenceOfflineAfterMs: 75000,
  presenceWindowMs: 600000,
};

let current: UiSettings = UI_DEFAULTS;

/** The effective settings (defaults merged with workspace overrides). Safe before hydration: defaults. */
export function uiSettings(): UiSettings {
  return current;
}

/** Merge workspace overrides over the defaults. Called by AuthProvider on every session load. */
export function hydrateUiSettings(overrides: Partial<UiSettings> | undefined): UiSettings {
  current = { ...UI_DEFAULTS, ...(overrides ?? {}) };
  return current;
}
