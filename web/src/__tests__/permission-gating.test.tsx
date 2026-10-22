/**
 * ROUTE GATING BY PERMISSION SLUG (T-378).
 *
 * *** EVERY CASE IS GENERATED FROM `ROUTE_PERMISSIONS`, NOT LISTED HERE. ***
 *
 * A hardcoded list of routes is how four of the six `'conditional'` admin tools went unasserted until
 * T-370 went looking: the list and the thing it guards are two objects, and two objects drift. Adding
 * a route to the map adds its cases here automatically, and a gated route therefore cannot ship
 * unasserted.
 *
 * WHAT THIS FILE DOES NOT PROVE, and it is the whole point of the comment in `permissions.ts`:
 * **hiding is not enforcing.** These assertions are about what the SPA renders. The BFF gates the same
 * routes and the core gates the tools behind them; a green run here says a caller is not shown a door
 * they cannot open, never that the door is locked.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { ROUTE_PERMISSIONS, SUPERADMIN, can, permissionFor } from '../permissions.js';
// Vite's `?raw` rather than `node:fs`: this is a BROWSER project with no `@types/node`, and adding
// node typings to it so one test can read a file would be the tail wagging the dog. `?raw` is
// resolved by the same bundler the app uses, so it also cannot drift from how the file really loads.
import appSource from '../App.tsx?raw';

afterEach(cleanup);

/** A gateway stub whose session carries exactly `permissions`. */
function stubSession(permissions: string[] | undefined, extra: Record<string, unknown> = {}): void {
  const session: Record<string, unknown> = {
    identity: 'alice@example.com',
    csrfToken: null,
    // The alias the BFF derives. Deliberately FALSE throughout: these tests must pass or fail on the
    // permission array alone, so a route still reading `isAdmin` cannot make them look green.
    isAdmin: false,
    subscriptionsEnabled: true,
    uiSettings: {},
    ...(permissions === undefined ? {} : { permissions }),
    ...extra,
  };
  const routes: Record<string, unknown> = { '/api/session': session, '/api/projects': { projects: [] } };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input).split('?')[0];
      const body = routes[path] ?? { notifications: [], unread: 0, sessions: [], boards: [], projects: [] };
      return { ok: true, status: 200, json: async () => body } as Response;
    }),
  );
}

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

describe('can() — the one reading of a permission', () => {
  it('FAILS CLOSED on an absent array: undefined is NONE, never ALL', () => {
    // The load-bearing case for the whole migration. `permissions` is absent from /api/session until
    // the BFF lands it; reading that as "unrestricted" would ship a wide-open gate that looks new and
    // careful, and every screen would work, so review would not catch it.
    expect(can(undefined, SUPERADMIN)).toBe(false);
  });
  it('POSITIVE CONTROL: true when the slug is held', () => {
    expect(can([SUPERADMIN], SUPERADMIN)).toBe(true);
  });
  it('REFUSAL: exact match only — no prefix in either direction', () => {
    expect(can(['batondeck:admin'], SUPERADMIN)).toBe(false);
    expect(can([`${SUPERADMIN}:write`], SUPERADMIN)).toBe(false);
    expect(can([SUPERADMIN], `${SUPERADMIN}:write`)).toBe(false);
  });
  it('REFUSAL: empty array', () => {
    expect(can([], SUPERADMIN)).toBe(false);
  });
});

/**
 * *** THE GUARD THAT MAKES GENERATION SAFE, AND IT EXISTS BECAUSE THE MUTATION EXPOSED THE GAP. ***
 *
 * Generating cases from `ROUTE_PERMISSIONS` stops the list and the map drifting — but it introduces
 * the opposite failure, which the mutation proof found rather than reasoning did: **deleting `/admin`
 * from the map did not turn the suite RED, it turned it GREEN WITH FOUR FEWER TESTS** (14 -> 10).
 * A guard whose coverage silently shrinks when the thing it guards is removed is not a guard.
 *
 * So the map is pinned against the ROUTER — the other side of the same fact — rather than against a
 * hardcoded list, which would only move the drift somewhere else. Removing a route from the map now
 * fails here, because `App.tsx` still wraps it in `<RequirePermission>`; removing BOTH is a visible,
 * deliberate two-file change rather than a silent loss of coverage.
 */
