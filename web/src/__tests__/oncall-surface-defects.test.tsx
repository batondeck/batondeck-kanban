/**
 * FIVE DEFECTS REPORTED FROM STAGING, 2026-08-29 — each pinned by the cheapest check that can see it.
 *
 * Four of the five are invisible to every existing test, which is how they shipped: they are about
 * an icon's shape, a URL suffix, a CSS class and which branch of a state machine a Cancel lands in.
 * Nothing asserts those, so nothing broke when they were wrong.
 *
 * WHAT JSDOM CANNOT DO HERE, stated so no one reads more into a green run than is there: it resolves
 * no custom properties and lays nothing out, so "the icons look different" and "the page is aligned"
 * are NOT provable. What is provable is the thing that actually regressed each time — the `d` string,
 * the class, the href, the branch — and that is what each test below asserts.
 */
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, cleanup, waitFor, within } from '@testing-library/react';
import { OncallSetup } from '../screens/oncall/OncallSetup.js';
import { SOURCE_LOGOS, sourceLogo } from '../screens/oncall/logos.js';
import type { Integration } from '../types.js';

/**
 * `import.meta.glob` + `?raw`, the mechanism `design-conventions.test.ts` and `tokens.test.ts`
 * already use. Node's `fs` is not reachable from this package — its tsconfig carries no `@types/node`,
 * so `readFileSync` type-errors even though vitest runs it happily. The suite passing is not the bar.
 */
const SOURCES = import.meta.glob('../**/*.{tsx,ts}', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

/**
 * COMMENTS ARE STRIPPED, and this is not housekeeping — it is the difference between a guard and a
 * decoration. Every fix below carries a comment explaining it, and those comments QUOTE the very
 * strings the assertions look for. The `className="page"` check passed with the class deleted from
 * the JSX, because the sentence above it still said `className="page"` (mutation M3, 2026-08-29).
 * Same `strip` as `design-conventions.test.ts`, for the same reason.
 */
const strip = (source: string): string =>
  source.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');

const src = (rel: string): string => {
  const hit = Object.entries(SOURCES).find(([k]) => k.endsWith(`/${rel}`));
  if (!hit) throw new Error(`source not found for ${rel} (have ${Object.keys(SOURCES).length} files)`);
  return strip(hit[1]);
};

it('the source scan finds its population — otherwise every lexical assertion is vacuous', () => {
  expect(Object.keys(SOURCES).length).toBeGreaterThan(15);
});

/* ------------------------------------------------------------------ 1. the nav icon ------ */

describe('On-call and Notifications do not draw the same icon', () => {
  /**
   * Both were bells. Two different `d` strings that render as the same 16px silhouette, stacked two
   * rows apart in one nav group, so only the text label told them apart.
   *
   * Compared as SOURCE rather than rendered SVG because the two live in different modules and only
   * meet inside the app shell; the regression is one of them being edited to match the other, which
   * is exactly what comparing the strings catches.
   */
  const oncallPath = (): string => {
    const m = /oncall: '([^']+)'/.exec(src('App.tsx'));
    if (!m) throw new Error('ICONS.oncall not found in App.tsx');
    return m[1]!;
  };
  const notificationsPath = (): string => {
    const m = /<path d="(M18 8A6[^"]+)"/.exec(src('screens/Notifications.tsx'));
    if (!m) throw new Error('the Notifications bell path not found');
    return m[1]!;
  };

  it('the two paths are not identical', () => {
    expect(oncallPath()).not.toBe(notificationsPath());
  });

  it('the on-call icon is not a bell at all', () => {
    // The bell both icons used opens with an arc sweeping over a wide top — `a7 7` or `A6 6`. A mast
    // starts with a straight vertical stroke. This is the shape property, not just string inequality:
    // swapping in a *different* bell would pass the test above and fail this one.
    expect(oncallPath()).not.toMatch(/^M12 2a7 7/);
    expect(oncallPath()).toMatch(/^M12 21v-7/);
  });
});

/* ----------------------------------------------------------------- 5. page alignment ----- */

