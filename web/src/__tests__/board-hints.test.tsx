/**
 * The board's small on-screen promises are true.
 *
 * - A racing card's tooltip read "racing — N runs open", the letter N and all.
 * - The board's search box advertised `( / )`, but `/` opens the command palette (palette.tsx), so
 *   what the reader typed went there.
 * - Every empty column offered "No tasks — press c", Done and Review included, though a created task
 *   always lands in a TODO column (create_task, ADR-0005).
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { Board } from '../screens/Board.js';
import { ProjectProvider } from '../projectContext.js';
import type { Task } from '../types.js';

class FakeES {
  readyState = 0;
  addEventListener(): void {}
  close(): void {
    this.readyState = 2;
  }
}
const board = {
  id: 'b1', projectId: 'p1', name: 'Board',
  columns: [
    { id: 'c1', name: 'To do', order: 1, status: 'TODO', wipLimit: null },
    { id: 'c2', name: 'Review', order: 2, status: 'REVIEW', wipLimit: null },
    { id: 'c3', name: 'Done', order: 3, status: 'DONE', wipLimit: null },
    { id: 'c4', name: 'In progress', order: 4, status: 'IN_PROGRESS', wipLimit: null },
  ],
};
const racing = {
  id: 'T-1', projectId: 'p1', boardId: 'b1', columnId: 'c4', title: 'Race me', description: '', status: 'IN_PROGRESS', priority: 'normal',
  assignee: null, labels: [], requiredCapabilities: [], estimate: null, dueAt: null, customFields: {}, parentTaskId: null, order: 'a0',
  blockedBy: [], blocks: [], contextSummary: '', version: 1, claim: null, createdBy: 'alice@example.com', racing: true,
  createdAt: '2026-10-30T00:00:00.000Z', updatedAt: '2026-10-30T00:00:00.000Z',
} as unknown as Task;

beforeEach(() => {
  localStorage.setItem('bd.projectId', 'p1');
  vi.stubGlobal('EventSource', FakeES);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      const routes: Array<[RegExp, unknown]> = [
        [/\/boards\/b1\/tasks\?/, { tasks: [racing] }],
        [/\/boards\/b1\/feed$/, { events: [] }],
        [/\/boards\/b1$/, { board, taskCounts: { c4: 1 } }],
        [/\/boards$/, { boards: [board] }],
        [/\/agents\/sessions/, { sessions: [] }],
        // A master may create tasks, so the empty columns' hint is the one under test.
        [/\/api\/projects$/, { projects: [{ projectId: 'p1', name: 'P1', role: 'master' }] }],
      ];
      return { ok: true, status: 200, json: async () => routes.find(([re]) => re.test(url))?.[1] ?? {} } as Response;
    }),
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
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
const column = (name: string) => screen.getByText(name, { selector: '.column header span' }).closest('.column') as HTMLElement;

describe('the board’s hints say what is true', () => {
  it('the racing badge’s tooltip carries no placeholder', async () => {
    renderBoard();
    const badge = await waitFor(() => {
      const b = document.querySelector('.badge.racing');
      expect(b).not.toBeNull();
      return b!;
    });
    expect(badge.getAttribute('title')).not.toMatch(/\bN\b/);
  });

  it('the search box does not advertise `/`, which opens the command palette', async () => {
    renderBoard();
    const box = await screen.findByRole('textbox', { name: 'Search tasks' });
    expect(box.getAttribute('placeholder')).not.toContain('/');
  });

  it('only a TODO column offers "press c"; other empty columns say "No tasks"', async () => {
    renderBoard();
    await waitFor(() => expect(within(column('To do')).getByText(/No tasks/)).toBeInTheDocument());
    await waitFor(() => expect(within(column('To do')).getByText('c', { selector: '.kbd' })).toBeInTheDocument());
    for (const name of ['Review', 'Done']) {
      expect(within(column(name)).getByText('No tasks')).toBeInTheDocument();
      expect(within(column(name)).queryByText('c', { selector: '.kbd' }), `${name} offers c`).toBeNull();
    }
  });
});
