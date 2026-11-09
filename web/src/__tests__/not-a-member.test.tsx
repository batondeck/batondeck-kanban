/**
 * A CALLER WHO IS NOT A MEMBER OF THE PROJECT IS TOLD SO, NOT "CHECKING YOUR ROLE" FOR GOOD (P-443).
 *
 * `list_projects` answers the caller's own projects only, so a workspace admin who opens another
 * project's board or task by URL has no roster row for it. `useMyProjectRole` answered `undefined`
 * then, the same as while the roster loads, so every gated control stayed closed with "Checking your
 * role" and never a reason. Now a roster that has loaded without the row answers `NOT_A_MEMBER`,
 * which passes no floor (the controls stay closed, the server still refuses) and which `closedWhy`
 * explains. While the roster loads, or if it failed, the answer is still `undefined`.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { Board } from '../screens/Board.js';
import { TaskDrawer } from '../screens/TaskDrawer.js';
import { AuthProvider } from '../auth.js';
import { ProjectProvider } from '../projectContext.js';
import { ShortcutProvider } from '../shortcuts.js';
import { NOT_A_MEMBER, roleAtLeast, useMyProjectRole } from '../displayNames.js';
import { closedWhy, mayCall } from '../toolFloors.js';
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
const writes = (f: Calls) => f.mock.calls.filter(([, init]) => init?.method && init.method !== 'GET').map(([u, init]) => `${init!.method} ${String(u)}`);
const NOT_MEMBER = (what: string) => `You are not a member of this project, so you cannot ${what}.`;

describe('the not-a-member role', () => {
  it('passes no floor, and closedWhy names membership whatever the tier', () => {
    expect(roleAtLeast(NOT_A_MEMBER, 'viewer')).toBe(false);
    expect(mayCall(NOT_A_MEMBER, 'add_comment')).toBe(false);
    expect(closedWhy(NOT_A_MEMBER, 'add_comment', 'comment')).toBe(NOT_MEMBER('comment'));
    expect(closedWhy(NOT_A_MEMBER, 'update_task', 'edit tasks')).toBe(NOT_MEMBER('edit tasks'));
    expect(closedWhy(NOT_A_MEMBER, 'update_integration', 'change integrations')).toBe(NOT_MEMBER('change integrations'));
    // Loading is still no reason, and a member keeps theirs.
    expect(closedWhy(undefined, 'add_comment', 'comment')).toBeUndefined();
    expect(closedWhy('viewer', 'add_comment', 'comment')).toBe('A viewer cannot comment.');
  });

  function Probe({ id }: { id: string }) {
    return <span data-testid="role">{String(useMyProjectRole(id))}</span>;
  }
  const probe = (respond: () => Promise<Response>) => {
    vi.stubGlobal('fetch', vi.fn(respond));
    render(
      <QueryClientProvider client={newClient()}>
        <ProjectProvider>
          <Probe id="p2" />
        </ProjectProvider>
      </QueryClientProvider>,
    );
  };

  it('useMyProjectRole: undefined while the roster loads, NOT_A_MEMBER once it has loaded without the row', async () => {
    let answer: (r: Response) => void = () => {};
    probe(() => new Promise<Response>((r) => { answer = r; }));
    await settle();
    expect(screen.getByTestId('role')).toHaveTextContent('undefined');
    await act(async () => answer(json({ projects: [{ projectId: 'p1', name: 'P1', role: 'admin' }] })));
    await waitFor(() => expect(screen.getByTestId('role')).toHaveTextContent('not-a-member'));
  });

  it('useMyProjectRole: a roster that failed says nothing about membership (undefined)', async () => {
    probe(async () => ({ ok: false, status: 500, json: async () => ({ error: { code: 'INTERNAL', message: 'x' } }) }) as Response);
    await settle();
    await settle();
    expect(screen.getByTestId('role')).toHaveTextContent('undefined');
  });
});

const board = { id: 'b1', projectId: 'p1', name: 'Board', columns: [{ id: 'c1', name: 'To do', order: 1, status: 'TODO', wipLimit: null }] };
const t1: Task = { ...task, blockedBy: [], dueAt: null };

describe('a workspace admin on a project they are not a member of', () => {
  it('the board: "+ New board", "+ New task" and the assign chip are closed with the membership reason, never "Checking your role"', async () => {
    vi.stubGlobal('EventSource', FakeES);
    const f = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      const routes: Array<[RegExp, unknown]> = [
        // The roster is the caller's own projects: p1 is not among them.
        [/\/api\/projects$/, { projects: [{ projectId: 'mine', name: 'Mine', role: 'admin' }] }],
        [/\/sprints\?boardId=b1/, { sprints: [] }],
        [/\/boards\/b1\/tasks\?/, { tasks: [t1] }],
        [/\/boards\/b1\/feed$/, { events: [] }],
        [/\/boards\/b1$/, { board, taskCounts: {} }],
        [/\/boards$/, { boards: [board] }],
        [/\/projects\/p1$/, { project: { id: 'p1', name: 'P1', members: {} } }],
      ];
      return json(routes.find(([re]) => re.test(url))?.[1] ?? {});
    });
    vi.stubGlobal('fetch', f);
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
    const newBoard = screen.getByRole('button', { name: '+ New board' });
    await waitFor(() => expect(newBoard).toHaveAccessibleDescription(NOT_MEMBER('create boards')));
    expect(newBoard).toBeDisabled();
    expect(screen.getByRole('button', { name: /New task/ })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'assign' })).toBeDisabled();
    expect(screen.queryByText(/Checking your role/)).toBeNull();
    expect(document.querySelector('[title="Checking your role"]')).toBeNull();
    expect(writes(f as unknown as Calls)).toEqual([]);
  });

  it('the task drawer: Edit, a note and Send are closed with the membership reason', async () => {
    const f = routeFetch({
      '/api/projects$': { json: { projects: [{ projectId: 'mine', name: 'Mine', role: 'admin' }] } },
      '/projects/p1$': { json: { project: { id: 'p1', name: 'P1', members: { 'someone@example.com': 'admin' }, settings: { reviewEnabled: true, quotaEnabled: false, txnCap: null } } } },
    }) as unknown as Calls;
    vi.stubGlobal('fetch', f);
    render(
      <QueryClientProvider client={newClient()}>
        <AuthProvider>
          <ProjectProvider>
            <TaskDrawer projectId="p1" taskId="T-1" onClose={vi.fn()} />
          </ProjectProvider>
        </AuthProvider>
      </QueryClientProvider>,
    );
    await screen.findByRole('dialog', { name: 'Task T-1' });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Edit' })).toHaveAccessibleDescription(NOT_MEMBER('edit tasks')));
    expect(screen.getByRole('button', { name: 'Edit' })).toBeDisabled();
    expect(screen.getByPlaceholderText('Add a note…')).toHaveAccessibleDescription(NOT_MEMBER('add notes'));
    expect(screen.getByRole('button', { name: 'Send' })).toHaveAccessibleDescription(NOT_MEMBER('send follow-ups'));
    // The Actions panel's status select and claim line say the same, not "a viewer" or "a master can".
    expect(screen.getByText(NOT_MEMBER('change the status'))).toBeInTheDocument();
    expect(screen.queryByText(/Checking your role/)).toBeNull();
    expect(writes(f)).toEqual([]);
  });

  it('POSITIVE CONTROL: a roster read before the caller was added defers to the project member map', async () => {
    const f = routeFetch({
      '/api/projects$': { json: { projects: [] } },
    }) as unknown as Calls;
    vi.stubGlobal('fetch', f);
    render(
      <QueryClientProvider client={newClient()}>
        <AuthProvider>
          <ProjectProvider>
            <TaskDrawer projectId="p1" taskId="T-1" onClose={vi.fn()} />
          </ProjectProvider>
        </AuthProvider>
      </QueryClientProvider>,
    );
    await screen.findByRole('dialog', { name: 'Task T-1' });
    // The fixture's member map makes alice an admin of p1.
    await waitFor(() => expect(screen.getByRole('button', { name: 'Edit' })).toBeEnabled());
    expect(screen.queryByText(/not a member of this project/)).toBeNull();
  });
});

/* ---- P-445: the board body says why it is empty -------------------------------------------------- */

