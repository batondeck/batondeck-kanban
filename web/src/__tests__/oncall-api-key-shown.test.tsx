/**
 * AN API KEY THE OPERATOR CANNOT LEARN IS A BROKEN INTEGRATION, NOT A COSMETIC GAP.
 *
 * Four of the vendor sources authenticate with `api_key`. The core stores only `sha256(apiKey)` and
 * echoes a `secret` for hmac ONLY, so an API key exists in exactly one place for exactly one moment:
 * the browser that minted it. The create button used to mint it inline inside `create.mutate({...})`,
 * where it was dropped the instant the call returned — so the integration was created, looked healthy
 * in the table, and refused every delivery with `api_key_mismatch`. The whole web suite was green
 * through that, because no test drove the create flow at all.
 *
 * WHAT THIS ASSERTS, at the layer that can observe it (rule 12): the drawer is rendered for real, the
 * gateway is stubbed at `fetch`, and the assertion is that the key SENT TO THE SERVER is the key
 * SHOWN TO THE USER. Asserting merely that "some secret panel appeared" would pass against a panel
 * showing the wrong value, which is the failure one layer down.
 *
 * The flow it drives is now: pick a source → fill the form → Create. The source list arrives from
 * `GET /api/oncall/sources`, so the stub must serve it — which is itself worth having under test,
 * because a screen that cannot load the catalog can offer nothing at all.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, cleanup, waitFor } from '@testing-library/react';
import { OncallSetup } from '../screens/oncall/OncallSetup.js';

// P-440: Setup's write controls follow the caller's project role. These cases are about what the
// controls do, so they run as a role that may change integrations (an admin).
vi.mock('../displayNames.js', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../displayNames.js')>()),
  useMyProjectRole: () => 'admin',
}));


const sent: Array<Record<string, unknown>> = [];

const CATALOG = {
  sources: [
    {
      id: 'grafana',
      label: 'Grafana',
      authModes: ['api_key'],
      docsPage: 'oncall-grafana',
      authNote: 'Grafana cannot compute our signature, but it can send a custom header.',
    },
    {
      id: 'generic',
      label: 'Generic signed JSON',
      authModes: ['hmac'],
      docsPage: 'oncall-generic',
      authNote: 'The full BatonDeck scheme: timestamped and replay-bounded.',
    },
  ],
};

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } });

function stubGateway(createResponse: unknown): void {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/api/oncall/sources')) return json(CATALOG);
      if (init?.method === 'POST' && /\/integrations$/.test(url)) {
        sent.push(JSON.parse(String(init.body)) as Record<string, unknown>);
        return json(createResponse);
      }
      return json({ integrations: [], deliveries: [], boards: [] });
    }),
  );
}

const row = (over: Record<string, unknown>) => ({
  id: 'ing_1',
  name: 'g',
  source: 'grafana',
  enabled: true,
  version: 1,
  endpointPath: '/i/ing_1',
  target: { boardId: 'B-1' },
  routing: { mode: 'broadcast' },
  mapping: {},
  policy: {},
  lifecycle: 'correlate',
  limits: { ratePerMin: 60, payloadCapKb: 256 },
  ...over,
});

function renderDrawer() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <OncallSetup projectId="P-1" integrations={[]} onClose={() => {}} />
    </QueryClientProvider>,
  );
}

/** Pick a source out of the picker, then wait for its form. */
async function pick(label: string): Promise<void> {
  fireEvent.click(await screen.findByRole('button', { name: new RegExp(label, 'i') }));
  await screen.findByLabelText(/^Name$/i);
}

afterEach(() => {
  vi.unstubAllGlobals();
  sent.length = 0;
  cleanup();
});

describe('creating an api_key integration from /oncall', () => {
  it('shows the operator the exact key it sent to the server', async () => {
    stubGateway({ integration: row({ auth: { mode: 'api_key' } }) }); // NO `secret` — the real shape
    renderDrawer();
    await pick('Grafana');

    fireEvent.change(screen.getByLabelText(/^Name$/i), { target: { value: 'grafana-prod' } });
    fireEvent.change(screen.getByLabelText(/target board id/i), { target: { value: 'B-1' } });
    // The mint is now an explicit act, and the value is visible BEFORE submit — an operator can copy
    // it from the field as well as from the shown-once panel.
    fireEvent.click(screen.getByRole('button', { name: /^Generate$/ }));

    fireEvent.click(screen.getByRole('button', { name: /^Create$/ }));

    await waitFor(() => expect(sent.length).toBe(1));
    const auth = sent[0]!['auth'] as { mode: string; apiKey?: string };
    expect(auth.mode).toBe('api_key');
    expect(auth.apiKey, 'no key was minted at all').toBeTruthy();

    // THE ASSERTION: the value on the wire is the value on the screen.
    expect(await screen.findByText(auth.apiKey!)).toBeTruthy();
    // And it is presented as unrecoverable, with the repair that actually applies to an API key —
    // NOT "rotate", which the store refuses for any non-hmac mode.
    expect(screen.getByText(/cannot be shown again/i)).toBeTruthy();
    expect(screen.getByText(/Rotate does not apply to API keys/i)).toBeTruthy();
  });

  it('will not create an api_key integration with no key at all', async () => {
    // The failure this closes: submitting before generating would send `apiKey: ''`, which the core
    // refuses — but only after the operator has filled in the whole form.
    stubGateway({ integration: row({ auth: { mode: 'api_key' } }) });
    renderDrawer();
    await pick('Grafana');
    fireEvent.change(screen.getByLabelText(/^Name$/i), { target: { value: 'no-key' } });
    fireEvent.change(screen.getByLabelText(/target board id/i), { target: { value: 'B-1' } });
    expect(screen.getByRole('button', { name: /^Create$/ })).toHaveProperty('disabled', true);
    expect(screen.getByText(/needs a key/i)).toBeTruthy();
  });

  // POSITIVE CONTROL (rule 5): hmac still uses the SERVER's secret, so this did not turn every
  // create into "show whatever the client made up".
  it('hmac still shows the secret the server minted, not a client-side value', async () => {
    stubGateway({
      integration: row({ id: 'ing_2', source: 'generic', auth: { mode: 'hmac' }, endpointPath: '/i/ing_2' }),
      secret: 'whsec_from_the_server',
      shownOnce: true,
    });
    renderDrawer();
    await pick('Generic signed JSON');

    fireEvent.change(screen.getByLabelText(/^Name$/i), { target: { value: 'gen' } });
    fireEvent.change(screen.getByLabelText(/target board id/i), { target: { value: 'B-1' } });
    fireEvent.click(screen.getByRole('button', { name: /^Create$/ }));

    await waitFor(() => expect(sent.length).toBe(1));
    expect((sent[0]!['auth'] as { apiKey?: string }).apiKey).toBeUndefined();
    expect(await screen.findByText('whsec_from_the_server')).toBeTruthy();
  });
});
