/**
 * The board's Activity rail shows who did it in full.
 *
 * A rail row was ONE nowrap line in a 280px column: avatar, name, verb, task id, time. Only the name
 * could shrink (a Marquee, min-width 0, capped at 120px), so it took the whole squeeze: next to "updated
 * an attachment on" the name was cut to a few letters, and "claude-code" and "claude-codex" read the
 * same ("claude-co"). The name now has a line of its own beside the time, and the action goes under it.
 *
 * LIMIT: jsdom does no layout, so this pins the STRUCTURE that gives the name the row's width (its own
 * line, no width cap, the full text and a tooltip), not measured pixels.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { Board } from '../screens/Board.js';

class FakeES {
  readyState = 0;
  addEventListener(): void {}
  close(): void {
    this.readyState = 2;
  }
}
const board = { id: 'b1', projectId: 'p1', name: 'Board', columns: [{ id: 'c1', name: 'To do', order: 1, status: 'TODO', wipLimit: null }] };
const events = [
  { id: 'E-1', type: 'attachment.updated', actor: 'U-1', agent: 'claude-codex', taskId: 'T-12', ts: new Date().toISOString() },
  { id: 'E-2', type: 'comment.added', actor: 'U-1', agent: 'claude-code', taskId: 'T-13', ts: new Date().toISOString() },
];

beforeEach(() => {
  localStorage.setItem('bd.boardRail', 'open');
  vi.stubGlobal('EventSource', FakeES);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      const routes: Array<[RegExp, unknown]> = [
        [/\/boards\/b1\/tasks\?/, { tasks: [] }],
        [/\/boards\/b1\/feed$/, { events }],
        [/\/boards\/b1$/, { board, taskCounts: {} }],
        [/\/boards$/, { boards: [board] }],
        [/\/agents\/sessions/, { sessions: [] }],
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

describe('the Activity rail', () => {
  it('gives each actor’s name a line of its own, uncapped, with the full name as its tooltip', async () => {
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MemoryRouter initialEntries={['/projects/p1/boards/b1']}>
          <Routes>
            <Route path="/projects/:projectId/boards/:boardId" element={<Board />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    for (const name of ['claude-codex', 'claude-code']) {
      const el = await waitFor(() => screen.getByRole('group', { name }));
      expect(el).toHaveTextContent(new RegExp(`^${name}$`));
      expect(el.getAttribute('title')).toBe(name);
      expect(el.style.maxWidth, 'a fixed cap cuts a long name short').toBe('');
      // Its line holds the name and the time only: the verb and the task id are not competing for it.
      const line = el.parentElement!;
      expect(line).not.toHaveTextContent(/attachment|commented|T-1[23]/);
    }
    expect(screen.getByText(/updated an attachment on/)).toHaveTextContent('T-12');
  });
});
