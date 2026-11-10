/**
 * THE ENDPOINT PANEL SAYS WHERE TO POST AND HOW TO SIGN.
 *
 * It showed only `/hooks/<id>` and told the operator to "paste this path onto the ingest function's
 * host" — a function this product does not have: the route is this server's own. And nothing in the
 * UI named the headers a delivery must carry, so a sender of "Generic signed JSON" had to read the Go
 * source to learn the v0 signature. The panel now shows the full URL as the browser reaches the
 * server, the headers each auth mode reads (with the signing string for hmac), and links the guide.
 */
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, cleanup, within } from '@testing-library/react';
import { OncallSetup } from '../screens/oncall/OncallSetup.js';
import type { Integration } from '../types.js';

const CATALOG = {
  sources: [
    { id: 'generic', label: 'Generic signed JSON', authModes: ['hmac'], docsPage: 'oncall-generic', authNote: 'The full BatonDeck scheme.' },
    { id: 'pagerduty', label: 'PagerDuty', authModes: ['hmac'], docsPage: 'oncall-pagerduty', authNote: 'PagerDuty issues its own secret.' },
    { id: 'grafana', label: 'Grafana', authModes: ['api_key'], docsPage: 'oncall-grafana', authNote: 'A custom header.' },
    { id: 'gcp', label: 'Google Cloud Monitoring', authModes: ['basic'], docsPage: 'oncall-gcp', authNote: 'Basic only.' },
    { id: 'webhook', label: 'Custom webhook', authModes: ['hmac', 'api_key', 'basic', 'jwt'], docsPage: 'oncall-webhook', authNote: 'Any.' },
  ],
};

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } });

beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      if (String(input).includes('/api/oncall/sources')) return json(CATALOG);
      return json({ integrations: [], deliveries: [], boards: [] });
    }),
  );
});
afterEach(() => {
  vi.unstubAllGlobals();
  cleanup();
});

const ID = 'I-0123456789abcdef0123456789abcdef';
const integration = (source: string, mode: Integration['auth']['mode'], extra: Partial<Integration['auth']> = {}): Integration =>
  ({
    id: ID,
    name: `${source}-${mode}`,
    source,
    enabled: true,
    version: 1,
    endpointPath: `/hooks/${ID}`,
    target: { boardId: 'B-1' },
    routing: { mode: 'broadcast' },
    mapping: {},
    policy: {},
    lifecycle: 'correlate',
    limits: { ratePerMin: 60, payloadCapKb: 256 },
    auth: { mode, ...extra },
    createdAt: '2026-11-10T00:00:00.000Z',
    updatedAt: '2026-11-10T00:00:00.000Z',
    createdBy: 'U-1',
    projectId: 'P-1',
  }) as unknown as Integration;

/** Renders the drawer with one integration, selects it, and answers its endpoint panel. */
async function endpointPanel(i: Integration): Promise<HTMLElement> {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <OncallSetup projectId="P-1" integrations={[i]} onClose={() => {}} />
    </QueryClientProvider>,
  );
  fireEvent.click(await screen.findByText(i.name));
  const heading = await screen.findByText(`${i.name} — endpoint`);
  return heading.closest('.panel') ?? heading.parentElement!.parentElement!;
}

describe('the endpoint panel', () => {
  it('shows the full URL, never the "ingest function" this product does not have', async () => {
    const panel = await endpointPanel(integration('generic', 'hmac'));
    expect(within(panel).getByText(`${window.location.origin}/hooks/${ID}`)).toBeTruthy();
    expect(panel.textContent).not.toMatch(/ingest function/i);
  });

  it('names the two hmac headers and the v0 signing string for a BatonDeck-signed source', async () => {
    const panel = await endpointPanel(integration('generic', 'hmac'));
    const text = panel.textContent ?? '';
    expect(text).toContain('X-Batondeck-Timestamp');
    expect(text).toContain('X-Batondeck-Signature');
    expect(text).toContain('v0=');
    expect(text).toMatch(/<timestamp>\.<raw body>/);
    // The worked example carries this integration's own URL, ready to copy.
    expect(text).toContain(`curl -sS '${window.location.origin}/hooks/${ID}'`);
  });

  it("says the same for a custom webhook given its own secret (the form follows the source)", async () => {
    const text = (await endpointPanel(integration('webhook', 'hmac', { externallyIssued: true }))).textContent ?? '';
    expect(text).toContain('X-Batondeck-Signature');
    expect(text).not.toContain('X-PagerDuty-Signature');
  });

  it.each([
    ['pagerduty', 'hmac', 'X-PagerDuty-Signature'],
    ['grafana', 'api_key', 'X-Batondeck-Key'],
    ['gcp', 'basic', 'HTTP Basic'],
    ['webhook', 'jwt', 'Authorization: Bearer'],
  ] as const)('names what a %s %s sender sends', async (source, mode, header) => {
    const text = (await endpointPanel(integration(source, mode, source === 'pagerduty' ? { externallyIssued: true } : {}))).textContent ?? '';
    expect(text).toContain(header);
    if (mode !== 'hmac' || source === 'pagerduty') expect(text).not.toContain('X-Batondeck-Timestamp');
  });

  it('links the on-call guide in the repository, not a /docs/ path this binary does not serve', async () => {
    const panel = await endpointPanel(integration('generic', 'hmac'));
    const link = within(panel).getByRole('link', { name: /on-call guide/i });
    expect(link.getAttribute('href')).toBe('https://github.com/batondeck/batondeck-kanban/blob/main/docs/oncall.md');
  });
});
