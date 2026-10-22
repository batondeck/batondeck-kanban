/**
 * THE SESSIONS WIDENING READS THE PERMISSION, NOT THE `isAdmin` ALIAS (T-378 tail).
 *
 * *** `all=true` IS A SCOPE, NOT A GATE, AND THAT DISTINCTION IS THE WHOLE TEST. *** A member asking
 * without it gets their own sessions; an admin asking with it gets the workspace's. The core enforces
 * the widening, so nothing here is a security control — what it pins is that the SPA asks the same
 * question it asked before the migration, computed from `permissions` instead of the boolean.
 *
 * Sound only because the BFF SYNTHESISES the slug for allowlist admins rather than deriving the
 * boolean from it — the coupling, and the grep that re-derives it, are stated next to `SUPERADMIN` in
 * `permissions.ts`. If that synthesis goes, these assertions still pass while the live app narrows a
 * widened fetch, which is why the note lives in the source and not only here.
 *
 * *** EVERY SESSION BELOW CARRIES `isAdmin: false`. *** Deliberately: a screen that still read the
 * alias would fail the positive case rather than pass it. A test that lets the retired model satisfy
 * it is the green-but-useless shape of CONTRIBUTING rule 12.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, waitFor, cleanup } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { SUPERADMIN } from '../permissions.js';

afterEach(cleanup);

/** Every URL the SPA fetched during the current render, in order. */
let calls: string[] = [];

/** A gateway stub whose session carries exactly `permissions` — and never a true `isAdmin`. */
function stubSession(permissions: string[]): void {
  calls = [];
  const session = {
    identity: 'alice@example.com',
    csrfToken: null,
    isAdmin: false,
    permissions,
    subscriptionsEnabled: false,
    uiSettings: {},
  };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      calls.push(url);
      const path = url.split('?')[0];
      const body =
        path === '/api/session'
          ? session
          : { notifications: [], unread: 0, sessions: [], boards: [], projects: [], events: [], tasks: [] };
      return { ok: true, status: 200, json: async () => body } as Response;
    }),
  );
}

/** The `/api/agents/sessions` requests only — the subject of every assertion below. */
const sessionRequests = (): string[] => calls.filter((u) => u.split('?')[0] === '/api/agents/sessions');

/** `all` as the SERVER would read it: absent is not the same value as absent-and-untested. */
const allParam = (url: string): string | null => new URL(url, 'http://localhost').searchParams.get('all');

async function renderAt(path: string): Promise<void> {
  const { QueryClient, QueryClientProvider } = await import('@tanstack/react-query');
  const { AuthProvider } = await import('../auth.js');
  const { App } = await import('../App.js');
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <App />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

/**
 * THE SCREENS THAT WIDEN, AND THE ROUTE EACH IS REACHED AT.
 *
 * Six screens call `api.agentSessions`; only these three pass the second argument. The other three
 * (`Settings`, `Agents`, `Board`/task drawer) ask identity-scoped and always did — including them here
 * would assert a widening that is not theirs.
 */
const WIDENING_SCREENS = [
  { source: '../screens/ActivityLog.tsx', route: '/activity' },
  { source: '../screens/Analytics.tsx', route: '/analytics' },
  { source: '../screens/Projects.tsx', route: '/projects' },
];

/**
 * *** THE TABLE ABOVE IS PINNED TO THE SOURCE, BECAUSE A HARDCODED LIST SHRINKS SILENTLY. ***
 *
 * `permission-gating.test.tsx` measured that exact failure one file over: deleting a map entry took
 * the suite from 14 tests to 10 — GREEN, with less coverage, not red. A list of screens has the same
 * shape. So the set is asserted against every screen whose source really passes the widening argument,
 * read with the `?raw` glob rather than `node:fs` (this is a browser project with no node typings, and
 * the bundler resolving it is the same one the app uses).
 */
const SCREEN_SOURCES = import.meta.glob('../screens/**/*.{ts,tsx}', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

describe('which screens widen — the table cannot drift from the source', () => {
  const widensInSource = (): string[] =>
    Object.entries(SCREEN_SOURCES)
      // A SECOND ARGUMENT to agentSessions IS the widening. `api.agentSessions(limit)` is not.
      .filter(([, src]) => /api\.agentSessions\([^)]*,[^)]*\)/.test(src))
      .map(([path]) => path)
      .sort();

  it('every screen that passes the widening argument is exercised below', () => {
    expect(
      widensInSource(),
      'a screen widens `/api/agents/sessions` but no case here drives it — add it to WIDENING_SCREENS, ' +
        'or the next screen to widen ships with the permission read unasserted',
    ).toEqual(WIDENING_SCREENS.map((s) => s.source).sort());
  });

  it('the glob actually found the screens — an empty scan would make the pin vacuous', () => {
    expect(Object.keys(SCREEN_SOURCES).length).toBeGreaterThan(WIDENING_SCREENS.length);
  });
});

// ── GENERATED FROM THE TABLE. Refusal and positive control per screen. ──
describe.each(WIDENING_SCREENS)('$route widens on the permission, never on the alias', ({ route }) => {
  it('POSITIVE CONTROL: a session HOLDING batondeck:superadmin asks for all=true', async () => {
    stubSession([SUPERADMIN]);
    await renderAt(route);
    await waitFor(() => expect(sessionRequests().length).toBeGreaterThan(0));
    for (const url of sessionRequests()) expect(allParam(url)).toBe('true');
  });

  it('REFUSAL: a session WITHOUT the slug still asks, and asks NARROWLY', async () => {
    stubSession([]);
    await renderAt(route);
    // NON-VACUOUS BY CONSTRUCTION: the request must have HAPPENED before its shape means anything.
    // "no request carried all=true" is equally true of a screen that made no request at all — which is
    // exactly what a broken query, a thrown render or a mistyped path would produce.
    await waitFor(() => expect(sessionRequests().length).toBeGreaterThan(0));
    for (const url of sessionRequests()) expect(allParam(url)).toBeNull();
  });

  it('REFUSAL: a DIFFERENT slug does not widen — exact match, no near miss', async () => {
    stubSession(['batondeck:admin', `${SUPERADMIN}:write`]);
    await renderAt(route);
    await waitFor(() => expect(sessionRequests().length).toBeGreaterThan(0));
    for (const url of sessionRequests()) expect(allParam(url)).toBeNull();
  });
});
