/**
 * P-287: the newcomer path. The Connect MCP dialog points at THIS server with an API token, the
 * sign-in screen says how agents authenticate and shows the `user add` hint on a server with no
 * account, the empty Projects screen lists the next steps, and Settings leads with Columns, Members
 * and CLI tokens.
 *
 * The dialog and sign-in render on their own with public-config seeded in the query cache, so
 * nothing they assert waits on a fetch or a timer; that wait is what timed out under load (rev76 N4).
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, within, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { AuthProvider } from '../auth.js';
import { App } from '../App.js';
import { ConnectMcp } from '../connect-mcp.js';
import { SignIn } from '../screens/SignIn.js';
import { publicConfig } from '../api/session.js';

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

/** A query client whose public-config answer is already in, so no render waits on it. */
function seeded(cfg: { oauth: boolean; noUsers: boolean }): QueryClient {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  qc.setQueryData(['publicConfig'], cfg);
  return qc;
}

function renderDialog(oauth: boolean, onClose = vi.fn()): HTMLElement {
  vi.stubGlobal('fetch', vi.fn(async () => json({})));
  render(
    <QueryClientProvider client={seeded({ oauth, noUsers: false })}>
      <MemoryRouter>
        <ConnectMcp onClose={onClose} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return screen.getByRole('dialog', { name: 'Connect an MCP client' });
}

function renderSignIn(noUsers: boolean): void {
  render(
    <QueryClientProvider client={seeded({ oauth: false, noUsers })}>
      <SignIn />
    </QueryClientProvider>,
  );
}

async function mountApp(path: string, admin: boolean): Promise<void> {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const p = String(input).split('?')[0]!;
    if (p === '/api/session') {
      return json({ identity: 'U-1', csrfToken: 'c', isAdmin: admin, permissions: admin ? ['batondeck:superadmin'] : [], subscriptionsEnabled: false, uiSettings: {} });
    }
    if (p === '/api/me/settings') return json({ maxTicketTokens: null, tokenPolicy: null, starredAssets: [], defaults: { maxTicketTokens: 50000, tokenPolicy: 'warn', charsPerToken: 4 }, estimated: true });
    return json({ projects: [], boards: [], sessions: [], tokens: [], users: [], events: [], tasks: [] });
  }));
  render(
    <QueryClientProvider client={seeded({ oauth: false, noUsers: false })}>
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <App />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('no hosted MCP URL and no agent-login wording anywhere in web/src', () => {
  it('scans every source file', () => {
    expect(Object.keys(SOURCES).length).toBeGreaterThan(50);
    const hits = Object.entries(SOURCES).flatMap(([f, src]) => FORBIDDEN.filter((w) => src.includes(w)).map((w) => `${f}: ${w}`));
    expect(hits).toEqual([]);
  });
});

describe('Connect MCP dialog', () => {
  it('every client snippet names this origin + /mcp and a bearer token, and nothing hosted', () => {
    const dlg = renderDialog(false);
    const url = `${window.location.origin}/mcp`;
    for (const tab of ['Claude Code', 'Codex CLI', 'Gemini CLI', 'Cursor']) {
      fireEvent.click(within(dlg).getByRole('tab', { name: tab }));
      expect(within(dlg).getByRole('tab', { name: tab })).toHaveAttribute('aria-selected', 'true');
      const server = [...dlg.querySelectorAll('pre')].map((p) => p.textContent ?? '').find((t) => t.includes('/mcp'));
      expect(server, tab).toContain(url);
      expect(server, tab).toMatch(/Authorization"?: "?Bearer (\$BATONDECK_TOKEN|\$\{BATONDECK_TOKEN\})|--bearer-token-env-var BATONDECK_TOKEN/);
      // cursor-agent 2026.04.17 sends `${env:…}` unresolved; Claude's default scope is this directory only.
      expect(server, tab).not.toContain('${env:');
      if (tab === 'Claude Code') expect(server, tab).toContain('claude mcp add --scope user ');
      // Quoted for the shell: an IPv6 origin's brackets are a glob in zsh.
      if (tab !== 'Cursor') expect(server, tab).toContain(`'${url}'`);
      expect(dlg.textContent, tab).not.toContain(HOSTED);
      expect(dlg.textContent, tab).not.toMatch(/marketplace|mcp-remote/);
    }
    expect(within(dlg).getAllByRole('tab').filter((t) => t.getAttribute('aria-selected') === 'true')).toHaveLength(1);
    expect(within(dlg).getByRole('link', { name: 'create a token' })).toHaveAttribute('href', '/settings#cli-tokens');
    expect(dlg.textContent).toContain('Only Claude Code with the plugin gets the device wake');
    expect(dlg.textContent).not.toMatch(/OAuth is on/);
  });

  it('shows the OAuth connector path only when public-config says OAuth is on', () => {
    expect(renderDialog(true).textContent).toMatch(/OAuth is on/);
  });

  it('takes focus on open and closes on Escape', () => {
    const onClose = vi.fn();
    const dlg = renderDialog(false, onClose);
    expect(dlg).toHaveAttribute('aria-modal', 'true');
    expect(document.activeElement).toBe(dlg);
    fireEvent.keyDown(window, { key: 'Enter' });
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

describe('Sign-in', () => {
  it('a server with no account shows the user add hint, and agents are told they use API tokens', () => {
    renderSignIn(true);
    expect(screen.getByRole('note').textContent).toContain('batondeck-kanban user add --admin <login>');
    expect(screen.getByText(/AI agents use API tokens/)).toBeInTheDocument();
    expect(document.querySelectorAll('a[href="/"]')).toHaveLength(0);
  });

  it('a server with an account shows no hint', () => {
    renderSignIn(false);
    expect(screen.getByText(/AI agents use API tokens/)).toBeInTheDocument();
    expect(screen.queryByRole('note')).toBeNull();
  });

  it('publicConfig reads oauth and noUsers off /api/public-config, false when absent', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ demoLogin: false, workosLogin: false, oauth: true, noUsers: true })));
    expect(await publicConfig()).toEqual({ oauth: true, noUsers: true });
    vi.stubGlobal('fetch', vi.fn(async () => json({ demoLogin: false, workosLogin: false })));
    expect(await publicConfig()).toEqual({ oauth: false, noUsers: false });
  });
});

