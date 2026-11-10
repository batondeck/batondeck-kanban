/**
 * The board's task list past one page, and a task list that fails to load.
 *
 * `list_tasks` takes `limit` 1 to 200 (internal/service/tasks.go) and pages with `cursor`. "Load more"
 * used to ask for `limit=400`, which the server refuses with 400 VALIDATION; nothing showed the
 * refusal, and the bigger `limit` stuck, so every later refetch of the list was refused too. The fake
 * below refuses a limit over 200 exactly as the server does and pages by cursor.
 *
 * The board's first page is set to 20 (`taskPageLimit`, admin-tunable) so the cases render 25 cards,
 * not 205: "Load more" still asks for 20 + 200 rows, past the server's 200, as it does at the default.
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
import { hydrateUiSettings } from '../settings.js';
import { listTasksUpTo } from '../api/boards.js';
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
/** Runs once after the board's first page is served: a change landing between two page reads. */
let afterFirstPage: (() => void) | null = null;
/** Holds the answer to the next task-list request (read at request time) until it resolves: a read
 *  still in flight. */
let holdNextRead: Promise<void> | null = null;
const json = (status: number, b: unknown) => ({ ok: status < 400, status, json: async () => b }) as Response;

beforeEach(() => {
  FakeES.instances = [];
  all = Array.from({ length: 25 }, (_, i) => mk(i + 1));
  hydrateUiSettings({ taskPageLimit: 20 });
  failTasks = false;
  taskUrls = [];
  afterFirstPage = null;
  holdNextRead = null;
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
        const next = from + limit < all.length ? { nextCursor: String(from + limit) } : {};
        if (from === 0 && afterFirstPage) { afterFirstPage(); afterFirstPage = null; }
        if (holdNextRead) { const h = holdNextRead; holdNextRead = null; await h; }
        return json(200, { tasks: page, ...next });
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
  hydrateUiSettings(undefined);
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
  it('Load more shows the rest of the board, and no request asks for more than 200', async () => {
    const qc = renderBoard();
    await waitFor(() => expect(cards()).toBe(20));
    // The header says the board holds more than is shown, from the board's own counts.
    expect(screen.getByText('20 of 25 tasks')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => expect(cards()).toBe(25));
    expect(screen.getByText('25 tasks')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
    for (const q of taskUrls) expect(Number(new URLSearchParams(q).get('limit')), q).toBeLessThanOrEqual(200);

    // A later refetch of the list (the settle refetch after a drag, a resync, the fallback poll) is
    // not refused either, and keeps every row.
    taskUrls = [];
    await act(async () => { await qc.invalidateQueries({ queryKey: ['tasks', 'p1', 'b1'] }); });
    await waitFor(() => expect(taskUrls.length).toBeGreaterThan(0));
    await waitFor(() => expect(cards()).toBe(25));
    expect(screen.queryByText(/Couldn’t load the tasks/)).toBeNull();
  });

  it('a board frame (one page, with its cursor) does not drop the rows Load more fetched', async () => {
    renderBoard();
    await waitFor(() => expect(cards()).toBe(20));
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => expect(cards()).toBe(25));
    const es = FakeES.instances.at(-1)!;
    // internal/api/stream.go boardTasksLimit: a frame carries one page (200 there; 20 here, as the
    // board's first page is), with its cursor.
    act(() => {
      es.readyState = 1;
      es.emit('open');
      es.emit('board', { board: { board, taskCounts: { c1: 25 } }, tasks: { tasks: all.slice(0, 20), nextCursor: '20' }, feed: { events: [] } });
    });
    await act(() => new Promise<void>((r) => setTimeout(r, 30)));
    expect(cards()).toBe(25);
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull();
  });

  it.each([
    ['after Load more', true],
    ['on the first page', false],
  ])('a read already in flight when a frame lands does not put the older rows back (%s)', async (_, more) => {
    const qc = renderBoard();
    await waitFor(() => expect(cards()).toBe(20));
    const shown = more ? 25 : 20;
    if (more) {
      fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
      await waitFor(() => expect(cards()).toBe(25));
    }
    const es = FakeES.instances.at(-1)!;
    act(() => {
      es.readyState = 1;
      es.emit('open');
    });

    // A re-read of the list starts (a settle refetch, say): the server reads the rows, and the answer
    // is still on its way.
    let release!: () => void;
    holdNextRead = new Promise<void>((r) => { release = r; });
    taskUrls = [];
    void qc.invalidateQueries({ queryKey: ['tasks', 'p1', 'b1'] });
    await waitFor(() => expect(taskUrls.length).toBeGreaterThan(0));

    // Then T-1 is renamed, and the frame for that change lands while the read is still out.
    all = [{ ...all[0]!, title: 'Renamed' }, ...all.slice(1)];
    act(() => {
      es.emit('board', { board: { board, taskCounts: { c1: 25 } }, tasks: { tasks: all.slice(0, 20), nextCursor: '20' }, feed: { events: [] } });
    });
    await waitFor(() => expect(screen.getByText('Renamed')).toBeInTheDocument());

    // The read that began before the rename finishes now: it must not overwrite the frame.
    await act(async () => { release(); await new Promise<void>((r) => setTimeout(r, 50)); });
    await waitFor(() => expect(cards()).toBe(shown));
    expect(screen.getByText('Renamed')).toBeInTheDocument();
    expect(screen.queryByText('Task 1'), 'the older row came back').toBeNull();
  });

  it('a frame that lands while Load more is reading is not lost when the longer list is shown', async () => {
    // main.tsx's staleTime: the longer list is not re-read just because the board starts showing it.
    renderBoard(new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } }));
    await waitFor(() => expect(cards()).toBe(20));
    const es = FakeES.instances.at(-1)!;
    act(() => {
      es.readyState = 1;
      es.emit('open');
    });
    let release!: () => void;
    holdNextRead = new Promise<void>((r) => { release = r; });
    taskUrls = [];
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => expect(taskUrls.length).toBeGreaterThan(0));
    // T-1 is renamed while the longer list is on its way; its frame lands first.
    all = [{ ...all[0]!, title: 'Renamed' }, ...all.slice(1)];
    act(() => {
      es.emit('board', { board: { board, taskCounts: { c1: 25 } }, tasks: { tasks: all.slice(0, 20), nextCursor: '20' }, feed: { events: [] } });
    });
    await waitFor(() => expect(screen.getByText('Renamed')).toBeInTheDocument());
    await act(async () => { release(); await new Promise<void>((r) => setTimeout(r, 50)); });
    await waitFor(() => expect(cards()).toBe(25));
    await waitFor(() => expect(screen.getByText('Renamed')).toBeInTheDocument());
    expect(screen.queryByText('Task 1'), 'the older row came back').toBeNull();
  });

  it('a Load more that fails says so, keeps the cards shown, and leaves later refetches working', async () => {
    const qc = renderBoard();
    await waitFor(() => expect(cards()).toBe(20));
    failTasks = true;
    fireEvent.click(screen.getByRole('button', { name: 'Load more' }));
    await waitFor(() => expect(screen.getByText(/Couldn’t load more tasks/)).toBeInTheDocument());
    expect(cards()).toBe(20);
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
    await waitFor(() => expect(cards()).toBe(20));
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

describe('listTasksUpTo', () => {
  it('reads past 200 rows page by page with the cursor, each request within 200', async () => {
    all = Array.from({ length: 450 }, (_, i) => mk(i + 1));
    const out = await listTasksUpTo('p1', 'b1', 420);
    expect(out.tasks).toHaveLength(420);
    expect(out.tasks[419]!.id).toBe('T-420');
    expect(out.nextCursor).toBe('420');
    expect(taskUrls.map((q) => Object.fromEntries(new URLSearchParams(q)))).toEqual([
      { limit: '200' },
      { limit: '200', cursor: '200' },
      { limit: '20', cursor: '400' },
    ]);
  });

  it('lists a task once when it moves between two page reads', async () => {
    all = Array.from({ length: 450 }, (_, i) => mk(i + 1));
    // T-1 is reordered down the board after the first page is read: the second page holds it again.
    afterFirstPage = () => { const [t1] = all.splice(0, 1); all.splice(250, 0, t1!); };
    const out = await listTasksUpTo('p1', 'b1', 420);
    const ids = out.tasks.map((t) => t.id);
    expect(ids.filter((id) => id === 'T-1'), 'T-1 listed twice').toHaveLength(1);
    expect(new Set(ids).size).toBe(ids.length);
    expect(ids).toHaveLength(420);
  });

  it('stops at the end of the board, with no cursor', async () => {
    const out = await listTasksUpTo('p1', 'b1', 400);
    expect(out.tasks).toHaveLength(25);
    expect(out.nextCursor).toBeUndefined();
  });
});
