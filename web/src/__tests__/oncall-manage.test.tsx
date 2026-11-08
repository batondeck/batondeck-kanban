/**
 * MANAGING AN INTEGRATION THAT EXISTS — the half of the drawer that did not exist.
 *
 * Before this, an integration could be created and disabled and nothing else. The screen said so in
 * its own note: *"Change it, the lifecycle mode and the policy toggles from the MCP surface until
 * those controls land here"* — an operator with a web app was told to go and use a protocol client.
 *
 * The three claims below are the ones where a plausible-looking implementation is wrong:
 *
 *  1. **Edit opens on what is actually configured**, not on defaults. A form that silently resets
 *     routing to `broadcast` because it hydrated from `blankForm()` would look completely normal and
 *     would un-assign an on-call agent on the first unrelated save.
 *  2. **Saving does NOT resend `auth`** unless the operator supplied new material. `buildAuth` MINTS
 *     a fresh hmac secret every time it runs, so an unconditional `auth` in the patch rotates the
 *     integration on every save and breaks the live sender — with the UI reporting success.
 *  3. **Delete is two-step and sends the version.** A DELETE has no body, so the version rides the
 *     query string; without it the core cannot refuse a stale tab.
 *  4. **The four settings nothing read are gone** (P-310, owner decision 6): no toggle for the
 *     unreachable-agent broadcast, the PR, the review cycle or the reviewer agent, and a row stored
 *     with them saves without sending them back, since the core's input schema now refuses them.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, cleanup, waitFor } from '@testing-library/react';
import { OncallSetup } from '../screens/oncall/OncallSetup.js';
import type { Integration } from '../types.js';

// P-440: Setup's write controls follow the caller's project role. These cases are about what the
// controls do, so they run as a role that may change integrations (an admin).
vi.mock('../displayNames.js', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../displayNames.js')>()),
  useMyProjectRole: () => 'admin',
}));


const calls: Array<{ method: string; url: string; body?: Record<string, unknown> }> = [];

const CATALOG = {
  sources: [
    {
      id: 'grafana',
      label: 'Grafana',
      authModes: ['api_key'],
      docsPage: 'oncall-grafana',
      authNote: 'Grafana cannot compute our signature, but it can send a custom header.',
    },
  ],
};

// The keys a row stored before P-310 still carries; nothing reads them, and a save must not resend
// them. Spread, so the type (which no longer names them) does not reject the fixture.
const LEGACY_ROUTING = { fallbackBroadcast: true };

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } });

const EXISTING: Integration = {
  id: 'ing_live',
  projectId: 'P-1',
  name: 'prod-grafana',
  source: 'grafana',
  enabled: true,
  target: { boardId: 'B-9' },
  auth: { mode: 'api_key' },
  // What is ACTUALLY configured — every one of these must survive an unrelated save.
  mapping: { severityMap: { P1: 'urgent' }, labels: ['oncall'] },
  routing: { mode: 'oncall', onCallAgent: 'claude-oncall', ...LEGACY_ROUTING },
  lifecycle: 'autoclose',
  policy: { openPrForReview: true, autoReviewCycle: true, reviewerAgent: 'rev' },
  limits: { ratePerMin: 120, payloadCapKb: 128 },
  version: 4,
  createdBy: 'u',
  createdAt: '2026-08-01T00:00:00.000Z',
  updatedAt: '2026-08-01T00:00:00.000Z',
  endpointPath: '/i/ing_live',
};

function stub(): void {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      calls.push({
        method,
        url,
        ...(init?.body ? { body: JSON.parse(String(init.body)) as Record<string, unknown> } : {}),
      });
      if (url.includes('/api/oncall/sources')) return json(CATALOG);
      if (method === 'DELETE') return json({ integration: EXISTING, deleted: true });
      if (method === 'POST') return json({ integration: { ...EXISTING, version: 5 } });
      return json({ integrations: [], deliveries: [] });
    }),
  );
}

function renderDrawer() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <OncallSetup projectId="P-1" integrations={[EXISTING]} onClose={() => {}} />
    </QueryClientProvider>,
  );
}

const openEditor = async (): Promise<void> => {
  fireEvent.click(await screen.findByRole('button', { name: /edit configuration/i }));
  await screen.findByLabelText(/^Name$/i);
};

afterEach(() => {
  vi.unstubAllGlobals();
  calls.length = 0;
  cleanup();
});

describe('managing an existing integration', () => {
  it('opens the editor on the CONFIGURED values, not on defaults', async () => {
    stub();
    renderDrawer();
    await openEditor();

    expect((screen.getByLabelText(/^Name$/i) as HTMLInputElement).value).toBe('prod-grafana');
    expect((screen.getByLabelText(/target board id/i) as HTMLInputElement).value).toBe('B-9');
    // Routing: the on-call radio is selected and the agent name is present. A form hydrated from
    // blankForm() would show `broadcast` here and quietly drop the agent on the next save.
    expect(screen.getByLabelText(/one named agent/i)).toHaveProperty('checked', true);
    expect((screen.getByLabelText(/^on-call agent$/i) as HTMLInputElement).value).toBe('claude-oncall');
    expect(screen.getByLabelText(/auto-close/i)).toHaveProperty('checked', true);
    // Limits live behind the advanced disclosure; expanding it must show the CONFIGURED numbers,
    // not the defaults 60/256.
    fireEvent.click(screen.getByRole('button', { name: /^Show$/ }));
    expect((await screen.findByLabelText(/deliveries per minute/i)) as HTMLInputElement).toHaveProperty(
      'value',
      '120',
    );
    expect(screen.getByLabelText(/body cap/i)).toHaveProperty('value', '128');
  });

  it('saves without resending auth — an unconditional auth would silently rotate the secret', async () => {
    stub();
    renderDrawer();
    await openEditor();
    fireEvent.change(screen.getByLabelText(/^Name$/i), { target: { value: 'renamed' } });
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }));

    await waitFor(() => expect(calls.some((c) => c.method === 'POST')).toBe(true));
    const save = calls.find((c) => c.method === 'POST')!;
    const patch = save.body!['patch'] as Record<string, unknown>;
    expect(save.body!['version'], 'the save must be version-checked').toBe(4);
    expect(patch['name']).toBe('renamed');
    expect(patch['auth'], 'auth must NOT be resent when no new material was supplied').toBeUndefined();
    // …and the untouched settings are carried through rather than dropped.
    expect(patch['lifecycle']).toBe('autoclose');
    expect(patch['routing']).toMatchObject({ mode: 'oncall', onCallAgent: 'claude-oncall' });
    expect(patch['mapping']).toMatchObject({ severityMap: { P1: 'urgent' }, labels: ['oncall'] });
    // Settings behind the COLLAPSED advanced disclosure are carried too — the form holds them
    // whether or not they are on screen, so an unrelated save cannot reset the rate limit.
    expect(patch['limits']).toMatchObject({ ratePerMin: 120, payloadCapKb: 128 });
    // …but not the retired settings the legacy row carries: the core's schema refuses them now.
    expect(patch, 'policy is no longer an input').not.toHaveProperty('policy');
    expect(patch['routing']).not.toHaveProperty('fallbackBroadcast');
  });

  it('shows none of the four retired settings, on a row that still has them set', async () => {
    stub();
    renderDrawer();
    await openEditor();
    fireEvent.click(screen.getByRole('button', { name: /^Show$/ }));
    // The positive control: the panel the PR/review toggles sat in is open and populated.
    expect(await screen.findByLabelText(/deliveries per minute/i)).toBeTruthy();
    expect(screen.getByLabelText(/^on-call agent$/i)).toBeTruthy();
    for (const label of [/broadcast when the named agent/i, /open a pr/i, /review cycle/i, /reviewer agent/i]) {
      expect(screen.queryByLabelText(label), String(label)).toBeNull();
    }
    for (const id of ['oc-fallback', 'oc-pr', 'oc-review', 'oc-reviewer']) {
      expect(document.getElementById(id), id).toBeNull();
    }
  });

  it('DOES send auth when the operator supplies a new key', async () => {
    // The positive control for the case above: the suppression is about ABSENCE of new material,
    // not about auth being unreachable from the editor.
    stub();
    renderDrawer();
    await openEditor();
    fireEvent.click(screen.getByRole('button', { name: /^Generate$/ }));
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }));

    await waitFor(() => expect(calls.some((c) => c.method === 'POST')).toBe(true));
    const patch = calls.find((c) => c.method === 'POST')!.body!['patch'] as Record<string, unknown>;
    expect(patch['auth']).toMatchObject({ mode: 'api_key' });
    expect((patch['auth'] as { apiKey: string }).apiKey.length).toBeGreaterThan(30);
  });

  it('delete is two-step and carries the version on the query string', async () => {
    stub();
    renderDrawer();
    const del = await screen.findByRole('button', { name: /^Delete$/ });
    fireEvent.click(del);
    // One click must not delete anything — Disable is the reversible action and the two sit
    // adjacent, so an unconfirmed Delete is a misclick away.
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false);

    fireEvent.click(await screen.findByRole('button', { name: /delete for good/i }));
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE')).toBe(true));
    const sent = calls.find((c) => c.method === 'DELETE')!;
    expect(sent.url).toContain('/integrations/ing_live');
    expect(sent.url, 'without the version the core cannot refuse a stale tab').toContain('version=4');
  });

  it('a source-issued secret cannot be rotated by minting one — it asks for the new value', async () => {
    // The core refuses `rotate_integration_secret` with no `externalSecret` on PagerDuty, because a
    // minted whsec_ is a secret PagerDuty will never send. Offering a one-click Rotate for it put
    // the operator whose PD integration is already misbehaving one click from breaking it further.
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = init?.method ?? 'GET';
        calls.push({
          method,
          url,
          ...(init?.body ? { body: JSON.parse(String(init.body)) as Record<string, unknown> } : {}),
        });
        if (url.includes('/api/oncall/sources')) return json(CATALOG);
        return json({ integration: EXISTING, integrations: [], deliveries: [] });
      }),
    );
    const pd: Integration = {
      ...EXISTING,
      source: 'pagerduty',
      auth: { mode: 'hmac', externallyIssued: true },
    };
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <OncallSetup projectId="P-1" integrations={[pd]} onClose={() => {}} />
      </QueryClientProvider>,
    );

    fireEvent.click(await screen.findByRole('button', { name: /rotate secret/i }));
    // Nothing is sent on the click itself — it opens the field instead.
    expect(calls.some((c) => c.url.includes('rotate-secret'))).toBe(false);
    const field = await screen.findByLabelText(/new signing secret from the source/i);
    fireEvent.change(field, { target: { value: 'pd-issued-new-value' } });
    fireEvent.click(screen.getByRole('button', { name: /^Rotate$/ }));

    await waitFor(() => expect(calls.some((c) => c.url.includes('rotate-secret'))).toBe(true));
    const sent = calls.find((c) => c.url.includes('rotate-secret'))!;
    expect(sent.body!['externalSecret']).toBe('pd-issued-new-value');
    expect(sent.body!['version']).toBe(4);
  });

  it('a BatonDeck-minted secret still rotates in one click — the positive control', async () => {
    // The gate above is about `externallyIssued`, not about rotation becoming two-step for everyone.
    stub();
    const hmac: Integration = { ...EXISTING, source: 'generic', auth: { mode: 'hmac' } };
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <OncallSetup projectId="P-1" integrations={[hmac]} onClose={() => {}} />
      </QueryClientProvider>,
    );
    fireEvent.click(await screen.findByRole('button', { name: /rotate secret/i }));
    await waitFor(() => expect(calls.some((c) => c.url.includes('rotate-secret'))).toBe(true));
    expect(calls.find((c) => c.url.includes('rotate-secret'))!.body!['externalSecret']).toBeUndefined();
  });

  it('says what Disable keeps and what Delete gives up', async () => {
    stub();
    renderDrawer();
    expect(await screen.findByText(/pauses deliveries and is reversible/i)).toBeTruthy();
    expect(screen.getByText(/cannot be brought back/i)).toBeTruthy();
  });
});
