/**
 * Creating a task on the board never shows it twice.
 *
 * The optimistic placeholder ("T-…") and the server's card have different React keys, so when the
 * server card arrived (a board frame, or the refetch after the create) the placeholder left through
 * the cards' exit fade while the real card entered beside it: for a moment the column held the task
 * twice. And two creates in flight shared one placeholder id, so one React key.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { Board } from '../screens/Board.js';
import { ProjectProvider } from '../projectContext.js';
import { boardTasksKey, isTempTaskId, useTaskCreate } from '../mutations.js';
import type { Task } from '../types.js';

class FakeES {
  static instances: FakeES[] = [];
  readyState = 0;
  listeners = new Map<string, Array<(e: { data: string; lastEventId: string }) => void>>();
  constructor(public url: string) {
    FakeES.instances.push(this);
  }
  addEventListener(t: string, fn: (e: { data: string; lastEventId: string }) => void): void {
    this.listeners.set(t, [...(this.listeners.get(t) ?? []), fn]);
  }
  close(): void {
    this.readyState = 2;
  }
  emit(t: string, data: unknown = {}): void {
    for (const fn of this.listeners.get(t) ?? []) fn({ data: JSON.stringify(data), lastEventId: '' });
  }
}

const board = { id: 'b1', projectId: 'p1', name: 'Board', columns: [{ id: 'c1', name: 'To do', order: 1, status: 'TODO', wipLimit: null }] };
const mk = (id: string, title: string): Task =>
  ({
    id, projectId: 'p1', boardId: 'b1', columnId: 'c1', title, description: '', status: 'TODO', priority: 'normal',
    assignee: null, labels: [], requiredCapabilities: [], estimate: null, dueAt: null, customFields: {}, parentTaskId: null, order: `a${id}`,
    blockedBy: [], blocks: [], contextSummary: '', version: 1, claim: null, createdBy: 'alice@example.com',
    createdAt: '2026-10-30T00:00:00.000Z', updatedAt: '2026-10-30T00:00:00.000Z',
  }) as unknown as Task;

let server: Task[] = [];
let releases: Array<() => void> = [];
let seq = 7;

beforeEach(() => {
  FakeES.instances = [];
  server = [mk('T-1', 'Existing')];
  releases = [];
  seq = 7;
  localStorage.setItem('bd.projectId', 'p1');
  vi.stubGlobal('EventSource', FakeES);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const ok = (b: unknown) => ({ ok: true, status: 200, json: async () => b }) as Response;
      if (init?.method === 'POST' && /\/boards\/b1\/tasks$/.test(url)) {
        const { title } = JSON.parse(String(init.body)) as { title: string };
        const t = mk(`T-${++seq}`, title);
        server = [...server, t]; // committed at once; the ANSWER is held until the test releases it
        return new Promise<Response>((r) => releases.push(() => r(ok({ task: t }))));
      }
      const routes: Array<[RegExp, () => unknown]> = [
        [/\/boards\/b1\/tasks\?/, () => ({ tasks: server })],
        [/\/boards\/b1\/feed$/, () => ({ events: [] })],
        [/\/boards\/b1$/, () => ({ board, taskCounts: { c1: server.length } })],
        [/\/boards$/, () => ({ boards: [board] })],
        [/\/agents\/sessions/, () => ({ sessions: [] })],
        [/\/api\/projects$/, () => ({ projects: [{ projectId: 'p1', name: 'P1', role: 'master' }] })],
      ];
      return ok(routes.find(([re]) => re.test(url))?.[1]() ?? {});
    }),
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  localStorage.clear();
});

function renderBoard() {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter initialEntries={['/projects/p1/boards/b1']}>
        <ProjectProvider>
          <Routes>
            <Route path="/projects/:projectId/boards/:boardId" element={<Board />} />
          </Routes>
        </ProjectProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}
const titled = (title: string) => [...document.querySelectorAll('.card .title')].filter((n) => n.textContent === title).length;
async function create(title: string) {
  const box = screen.getByRole('textbox', { name: 'Task title' });
  fireEvent.change(box, { target: { value: title } });
  fireEvent.keyDown(box, { key: 'Enter' });
  await waitFor(() => expect(titled(title)).toBe(1)); // the placeholder is up
}
/** Watch the column for a while and report the most cards it ever held with this title. */
async function most(title: string, ms = 400) {
  let max = 0;
  const end = Date.now() + ms;
  while (Date.now() < end) {
    max = Math.max(max, titled(title));
    await act(() => new Promise<void>((r) => setTimeout(r, 10)));
  }
  return max;
}

describe('a created task is on the board once', () => {
  beforeEach(async () => {
    renderBoard();
    await waitFor(() => expect(titled('Existing')).toBe(1));
    fireEvent.click(await screen.findByRole('button', { name: '+ New task' }));
  });

  it('when the create answers, the placeholder becomes the server card with no moment of both', async () => {
    await create('Write the docs');
    act(() => releases.shift()!());
    expect(await most('Write the docs')).toBe(1);
    await waitFor(() => expect(document.querySelector('.card[data-task-id="T-8"]')).not.toBeNull());
  });

  it('when a board frame brings the server card first, the placeholder does not linger beside it', async () => {
    await create('Write the docs');
    const es = FakeES.instances.at(-1)!;
    act(() => {
      es.readyState = 1;
      es.emit('open');
      es.emit('board', { board: { board, taskCounts: { c1: server.length } }, tasks: { tasks: server }, feed: { events: [] } });
    });
    expect(await most('Write the docs')).toBe(1);
    act(() => releases.shift()!());
    expect(await most('Write the docs')).toBe(1);
  });
});

describe('useTaskCreate placeholders', () => {
  it('two creates in flight are two placeholders with ids of their own, each swapped for its own task', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    qc.setQueryData(boardTasksKey('p1', 'b1', 200), { tasks: [mk('T-1', 'Existing')] });
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
    const { result } = renderHook(() => useTaskCreate('p1', 'b1'), { wrapper });
    const ids = () => (qc.getQueryData(boardTasksKey('p1', 'b1', 200)) as { tasks: Task[] }).tasks.map((t) => t.id);
    act(() => result.current.mutate({ title: 'First' }));
    act(() => result.current.mutate({ title: 'Second' }));
    await waitFor(() => expect(ids()).toHaveLength(3));
    const temps = ids().filter(isTempTaskId);
    expect(new Set(temps).size, 'two placeholders under one React key').toBe(2);
    act(() => { for (const r of releases.splice(0)) r(); });
    await waitFor(() => expect(ids().filter(isTempTaskId)).toHaveLength(0));
    expect(ids().sort()).toEqual(['T-1', 'T-8', 'T-9']);
  });
});
