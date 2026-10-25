/**
 * P-287: the newcomer path. The Connect MCP dialog points at THIS server with an API token, the
 * sign-in screen says how agents authenticate and shows the `user add` hint on a server with no
 * account, the empty Projects screen lists the next steps, and Settings leads with Columns, Members
 * and CLI tokens.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, within, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

// Assembled so this file is not itself a hit for the lexical guard below.
const HOSTED = ['mcp', 'batondeck', 'com'].join('.');
const FORBIDDEN = [HOSTED, ['service', 'accounts'].join(' ')];

const SOURCES = import.meta.glob(['../**/*.ts', '../**/*.tsx', '!../__tests__/**'], {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

const json = (body: unknown, status = 200) => ({ ok: status < 400, status, statusText: '', json: async () => body }) as Response;

async function mount(path: string, opts: { signedIn: boolean; admin?: boolean; oauth?: boolean; noUsers?: boolean }): Promise<import('@tanstack/react-query').QueryClient> {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const p = String(input).split('?')[0]!;
    if (p === '/api/public-config') return json({ demoLogin: false, workosLogin: false, oauth: opts.oauth ?? false, noUsers: opts.noUsers ?? false });
    if (p === '/api/session') {
      if (!opts.signedIn) return json({ code: 'UNAUTHENTICATED', message: 'unauthenticated' }, 401);
      const perms = opts.admin ? ['batondeck:superadmin'] : [];
      return json({ identity: 'U-1', csrfToken: 'c', isAdmin: !!opts.admin, permissions: perms, subscriptionsEnabled: false, uiSettings: {} });
    }
    if (p === '/api/me/settings') return json({ maxTicketTokens: null, tokenPolicy: null, starredAssets: [], defaults: { maxTicketTokens: 50000, tokenPolicy: 'warn', charsPerToken: 4 }, estimated: true });
    return json({ projects: [], boards: [], sessions: [], tokens: [], users: [], events: [], tasks: [] });
  }));
  const { QueryClient, QueryClientProvider } = await import('@tanstack/react-query');
  const { AuthProvider } = await import('../auth.js');
  const { App } = await import('../App.js');
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
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

describe('no hosted URL and no service accounts anywhere in web/src', () => {
  it('scans every source file', () => {
    expect(Object.keys(SOURCES).length).toBeGreaterThan(50);
    const hits = Object.entries(SOURCES).flatMap(([f, src]) => FORBIDDEN.filter((w) => src.includes(w)).map((w) => `${f}: ${w}`));
    expect(hits).toEqual([]);
  });
});

describe('Connect MCP dialog', () => {
  async function open(oauth: boolean): Promise<HTMLElement> {
    const qc = await mount('/projects', { signedIn: true, oauth });
    fireEvent.click(await screen.findByRole('button', { name: 'Connect an agent' }));
    // The OAuth line hangs on public-config: assert on it only once that answer is in.
    await waitFor(() => expect(qc.getQueryState(['publicConfig'])?.status).toBe('success'));
    return screen.getByRole('dialog', { name: 'Connect an MCP client' });
  }

  it('every client snippet names this origin + /mcp and a bearer token, and nothing hosted', async () => {
    const dlg = await open(false);
    const url = `${window.location.origin}/mcp`;
    for (const tab of ['Claude Code', 'Codex CLI', 'Gemini CLI', 'Cursor']) {
      fireEvent.click(within(dlg).getByRole('button', { name: tab }));
      const pres = dlg.querySelectorAll('pre');
      const server = [...pres].map((p) => p.textContent ?? '').find((t) => t.includes('/mcp'));
      expect(server, tab).toContain(url);
      expect(server, tab).toMatch(/Authorization"?: "?Bearer (\$BATONDECK_TOKEN|\$\{env:BATONDECK_TOKEN\})|--bearer-token-env-var BATONDECK_TOKEN/);
      expect(dlg.textContent, tab).not.toContain(HOSTED);
      expect(dlg.textContent, tab).not.toMatch(/marketplace|mcp-remote/);
    }
    expect(within(dlg).getByRole('link', { name: 'create a token' })).toHaveAttribute('href', '/settings#cli-tokens');
    expect(dlg.textContent).toContain('Only Claude Code with the plugin gets the device wake');
    expect(dlg.textContent).not.toMatch(/OAuth is on/);
  });

  it('shows the OAuth connector path only when public-config says OAuth is on', async () => {
    const dlg = await open(true);
    await waitFor(() => expect(dlg.textContent).toMatch(/OAuth is on/));
  });
});

describe('Sign-in', () => {
  it('a server with no account shows the user add hint, and agents are told they use API tokens', async () => {
    await mount('/projects', { signedIn: false, noUsers: true });
    const note = await screen.findByRole('note');
    expect(note.textContent).toContain('batondeck-kanban user add --admin <login>');
    expect(screen.getByText(/AI agents use API tokens/)).toBeInTheDocument();
    expect(document.querySelectorAll('a[href="/"]')).toHaveLength(0);
  });

  it('a server with an account shows no hint', async () => {
    const qc = await mount('/projects', { signedIn: false, noUsers: false });
    await screen.findByText(/AI agents use API tokens/);
    await waitFor(() => expect(qc.getQueryState(['publicConfig'])?.status).toBe('success'));
    expect(screen.queryByRole('note')).toBeNull();
  });
});

describe('Projects: the empty workspace', () => {
  it('lists the three next steps', async () => {
    await mount('/projects', { signedIn: true });
    const steps = await screen.findByRole('region', { name: 'Get started' });
    const items = within(steps).getAllByRole('listitem').map((li) => li.textContent ?? '');
    expect(items).toHaveLength(3);
    expect(items[0]).toMatch(/Create a project/);
    expect(within(steps).getByRole('link', { name: 'Create a token' })).toHaveAttribute('href', '/settings#cli-tokens');
    expect(items[2]).toMatch(/Connect an agent/);
  });
});

describe('Settings order', () => {
  it('Columns, Members and CLI tokens come before every other section, and none is gone', async () => {
    await mount('/settings', { signedIn: true, admin: true });
    await screen.findByRole('heading', { name: 'Your settings' });
    await waitFor(() => expect(document.getElementById('users')).not.toBeNull());
    const order = [
      screen.getByRole('heading', { name: 'Columns' }),
      screen.getByRole('heading', { name: 'Members' }),
      document.getElementById('cli-tokens')!,
      document.getElementById('users')!,
      screen.getByRole('heading', { name: 'Your settings' }),
      screen.getByRole('heading', { name: 'Workflow Settings' }),
      document.getElementById('sessions')!,
    ];
    for (let i = 1; i < order.length; i++) {
      expect(order[i - 1]!.compareDocumentPosition(order[i]!) & Node.DOCUMENT_POSITION_FOLLOWING, `section ${i}`).toBeTruthy();
    }
  });
});
