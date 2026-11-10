/**
 * A CARD DROPPED BETWEEN TWO CARDS THAT SHARE ONE ORDER KEY SAYS WHY IT DID NOT MOVE.
 *
 * No key sorts strictly between two equal keys, so `generateKeyBetween` throws for that slot. The
 * drop handler caught it and returned: the card snapped back and nothing was said, while a drop at
 * the column's top or bottom worked, so the failure looked random. Boards written before the server
 * gave every landed card a key of its own can still hold such pairs. The drop now says why and how
 * to place the card, and sends nothing; a drop into a slot that has room still moves.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { Board } from '../screens/Board.js';
import { ProjectProvider } from '../projectContext.js';
import { ShortcutProvider } from '../shortcuts.js';
import { task } from './taskFixtures.js';
import type { Task } from '../types.js';

/** The board's DndContext onDragEnd, captured so a test can drop without a pointer. */
const dnd = vi.hoisted(() => ({ onDragEnd: null as null | ((e: unknown) => void) }));
vi.mock('@dnd-kit/core', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@dnd-kit/core')>();
  const Capturing = (props: Parameters<typeof actual.DndContext>[0]) => {
    dnd.onDragEnd = (props.onDragEnd as (e: unknown) => void) ?? null;
    return <actual.DndContext {...props} />;
  };
  return { ...actual, DndContext: Capturing };
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
const writes = (f: Calls) => f.mock.calls.filter(([, init]) => init?.method && init.method !== 'GET').map(([u, init]) => `${init!.method} ${String(u)}`);

const board = {
  id: 'b1', projectId: 'p1', name: 'Board',
  columns: [{ id: 'c1', name: 'To do', order: 1, status: 'TODO', wipLimit: null }, { id: 'c2', name: 'Building', order: 2, status: 'IN_PROGRESS', wipLimit: null }],
};
const base: Task = { ...task, blockedBy: [], dueAt: null, labels: [], requiredCapabilities: [] };
const first: Task = { ...base, id: 'T-1', title: 'First built', columnId: 'c2', status: 'IN_PROGRESS', order: 'a0' };
const second: Task = { ...base, id: 'T-2', title: 'Second built', columnId: 'c2', status: 'IN_PROGRESS', order: 'a0' };
const moving: Task = { ...base, id: 'T-3', title: 'To place', columnId: 'c1', status: 'TODO', order: 'a1' };

async function renderBoard(): Promise<Calls> {
  vi.stubGlobal('EventSource', FakeES);
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    const routes: Array<[RegExp, unknown]> = [
      [/\/api\/projects$/, { projects: [{ projectId: 'p1', name: 'P1', role: 'master' }] }],
      [/\/sprints\?boardId=b1/, { sprints: [] }],
      [/\/boards\/b1\/tasks\?/, { tasks: [first, second, moving] }],
      [/\/boards\/b1\/feed$/, { events: [] }],
      [/\/boards\/b1$/, { board, taskCounts: {} }],
      [/\/boards$/, { boards: [board] }],
      [/\/projects\/p1$/, { project: { id: 'p1', name: 'P1', members: {} } }],
    ];
    return json(routes.find(([re]) => re.test(url))?.[1] ?? {});
  });
  vi.stubGlobal('fetch', fetchMock);
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
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
  await screen.findByText('Second built');
  // A master may move cards: the sortables open once the roster role is read.
  await waitFor(() => expect(document.querySelector('[aria-roledescription="sortable"][aria-disabled="false"]')).not.toBeNull());
  return fetchMock as unknown as Calls;
}

const drop = (activeId: string, overId: string) =>
  act(async () => {
    dnd.onDragEnd!({ active: { id: activeId }, over: { id: overId } });
    await new Promise<void>((r) => setTimeout(r, 30));
  });

describe('a drop between two cards that share an order key', () => {
  it('says why the card did not move and sends nothing', async () => {
    const f = await renderBoard();
    await drop('T-3', 'T-2'); // between T-1 and T-2, both at "a0"
    expect(screen.getByText(/share one position/)).toBeInTheDocument();
    expect(writes(f)).toEqual([]);
  });

  it('POSITIVE CONTROL: a drop at the top of the column, where there is room, moves the card', async () => {
    const f = await renderBoard();
    await drop('T-3', 'T-1');
    await waitFor(() => expect(writes(f).some((w) => /\/tasks\/T-3/.test(w))).toBe(true));
    expect(screen.queryByText(/share one position/)).toBeNull();
  });
});
