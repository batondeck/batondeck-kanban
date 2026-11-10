/**
 * AN OPEN TICKET FOLLOWS THE BOARD'S STREAM (web-board#12).
 *
 * The full ticket page joined the shared socket for presence only, so no board frame ever reached it:
 * an agent's new comment, context item, subtask or attachment did not appear while the tab stayed
 * focused. And comments were in no nudge at all, so even the drawer (whose board invalidates the
 * other ticket queries) left the thread stale. The ticket hook now joins the stream scoped to the
 * ticket's board and refreshes the ticket's own reads, comments included, on each frame.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { routeFetch } from './taskFixtures.js';

const boardUpdates: Array<{ projectId: string; boardId: string; onUpdate: () => void }> = [];
vi.mock('../api/stream.js', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/stream.js')>();
  return {
    ...real,
    userStream: () => () => {},
    stream: (projectId: string, boardId: string, onUpdate: () => void) => {
      const sub = { projectId, boardId, onUpdate };
      boardUpdates.push(sub);
      return () => { boardUpdates.splice(boardUpdates.indexOf(sub), 1); };
    },
  };
});

const { TaskPage } = await import('../screens/TaskPage.js');
const { ProjectProvider } = await import('../projectContext.js');
const { AuthProvider } = await import('../auth.js');

let fetchMock: ReturnType<typeof vi.fn>;
beforeEach(() => {
  fetchMock = routeFetch() as unknown as ReturnType<typeof vi.fn>;
  vi.stubGlobal('fetch', fetchMock);
});
afterEach(() => {
  vi.unstubAllGlobals();
  cleanup();
});

const reads = (re: RegExp): number => fetchMock.mock.calls.filter(([u, init]) => re.test(String(u)) && ((init as RequestInit | undefined)?.method ?? 'GET') === 'GET').length;

describe('the ticket page follows the board stream', () => {
  it("a board frame refreshes the ticket's comments, context and subtasks", async () => {
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <AuthProvider>
          <MemoryRouter initialEntries={['/projects/p1/boards/b1/tasks/T-1']}>
            <ProjectProvider>
              <Routes><Route path="/projects/:projectId/boards/:boardId/tasks/:taskId" element={<TaskPage />} /></Routes>
            </ProjectProvider>
          </MemoryRouter>
        </AuthProvider>
      </QueryClientProvider>,
    );
    await screen.findAllByText('Ship the parser');
    await waitFor(() => expect(reads(/\/tasks\/T-1\/comments/)).toBeGreaterThan(0));
    const before = { comments: reads(/\/tasks\/T-1\/comments/), ctx: reads(/\/tasks\/T-1\/context$/), subs: reads(/\/tasks\/T-1\/subtasks$/) };
    // The page joined the stream scoped to the ticket's board…
    await waitFor(() => expect(boardUpdates.some((s) => s.projectId === 'p1' && s.boardId === 'b1')).toBe(true));
    // …and a frame for it refreshes the ticket's reads.
    for (const s of boardUpdates) s.onUpdate();
    await waitFor(() => expect(reads(/\/tasks\/T-1\/comments/)).toBeGreaterThan(before.comments));
    await waitFor(() => expect(reads(/\/tasks\/T-1\/context$/)).toBeGreaterThan(before.ctx));
    await waitFor(() => expect(reads(/\/tasks\/T-1\/subtasks$/)).toBeGreaterThan(before.subs));
  });
});
