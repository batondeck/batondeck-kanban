/**
 * The board's task list past one page, and a task list that fails to load.
 *
 * `list_tasks` takes `limit` 1 to 200 (internal/service/tasks.go) and pages with `cursor`. "Load more"
 * used to ask for `limit=400`, which the server refuses with 400 VALIDATION; nothing showed the
 * refusal, and the bigger `limit` stuck, so every later refetch of the list was refused too. The fake
 * below refuses a limit over 200 exactly as the server does and pages by cursor.
 *
 * And a list that failed rendered as an EMPTY board ("0 tasks", "No tasks" in every column), with
 * nothing saying the read failed.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { Board } from '../screens/Board.js';
import { boardTasksKey, mergeFirstPage } from '../mutations.js';
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
const mk = (i: number): Task =>
  ({
    id: `T-${i}`, projectId: 'p1', boardId: 'b1', columnId: 'c1', title: `Task ${i}`, description: '', status: 'TODO', priority: 'normal',
    assignee: null, labels: [], requiredCapabilities: [], estimate: null, dueAt: null, customFields: {}, parentTaskId: null,
    // Zero-padded so string order is list order, as the store's (order_key, id) is.
    order: `a${String(i).padStart(4, '0')}`,
    blockedBy: [], blocks: [], contextSummary: '', version: 1, claim: null, createdBy: 'alice@example.com',
    createdAt: '2026-10-30T00:00:00.000Z', updatedAt: '2026-10-30T00:00:00.000Z',
  }) as unknown as Task;

let all: Task[] = [];
let failTasks = false;
let taskUrls: string[] = [];
const json = (status: number, b: unknown) => ({ ok: status < 400, status, json: async () => b }) as Response;

beforeEach(() => {
  FakeES.instances = [];
  all = Array.from({ length: 205 }, (_, i) => mk(i + 1));
  failTasks = false;
  taskUrls = [];
  vi.stubGlobal('EventSource', FakeES);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), 'http://x');
      if (/\/boards\/b1\/tasks$/.test(url.pathname)) {
        taskUrls.push(url.search);
        if (failTasks) return json(500, { code: 'INTERNAL', message: 'database is locked' });
        const limit = Number(url.searchParams.get('limit') ?? '50');
        // internal/service/tasks.go: `if req.Limit < 0 || req.Limit > listMaxLimit` -> VALIDATION.
        if (limit < 0 || limit > 200) return json(400, { code: 'VALIDATION', message: 'limit is 1 to 200' });
        const from = Number(url.searchParams.get('cursor') ?? '0');
        const page = all.slice(from, from + limit);
        return json(200, { tasks: page, ...(from + limit < all.length ? { nextCursor: String(from + limit) } : {}) });
      }
      const routes: Array<[RegExp, unknown]> = [
        [/\/boards\/b1\/feed$/, { events: [] }],
        [/\/boards\/b1$/, { board, taskCounts: { c1: all.length } }],
        [/\/boards$/, { boards: [board] }],
        [/\/agents\/sessions/, { sessions: [] }],
      ];
      return json(200, routes.find(([re]) => re.test(url.pathname))?.[1] ?? {});
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const newClient = () => new QueryClient({ defaultOptions: { queries: { retry: false } } });
function renderBoard(qc = newClient()) {
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/projects/p1/boards/b1']}>
        <Routes>
          <Route path="/projects/:projectId/boards/:boardId" element={<Board />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return qc;
}
const cards = () => document.querySelectorAll('.card[data-task-id]').length;

describe('the board pages its task list with the cursor (limit stays within 1..200)', () => {
  it('Load more shows the rest of a 205-task board, and no request asks for more than 200', async () => {
    const qc = renderBoard();
    await waitFor(() => expect(cards()).toBe(200));
    // The header says the board holds more than is shown, from the board's own counts.
    expect(screen.getByText('200 of 205 tasks')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => expect(cards()).toBe(205));
    expect(screen.getByText('205 tasks')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
    for (const q of taskUrls) expect(Number(new URLSearchParams(q).get('limit')), q).toBeLessThanOrEqual(200);
    expect(taskUrls.some((q) => new URLSearchParams(q).has('cursor')), 'the second page is read by cursor').toBe(true);

    // A later refetch of the list (the settle refetch after a drag, a resync, the fallback poll) is
    // not refused either, and keeps every row.
    taskUrls = [];
    await act(async () => { await qc.invalidateQueries({ queryKey: ['tasks', 'p1', 'b1'] }); });
    await waitFor(() => expect(taskUrls.length).toBeGreaterThan(0));
    await waitFor(() => expect(cards()).toBe(205));
    expect(screen.queryByText(/Couldn’t load the tasks/)).toBeNull();
  });

  it('a board frame (the first 200 tasks) does not drop the rows Load more fetched', async () => {
    renderBoard();
    await waitFor(() => expect(cards()).toBe(200));
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => expect(cards()).toBe(205));
    const es = FakeES.instances.at(-1)!;
    // internal/api/stream.go boardTasksLimit: a frame carries one page of 200, with its cursor.
    act(() => {
      es.readyState = 1;
      es.emit('open');
      es.emit('board', { board: { board, taskCounts: { c1: 205 } }, tasks: { tasks: all.slice(0, 200), nextCursor: '200' }, feed: { events: [] } });
    });
    await act(() => new Promise<void>((r) => setTimeout(r, 30)));
    expect(cards()).toBe(205);
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
  });

  it('a Load more that fails says so, keeps the cards shown, and leaves later refetches working', async () => {
    const qc = renderBoard();
    await waitFor(() => expect(cards()).toBe(200));
    failTasks = true;
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => expect(screen.getByText(/Couldn’t load more tasks/)).toBeInTheDocument());
    expect(cards()).toBe(200);
    failTasks = false;
    taskUrls = [];
    await act(async () => { await qc.invalidateQueries({ queryKey: ['tasks', 'p1', 'b1'] }); });
    await waitFor(() => expect(taskUrls.length).toBeGreaterThan(0));
    for (const q of taskUrls) expect(Number(new URLSearchParams(q).get('limit')), q).toBeLessThanOrEqual(200);
  });
});

describe('a task list that failed to load is an error, not an empty board', () => {
  it('says the read failed, offers Retry, and does not claim the columns are empty', async () => {
    failTasks = true;
    renderBoard();
    await waitFor(() => expect(screen.getByText(/Couldn’t load the tasks/)).toBeInTheDocument());
    expect(screen.getByText(/database is locked/)).toBeInTheDocument();
    expect(screen.queryByText(/^No tasks/)).toBeNull();
    expect(screen.queryByText('0 tasks')).toBeNull();

    failTasks = false;
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(cards()).toBe(200));
    expect(screen.queryByText(/Couldn’t load the tasks/)).toBeNull();
  });

  it('CONTROL: a board that loaded and is empty still says so', async () => {
    all = [];
    renderBoard();
    await waitFor(() => expect(screen.getByText('0 tasks')).toBeInTheDocument());
    await waitFor(() => expect(screen.getByText(/^No tasks/)).toBeInTheDocument());
  });
});

describe('mergeFirstPage', () => {
  const page = (tasks: Task[], nextCursor?: string) => ({ tasks, ...(nextCursor ? { nextCursor } : {}) });
  it('a frame that holds the whole board replaces the cache outright', () => {
    const out = mergeFirstPage(page(all.slice(0, 3)), page(all.slice(0, 2)));
    expect(out.tasks.map((t) => t.id)).toEqual(['T-1', 'T-2']);
  });
  it('a first-page frame keeps the cached rows beyond its page, minus the ones it now carries', () => {
    const cached = page(all.slice(0, 5));
    const frame = page([all[0]!, all[2]!, all[3]!], 'c'); // T-2 was deleted, so T-4 moved up into page one
    expect(mergeFirstPage(cached, frame).tasks.map((t) => t.id)).toEqual(['T-1', 'T-3', 'T-4', 'T-5']);
  });
  it('CONTROL: a cache of one page is replaced by the frame', () => {
    const k = boardTasksKey('p1', 'b1', 200);
    expect(k).toEqual(['tasks', 'p1', 'b1', 200]);
    const frame = page(all.slice(1, 4), 'c');
    expect(mergeFirstPage(page(all.slice(0, 3), 'c'), frame)).toBe(frame);
  });
});