async function boardWithError(roster: Array<{ projectId: string; name: string; role: string }>, status: number, body: unknown): Promise<void> {
  vi.stubGlobal('EventSource', FakeES);
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (/\/api\/projects$/.test(url)) return json({ projects: roster });
    if (/\/boards\/b1$/.test(url) || /\/boards\/b1\/tasks\?/.test(url) || /\/boards$/.test(url)) {
      return { ok: false, status, statusText: 'x', json: async () => body } as Response;
    }
    return json({});
  }));
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
  await settle();
}

const NOT_MEMBER_BOARD = 'You are not a member of this project, so its board cannot be shown. A project admin can add you.';

describe('the board body when the board cannot be read (P-445)', () => {
  it('a non-member, refused FORBIDDEN "Not a member of project …", is told so, not "Couldn’t load this board."', async () => {
    await boardWithError([{ projectId: 'mine', name: 'Mine', role: 'admin' }], 403, { code: 'FORBIDDEN', message: 'Not a member of project p1' });
    expect(await screen.findByText(NOT_MEMBER_BOARD)).toBeInTheDocument();
    expect(screen.queryByText('Couldn’t load this board.')).toBeNull();
  });

  it('the server\'s refusal alone says it too, while the roster still loads', async () => {
    // A roster that never answers: the role is unknown, the board read's refusal names membership.
    vi.stubGlobal('EventSource', FakeES);
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (/\/api\/projects$/.test(url)) return new Promise<Response>(() => {});
      if (/\/boards\/b1$/.test(url)) return { ok: false, status: 403, statusText: 'x', json: async () => ({ code: 'FORBIDDEN', message: 'Not a member of project p1' }) } as Response;
      return json({});
    }));
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
    expect(await screen.findByText(NOT_MEMBER_BOARD)).toBeInTheDocument();
  });

  it('POSITIVE CONTROL: a member whose board read fails for another reason keeps "Couldn’t load this board."', async () => {
    await boardWithError([{ projectId: 'p1', name: 'P1', role: 'master' }], 500, { code: 'INTERNAL', message: 'boom' });
    expect(await screen.findByText('Couldn’t load this board.')).toBeInTheDocument();
    expect(screen.queryByText(NOT_MEMBER_BOARD)).toBeNull();
  });

  it('POSITIVE CONTROL: another FORBIDDEN (not membership) keeps "Couldn’t load this board."', async () => {
    await boardWithError([{ projectId: 'p1', name: 'P1', role: 'viewer' }], 403, { code: 'FORBIDDEN', message: 'Role viewer cannot call get_board' });
    expect(await screen.findByText('Couldn’t load this board.')).toBeInTheDocument();
  });
});
