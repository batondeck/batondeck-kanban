/**
 * A WRITE BELOW THE CALLER'S TIER IS SHOWN CLOSED, WITH ITS REASON (P-432).
 *
 * The UI e2e suite found a viewer offered "+ New task" on the board, Comment on a task and
 * "+ Add Member" in Settings, each learnt of only from the server's refusal ("FORBIDDEN: Role viewer
 * cannot call create_task"), while Status and Claim were already closed with a reason (P-209). The
 * tiers are `internal/domain/grants.go`'s: create_task and add_column master (add_column by the
 * owner's ruling of 2026-11-10), add_comment write (a worker), update_column and remove_column
 * structural (a member), add_member, remove_member and update_settings admin. The role is the
 * caller's in the project, as the UI already holds it.
 *
 * Each closed case has its positive control: the role that may act sees the control open. The server
 * still decides; these assert only what is offered, and that a closed control sends nothing.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { Board } from '../screens/Board.js';
import { Settings } from '../screens/Settings.js';
import { TaskDrawer } from '../screens/TaskDrawer.js';
import { AuthProvider } from '../auth.js';
import { ProjectProvider } from '../projectContext.js';
import { ShortcutProvider } from '../shortcuts.js';
import { routeFetch, task } from './taskFixtures.js';
import type { Task } from '../types.js';

class FakeES {
  readyState = 0;
  addEventListener(): void {}
  close(): void {
    this.readyState = 2;
  }
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

type Calls = { mock: { calls: Array<[string, RequestInit?]> } };
const json = (b: unknown) => ({ ok: true, status: 200, json: async () => b }) as Response;
const newClient = () => new QueryClient({ defaultOptions: { queries: { retry: false } } });
const settle = () => act(() => new Promise<void>((r) => setTimeout(r, 30)));

/* ---- the board: "+ New task" and `c` ------------------------------------------------------------ */

const board = { id: 'b1', projectId: 'p1', name: 'Board', columns: [{ id: 'c1', name: 'To do', order: 1, status: 'TODO', wipLimit: null }] };

async function boardAs(role: string): Promise<void> {
  vi.stubGlobal('EventSource', FakeES);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      const routes: Array<[RegExp, unknown]> = [
        [/\/api\/projects$/, { projects: [{ projectId: 'p1', name: 'P1', role }] }],
        [/\/boards\/b1\/tasks\?/, { tasks: [] }],
        [/\/boards\/b1\/feed$/, { events: [] }],
        [/\/boards\/b1$/, { board, taskCounts: {} }],
        [/\/boards$/, { boards: [board] }],
        [/\/projects\/p1$/, { project: { id: 'p1', name: 'P1', members: {} } }],
      ];
      return json(routes.find(([re]) => re.test(url))?.[1] ?? {});
    }),
  );
  render(
    <QueryClientProvider client={newClient()}>
      <ProjectProvider>
        <ShortcutProvider>
          <MemoryRouter initialEntries={['/projects/p1/boards/b1']}>
            <Routes>
              <Route path="/projects/:projectId/boards/:boardId" element={<Board />} />
            </Routes>
          </MemoryRouter>
        </ShortcutProvider>
      </ProjectProvider>
    </QueryClientProvider>,
  );
  await screen.findByRole('heading', { name: 'Board' });
  await settle();
}

describe('the board offers "+ New task" only from master up (create_task is tier master)', () => {
  for (const role of ['viewer', 'worker']) {
    it(`a ${role} sees it disabled with the reason, and \`c\` opens nothing`, async () => {
      await boardAs(role);
      const btn = await screen.findByRole('button', { name: '+ New task' });
      await waitFor(() => expect(screen.getByText('Only a master can create tasks.')).toBeInTheDocument());
      expect(btn).toBeDisabled();
      expect(btn).toHaveAccessibleDescription('Only a master can create tasks.');
      fireEvent.keyDown(window, { key: 'c' });
      await settle();
      expect(screen.queryByLabelText('Task title')).toBeNull();
    });
  }

  it('POSITIVE CONTROL: a master sees it open, and both the button and `c` open the create row', async () => {
    await boardAs('master');
    const btn = await screen.findByRole('button', { name: '+ New task' });
    await waitFor(() => expect(btn).toBeEnabled());
    expect(screen.queryByText('Only a master can create tasks.')).toBeNull();
    fireEvent.keyDown(window, { key: 'c' });
    expect(await screen.findByLabelText('Task title')).toBeInTheDocument();
  });
});

