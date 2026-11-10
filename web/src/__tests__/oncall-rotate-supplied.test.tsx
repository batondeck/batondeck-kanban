/**
 * ROTATING A SECRET YOU SUPPLIED DOES NOT SAY BATONDECK CANNOT MINT ONE.
 *
 * An hmac integration given its own `externalSecret` rotates by pasting the new value. The note said
 * "This source issued its own signing secret … BatonDeck cannot mint one it would ever send" for
 * every such integration, but only PagerDuty issues its own: a generic or custom-webhook sender signs
 * BatonDeck's v0 form with whatever secret it is given, and the core mints for it on request. The
 * note now names who issued the secret: PagerDuty for a PagerDuty integration, you for the others.
 */
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, cleanup } from '@testing-library/react';
import { OncallSetup } from '../screens/oncall/OncallSetup.js';
import type { Integration } from '../types.js';

// The rotate control is open to a role that may change integrations.
vi.mock('../displayNames.js', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../displayNames.js')>()),
  useMyProjectRole: () => 'admin',
}));

const CATALOG = {
  sources: [
    { id: 'generic', label: 'Generic signed JSON', authModes: ['hmac'], docsPage: 'oncall-generic', authNote: '' },
    { id: 'pagerduty', label: 'PagerDuty', authModes: ['hmac'], docsPage: 'oncall-pagerduty', authNote: '' },
    { id: 'webhook', label: 'Custom webhook', authModes: ['hmac', 'api_key', 'basic', 'jwt'], docsPage: 'oncall-webhook', authNote: '' },
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

const supplied = (source: string): Integration =>
  ({
    id: 'I-supplied',
    name: `${source} alerts`,
    source,
    enabled: true,
    version: 1,
    endpointPath: '/hooks/I-supplied',
    target: { boardId: 'B-1' },
    routing: { mode: 'broadcast' },
    mapping: {},
    policy: {},
    lifecycle: 'correlate',
    limits: { ratePerMin: 60, payloadCapKb: 256 },
    auth: { mode: 'hmac', externallyIssued: true },
    createdAt: '2026-11-10T00:00:00.000Z',
    updatedAt: '2026-11-10T00:00:00.000Z',
    createdBy: 'U-1',
    projectId: 'P-1',
  }) as unknown as Integration;

/** Opens the drawer on i and starts its rotation; answers the rotation panel's text. */
async function rotationNote(i: Integration): Promise<string> {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <OncallSetup projectId="P-1" integrations={[i]} onClose={() => {}} />
    </QueryClientProvider>,
  );
  fireEvent.click(await screen.findByText(i.name));
  fireEvent.click(await screen.findByRole('button', { name: 'Rotate secret' }));
  const input = await screen.findByLabelText('New signing secret from the source');
  return input.closest('.stack, [class*="stack"]')?.textContent ?? input.parentElement!.parentElement!.textContent ?? '';
}

describe('rotating an hmac secret that was supplied', () => {
  it.each(['generic', 'webhook'])('a %s integration: says you supplied it, not that BatonDeck cannot mint one', async (source) => {
    const text = await rotationNote(supplied(source));
    expect(text).not.toMatch(/cannot mint/i);
    expect(text).not.toMatch(/source issued its own/i);
    expect(text).toMatch(/you supplied this integration's signing secret/i);
    expect(text).toMatch(/previous one keeps verifying for 24 hours/i);
  });

  it('a PagerDuty integration: says PagerDuty issued it and must be rotated there first', async () => {
    const text = await rotationNote(supplied('pagerduty'));
    expect(text).toMatch(/PagerDuty issued this integration's signing secret/);
    expect(text).toMatch(/cannot mint one it would ever send/);
  });
});
