/**
 * T-396 — the pending-invitation panel in Settings: a sent invite used to leave the product entirely.
 * Rendering is NOT authorization — the BFF's live membership read is the only thing that refuses, and
 * its guards are `gateway/ui-backend/test/invitations{,-scope}.test.ts`.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

afterEach(cleanup);

const INVITATION = {
  id: 'invitation_1',
  email: 'bob@example.com',
  state: 'pending' as const,
  createdAt: '2026-08-10T00:00:00.000Z',
  expiresAt: '2026-09-01T00:00:00.000Z',
};

/** Every path the SPA fetched, so a refusal case can assert the request was never MADE. */
let fetched: string[] = [];

function stubGateway(permissions: string[]): void {
  fetched = [];
  const routes: Record<string, unknown> = {
    '/api/session': {
      identity: 'alice@example.com',
      csrfToken: 'csrf-1',
      isAdmin: false,
      subscriptionsEnabled: true,
      uiSettings: {},
      permissions,
    },
    '/api/tenants/invitations': { invitations: [INVITATION] },
    '/api/projects': { projects: [] },
  };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input).split('?')[0];
      fetched.push(path);
      const body = routes[path] ?? { notifications: [], unread: 0, sessions: [], boards: [], projects: [] };
      return { ok: true, status: 200, json: async () => body } as Response;
    }),
  );
}

async function renderSettings(): Promise<void> {
  const { QueryClient, QueryClientProvider } = await import('@tanstack/react-query');
  const { AuthProvider } = await import('../auth.js');
  const { App } = await import('../App.js');
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/settings']}>
        <AuthProvider>
          <App />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('pending invitations on Settings', () => {
  it('POSITIVE CONTROL: a tenant admin sees the invitee, with Resend and Revoke', async () => {
    stubGateway(['batondeck:admin']);
    await renderSettings();

    // 15s, not the 5s default: a COLD run of this file is ~17s wall and the first `findBy` timed out on
    // 1 run in 3. A ceiling, not a delay — a passing run still returns as soon as the row appears.
    expect(await screen.findByText('bob@example.com', undefined, { timeout: 15_000 })).toBeTruthy();
    expect(screen.getByText('Pending invitations')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Resend' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Revoke' })).toBeTruthy();
  });

  /**
   * Goes red on dropping `enabled: admin` or the `if (!admin) return null`. Asserts the FETCH was never
   * made, not merely that nothing rendered — a member silently polling a 403 hides behind a clean screen.
   */
  it('REFUSAL: an ordinary member sees no panel and never requests the list', async () => {
    stubGateway(['batondeck:projects']);
    await renderSettings();

    /**
     * Wait for `/api/projects`, NOT `/api/session`: session lands before Settings renders, so asserting
     * there passes vacuously — measured GREEN with `enabled: admin` deleted. `/api/projects` mounts in
     * the same commit as `InviteTeammate`, so the absence below is evidence rather than timing.
     */
    await waitFor(() => expect(fetched).toContain('/api/projects'));
    expect(screen.queryByText('Pending invitations')).toBeNull();
    expect(screen.queryByText('bob@example.com')).toBeNull();
    expect(fetched, 'a member must not poll an endpoint that refuses them').not.toContain(
      '/api/tenants/invitations',
    );
  });
});