/* ---- the task: Comment ----------------------------------------------------------------------------- */

const comment = { id: 'cm1', taskId: 'T-1', author: 'alice@example.com', body: 'Looks right.', mentions: [], parentId: null, ts: new Date().toISOString() };

async function taskAs(role: string): Promise<Calls> {
  const inProgress: Task = { ...task, status: 'IN_PROGRESS', columnId: 'c2', blockedBy: [] };
  const fetchMock = routeFetch({
    '/api/session$': { json: { identity: 'alice@example.com', csrfToken: null } },
    '/tasks/T-1$': { json: { task: inProgress } },
    '/tasks/T-1/comments(\\?|$)': { json: { comments: [comment] } },
    '/projects/p1$': { json: { project: { id: 'p1', name: 'P1', members: { 'alice@example.com': role }, settings: { reviewEnabled: true, quotaEnabled: false, txnCap: null } } } },
  }) as unknown as Calls;
  vi.stubGlobal('fetch', fetchMock);
  render(
    <QueryClientProvider client={newClient()}>
      <AuthProvider>
        <TaskDrawer projectId="p1" taskId="T-1" onClose={vi.fn()} />
      </AuthProvider>
    </QueryClientProvider>,
  );
  await screen.findByRole('dialog', { name: 'Task T-1' });
  await screen.findByText('Looks right.');
  const urls = () => fetchMock.mock.calls.map(([u]) => String(u));
  await waitFor(() => {
    expect(urls().some((u) => /\/projects\/p1$/.test(u))).toBe(true);
    expect(urls().some((u) => u.endsWith('/api/session'))).toBe(true);
  });
  await settle();
  return fetchMock;
}

const composer = () => screen.getByPlaceholderText(/Write a comment/);
const commentPosted = (f: Calls) => f.mock.calls.some(([u, init]) => String(u).endsWith('/tasks/T-1/comments') && init?.method === 'POST');

describe("the task's Comment is offered from worker up (add_comment is tier write)", () => {
  it('a viewer sees the composer and Comment disabled with the reason, no Reply, and nothing is posted', async () => {
    const f = await taskAs('viewer');
    await waitFor(() => expect(screen.getByText('A viewer cannot comment.')).toBeInTheDocument());
    expect(composer()).toBeDisabled();
    const btn = screen.getByRole('button', { name: 'Comment', exact: true } as never);
    expect(btn).toBeDisabled();
    expect(btn).toHaveAccessibleDescription('A viewer cannot comment.');
    expect(screen.queryByRole('button', { name: 'Reply' })).toBeNull();
    fireEvent.keyDown(composer(), { key: 'Enter', ctrlKey: true });
    await settle();
    expect(commentPosted(f)).toBe(false);
  });

  it('POSITIVE CONTROL: a worker can write, reply and post', async () => {
    const f = await taskAs('worker');
    await waitFor(() => expect(composer()).toBeEnabled());
    expect(screen.queryByText('A viewer cannot comment.')).toBeNull();
    expect(screen.getByRole('button', { name: 'Reply' })).toBeEnabled();
    fireEvent.change(composer(), { target: { value: 'Rebuilt.', selectionStart: 8, selectionEnd: 8 } });
    const btn = screen.getByRole('button', { name: 'Comment', exact: true } as never);
    expect(btn).toBeEnabled();
    fireEvent.click(btn);
    await waitFor(() => expect(commentPosted(f)).toBe(true));
  });
});

/* ---- Settings: members, columns, workflow ----------------------------------------------------------- */

function settingsAs(role: string): Calls {
  // Settings configures the selected project; on "All projects" it only asks for one (P-442).
  localStorage.setItem('bd.projectId', 'p1');
  const project = {
    id: 'p1',
    name: 'P1',
    members: { 'U-1': role, 'U-2': 'worker' },
    settings: { reviewEnabled: true, quotaEnabled: false, txnCap: null },
  };
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.endsWith('/api/session')) return json({ identity: 'U-1', csrfToken: 'c', isAdmin: false, permissions: [], subscriptionsEnabled: false, uiSettings: {} });
    if (/\/api\/projects$/.test(url)) return json({ projects: [{ projectId: 'p1', name: 'P1', role }] });
    if (/\/api\/projects\/p1$/.test(url)) return json({ project, memberDisplay: { 'U-1': 'Ada', 'U-2': 'Wes' } });
    if (/\/api\/projects\/p1\/boards$/.test(url)) return json({ boards: [board] });
    return json({});
  });
  vi.stubGlobal('fetch', fetchMock);
  render(
    <QueryClientProvider client={newClient()}>
      <AuthProvider>
        <ProjectProvider>
          <Settings />
        </ProjectProvider>
      </AuthProvider>
    </QueryClientProvider>,
  );
  return fetchMock as unknown as Calls;
}

