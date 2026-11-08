/**
 * EVERY WRITE CONTROL BELOW THE CALLER'S TIER IS CLOSED, WITH ITS REASON (P-440).
 *
 * P-432 closed "+ New task", Comment and the Settings writes. The rest still offered a viewer what the
 * server then refused: "+ New board", the card's "+ assign", the task's Edit and Details, a note, a
 * link, memory, the file drop, the Follow-ups composer, the palette's status and priority, and more.
 * The floors are `toolFloors.ts`'s, which `tool-floors.test.ts` pins against `internal/domain/grants.go`;
 * the role is the caller's in the project, from the roster (`useMyProjectRole`).
 *
 * Each surface is asserted for a viewer (closed, described by its reason, sends nothing) and for a
 * role that may write (open, no reason). The server still decides; these assert only what is offered.
 */
import { describe, it, expect, vi, afterEach, beforeAll } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { Board } from '../screens/Board.js';
import { Projects } from '../screens/Projects.js';
import { TaskDrawer } from '../screens/TaskDrawer.js';
import { TaskPage } from '../screens/TaskPage.js';
import { Chronicle, ChronicleHistory, ChronicleNew, ChroniclePageView } from '../screens/Chronicle.js';
import { CommandPalette } from '../palette.js';
import { AuthProvider } from '../auth.js';
import { ProjectProvider } from '../projectContext.js';
import { ShortcutProvider } from '../shortcuts.js';
import { routeFetch, task } from './taskFixtures.js';
import type { Task } from '../types.js';

beforeAll(() => {
  // cmdk measures its list and scrolls the active item into view; jsdom has neither.
  (globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = class {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  };
  Element.prototype.scrollIntoView = (): void => {};
});

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
/** Every request that is not a read. */
const writes = (f: Calls) => f.mock.calls.filter(([, init]) => init?.method && init.method !== 'GET').map(([u, init]) => `${init!.method} ${String(u)}`);

/* ---- the board: "+ New board", "+ assign", drag, the sprints rail, the palette -------------------- */

const board = { id: 'b1', projectId: 'p1', name: 'Board', columns: [{ id: 'c1', name: 'To do', order: 1, status: 'TODO', wipLimit: null }] };
const unassigned: Task = { ...task, blockedBy: [], dueAt: null };
const assigned: Task = { ...task, id: 'T-3', title: 'Second', order: 'a1', assignee: 'bot-1', blockedBy: [], dueAt: null };
const activeSprint = {
  id: 'S-1', projectId: 'p1', boardId: 'b1', name: 'Sprint 1', goal: 'ship', acceptance: 'done', status: 'ACTIVE',
  proposalTaskId: null, approvedBy: null, memberCount: 0, checkpoint: null, eventCursor: null, createdBy: 'a',
  createdAt: '2026-08-03T00:00:00Z', startedAt: null, closedAt: null, updatedBy: 'a', updatedAt: '2026-08-03T00:00:00Z', version: 1,
};

async function boardAs(role: string): Promise<Calls> {
  localStorage.setItem('bd.sprintRail', 'open');
  vi.stubGlobal('EventSource', FakeES);
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    const routes: Array<[RegExp, unknown]> = [
      [/\/api\/projects$/, { projects: [{ projectId: 'p1', name: 'P1', role }] }],
      [/\/sprints\?boardId=b1/, { sprints: [activeSprint] }],
      [/\/boards\/b1\/tasks\?/, { tasks: [unassigned, assigned] }],
      [/\/boards\/b1\/feed$/, { events: [] }],
      [/\/boards\/b1$/, { board, taskCounts: {} }],
      [/\/boards$/, { boards: [board] }],
      [/\/projects\/p1$/, { project: { id: 'p1', name: 'P1', members: {} } }],
    ];
    return json(routes.find(([re]) => re.test(url))?.[1] ?? {});
  });
  vi.stubGlobal('fetch', fetchMock);
  render(
    <QueryClientProvider client={newClient()}>
      <ProjectProvider>
        <ShortcutProvider>
          <MemoryRouter initialEntries={['/projects/p1/boards/b1']}>
            <CommandPalette />
            <Routes>
              <Route path="/projects/:projectId/boards/:boardId" element={<Board />} />
            </Routes>
          </MemoryRouter>
        </ShortcutProvider>
      </ProjectProvider>
    </QueryClientProvider>,
  );
  await screen.findByRole('heading', { name: 'Board' });
  await screen.findByText('Second');
  await settle();
  return fetchMock as unknown as Calls;
}