// P-305: the two tests below mount the whole App, so their first screen waits on /api/session and the
// screen's own queries through the stubbed fetch. Under load that took longer than testing-library's 1s
// default (1 failure in 13, rev76), so the first wait has an explicit FIRST_SCREEN timeout and the test a
// ceiling above it. Nothing they assert is changed.
const FIRST_SCREEN = { timeout: 10_000 };
const FULL_APP_TEST_MS = 20_000;

describe('Projects: the empty workspace', () => {
  it('lists the three next steps, the last opening the dialog', async () => {
    await mountApp('/projects', false);
    const steps = await screen.findByRole('region', { name: 'Get started' }, FIRST_SCREEN);
    const items = within(steps).getAllByRole('listitem').map((li) => li.textContent ?? '');
    expect(items).toHaveLength(3);
    expect(items[0]).toMatch(/Create a project/);
    expect(within(steps).getByRole('link', { name: 'Create a token' })).toHaveAttribute('href', '/settings#cli-tokens');
    fireEvent.click(within(steps).getByRole('button', { name: 'Connect an agent' }));
    expect(screen.getByRole('dialog', { name: 'Connect an MCP client' })).toBeInTheDocument();
  }, FULL_APP_TEST_MS);
});

describe('Settings order', () => {
  it('Columns, Members and CLI tokens come before every other section, and none is gone', async () => {
    await mountApp('/settings', true);
    await screen.findByRole('heading', { name: 'Your settings' }, FIRST_SCREEN);
    await waitFor(() => expect(document.getElementById('users')).not.toBeNull(), FIRST_SCREEN);
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
  }, FULL_APP_TEST_MS);
});