describe('routed screens carry the app gutter', () => {
  /**
   * `.page` is the app's 16px inset. On-call and Reports rendered flush against the sidebar while
   * the ten screens beside them were inset — reported as "not consistent with rest of the app".
   * Asserting the CLASS, because jsdom resolves no stylesheet and cannot see the padding itself.
   */
  it.each([
    ['screens/Oncall.tsx'],
    ['screens/Reports.tsx'],
    // Chronicle was left out of the first pass on a GUESS that a document reader wants full bleed.
    // It does not: reported from staging as "no gaps on left and right for chronicle page".
    ['screens/Chronicle.tsx'],
  ])('%s renders className="page"', (file) => {
    expect(src(file)).toContain('className="page"');
  });
});

/* ------------------------------------------------- 4. the custom-webhook form's clarity -- */

describe('the custom mapping form is grouped, not a flat column of eleven', () => {
  it('uses FieldGroup and marks the one required field structurally', () => {
    const form = src('screens/oncall/IntegrationForm.tsx');
    expect(form).toContain('<FieldGroup');
    // "(required)" / "(optional)" typed into label strings is what made every field read alike.
    expect(form).not.toMatch(/label="[^"]*\((required|optional)\)"/);
  });

  it('Field renders the required marker beside the label, not inside it', () => {
    const fields = src('screens/oncall/fields.tsx');
    expect(fields).toContain('required?: boolean');
    expect(fields).toContain('export function FieldGroup');
  });
});

/* ------------------------------------------ 3. the create/cancel state machine (drawer) -- */

const CATALOG = {
  sources: [
    {
      id: 'webhook',
      label: 'Custom webhook',
      authModes: ['hmac'],
      docsPage: 'oncall-webhook',
      authNote: 'Any tool that can POST JSON.',
    },
    {
      id: 'grafana',
      label: 'Grafana',
      authModes: ['api_key'],
      docsPage: 'oncall-grafana',
      authNote: 'Grafana can send a custom header.',
    },
  ],
};

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } });

function stub(): void {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      if (String(input).includes('/api/oncall/sources')) return json(CATALOG);
      return json({ integrations: [], deliveries: [], boards: [] });
    }),
  );
}

function renderDrawer(integrations: Integration[] = []) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <OncallSetup projectId="P-1" integrations={integrations} onClose={() => {}} />
    </QueryClientProvider>,
  );
}

beforeEach(() => stub());
afterEach(() => {
  vi.unstubAllGlobals();
  cleanup();
});

describe('cancelling a new integration lands on the source list, not a dead end', () => {
  /**
   * THE REPORTED BUG: every cancel set `creating` to `null`, and with zero integrations `null` renders
   * "None yet." beside a `+ New` button whose only job is to restore the list you were just looking
   * at. *"basically new button there on top is useless"* — correct; a dead end plus a button to escape
   * it is a missing default, not a state.
   */
  it('shows the source list by default when nothing is configured', async () => {
    renderDrawer([]);
    // The picker is the resting state — not an empty panel the operator must click out of.
    expect(await screen.findByRole('button', { name: /Custom webhook/i })).toBeTruthy();
    expect(screen.queryByText(/None yet\./i)).toBeNull();
  });

  it('offers no "+ New" while the picker is already open', async () => {
    renderDrawer([]);
    await screen.findByRole('button', { name: /Custom webhook/i });
    // It would re-select the state you are in.
    expect(screen.queryByRole('button', { name: /\+ New/ })).toBeNull();
  });

  it('cancelling the create form returns to the source list', async () => {
    renderDrawer([]);
    fireEvent.click(await screen.findByRole('button', { name: /Custom webhook/i }));
    // We are now in the form for that source…
    await screen.findByLabelText(/^Name$/i);
    fireEvent.click(screen.getByRole('button', { name: /^Cancel$/ }));
    // …and cancelling must show the sources again, NOT "None yet." + a button to get them back.
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Grafana/i })).toBeTruthy();
    });
    expect(screen.queryByText(/None yet\./i)).toBeNull();
  });

  it('the picker offers no Cancel when there is nothing to go back to', async () => {
    renderDrawer([]);
    await screen.findByRole('button', { name: /Custom webhook/i });
    // With zero integrations the list IS the resting state; a Cancel here led to the dead end.
    expect(screen.queryByRole('button', { name: /^Cancel$/ })).toBeNull();
  });
});

/**
 * An existing integration — the case that DISCRIMINATES the two halves of the fix.
 *
 * With zero integrations both halves are redundant: `creating` initialises to `'picking'`, so the
 * picker shows whether or not `showPicker` widened and whether or not Cancel returns to the list.
 * Mutating either one alone left the suite green (M4/M5, 2026-08-29). Only these two paths separate
 * them — cancelling while something exists, and the last one being deleted underneath a `null`.
 */