const newBoard = () => screen.getByRole('button', { name: '+ New board' });
const assignChip = () => screen.getByRole('button', { name: 'assign' });
const assignedChip = () => screen.getByTitle(/^Assigned to bot-1/);
/** The palette's own list: the board also has a status filter whose `<option>`s share these names. */
const palette = () => within(document.querySelector<HTMLElement>('[cmdk-list]')!);
const sortables = () => [...document.querySelectorAll('[aria-roledescription="sortable"]')];

describe('the board closes what a viewer cannot write', () => {
  it('a viewer: "+ New board", both assign chips, drag and the sprint lifecycle are closed with reasons, and nothing is sent', async () => {
    const f = await boardAs('viewer');
    await waitFor(() => expect(screen.getByText('A viewer cannot create boards.')).toBeInTheDocument());
    expect(newBoard()).toBeDisabled();
    expect(newBoard()).toHaveAccessibleDescription('A viewer cannot create boards.');
    fireEvent.click(newBoard());

    const cardsWhy = 'Only a master can assign, move or reprioritise tasks.';
    expect(screen.getByText(cardsWhy)).toBeInTheDocument();
    expect(assignChip()).toBeDisabled();
    expect(assignChip()).toHaveAccessibleDescription(cardsWhy);
    expect(assignedChip()).toBeDisabled();
    expect(assignedChip()).toHaveAccessibleDescription(cardsWhy);
    fireEvent.click(assignChip());
    expect(screen.queryByRole('menu')).toBeNull();

    expect(sortables()).toHaveLength(2);
    for (const s of sortables()) expect(s).toHaveAttribute('aria-disabled', 'true');

    await screen.findAllByText('Sprint 1');
    expect(screen.getByText('Only a master can start, close or abandon sprints.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Close' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Abandon' })).toBeNull();

    await settle();
    expect(writes(f)).toEqual([]);
  });

  it('a viewer: the palette lists Set status and Set priority disabled, each with its reason, and `s` opens no status list', async () => {
    const f = await boardAs('viewer');
    await waitFor(() => expect(screen.getByText('A viewer cannot create boards.')).toBeInTheDocument());
    fireEvent.keyDown(window, { key: 'j' });
    fireEvent.keyDown(window, { key: 's' });
    const status = await waitFor(() => palette().getByRole('option', { name: /Set status/ }));
    expect(status).toHaveAttribute('aria-disabled', 'true');
    expect(status).toHaveAccessibleDescription('Only a master can change the status.');
    const priority = palette().getByRole('option', { name: /Set priority/ });
    expect(priority).toHaveAttribute('aria-disabled', 'true');
    expect(priority).toHaveAccessibleDescription('Only a master can change the priority.');
    fireEvent.click(status);
    await settle();
    expect(palette().queryByRole('option', { name: 'IN PROGRESS' })).toBeNull();
    expect(palette().getByRole('option', { name: /Set status/ })).toBeInTheDocument();
    expect(writes(f)).toEqual([]);
  });

  it('POSITIVE CONTROL: a master has every board control open and no reason shown', async () => {
    await boardAs('master');
    await waitFor(() => expect(newBoard()).toBeEnabled());
    expect(assignChip()).toBeEnabled();
    expect(assignedChip()).toBeEnabled();
    for (const s of sortables()) expect(s).toHaveAttribute('aria-disabled', 'false');
    await screen.findAllByText('Sprint 1');
    expect(screen.getByRole('button', { name: 'Close' })).toBeInTheDocument();
    expect(screen.queryByText(/cannot create boards|Only a master can/)).toBeNull();
    fireEvent.keyDown(window, { key: 'j' });
    fireEvent.keyDown(window, { key: 's' });
    // `s` jumps straight into the status list: the action has children.
    await waitFor(() => expect(palette().getByRole('option', { name: 'IN PROGRESS' })).toBeInTheDocument());
  });
});

/* ---- the task drawer: Edit, Details, notes, links, memory, files, Follow-ups, deps, subtasks, runs -- */

/**
 * The drawer under the project roster. `roster` is the role `/api/projects` reports (the source);
 * `member` is what `get_project`'s member map says, which must not open anything the roster closes.
 */
async function drawerAs(roster: string, member = roster): Promise<Calls> {
  const fetchMock = routeFetch({
    '/api/projects$': { json: { projects: [{ projectId: 'p1', name: 'P1', role: roster }] } },
    '/projects/p1$': { json: { project: { id: 'p1', name: 'P1', members: { 'alice@example.com': member }, settings: { reviewEnabled: true, quotaEnabled: false, txnCap: null } } } },
  }) as unknown as Calls;
  vi.stubGlobal('fetch', fetchMock);
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
  const urls = () => fetchMock.mock.calls.map(([u]) => String(u));
  await waitFor(() => {
    expect(urls().some((u) => /\/api\/projects$/.test(u))).toBe(true);
    expect(urls().some((u) => /\/projects\/p1$/.test(u))).toBe(true);
    expect(urls().some((u) => u.endsWith('/api/session'))).toBe(true);
  });
  await settle();
  return fetchMock;
}

const btn = (name: string | RegExp) => screen.getByRole('button', { name });
const described = (el: HTMLElement, why: string) => {
  expect(el).toBeDisabled();
  expect(el).toHaveAccessibleDescription(why);
};
const dropFile = () =>
  fireEvent.drop(btn(/Drop a file/), { dataTransfer: { files: [new File(['x'], 'a.txt', { type: 'text/plain' })] } });
const attachmentPosted = (f: Calls) => writes(f).some((w) => /^POST .*\/tasks\/T-1\/attachments$/.test(w));

describe('the task drawer closes every write a viewer cannot make', () => {
  it('a viewer: each control is closed with its reason, and neither typing, Enter nor a dropped file sends anything', async () => {
    const f = await drawerAs('viewer');
    await waitFor(() => expect(screen.getByText('Only a master can edit tasks.')).toBeInTheDocument());
    described(btn('Edit'), 'Only a master can edit tasks.');

    described(screen.getByLabelText('Priority'), 'Only a master can edit details.');
    described(screen.getByLabelText('Assignee'), 'Only a master can edit details.');
    described(screen.getByLabelText('Due'), 'Only a master can edit details.');

    described(screen.getByPlaceholderText('Add a note…'), 'A viewer cannot add notes.');
    described(within(screen.getByPlaceholderText('Add a note…').parentElement!).getByRole('button', { name: 'Add' }), 'A viewer cannot add notes.');
    described(screen.getByPlaceholderText(/Paste a link/), 'A viewer cannot attach links.');
    described(btn('Attach'), 'A viewer cannot attach links.');
    described(screen.getByPlaceholderText('key'), 'A viewer cannot write memory.');
    described(btn('Save'), 'A viewer cannot write memory.');

    described(btn(/Drop a file/), 'A viewer cannot attach files.');
    dropFile();

    described(screen.getByPlaceholderText(/Add a follow-up/), 'Only a master can send follow-ups.');
    described(btn('Send'), 'Only a master can send follow-ups.');
    expect(screen.getByText('Only a master can add or remove dependencies.')).toBeInTheDocument();
    expect(screen.getByText('Only a master can add subtasks or mark them done.')).toBeInTheDocument();
    described(btn('Race this task'), 'Only a master can race tasks.');
    expect(screen.getByText(/A viewer cannot edit the digest\./)).toBeInTheDocument();

    fireEvent.keyDown(screen.getByPlaceholderText('Add a note…'), { key: 'Enter' });
    fireEvent.click(btn('Edit'));
    await settle();
    expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull();
    expect(attachmentPosted(f)).toBe(false);
    expect(writes(f)).toEqual([]);
  });

  it('the roster role decides: a roster viewer whom the member map calls admin is still closed', async () => {
    await drawerAs('viewer', 'admin');
    await waitFor(() => expect(screen.getByText('Only a master can edit tasks.')).toBeInTheDocument());
    expect(btn('Edit')).toBeDisabled();
    expect(screen.getByPlaceholderText('Add a note…')).toBeDisabled();
  });

  it('a worker may annotate, attach and write memory (tier write) but not edit, follow up or race (master)', async () => {
    const f = await drawerAs('worker');
    await waitFor(() => expect(screen.getByText('Only a master can edit tasks.')).toBeInTheDocument());
    expect(screen.getByPlaceholderText('Add a note…')).toBeEnabled();
    expect(screen.getByPlaceholderText(/Paste a link/)).toBeEnabled();
    expect(screen.getByPlaceholderText('key')).toBeEnabled();
    expect(btn(/Drop a file/)).toBeEnabled();
    expect(screen.queryByText(/A viewer cannot/)).toBeNull();
    described(btn('Edit'), 'Only a master can edit tasks.');
    described(btn('Send'), 'Only a master can send follow-ups.');
    described(btn('Race this task'), 'Only a master can race tasks.');
    dropFile();
    await waitFor(() => expect(attachmentPosted(f)).toBe(true));
  });

  it('POSITIVE CONTROL: an admin has every control open, no reason shown, and a note posts', async () => {
    const f = await drawerAs('admin');
    await waitFor(() => expect(btn('Edit')).toBeEnabled());
    expect(screen.getByLabelText('Priority')).toBeEnabled();
    expect(screen.getByPlaceholderText(/Add a follow-up/)).toBeEnabled();
    expect(btn('Race this task')).toBeEnabled();
    expect(screen.queryByText(/A viewer cannot|Only a master can/)).toBeNull();
    fireEvent.change(screen.getByPlaceholderText('Add a note…'), { target: { value: 'Checked.' } });
    fireEvent.click(within(screen.getByPlaceholderText('Add a note…').parentElement!).getByRole('button', { name: 'Add' }));
    await waitFor(() => expect(writes(f).some((w) => /^POST .*\/tasks\/T-1\/context/.test(w))).toBe(true));
  });
});

/* ---- the task page: Edit -------------------------------------------------------------------------- */

async function pageAs(role: string): Promise<void> {
  vi.stubGlobal('EventSource', FakeES);
  vi.stubGlobal(
    'fetch',
    routeFetch({
      '/api/projects$': { json: { projects: [{ projectId: 'p1', name: 'P1', role }] } },
      '/projects/p1$': { json: { project: { id: 'p1', name: 'P1', members: { 'alice@example.com': role }, settings: { reviewEnabled: true, quotaEnabled: false, txnCap: null } } } },
    }),
  );
  render(
    <QueryClientProvider client={newClient()}>
      <AuthProvider>
        <MemoryRouter initialEntries={['/projects/p1/boards/b1/tasks/T-1']}>
          <ProjectProvider>
            <Routes>
              <Route path="/projects/:projectId/boards/:boardId/tasks/:taskId" element={<TaskPage />} />
            </Routes>
          </ProjectProvider>
        </MemoryRouter>
      </AuthProvider>
    </QueryClientProvider>,
  );
  await screen.findByRole('button', { name: 'Edit' });
  await settle();
}

describe('the task page closes Edit below master', () => {
  it('a viewer sees Edit and Details closed with their reasons', async () => {
    await pageAs('viewer');
    await waitFor(() => described(btn('Edit'), 'Only a master can edit tasks.'));
    described(screen.getByLabelText('Priority'), 'Only a master can edit details.');
    described(btn(/Drop a file/), 'A viewer cannot attach files.');
  });

  it('POSITIVE CONTROL: a master can edit', async () => {
    await pageAs('master');
    await waitFor(() => expect(btn('Edit')).toBeEnabled());
    fireEvent.click(btn('Edit'));
    expect(await screen.findByRole('button', { name: 'Cancel' })).toBeInTheDocument();
  });
});

/* ---- Projects: "New board" and opening a project with no boards ------------------------------------ */

function projectsAs(role: string): Calls {
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith('/api/session')) return json({ identity: 'alice@example.com', csrfToken: null, isAdmin: false, subscriptionsEnabled: false, uiSettings: {} });
    if (/\/api\/projects$/.test(url)) return json({ projects: [{ projectId: 'P-1', name: 'Apollo', role }] });
    if (/\/projects\/P-1\/boards$/.test(url)) return json(init?.method === 'POST' ? { board: { id: 'B-9', name: 'Main' } } : { boards: [] });
    return json({ notifications: [], unread: 0, sessions: [] });
  });
  vi.stubGlobal('fetch', fetchMock);
  render(
    <QueryClientProvider client={newClient()}>
      <AuthProvider>
        <MemoryRouter initialEntries={['/projects']}>
          <ProjectProvider>
            <Routes>
              <Route path="/projects" element={<Projects />} />
              <Route path="/projects/:projectId/boards/:boardId" element={<p>the board</p>} />
            </Routes>
          </ProjectProvider>
        </MemoryRouter>
      </AuthProvider>
    </QueryClientProvider>,
  );
  return fetchMock as unknown as Calls;
}

