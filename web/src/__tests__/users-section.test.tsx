/**
 * Settings → Users (R1.15). Rendering is not authorization: the BFF's 403 is the control
 * (internal/api/users_test.go). This pins that the panel is the admin's only, adds a user, shows the
 * generated password once, and offers no disable, password or role control.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

let fetched: string[] = [];

async function mount(permissions: string[]): Promise<void> {
  fetched = [];
  const users = [
    { id: 'U-1', login: 'root', displayName: 'root', role: 'admin', disabled: false, createdAt: 1 },
    { id: 'U-2', login: 'carol', displayName: 'Carol', role: 'member', disabled: true, createdAt: 2 },
  ];
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input).split('?')[0]!;
    fetched.push(`${init?.method ?? 'GET'} ${path}`);
    if (path === '/api/session') {
      return { ok: true, status: 200, json: async () => ({ identity: 'U-1', csrfToken: 'c', isAdmin: permissions.length > 0, permissions, subscriptionsEnabled: false, uiSettings: {} }) } as Response;
    }
    if (path === '/api/users' && init?.method === 'POST') {
      const b = JSON.parse(String(init.body)) as { login: string };
      const u = { id: 'U-3', login: b.login, displayName: b.login, role: 'member', disabled: false, createdAt: 3 };
      users.push(u);
      return { ok: true, status: 200, json: async () => ({ user: u, password: 'generated-once-0123456789' }) } as Response;
    }
    if (path === '/api/users') return { ok: true, status: 200, json: async () => ({ users }) } as Response;
    return { ok: true, status: 200, json: async () => ({ projects: [], boards: [], sessions: [] }) } as Response;
  }));
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
  await screen.findByRole('heading', { name: 'Board Configuration' });
}

describe('Settings → Users', () => {
  it('the workspace admin sees the users, adds one and is shown the password once', async () => {
    await mount(['batondeck:superadmin']);
    const table = await screen.findByRole('table', { name: 'Users' });
    expect(within(table).getByText('carol')).toBeInTheDocument();
    expect(within(table).getByText('Disabled')).toBeInTheDocument();
    // No control beyond list and add: disable, password and role changes are the host's CLI.
    expect(screen.queryByRole('button', { name: /disable|password|role|remove/i })).toBeNull();

    fireEvent.change(screen.getByLabelText('New user login'), { target: { value: 'bob' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add user' }));
    expect(await screen.findByText('generated-once-0123456789')).toBeInTheDocument();
    expect(await within(screen.getByRole('table', { name: 'Users' })).findAllByText('bob')).not.toHaveLength(0);
    fireEvent.click(screen.getByRole('button', { name: 'Done' }));
    expect(screen.queryByText('generated-once-0123456789')).toBeNull();
  });

  it('REFUSAL: a member sees no Users panel and never asks for the list', async () => {
    await mount([]);
    expect(screen.queryByRole('table', { name: 'Users' })).toBeNull();
    expect(screen.queryByText('Add user')).toBeNull();
    expect(fetched.filter((f) => f.endsWith(' /api/users'))).toEqual([]);
  });
});
