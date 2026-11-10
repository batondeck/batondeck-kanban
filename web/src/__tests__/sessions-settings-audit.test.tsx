/**
 * R2.5: the Agents, Settings and Workspace Log screens over the session, CLI-token and audit routes.
 * Rendering is not authorization: the BFF's 403 is the control (internal/api/me_test.go). These pin
 * what the screens show from what the routes answer, and which routes they call.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, within, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { presenceSessions } from '../api/stream.js';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

let fetched: string[] = [];
// A fake minted token, assembled so no line holds the token format.
const MINTED = ['bd', 'fedcba9876543210', 'secret-shown-once'].join('_');
const json = (body: unknown) => ({ ok: true, status: 200, json: async () => body }) as Response;

const session = (over: Record<string, unknown>) => ({
  sessionKey: 'U-1::agent-a', agentId: 'agent-a', agentName: 'agent-a', identityId: 'U-1', kind: 'service_account',
  firstSeenAt: '2026-10-24T08:00:00.000Z', lastSeenAt: '2026-10-24T08:00:00.000Z', sessionCount: 1, requestCount: 69,
  toolCounts: { list_tasks: 69 }, ...over,
});

async function mount(path: string, admin: boolean, routes: Record<string, (init?: RequestInit) => unknown>, prefill?: (qc: import('@tanstack/react-query').QueryClient) => void): Promise<import('@tanstack/react-query').QueryClient> {
  fetched = [];
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const p = String(input).split('?')[0]!;
    fetched.push(`${init?.method ?? 'GET'} ${String(input)}`);
    if (p === '/api/session') return json({ identity: 'U-1', csrfToken: 'c', isAdmin: admin, permissions: admin ? ['batondeck:superadmin'] : [], subscriptionsEnabled: false, uiSettings: {} });
    const key = `${init?.method ?? 'GET'} ${p}`;
    if (routes[key]) return json(routes[key]!(init));
    return json({ projects: [], boards: [], sessions: [], tokens: [], events: [], tasks: [] });
  }));
  const { QueryClient, QueryClientProvider } = await import('@tanstack/react-query');
  const { AuthProvider } = await import('../auth.js');
  const { App } = await import('../App.js');
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  prefill?.(qc);
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <App />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return qc;
}

describe('Settings: CLI tokens and your MCP sessions', () => {
  it('counts the live token and the logged session, shows a minted token once, and calls no marketplace route', async () => {
    const tokens = [{ id: '0123456789abcdef', label: 'cli', createdAt: '2026-10-24T08:00:00Z', expiresAt: '2026-11-23T08:00:00Z', revokedAt: null, status: 'live' }];
    await mount('/settings', false, {
      'GET /api/me/cli-tokens': () => ({ tokens }),
      'POST /api/me/cli-tokens': () => ({ ...tokens[0], id: 'fedcba9876543210', label: 'laptop', token: MINTED, shownOnce: true }),
      'GET /api/agents/sessions': () => ({ sessions: [session({})] }),
      'GET /api/me/settings': () => ({ maxTicketTokens: null, tokenPolicy: null, starredAssets: [], defaults: { maxTicketTokens: 50000, tokenPolicy: 'warn', charsPerToken: 4 }, estimated: true }),
    });
    expect(await screen.findByText('1 live')).toBeInTheDocument();
    expect(await screen.findByText('1 logged')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Token label'), { target: { value: 'laptop' } });
    fireEvent.click(screen.getByRole('button', { name: 'Mint token' }));
    expect(await screen.findByDisplayValue(MINTED)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(screen.queryByDisplayValue(MINTED)).toBeNull();
    expect(screen.queryByText(/marketplace/i)).toBeNull();
    expect(fetched.filter((f) => f.includes('marketplace'))).toEqual([]);
    expect(fetched).toContain('POST /api/me/cli-tokens');
  });

  it('claims no last use for a token: the server records none, so there is no Last used column', async () => {
    const tokens = [{ id: '0123456789abcdef', label: 'cli', createdAt: '2026-10-24T08:00:00Z', expiresAt: '2026-11-23T08:00:00Z', revokedAt: null, status: 'live' }];
    await mount('/settings', false, { 'GET /api/me/cli-tokens': () => ({ tokens }) });
    const row = (await screen.findByText('cli', { selector: 'td' })).closest('tr')!;
    const table = row.closest('table')!;
    expect(within(table).getAllByRole('columnheader').map((h) => h.textContent)).toEqual(['Label', 'Status', 'Expires', '']);
    expect(within(row).queryByText('never')).toBeNull();
  });
});

describe('Settings: the shared session cache keeps the device rows', () => {
  // Settings reads the ['agentSessions'] entry Agents merges presence frames into. A plain list
  // answer replaced the entry, so visiting Settings dropped every live device row from Agents until
  // the directory next changed (P-394).
  it('its list answer keeps the device rows a presence frame put in the cache', async () => {
    const frame = presenceSessions({ sessions: [{ pid: 4242, role: 'worker', identityId: 'U-1', agentId: 'claude', registeredAt: '2026-10-24T08:00:00Z' }] }, '2026-10-24T08:00:00Z');
    const qc = await mount('/settings', false, { 'GET /api/agents/sessions': () => ({ sessions: [session({})] }) }, (q) =>
      q.setQueryData(['agentSessions'], { sessions: frame }, { updatedAt: 0 }),
    );
    await waitFor(() => expect(fetched.some((f) => f.includes('/api/agents/sessions'))).toBe(true));
    await waitFor(() => expect(qc.isFetching({ queryKey: ['agentSessions'] })).toBe(0));
    const keys = qc.getQueryData<{ sessions: Array<{ sessionKey: string }> }>(['agentSessions'])!.sessions.map((r) => r.sessionKey);
    expect(keys, 'the device row was dropped by the Settings refetch').toEqual(expect.arrayContaining(['U-1::agent-a', 'device:4242']));
    // Settings itself still lists only the MCP session (a device row is not something it can end).
    expect(await screen.findByText('1 logged')).toBeInTheDocument();
  });
});

describe('Settings: the minted plaintext leaves memory', () => {
  it('is in no mutation once Done is pressed, nor after the screen unmounts', async () => {
    const qc = await mount('/settings', false, {
      'POST /api/me/cli-tokens': () => ({ id: 'fedcba9876543210', label: 'laptop', createdAt: '2026-10-24T08:00:00Z', expiresAt: '2026-11-23T08:00:00Z', revokedAt: null, status: 'live', token: MINTED, shownOnce: true }),
    });
    const held = () => qc.getMutationCache().getAll().filter((m) => JSON.stringify(m.state.data ?? null).includes(MINTED)).length;
    fireEvent.change(await screen.findByLabelText('Token label'), { target: { value: 'laptop' } });
    fireEvent.click(screen.getByRole('button', { name: 'Mint token' }));
    expect(await screen.findByDisplayValue(MINTED)).toBeInTheDocument();
    expect(held(), 'the mint answer is held while it is shown').toBe(1);
    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    await waitFor(() => expect(held(), 'held after Done').toBe(0));
    cleanup();
    await waitFor(() => expect(held(), 'held after unmount').toBe(0));
  });
});

describe('Agents: the delivered cursor', () => {
  it('a device row shows its last delivered cursor, a never-rung one "none", an MCP session "—"', async () => {
    await mount('/agents', false, {
      'GET /api/agents/sessions': () => ({
        sessions: [
          session({}),
          ...presenceSessions({ sessions: [
            { pid: 4242, role: 'worker', identityId: 'U-1', agentId: 'claude', adapter: 'claude-code', registeredAt: '2026-10-24T08:00:00Z', lastDeliveredCursor: 42 },
            { pid: 4343, role: 'master', identityId: 'U-1', agentId: 'claude-m', registeredAt: '2026-10-24T08:00:00Z', lastDeliveredCursor: 0 },
          ] }, '2026-10-24T08:00:00Z'),
        ],
      }),
    });
    await waitFor(() => expect(screen.getAllByTestId('delivered-cursor')).toHaveLength(3));
    const cells = screen.getAllByTestId('delivered-cursor').map((c) => c.textContent);
    expect(cells.sort()).toEqual(['#42', 'none', '—']);
    expect(screen.getByRole('columnheader', { name: 'Delivered' })).toBeInTheDocument();
    expect(fetched.filter((f) => f.includes('/overview'))).toEqual([]);
  });

  it('a session list answering after a presence frame keeps the frame\'s device rows', async () => {
    const frame = presenceSessions({ sessions: [{ pid: 4242, role: 'worker', identityId: 'U-1', agentId: 'claude', adapter: 'claude-code', registeredAt: '2026-10-24T08:00:00Z', lastDeliveredCursor: 8 }] }, '2026-10-24T08:00:00Z');
    await mount('/agents', false, { 'GET /api/agents/sessions': () => ({ sessions: [session({})] }) }, (qc) => qc.setQueryData(['agentSessions'], { sessions: frame }));
    await waitFor(() => expect(screen.getAllByText('69')).not.toHaveLength(0));
    expect(screen.getAllByTestId('delivered-cursor').map((c) => c.textContent).sort()).toEqual(['#8', '—']);
  });

  it('presenceSessions carries the frame cursor, 0 when the frame has none', () => {
    const rows = presenceSessions({ sessions: [{ pid: 1, role: 'worker', identityId: 'U-1', agentId: 'a', registeredAt: 'x', lastDeliveredCursor: 7 }, { pid: 2, role: 'worker', identityId: 'U-1', agentId: 'b', registeredAt: 'x' }] }, 'now');
    expect(rows.map((r) => [r.sessionKey, r.lastDeliveredCursor])).toEqual([['device:1', 7], ['device:2', 0]]);
  });
});

describe('Agents: how a session authenticated and whether it is online (P-257, drifts D6 and D9)', () => {
  it('an API-token session says "api token", an OAuth one "oauth"; a device row is online with an old registration, a stale MCP row is not', async () => {
    const stale = '2020-01-01T00:00:00.000Z';
    await mount('/agents', false, {
      'GET /api/agents/sessions': () => ({
        sessions: [
          session({ sessionKey: 'U-1::agent-a', kind: 'api_token', lastSeenAt: stale }),
          session({ sessionKey: 'U-1::agent-o', agentId: 'agent-o', agentName: 'agent-o', kind: 'oauth_user', lastSeenAt: stale }),
          ...presenceSessions({ sessions: [{ pid: 4242, role: 'worker', identityId: 'U-1', agentId: 'claude', registeredAt: stale }] }, new Date().toISOString()),
        ],
      }),
    });
    await waitFor(() => expect(screen.getByText('api token')).toBeInTheDocument());
    expect(screen.getByText('api token')).toHaveAttribute('title', 'Connected with an API token');
    expect(screen.getByText('oauth')).toHaveAttribute('title', 'Connected with an OAuth access token');
    expect(screen.queryByText('service')).toBeNull();
    expect(screen.getByText('3 logged ·', { exact: false })).toHaveTextContent('3 logged · 1 online');
  });
});

describe('Workspace Log: the audit panel', () => {
  const rows = { days: 90, entries: [{ ts: '2026-10-24T08:00:00.000Z', action: 'task.deleted', actor: 'U-1', agent: 'claude', subject: 'T-7', projectId: 'P-1', result: 'ok', errorCode: '', data: '{}' }] };

  it('the workspace admin sees the rows list_audit_log answers', async () => {
    await mount('/activity', true, { 'GET /api/audit': () => rows });
    const table = await screen.findByRole('table');
    const row = within(table).getByTestId('audit-row');
    expect(within(row).getByText('task.deleted')).toBeInTheDocument();
    expect(within(row).getByText('T-7')).toBeInTheDocument();
    expect(within(row).getByText('U-1 (claude)')).toBeInTheDocument();
  });

  it('pages past the first rows: Load more asks for the nextCursor the route answered and appends what it returns', async () => {
    const older = { days: 90, entries: [{ ...rows.entries[0]!, ts: '2026-10-20T08:00:00.000Z', action: 'token.revoked', subject: 'T-3' }] };
    await mount('/activity', true, {
      'GET /api/audit': () => (fetched.at(-1)!.includes('cursor=c1') ? older : { ...rows, nextCursor: 'c1' }),
    });
    const table = await screen.findByRole('table');
    expect(within(table).getAllByTestId('audit-row')).toHaveLength(1);
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => expect(within(table).getAllByTestId('audit-row')).toHaveLength(2));
    expect(within(table).getByText('token.revoked')).toBeInTheDocument();
    expect(fetched.filter((f) => f.includes('/api/audit')).at(-1)).toContain('cursor=c1');
    // The last page answered no nextCursor: nothing more to load.
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
  });

  it('REFUSAL: a member sees no audit panel and never asks for it', async () => {
    await mount('/activity', false, { 'GET /api/audit': () => rows });
    await screen.findByRole('heading', { name: 'Workspace Log' });
    expect(screen.queryByText('Audit log')).toBeNull();
    expect(fetched.filter((f) => f.includes('/api/audit'))).toEqual([]);
  });
});