describe('Projects creates a board only for a role that may (create_board is tier write)', () => {
  it('a viewer opening a project with no boards selects it, creates nothing, and sees "New board" closed with the reason', async () => {
    const f = projectsAs('viewer');
    fireEvent.click(await screen.findByText('Apollo'));
    await waitFor(() => expect(screen.getByText('No boards in this project yet.')).toBeInTheDocument());
    expect(localStorage.getItem('bd.projectId')).toBe('P-1');
    described(screen.getByLabelText('New board name'), 'A viewer cannot create boards.');
    described(btn('New board'), 'A viewer cannot create boards.');
    expect(writes(f)).toEqual([]);
  });

  it('POSITIVE CONTROL: a worker opening it creates the first board and lands on it', async () => {
    const f = projectsAs('worker');
    fireEvent.click(await screen.findByText('Apollo'));
    expect(await screen.findByText('the board')).toBeInTheDocument();
    expect(writes(f).some((w) => /^POST .*\/projects\/P-1\/boards$/.test(w))).toBe(true);
  });
});

/* ---- Chronicle: New record, Create record, Edit, Delete, Land, Revert ------------------------------- */

const PAGE = {
  id: 'CP-1', projectId: 'P-1', kind: 'adr' as const, slug: 'adr/0001-x', title: 'A decision', headVersionId: 'CV-2',
  sourcePath: null, createdAt: '2026-07-31T00:00:00Z', updatedAt: '2026-07-31T00:00:00Z',
};
const head = {
  id: 'CV-2', pageId: 'CP-1', parentVersionId: null, source: 'human' as const, author: 'a@b.c', agent: null, ts: '2026-07-31T00:00:00Z',
  blocks: [{ id: 'b1', state: 'derived' as const, text: 'original text', citations: [], origin: null }],
};
const meta = (n: number) => ({ id: `CV-${n}`, parentVersionId: null, source: 'human' as const, author: `u${n}@x`, agent: null, ts: '2026-07-31T00:00:00Z', blockCount: 1 });