const EXISTING: Integration = {
  id: 'ing_1',
  projectId: 'P-1',
  // NOT 'prod-grafana': the name renders as a button and would collide with the picker's Grafana
  // entry, so \`getByRole(/Grafana/i)\` matches two elements and throws before asserting anything.
  name: 'prod-alerts',
  source: 'grafana',
  enabled: true,
  target: { boardId: 'B-9' },
  auth: { mode: 'api_key' },
  routing: { mode: 'broadcast' },
  mapping: {},
  limits: { ratePerMin: 120, payloadCapKb: 128 },
  lifecycle: 'autoclose',
  policy: {},
  version: 1,
  createdBy: 'u',
  createdAt: '2026-08-01T00:00:00.000Z',
  updatedAt: '2026-08-01T00:00:00.000Z',
  endpointPath: '/i/ing_1',
};

describe('the two halves of the fix, separated', () => {
  it('cancelling with an integration present returns to the SOURCE LIST, not its detail', async () => {
    // Kills `cancelToList = () => setCreating(null)`, which lands on the integration detail instead.
    renderDrawer([EXISTING]);
    fireEvent.click(await screen.findByRole('button', { name: /\+ New/ }));
    fireEvent.click(await screen.findByRole('button', { name: /Custom webhook/i }));
    await screen.findByLabelText(/^Name$/i);
    fireEvent.click(screen.getByRole('button', { name: /^Cancel$/ }));
    // SCOPED to the source picker. This asked for a button named /Grafana/i anywhere on screen,
    // which was unique only while the integration rows did not show their own source; once they did,
    // the query matched two and threw. What the test MEANS is "the source list is showing".
    await waitFor(() => {
      const picker = document.querySelector('.source-picker');
      expect(picker, 'Cancel must show the sources again').toBeTruthy();
      expect(
        within(picker as HTMLElement).getByRole('button', { name: /Grafana/i }),
        'the source list must list its vendors',
      ).toBeTruthy();
    });
  });

  it('deleting the last integration falls back to the source list', async () => {
    // Kills the `creating === null && integrations.length === 0` clause. `creating` is set by a
    // useState INITIALISER, which does not re-run when the prop empties — so without that clause the
    // drawer is left on `null` with nothing to show.
    const { rerender } = renderDrawer([EXISTING]);
    await screen.findByRole('button', { name: /\+ New/ });
    rerender(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <OncallSetup projectId="P-1" integrations={[]} onClose={() => {}} />
      </QueryClientProvider>,
    );
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Custom webhook/i })).toBeTruthy();
    });
  });
});

/* -------------------------------------------------------- 6. platform logos in the picker -- */

describe('every integratable platform has a mark, webhooks included', () => {
  /** The catalog's ids, from the core's own source list — not a copy that can drift out of step. */
  const CATALOG_IDS = [
    'alertmanager', 'datadog', 'dynatrace', 'gcp', 'generic', 'grafana', 'pagerduty', 'webhook',
  ];

  it('covers every source the core can offer', () => {
    const missing = CATALOG_IDS.filter((id) => !sourceLogo(id));
    expect(missing, `no mark for: ${missing.join(', ')}`).toEqual([]);
  });

  it('each mark carries a real path and a brand colour', () => {
    for (const [id, logo] of Object.entries(SOURCE_LOGOS)) {
      expect(logo.path.length, `${id} path looks empty`).toBeGreaterThan(50);
      expect(logo.hex, `${id} hex is not 6 hex digits`).toMatch(/^[0-9A-Fa-f]{6}$/);
    }
  });

  it('no two platforms share a mark', () => {
    // A copy-paste between two entries would render Datadog's logo on Dynatrace and still typecheck.
    const paths = Object.values(SOURCE_LOGOS).map((l) => l.path);
    expect(new Set(paths).size).toBe(paths.length);
  });

  it('returns undefined for a source this build has never heard of', () => {
    // The catalog is a network read; a placeholder mark would claim a vendor identity we do not have.
    expect(sourceLogo('some-future-vendor')).toBeUndefined();
  });

  it('renders the mark beside each source in the picker', async () => {
    renderDrawer([]);
    const row = (await screen.findByRole('button', { name: /Custom webhook/i }));
    expect(row.querySelector('svg path'), 'the picker row should carry a logo').toBeTruthy();
  });
});
