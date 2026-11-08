/**
 * EVERY SETTINGS CONTROL HAS AN ACCESSIBLE NAME (P-433).
 *
 * The UI e2e suite reached several Settings controls by page structure because they had no name: the
 * top bar's project select, the "Require review step" switch, the Members add input and role select,
 * and the column name and WIP inputs. A screen reader announced them as "combo box", "switch", "edit
 * text". Each now has a visible label where one fits, else `aria-label`.
 *
 * Two halves: the named controls are found BY NAME, which is how a reader (and the e2e) reaches them;
 * and a sweep over every form control and button Settings renders, with every section populated, so a
 * control added later without a name fails here rather than in a screen reader.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { Settings } from '../screens/Settings.js';
import { AuthProvider } from '../auth.js';
import { ProjectProvider } from '../projectContext.js';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

const json = (b: unknown) => ({ ok: true, status: 200, json: async () => b }) as Response;

const board = {
  id: 'b1',
  projectId: 'p1',
  name: 'Main board',
  columns: [
    { id: 'c1', name: 'To do', order: 1, status: 'TODO', wipLimit: null },
    { id: 'c2', name: 'Review', order: 2, status: 'REVIEW', wipLimit: 3 },
  ],
};

function mount(): HTMLElement {
  // Settings configures the selected project; on "All projects" it only asks for one (P-442).
  localStorage.setItem('bd.projectId', 'p1');
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/api/session')) return json({ identity: 'U-1', csrfToken: 'c', isAdmin: true, permissions: ['batondeck:superadmin'], subscriptionsEnabled: false, uiSettings: {} });
      if (/\/api\/projects$/.test(url)) return json({ projects: [{ projectId: 'p1', name: 'Roadmap', role: 'admin' }, { projectId: 'p2', name: 'Ops', role: 'admin' }] });
      if (/\/api\/projects\/p1$/.test(url))
        return json({ project: { id: 'p1', name: 'Roadmap', members: { 'U-1': 'admin', 'U-2': 'worker' }, settings: { reviewEnabled: true, quotaEnabled: false, txnCap: null } }, memberDisplay: { 'U-1': 'Ada Admin', 'U-2': 'Wes Worker' } });
      if (/\/api\/projects\/p1\/boards$/.test(url)) return json({ boards: [board] });
      if (url.endsWith('/api/me/settings')) return json({ maxTicketTokens: null, tokenPolicy: null, starredAssets: [], defaults: { maxTicketTokens: 50000, tokenPolicy: 'warn', charsPerToken: 4 }, estimated: true });
      if (url.endsWith('/api/me/cli-tokens'))
        return json({ tokens: [{ id: 't1', label: 'laptop', createdAt: '2026-11-01T00:00:00Z', expiresAt: '2027-11-01T00:00:00Z', lastUsedAt: null, revokedAt: null, status: 'live' }] });
      if (url.endsWith('/api/users')) return json({ users: [{ id: 'U-1', login: 'ada@example.com', displayName: 'Ada Admin', role: 'admin', disabled: false, createdAt: 1 }] });
      if (/\/agents\/sessions/.test(url))
        return json({ sessions: [{ sessionKey: 's1', identityId: 'U-1', agentId: 'a1', agentName: 'claude-1', clientName: 'claude-code', lastSeenAt: '2026-11-01T00:00:00Z', revokedAt: null }] });
      return json({});
    }),
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { container } = render(
    <QueryClientProvider client={qc}>
      <AuthProvider>
        <ProjectProvider>
          <Settings />
        </ProjectProvider>
      </AuthProvider>
    </QueryClientProvider>,
  );
  return container;
}

/** Every section populated: columns, members, workflow, your settings, CLI tokens, users, sessions. */
async function loaded(): Promise<void> {
  await screen.findByText('Wes Worker');
  await screen.findByRole('switch');
  await screen.findByText('laptop');
  await screen.findByText('claude-1');
  await screen.findByRole('table', { name: 'Users' });
  await screen.findByText('Your ticket token limit');
  await waitFor(() => expect(screen.getByRole('button', { name: '+ Add Member' })).not.toHaveAttribute('title'));
}

describe('Settings controls are found by name (P-433)', () => {
  it('the project and board pickers, the columns, the members form and the review switch', async () => {
    mount();
    await loaded();
    expect(screen.getByRole('combobox', { name: 'Project to configure' })).toHaveValue('p1');
    expect(screen.getByText('Project', { selector: '.picker-caption' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Board to configure' })).toHaveValue('b1');

    expect(screen.getByRole('textbox', { name: 'Name of column To do' })).toHaveValue('To do');
    expect(screen.getByRole('textbox', { name: 'WIP limit of column Review' })).toHaveValue('3');
    expect(screen.getByRole('button', { name: 'Save column To do' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete column Review' })).toBeInTheDocument();
    expect(screen.getByRole('textbox', { name: 'New column name' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'New column status' })).toHaveValue('TODO');
    expect(screen.getByRole('textbox', { name: 'New column WIP limit' })).toBeInTheDocument();

    expect(screen.getByRole('textbox', { name: 'New member login' })).toHaveAttribute('type', 'email');
    expect(screen.getByRole('combobox', { name: 'New member role' })).toHaveValue('member');
    expect(screen.getByRole('button', { name: 'Remove Wes Worker' })).toBeInTheDocument();

    expect(screen.getByRole('switch', { name: 'Require review step' })).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByRole('spinbutton', { name: 'Your ticket token limit' })).toBeInTheDocument();
  });

  it('SWEEP: every input, select, textarea, button and switch Settings renders has a name', async () => {
    const container = mount();
    await loaded();
    const controls = [...container.querySelectorAll<HTMLElement>('input, select, textarea, button, [role="switch"]')];
    expect(controls.length).toBeGreaterThan(20);
    const unnamed = controls.filter((el) => {
      try {
        expect(el).toHaveAccessibleName();
        return false;
      } catch {
        return true;
      }
    });
    expect(unnamed.map((el) => el.outerHTML.slice(0, 120))).toEqual([]);
  });
});