function chronicleAs(role: string, entry: string): Calls {
  localStorage.setItem('bd.projectId', 'P-1');
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes('/api/session')) return json({ identity: 'a@b.c', csrfToken: 't', isAdmin: false });
    if (url.endsWith('/api/projects')) return json({ projects: [{ projectId: 'P-1', name: 'P', role }] });
    if (url.includes('/chronicle/CP-1/versions')) return json({ versions: [meta(2), meta(1)] });
    if (url.includes('/chronicle/CP-1')) return json({ page: PAGE, head, drifted: [], conflicts: null });
    if (url.includes('/chronicle')) return json({ pages: [] });
    return json({});
  });
  vi.stubGlobal('fetch', fetchMock);
  render(
    <QueryClientProvider client={newClient()}>
      <MemoryRouter initialEntries={[entry]}>
        <ProjectProvider>
          <Routes>
            <Route path="/chronicle" element={<Chronicle />} />
            <Route path="/chronicle/new" element={<ChronicleNew />} />
            <Route path="/chronicle/:pageId" element={<ChroniclePageView />} />
            <Route path="/chronicle/:pageId/history" element={<ChronicleHistory />} />
          </Routes>
        </ProjectProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return fetchMock as unknown as Calls;
}

describe('Chronicle offers writing from worker up (create, edit and delete are tier write)', () => {
  it('a viewer: the index offers no "New record" link and says why', async () => {
    chronicleAs('viewer', '/chronicle');
    await waitFor(() => expect(screen.getAllByText('A viewer cannot write records.').length).toBeGreaterThan(0));
    expect(screen.queryByRole('link', { name: 'New record' })).toBeNull();
    expect(screen.queryByRole('link', { name: 'Write the first record' })).toBeNull();
  });

  it('POSITIVE CONTROL: a worker gets the "New record" link', async () => {
    chronicleAs('worker', '/chronicle');
    expect(await screen.findByRole('link', { name: 'New record' })).toBeInTheDocument();
    expect(screen.queryByText('A viewer cannot write records.')).toBeNull();
  });

  it('a viewer on /chronicle/new: Create record is closed with the reason', async () => {
    const f = chronicleAs('viewer', '/chronicle/new');
    await waitFor(() => described(btn('Create record'), 'A viewer cannot write records.'));
    fireEvent.click(btn('Create record'));
    await settle();
    expect(writes(f)).toEqual([]);
  });

  it('POSITIVE CONTROL: a worker on /chronicle/new has no reason shown', async () => {
    chronicleAs('worker', '/chronicle/new');
    await screen.findByRole('button', { name: 'Create record' });
    await settle();
    expect(screen.queryByText('A viewer cannot write records.')).toBeNull();
  });

  it('a viewer on a record: Edit and Delete are closed with the reason, and neither opens anything', async () => {
    const f = chronicleAs('viewer', '/chronicle/CP-1');
    await screen.findByText('original text');
    await waitFor(() => described(btn('Edit'), 'A viewer cannot edit or delete records.'));
    described(btn('Delete'), 'A viewer cannot edit or delete records.');
    fireEvent.click(btn('Edit'));
    await settle();
    expect(screen.queryByText('Save version')).toBeNull();
    expect(writes(f)).toEqual([]);
  });

  it('POSITIVE CONTROL: a worker can open the editor', async () => {
    chronicleAs('worker', '/chronicle/CP-1');
    await screen.findByText('original text');
    await waitFor(() => expect(btn('Edit')).toBeEnabled());
    expect(btn('Delete')).toBeEnabled();
    fireEvent.click(btn('Edit'));
    expect(await screen.findByText('Save version')).toBeInTheDocument();
  });

  it('a viewer on history: Revert is closed with the reason', async () => {
    const f = chronicleAs('viewer', '/chronicle/CP-1/history');
    await waitFor(() => expect(screen.getByText('A viewer cannot revert records.')).toBeInTheDocument());
    for (const b of await screen.findAllByRole('button', { name: 'Revert to this' })) described(b, 'A viewer cannot revert records.');
    fireEvent.click(screen.getAllByRole('button', { name: 'Revert to this' })[0]!);
    await settle();
    expect(writes(f)).toEqual([]);
  });

  it('POSITIVE CONTROL: a worker can revert', async () => {
    chronicleAs('worker', '/chronicle/CP-1/history');
    const reverts = await screen.findAllByRole('button', { name: 'Revert to this' });
    await waitFor(() => expect(reverts[0]).toBeEnabled());
    expect(screen.queryByText('A viewer cannot revert records.')).toBeNull();
  });
});
