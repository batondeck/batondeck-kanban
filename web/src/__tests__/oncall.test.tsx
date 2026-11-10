/**
 * A DELIVERY WHOSE TICKET IS GONE STILL RENDERS (plan §Fold 2).
 *
 * Autoclose DELETES an untouched incident ticket (TODO, unclaimed, never attempted, nothing written) when the
 * source resolves it, and the delivery log keeps the row — so a delivery may carry a `taskId` that
 * no longer resolves. The log shows that id as text: no link to a ticket that 404s, no lookup of
 * it, and nothing on the console. The fixture is the wire shape the BFF answers
 * (`GET /api/oncall/{projectId}/integrations/{integrationId}/deliveries`).
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, cleanup, within, fireEvent, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { OncallSetup } from '../screens/oncall/OncallSetup.js';
import type { Delivery, Integration } from '../types.js';

// P-440: Setup's write controls follow the caller's project role. These cases are about what the
// controls do, so they run as a role that may change integrations (an admin).
vi.mock('../displayNames.js', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../displayNames.js')>()),
  useMyProjectRole: () => 'admin',
}));


const INTEGRATION: Integration = {
  id: 'I-alerts',
  projectId: 'P-1',
  name: 'Alerts',
  source: 'generic',
  enabled: true,
  target: { boardId: 'B-1' },
  auth: { mode: 'hmac', rotatedAt: '2026-10-24T09:00:00.000Z' },
  mapping: {},
  routing: { mode: 'broadcast' },
  lifecycle: 'autoclose',
  policy: {},
  limits: { ratePerMin: 60, payloadCapKb: 256 },
  version: 1,
  createdBy: 'U-root',
  createdAt: '2026-10-24T09:00:00.000Z',
  updatedAt: '2026-10-24T09:00:00.000Z',
  endpointPath: '/hooks/I-alerts',
};

/** The firing that filed T-gone, and the resolve that autoclosed (deleted) it. */
const DELIVERIES: Delivery[] = [
  {
    deliveryId: 'disk-full:resolved', ts: '2026-10-24T09:05:00.000Z', kind: 'resolved', result: 'closed',
    taskId: 'T-gone', fingerprint: 'disk-full', title: 'Disk full', severity: 'critical',
  },
  {
    deliveryId: 'disk-full:firing', ts: '2026-10-24T09:01:00.000Z', kind: 'firing', result: 'created',
    taskId: 'T-gone', fingerprint: 'disk-full', title: 'Disk full', severity: 'critical',
  },
];

const fetched: string[] = [];

function stub(): void {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      fetched.push(url);
      const json = (b: unknown) =>
        new Response(JSON.stringify(b), { status: 200, headers: { 'content-type': 'application/json' } });
      if (url.includes('/api/oncall/sources')) {
        return json({
          sources: [{ id: 'generic', label: 'Generic signed JSON', authModes: ['hmac'], docsPage: 'oncall-generic', authNote: '' }],
        });
      }
      if (url.includes('/deliveries')) return json({ deliveries: DELIVERIES });
      if (url.includes('/integrations')) return json({ integrations: [INTEGRATION] });
      // A lookup of the deleted ticket is what this test refuses; answer it as the BFF would.
      return new Response(JSON.stringify({ code: 'NOT_FOUND', message: 'no such task' }), { status: 404 });
    }),
  );
}

const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    {children}
  </QueryClientProvider>
);

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  fetched.length = 0;
});

