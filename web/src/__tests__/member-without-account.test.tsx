/**
 * ADDING A MEMBER BY A LOGIN NO ACCOUNT HOLDS SAYS SO.
 *
 * add_member keeps a reference that is no account's id or login as given: the Members table gains a
 * row keyed by the typed login, which grants nothing, since every token resolves to an account id.
 * Settings cleared the field and said nothing, so the admin believed the teammate was in. It now
 * says that no account has that login yet, and what to do. A login that names an account (the
 * answer keys the member by the account's id) says nothing. The row itself says it too, so a row
 * seen after a reload, or added earlier, does not pass for a member either.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Settings } from '../screens/Settings.js';
import { AuthProvider } from '../auth.js';
import { ProjectProvider } from '../projectContext.js';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

const json = (b: unknown) => ({ ok: true, status: 200, json: async () => b }) as Response;

/**
 * members is what add_member answers and the project then holds; display the accounts among them.
 * held: the project holds after already, as on a reload after the add.
 */
function mount(after: { members: Record<string, string>; display: Record<string, string> }, held = false): void {
  localStorage.setItem('bd.projectId', 'p1');
  let added = held;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/api/session')) return json({ identity: 'U-1', csrfToken: 'c', isAdmin: false, permissions: [], subscriptionsEnabled: false, uiSettings: {} });
      if (/\/api\/projects$/.test(url)) return json({ projects: [{ projectId: 'p1', name: 'Roadmap', role: 'admin' }] });
      if (/\/api\/projects\/p1\/members$/.test(url) && init?.method === 'POST') {
        added = true;
        return json({ members: after.members });
      }
      if (/\/api\/projects\/p1$/.test(url)) {
        const members = added ? after.members : { 'U-1': 'admin' };
        const memberDisplay = added ? after.display : { 'U-1': 'Ada Admin' };
        return json({ project: { id: 'p1', name: 'Roadmap', members, settings: { reviewEnabled: true, quotaEnabled: false, txnCap: null } }, memberDisplay });
      }
      if (/\/api\/projects\/p1\/boards$/.test(url)) return json({ boards: [] });
      return json({});
    }),
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <AuthProvider>
        <ProjectProvider>
          <Settings />
        </ProjectProvider>
      </AuthProvider>
    </QueryClientProvider>,
  );
}

async function add(login: string): Promise<void> {
  await screen.findByText('Ada Admin');
  const input = screen.getByRole('textbox', { name: 'New member login' });
  await waitFor(() => expect(input).not.toBeDisabled());
  fireEvent.change(input, { target: { value: login } });
  fireEvent.click(screen.getByRole('button', { name: '+ Add Member' }));
}

describe('adding a member by a login with no account', () => {
  it('says no account has that login yet', async () => {
    mount({ members: { 'U-1': 'admin', 'carol@example.com': 'member' }, display: { 'U-1': 'Ada Admin' } });
    await add(' carol@example.com ');
    const note = await screen.findByRole('status');
    expect(note).toHaveTextContent('No account has the login carol@example.com yet');
    expect(note).toHaveTextContent('grants nothing');
  });

  it('says nothing when the login names an account', async () => {
    mount({ members: { 'U-1': 'admin', 'U-3': 'member' }, display: { 'U-1': 'Ada Admin', 'U-3': 'Carol' } });
    await add('carol@example.com');
    await screen.findByText('Carol');
    expect(screen.queryByText(/No account has the login/)).not.toBeInTheDocument();
  });

  it('says nothing when an account id is added', async () => {
    mount({ members: { 'U-1': 'admin', 'U-3': 'member' }, display: { 'U-1': 'Ada Admin', 'U-3': 'Carol' } });
    await add('U-3');
    await screen.findByText('Carol');
    expect(screen.queryByText(/No account has the login/)).not.toBeInTheDocument();
  });

  it('marks a row no account is, after a reload too', async () => {
    mount({ members: { 'U-1': 'admin', 'carol@example.com': 'member' }, display: { 'U-1': 'Ada Admin' } }, true);
    await screen.findByText('Ada Admin');
    const row = (await screen.findByText('carol@example.com')).closest('tr')!;
    expect(row).toHaveTextContent('no account');
    expect(screen.getByText('Ada Admin').closest('tr')).not.toHaveTextContent('no account');
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });
});