const addMember = () => screen.getByRole('button', { name: '+ Add Member' });
const addColumn = () => screen.getByRole('button', { name: '+ Add Column' });
const reviewSwitch = () => screen.getByRole('switch');
/** Below master the columns name both floors: adding is master's, the rest a member's. */
const BELOW_ADD_COLUMN = 'Only a master can add columns. Only a member or an admin can rename, limit or delete them.';

describe('Settings offers each write from its tier up', () => {
  it('a viewer: members, columns and the review step are closed, each with its reason', async () => {
    const f = settingsAs('viewer');
    await screen.findByText('Wes');
    await waitFor(() => expect(screen.getByText('Only a project admin can add or remove members.')).toBeInTheDocument());
    expect(addMember()).toBeDisabled();
    expect(addMember()).toHaveAccessibleDescription('Only a project admin can add or remove members.');
    expect(screen.getByPlaceholderText('teammate@example.com')).toBeDisabled();
    for (const b of screen.getAllByRole('button', { name: /^Remove/ })) expect(b).toBeDisabled();
    expect(screen.getByText(BELOW_ADD_COLUMN)).toBeInTheDocument();
    expect(addColumn()).toBeDisabled();
    expect(screen.getByPlaceholderText('New column name')).toBeDisabled();
    expect(screen.getByDisplayValue('To do')).toBeDisabled();
    expect(screen.getByText('Only a project admin can change workflow settings.')).toBeInTheDocument();
    expect(reviewSwitch()).toBeDisabled();
    expect(reviewSwitch()).toHaveAccessibleDescription('Only a project admin can change workflow settings.');
    fireEvent.click(reviewSwitch());
    await settle();
    expect(f.mock.calls.some(([, init]) => init?.method && init.method !== 'GET')).toBe(false);
  });

  it('a worker may not add a column (tier master), rename, limit or delete one, nor manage members, and sends nothing', async () => {
    const f = settingsAs('worker');
    await screen.findByText('Wes');
    await waitFor(() => expect(screen.getByText(BELOW_ADD_COLUMN)).toBeInTheDocument());
    expect(screen.getByPlaceholderText('New column name')).toBeDisabled();
    expect(addColumn()).toBeDisabled();
    expect(addColumn()).toHaveAccessibleDescription(BELOW_ADD_COLUMN);
    fireEvent.click(addColumn());
    expect(screen.getByDisplayValue('To do')).toBeDisabled();
    expect(addMember()).toBeDisabled();
    expect(reviewSwitch()).toBeDisabled();
    await settle();
    expect(f.mock.calls.some(([, init]) => init?.method && init.method !== 'GET')).toBe(false);
  });

  it('POSITIVE CONTROL: a master may add a column but not rename, limit or delete one', async () => {
    settingsAs('master');
    await screen.findByText('Wes');
    await waitFor(() => expect(screen.getByText('Only a member or an admin can rename, limit or delete columns.')).toBeInTheDocument());
    expect(screen.getByPlaceholderText('New column name')).toBeEnabled();
    fireEvent.change(screen.getByPlaceholderText('New column name'), { target: { value: 'QA' } });
    expect(addColumn()).toBeEnabled();
    expect(screen.getByDisplayValue('To do')).toBeDisabled();
    expect(screen.queryByText(BELOW_ADD_COLUMN)).toBeNull();
    expect(addMember()).toBeDisabled();
  });

  it('POSITIVE CONTROL: a project admin has every control open and no reason shown', async () => {
    settingsAs('admin');
    await screen.findByText('Wes');
    await waitFor(() => expect(screen.getByPlaceholderText('teammate@example.com')).toBeEnabled());
    fireEvent.change(screen.getByPlaceholderText('teammate@example.com'), { target: { value: 'vic@example.com' } });
    expect(addMember()).toBeEnabled();
    for (const b of screen.getAllByRole('button', { name: /^Remove/ })) expect(b).toBeEnabled();
    expect(screen.getByDisplayValue('To do')).toBeEnabled();
    expect(reviewSwitch()).toBeEnabled();
    expect(screen.queryByText(/Only a project admin|cannot change columns|Only a master|Only a member or an admin/)).toBeNull();
  });
});
