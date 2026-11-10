/**
 * EDIT SAVES WHAT THE OPERATOR CHANGED, and nothing blocks a save it does not need.
 *
 * The drawer's editor is hydrated from the integration the server describes (`formFrom`). Each case
 * below is a setting that view cannot carry or a clear the patch could not express, where Save
 * either refused for no reason or answered success and changed nothing.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, cleanup, waitFor } from '@testing-library/react';
import { OncallSetup } from '../screens/oncall/OncallSetup.js';
import type { Integration } from '../types.js';

// Setup's write controls follow the caller's project role; these cases are about what Save sends.
vi.mock('../displayNames.js', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../displayNames.js')>()),
  useMyProjectRole: () => 'admin',
}));

const calls: Array<{ method: string; url: string; body?: Record<string, unknown> }> = [];

const CATALOG = {
  sources: [
    { id: 'webhook', label: 'Custom webhook', authModes: ['hmac', 'api_key', 'basic', 'jwt'], docsPage: 'oncall-webhook', authNote: 'Any tool.' },
    { id: 'grafana', label: 'Grafana', authModes: ['api_key'], docsPage: 'oncall-grafana', authNote: 'A header.' },
  ],
};

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } });

const BASE: Integration = {
  id: 'ing_1',
  projectId: 'P-1',
  name: 'alerts',
  source: 'webhook',
  enabled: true,
  target: { boardId: 'B-9' },
  auth: { mode: 'jwt' },
  mapping: { extract: { fingerprint: ['alert.id'] } },
  routing: { mode: 'broadcast' },
  lifecycle: 'correlate',
  policy: {},
  limits: { ratePerMin: 60, payloadCapKb: 256 },
  version: 3,
  createdBy: 'u',
  createdAt: '2026-08-01T00:00:00.000Z',
  updatedAt: '2026-08-01T00:00:00.000Z',
  endpointPath: '/hooks/ing_1',
};

function mount(integration: Integration): void {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      calls.push({ method, url, ...(init?.body ? { body: JSON.parse(String(init.body)) as Record<string, unknown> } : {}) });
      if (url.includes('/api/oncall/sources')) return json(CATALOG);
      if (method === 'POST') return json({ integration: { ...integration, version: integration.version + 1 } });
      return json({ integrations: [], deliveries: [] });
    }),
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <OncallSetup projectId="P-1" integrations={[integration]} onClose={() => {}} />
    </QueryClientProvider>,
  );
}

const openEditor = async (): Promise<void> => {
  fireEvent.click(await screen.findByRole('button', { name: /edit configuration/i }));
  await screen.findByLabelText(/^Name$/i);
};

const saveButton = (): HTMLButtonElement => screen.getByRole('button', { name: /save changes/i }) as HTMLButtonElement;

const sentPatch = async (): Promise<Record<string, unknown>> => {
  await waitFor(() => expect(calls.some((c) => c.method === 'POST')).toBe(true));
  return calls.find((c) => c.method === 'POST')!.body!['patch'] as Record<string, unknown>;
};

afterEach(() => {
  vi.unstubAllGlobals();
  calls.length = 0;
  cleanup();
});

describe('a jwt integration', () => {
  it('saves a rename without the issuer, audience and JWKS URL the view cannot show', async () => {
    // The server's auth view is {mode, rotatedAt, externallyIssued, dualAcceptUntil}: the editor
    // opens with the three jwt fields blank. Blank on an edit means "keep what is configured".
    mount(BASE);
    await openEditor();
    fireEvent.change(screen.getByLabelText(/^Name$/i), { target: { value: 'renamed' } });
    expect(screen.queryByText(/JWT mode needs/i), 'no jwt refusal on an unrelated edit').toBeNull();
    expect(saveButton().disabled, 'Save must not wait on values the form cannot show').toBe(false);
    fireEvent.click(saveButton());
    const patch = await sentPatch();
    expect(patch['name']).toBe('renamed');
    expect(patch['auth'], 'blank jwt fields keep the configured ones: auth is not resent').toBeUndefined();
  });

  it('still asks for all three once any one is typed — a partial set would be refused', async () => {
    mount(BASE);
    await openEditor();
    fireEvent.change(screen.getByLabelText(/^Audience$/i), { target: { value: 'batondeck' } });
    expect(screen.getByText(/JWT mode needs an issuer, an audience and a JWKS URL/i)).toBeTruthy();
    expect(saveButton().disabled).toBe(true);
  });

  it('sends the new jwt settings when all three are given', async () => {
    mount(BASE);
    await openEditor();
    fireEvent.change(screen.getByLabelText(/^Issuer$/i), { target: { value: 'https://idp.example.com/' } });
    fireEvent.change(screen.getByLabelText(/^Audience$/i), { target: { value: 'batondeck' } });
    fireEvent.change(screen.getByLabelText(/^JWKS URL$/i), { target: { value: 'https://idp.example.com/jwks.json' } });
    fireEvent.click(saveButton());
    const patch = await sentPatch();
    expect(patch['auth']).toEqual({
      mode: 'jwt',
      jwt: { issuer: 'https://idp.example.com/', audience: 'batondeck', jwksUrl: 'https://idp.example.com/jwks.json' },
    });
  });

  it('switching an integration TO jwt on edit still needs all three', async () => {
    mount({ ...BASE, auth: { mode: 'hmac' } });
    await openEditor();
    fireEvent.click(screen.getByLabelText(/^jwt$/));
    expect(screen.getByText(/JWT mode needs an issuer, an audience and a JWKS URL/i)).toBeTruthy();
    expect(saveButton().disabled).toBe(true);
  });

  it('says blank keeps the configured values only when jwt IS configured', async () => {
    mount(BASE);
    await openEditor();
    expect(screen.getByText(/leave all three\s+blank to keep them/i), 'a jwt integration: blank keeps them').toBeTruthy();
    cleanup();
    calls.length = 0;
    // Switching an hmac integration to jwt: nothing is configured to keep, and blank is refused.
    mount({ ...BASE, auth: { mode: 'hmac' } });
    await openEditor();
    fireEvent.click(screen.getByLabelText(/^jwt$/));
    expect(screen.queryByText(/blank to keep them/i), 'no "keep them" on a switch to jwt').toBeNull();
  });
});

describe('switching an integration to a key mode on edit', () => {
  // The core refuses api_key or basic without a key ("requires apiKey"), and a mode switch has no
  // current key to keep: the form asks for one rather than offering "leave blank".
  for (const mode of ['api_key', 'basic'] as const) {
    it(`asks for a key on a switch from hmac to ${mode}`, async () => {
      mount({ ...BASE, auth: { mode: 'hmac' } });
      await openEditor();
      fireEvent.click(screen.getByLabelText(new RegExp(`^${mode}$`)));
      expect(screen.queryByText(/leave blank to keep the current one/i), 'no current key to keep').toBeNull();
      expect(screen.getByText(/this mode needs a key/i)).toBeTruthy();
      expect(saveButton().disabled).toBe(true);
      fireEvent.click(screen.getByRole('button', { name: /^Generate$/ }));
      expect(saveButton().disabled).toBe(false);
      fireEvent.click(saveButton());
      const patch = await sentPatch();
      expect((patch['auth'] as { mode: string; apiKey: string }).mode).toBe(mode);
      expect((patch['auth'] as { apiKey: string }).apiKey.length).toBeGreaterThanOrEqual(8);
    });
  }

  it('POSITIVE CONTROL: an api_key integration keeps its key on a blank edit', async () => {
    mount({ ...BASE, auth: { mode: 'api_key' } });
    await openEditor();
    expect(screen.getByText(/leave blank to keep the current one/i)).toBeTruthy();
    expect(screen.queryByText(/this mode needs a key/i)).toBeNull();
    fireEvent.change(screen.getByLabelText(/^Name$/i), { target: { value: 'renamed' } });
    fireEvent.click(saveButton());
    expect((await sentPatch())['auth']).toBeUndefined();
  });
});

describe('clearing a setting on edit', () => {
  // The core replaces the stored mapping whole with any mapping the patch carries (`{}` clears it),
  // reads an absent one as "unchanged", and clears the target column only on an explicit null. A
  // patch that just OMITS what was cleared is answered 200 and changes nothing. internal/api's
  // TestOncallUpdateReplacesMappingAndClearsColumnOverREST pins the core's half on every engine.
  const GRAFANA: Integration = {
    ...BASE,
    source: 'grafana',
    auth: { mode: 'api_key' },
    target: { boardId: 'B-9', columnId: 'C-triage' },
    mapping: { labels: ['oncall'] },
  };

  it('clearing the only stamped label clears it, rather than saving nothing', async () => {
    mount(GRAFANA);
    await openEditor();
    fireEvent.click(screen.getByRole('button', { name: /^Show$/ }));
    fireEvent.change(await screen.findByLabelText(/labels stamped on every ticket/i), { target: { value: '' } });
    fireEvent.click(saveButton());
    const patch = await sentPatch();
    expect(patch, 'an omitted mapping is "unchanged" to the core').toHaveProperty('mapping');
    expect(patch['mapping'], 'an empty mapping replaces the stored one').toEqual({});
  });

  it('blanking the target column sends null, which is what clears it', async () => {
    mount(GRAFANA);
    await openEditor();
    expect((screen.getByLabelText(/target column id/i) as HTMLInputElement).value).toBe('C-triage');
    fireEvent.change(screen.getByLabelText(/target column id/i), { target: { value: '' } });
    fireEvent.click(saveButton());
    const patch = await sentPatch();
    expect(patch).toHaveProperty('columnId', null);
  });

  it('POSITIVE CONTROL: an unrelated save keeps the column and the label', async () => {
    mount(GRAFANA);
    await openEditor();
    fireEvent.change(screen.getByLabelText(/^Name$/i), { target: { value: 'renamed' } });
    fireEvent.click(saveButton());
    const patch = await sentPatch();
    expect(patch['columnId']).toBe('C-triage');
    expect(patch['mapping']).toEqual({ labels: ['oncall'] });
  });

  it('an integration with no column and no mapping sends neither', async () => {
    mount({ ...GRAFANA, target: { boardId: 'B-9' }, mapping: {} });
    await openEditor();
    fireEvent.change(screen.getByLabelText(/^Name$/i), { target: { value: 'renamed' } });
    fireEvent.click(saveButton());
    const patch = await sentPatch();
    expect(patch).not.toHaveProperty('columnId');
    expect(patch).not.toHaveProperty('mapping');
  });
});