describe('the map is pinned to the router, so deleting an entry cannot silently shrink coverage', () => {
  /** Paths `App.tsx` actually gates, read from source — the router's own answer, not a restatement. */
  const gatedInRouter = (): string[] => {
    const src = appSource;
    // DEDUPED: `permissionFor` is called at the route AND at its nav link, deliberately — one
    // predicate in both places is the fix for the nav/route divergence. The assertion is about the
    // SET of gated paths, so two call sites for one path is agreement, not drift.
    return [...new Set([...src.matchAll(/permissionFor\('([^']+)'\)/g)].map((m) => m[1]!))].sort();
  };

  it('every route the ROUTER gates has a map entry', () => {
    for (const path of gatedInRouter()) {
      expect(
        permissionFor(path),
        `App.tsx gates ${path} but ROUTE_PERMISSIONS has no entry — permissionFor() returns undefined ` +
          `and the non-null assertion at the call site would hand RequirePermission an undefined slug`,
      ).toBeTruthy();
    }
  });

  it('every map entry is actually gated by the ROUTER', () => {
    const router = gatedInRouter();
    for (const path of Object.keys(ROUTE_PERMISSIONS)) {
      expect(
        router,
        `${path} is in ROUTE_PERMISSIONS but App.tsx does not wrap it in <RequirePermission> — the map ` +
          `says it is protected and the router does not protect it`,
      ).toContain(path);
    }
  });

  it('the two sides are the SAME SET — neither may quietly lose a route', () => {
    expect(gatedInRouter()).toEqual(Object.keys(ROUTE_PERMISSIONS).sort());
  });
});

describe('the map and the router agree', () => {
  it('every gated route names a slug that is a non-empty string', () => {
    const entries = Object.entries(ROUTE_PERMISSIONS);
    expect(entries.length, 'the map is empty — every route would be ungated').toBeGreaterThan(0);
    for (const [route, slug] of entries) {
      expect(typeof slug, `${route} has a non-string slug`).toBe('string');
      expect(slug.length, `${route} has an empty slug`).toBeGreaterThan(0);
      expect(permissionFor(route)).toBe(slug);
    }
  });
  it('an UNGATED path resolves to no permission', () => {
    expect(permissionFor('/projects')).toBeUndefined();
  });
});

// ── GENERATED FROM THE MAP. Add a route to ROUTE_PERMISSIONS and these appear. ──
describe.each(Object.entries(ROUTE_PERMISSIONS))('gated route %s (needs %s)', (route, slug) => {
  it('REFUSES, with an explicit reason, when the session lacks the slug', async () => {
    stubSession([]);
    await renderAt(route);
    const refusal = await screen.findByTestId('permission-refused');
    expect(refusal).toBeInTheDocument();
    // The refusal must NAME the permission. A bare "not allowed" leaves the caller and whoever they
    // report it to unable to tell a missing grant from a broken link — the reason this replaced a
    // silent <Navigate>.
    expect(refusal.textContent).toContain(slug);
  });

  it('REFUSES when `permissions` is ABSENT — the pre-BFF state must not open the door', async () => {
    stubSession(undefined);
    await renderAt(route);
    expect(await screen.findByTestId('permission-refused')).toBeInTheDocument();
  });

  it('REFUSES when the session holds a DIFFERENT slug', async () => {
    // DERIVED, never a literal. This read `stubSession(['batondeck:admin'])` until 2026-09-01, when
    // `/studio` entered the map needing exactly that slug — the foil became the key, and this case
    // went red claiming a gating defect that did not exist. Deriving the foil from the map cannot
    // collide again. The concrete admin-vs-superadmin pair stays pinned by the `can()` REFUSAL test
    // above, so nothing is lost by not naming a permission here.
    const foil = Object.values(ROUTE_PERMISSIONS).find((s) => s !== slug) ?? 'batondeck:no-such-permission';
    stubSession([foil]);
    await renderAt(route);
    expect(await screen.findByTestId('permission-refused')).toBeInTheDocument();
  });

  it('POSITIVE CONTROL: ADMITS when the slug is held', async () => {
    stubSession([slug]);
    await renderAt(route);
    // BOTH halves. Asserting only the refusal's ABSENCE is vacuous — it is equally true when the
    // route renders nothing, and a mutation returning `null` from the granted branch passed all 17
    // cases. The granted marker is what makes this a positive control rather than a double negative;
    // asserting each page's own contents here would instead couple this file to every gated screen.
    expect(await screen.findByTestId('permission-granted')).toBeInTheDocument();
    expect(screen.queryByTestId('permission-refused')).not.toBeInTheDocument();
  });
});

/**
 * THE NAV AND THE ROUTES MUST AGREE — and this suite exists because the fix was UNGUARDED.
 *
 * The nav originally decided on `isAdmin` while the routes decided on the permission. That is the
 * same "seen versus callable" divergence T-366's review found in the core, reproduced in one file.
 * It matters most during the migration window: `permissions` is absent until the BFF lands, so the
 * routes refuse while `isAdmin` may still be true — the nav would offer a link to a door that
 * refuses, which is precisely what `permissions.ts` claims this whole mechanism prevents.
 *
 * Fixing it was one line. **Nothing failed when the fix was reverted** — a nav mutation ignoring the
 * permission passed all 17 cases — so the fix was a convention until these two tests existed.
 */
describe.each(Object.entries(ROUTE_PERMISSIONS))('nav link for %s agrees with the gate', (route, slug) => {
  const label = route.replace('/', '');
  const linkNamed = (): HTMLElement | null =>
    screen.queryAllByRole('link').find((a) => a.getAttribute('href') === route) ?? null;

  it(`is HIDDEN without ${slug} — never offer a door that refuses`, async () => {
    stubSession([]);
    await renderAt('/projects');
    await screen.findByRole('navigation');
    expect(linkNamed(), `the ${label} link is offered to a session that the route would refuse`).toBeNull();
  });

  it(`is SHOWN with ${slug}`, async () => {
    stubSession([slug]);
    await renderAt('/projects');
    await screen.findByRole('navigation');
    expect(linkNamed(), `the ${label} link is hidden from a session that holds ${slug}`).not.toBeNull();
  });
});
