/**
 * P-440 on On-call Setup: reading integrations needs a workspace admin who is a project master, but
 * changing one is TierStructural (create_integration, update_integration, rotate_integration_secret,
 * delete_integration: a member or above, internal/domain/grants.go). A master who may read them sees
 * Enable/Disable, Rotate, Delete and Save closed with the reason, and sends nothing; a member keeps them.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, cleanup } from '@testing-library/react';
import type { Integration } from '../types.js';

let role: string | undefined = 'master';
vi.mock('../displayNames.js', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../displayNames.js')>()),
  useMyProjectRole: () => role,
}));
const { OncallSetup } = await import('../screens/oncall/OncallSetup.js');

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } });

const EXISTING: Integration = {
  id: 'ing_live',
  projectId: 'P-1',
  name: 'prod-grafana',
  source: 'grafana',
  enabled: true,
  target: { boardId: 'B-9' },
  auth: { mode: 'hmac' },
  mapping: { labels: ['oncall'] },
  routing: { mode: 'broadcast' },
  lifecycle: 'autoclose',
  policy: {},
  limits: { ratePerMin: 60, payloadCapKb: 256 },
  version: 1,
  createdBy: 'u',
  createdAt: '2026-08-01T00:00:00.000Z',
  updatedAt: '2026-08-01T00:00:00.000Z',
  endpointPath: '/i/ing_live',
} as Integration;

const writes: string[] = [];
function stub(): void {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const method = init?.method ?? 'GET';
      if (method !== 'GET') writes.push(`${method} ${String(input)}`);
      if (String(input).includes('/api/oncall/sources')) {
        return json({ sources: [{ id: 'grafana', label: 'Grafana', authModes: ['hmac'], docsPage: 'oncall-grafana' }] });
      }
      return json({ integrations: [], deliveries: [] });
    }),
  );
}

function renderSetup() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <OncallSetup projectId="P-1" integrations={[EXISTING]} onClose={() => {}} />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  writes.length = 0;
  cleanup();
});

describe('On-call Setup follows the integration tools\' tier (P-440)', () => {
  it('a master is shown the integration and its write controls closed, with the reason, and writes nothing', async () => {
    role = 'master';
    stub();
    renderSetup();
    const reason = await screen.findByText('Only a member or an admin can change integrations.');
    const toggle = screen.getByRole('button', { name: 'Disable' });
    const rotate = screen.getByRole('button', { name: 'Rotate secret' });
    const del = screen.getByRole('button', { name: 'Delete' });
    for (const b of [toggle, rotate, del]) {
      expect(b).toBeDisabled();
      expect(b.getAttribute('aria-describedby')).toBe(reason.id);
    }
    fireEvent.click(toggle);
    fireEvent.click(del);
    fireEvent.click(del);
    expect(writes).toEqual([]);
    // Reading it is not closed: Send test event is master tier.
    expect(screen.getByRole('button', { name: 'Send test event' })).not.toBeDisabled();
  });

  it('a member keeps them (the positive control)', async () => {
    role = 'member';
    stub();
    renderSetup();
    expect(await screen.findByRole('button', { name: 'Disable' })).not.toBeDisabled();
    expect(screen.getByRole('button', { name: 'Rotate secret' })).not.toBeDisabled();
    expect(screen.getByRole('button', { name: 'Delete' })).not.toBeDisabled();
    expect(screen.queryByText('Only a member or an admin can change integrations.')).toBeNull();
  });
});