describe('the delivery log with a dangling taskId', () => {
  it('shows the old id as text, links nowhere, looks nothing up and logs no error', async () => {
    const errors = vi.spyOn(console, 'error');
    stub();
    render(<OncallSetup projectId="P-1" integrations={[INTEGRATION]} onClose={() => {}} />, { wrapper });

    const cells = await screen.findAllByText('T-gone', {}, { timeout: 3_000 });
    expect(cells).toHaveLength(2);
    for (const cell of cells) {
      expect(cell.tagName, 'the id is a table cell').toBe('TD');
      expect(cell.closest('a'), 'a link to a deleted ticket 404s').toBeNull();
      expect(within(cell).queryAllByRole('link')).toEqual([]);
    }
    // Both rows render whole: the resolve that deleted the ticket, and the firing that filed it.
    const rows = cells.map((c) => c.closest('tr')!.textContent ?? '');
    expect(rows[0]).toMatch(/resolved · disk-full/);
    expect(rows[0]).toMatch(/closed/);
    expect(rows[1]).toMatch(/firing · disk-full/);
    expect(
      [...document.querySelectorAll('a')].filter((a) => (a.getAttribute('href') ?? '').includes('T-gone')),
    ).toEqual([]);
    expect(fetched.filter((u) => u.includes('T-gone')), 'nothing resolves the gone ticket').toEqual([]);
    expect(screen.queryByRole('alert')).toBeNull();
    expect(errors).not.toHaveBeenCalled();
  });
});

describe('a rotated secret is shown once', () => {
  it('leaves the mutation cache when the drawer closes', async () => {
    const SECRET = 'whsec_rotated_once';
    stub();
    const base = globalThis.fetch;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) =>
        String(input).endsWith('/rotate-secret')
          ? new Response(JSON.stringify({ integration: { ...INTEGRATION, version: 2 }, secret: SECRET, shownOnce: true }), {
              status: 200,
              headers: { 'content-type': 'application/json' },
            })
          : base(input, init),
      ),
    );
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const held = () => qc.getMutationCache().getAll().filter((m) => JSON.stringify(m.state.data ?? null).includes(SECRET));
    const view = render(
      <QueryClientProvider client={qc}>
        <OncallSetup projectId="P-1" integrations={[INTEGRATION]} onClose={() => {}} />
      </QueryClientProvider>,
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Rotate secret' }));
    await screen.findByText(SECRET);
    expect(held()).toHaveLength(1);
    view.unmount();
    await waitFor(() => expect(held(), 'the secret outlives the drawer in the mutation cache').toEqual([]));
  });
});

describe('a shown secret is not lost to a stray click or Escape', () => {
  // The panel is the one copy of the secret: closing the drawer unmounts it. The overlay and Escape
  // are the two ways to close it by accident, so while the panel is up neither closes the drawer;
  // "I have stored it" is how it goes away.
  const SECRET = 'whsec_shown_once_guarded';
  function mountWithRotate(): ReturnType<typeof vi.fn> {
    stub();
    const base = globalThis.fetch;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) =>
        String(input).endsWith('/rotate-secret')
          ? new Response(JSON.stringify({ integration: { ...INTEGRATION, version: 2 }, secret: SECRET, shownOnce: true }), {
              status: 200,
              headers: { 'content-type': 'application/json' },
            })
          : base(input, init),
      ),
    );
    const onClose = vi.fn();
    render(<OncallSetup projectId="P-1" integrations={[INTEGRATION]} onClose={onClose} />, { wrapper });
    return onClose;
  }

  it('the overlay and Escape do not close the drawer while the secret is shown', async () => {
    const onClose = mountWithRotate();
    fireEvent.click(await screen.findByRole('button', { name: 'Rotate secret' }));
    await screen.findByText(SECRET);
    const dialog = screen.getByRole('dialog', { name: 'On-call setup' });
    fireEvent.click(dialog.parentElement!);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose, 'the drawer closed over a secret shown once').not.toHaveBeenCalled();
    expect(screen.getByText(SECRET)).toBeInTheDocument();
  });

  it('POSITIVE CONTROL: once it is stored, the overlay and Escape close the drawer again', async () => {
    const onClose = mountWithRotate();
    fireEvent.click(await screen.findByRole('button', { name: 'Rotate secret' }));
    await screen.findByText(SECRET);
    fireEvent.click(screen.getByRole('button', { name: 'I have stored it' }));
    (document.activeElement as HTMLElement | null)?.blur();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('dialog', { name: 'On-call setup' }).parentElement!);
    expect(onClose).toHaveBeenCalledTimes(2);
  });
});
