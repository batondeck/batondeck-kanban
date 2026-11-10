/**
 * THE REFUSED PAYLOAD IS VIEWED, NOT DOWNLOADED — and the viewer explains an absence.
 *
 * The delivery LOG ENTRY is kept for `INGEST_DELIVERY_RETENTION_DAYS` (30). The raw body it refused
 * is kept for `INGEST_RAW_RETENTION_DAYS` (7). The row therefore outlives its payload by 23 days,
 * and for all of those days the UI offered a Download link that navigated to a bare 404. Reported
 * from staging as "download button doesn't work" — it worked exactly as built; what was wrong was
 * offering an action whose data had already been deleted, with nothing on screen saying so.
 *
 * A navigation cannot explain a 404. A dialog can, which is why this is a dialog.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { OncallSetup } from '../screens/oncall/OncallSetup.js';
import type { Integration } from '../types.js';

const INTEGRATION = {
  id: 'ing_1', projectId: 'p1', name: 'probe', source: 'generic', enabled: true,
  target: { boardId: 'b1' }, auth: { mode: 'hmac' }, routing: { mode: 'broadcast' },
  limits: { ratePerMin: 60 }, lifecycle: 'per_alert', endpointPath: '/i/ing_1',
  createdAt: '2026-08-01T00:00:00.000Z', updatedAt: '2026-08-01T00:00:00.000Z',
} as unknown as Integration;

/** `rawStatus` decides what the payload route answers — 200 with bytes, 404 once aged out, or
 *  another refusal whose JSON error body is `error`. */
function stub(rawStatus: number, body = '{"hostile":"<img src=x onerror=alert(1)>"}', error: unknown = {}) {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    const json = (b: unknown) => ({ ok: true, status: 200, json: async () => b, text: async () => JSON.stringify(b) }) as Response;
    if (url.includes('/raw')) {
      return { ok: rawStatus < 400, status: rawStatus, text: async () => body, json: async () => error } as Response;
    }
    if (url.includes('/deliveries')) {
      return json({ deliveries: [{
        deliveryId: 'd1#rejected', kind: 'rejected', result: 'rejected', error: 'timestamp_skew',
        taskId: null, ts: '2026-08-22T00:00:00.000Z',
      }] });
    }
    if (url.includes('/oncall/sources') || url.includes('/sources')) {
      return json({ sources: [{ id: 'generic', label: 'Generic signed JSON', authModes: ['hmac'], docsPage: 'g', authNote: '' }] });
    }
    return json({});
  }) as unknown as typeof fetch);
}

const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    {children}
  </QueryClientProvider>
);

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

async function openViewer(rawStatus: number, body?: string, error?: unknown) {
  stub(rawStatus, body, error);
  render(<OncallSetup projectId="p1" integrations={[INTEGRATION]} onClose={() => {}} />, { wrapper });
  const btn = await screen.findByRole('button', { name: /view payload/i }, { timeout: 3_000 });
  fireEvent.click(btn);
  return await screen.findByRole('dialog', { name: /refused payload/i }, { timeout: 3_000 });
}

describe('the refused-payload viewer', () => {
  it('offers a viewer, not a download link', async () => {
    stub(200);
    render(<OncallSetup projectId="p1" integrations={[INTEGRATION]} onClose={() => {}} />, { wrapper });
    await screen.findByRole('button', { name: /view payload/i }, { timeout: 3_000 });
    // The old control was an <a href=…/raw>. A navigation cannot explain a 404, which is the whole bug.
    expect(
      [...document.querySelectorAll('a')].filter((a) => (a.getAttribute('href') ?? '').includes('/raw')),
      'a bare download link is back — it 404s for 23 of the row’s 30 days',
    ).toEqual([]);
  });

  it('EXPLAINS an aged-out payload instead of showing an empty box', async () => {
    const dialog = await openViewer(404);
    await waitFor(() => {
      expect(dialog.textContent, 'the viewer must say WHY there is nothing to show').toMatch(/no longer stored/i);
    });
    expect(dialog.textContent, 'and name the two retentions that differ').toMatch(/30 days/);
    expect(dialog.textContent).toMatch(/7/);
    // Both windows are server settings (BATONDECK_RETENTION_*_DAYS), so the numbers are the defaults.
    expect(dialog.textContent, 'the numbers are defaults, not facts about this server').toMatch(/by default/i);
  });

  it('a refusal that is NOT an aged-out body says what went wrong, not "nothing went wrong"', async () => {
    const dialog = await openViewer(500, undefined, { code: 'INTERNAL', message: 'Internal error' });
    await waitFor(() => {
      expect(dialog.textContent, 'the error is shown').toMatch(/Internal error/);
    });
    expect(dialog.textContent, 'a 500 is not an aged-out payload').not.toMatch(/no longer stored/i);
    expect(dialog.textContent).not.toMatch(/nothing went wrong/i);
  });

  it("a 403 shows the server's reason", async () => {
    const dialog = await openViewer(403, undefined, { code: 'FORBIDDEN', message: 'only a project master reads raw bodies' });
    await waitFor(() => {
      expect(dialog.textContent).toMatch(/only a project master reads raw bodies/);
    });
    expect(dialog.textContent).not.toMatch(/no longer stored/i);
  });

  it('POSITIVE CONTROL: a live payload is shown, and shown as TEXT', async () => {
    // Without this, the expiry assertion above passes on a viewer that never renders anything.
    const dialog = await openViewer(200);
    await waitFor(() => {
      expect(dialog.textContent, 'the payload must actually render').toContain('hostile');
    });
    // Hostile bytes must not become markup. React escapes text children; assert no element was built
    // from the payload rather than trusting that.
    expect(dialog.querySelector('img'), 'the payload was interpreted as markup').toBeNull();
    expect(dialog.querySelector('pre'), 'the payload renders in a pre, uninterpreted').toBeTruthy();
  });

  it('Escape closes the payload dialog, not the setup drawer behind it', async () => {
    // Both listen on window: the dialog's Escape also reached the drawer's handler and closed the
    // whole drawer, losing the delivery log and anything half-typed in the editor.
    stub(200);
    const onClose = vi.fn();
    render(<OncallSetup projectId="p1" integrations={[INTEGRATION]} onClose={onClose} />, { wrapper });
    fireEvent.click(await screen.findByRole('button', { name: /view payload/i }, { timeout: 3_000 }));
    await screen.findByRole('dialog', { name: /refused payload/i });
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByRole('dialog', { name: /refused payload/i }), 'the payload dialog closes').toBeNull();
    expect(onClose, 'the drawer closed along with the dialog').not.toHaveBeenCalled();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose, 'a second Escape closes the drawer as before').toHaveBeenCalledTimes(1);
  });
});
