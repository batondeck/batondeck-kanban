/**
 * T-51 guard: board card labels must never be dropped or truncated away.
 *
 * The bug this pins: a card rendered `contex…` `cross-ta…` `+1` — three chips, none legible, and the
 * third label reachable by nobody. The fix routes every truncating chip surface through `Marquee`,
 * which keeps ALL of the content in the DOM (clipping is visual only) and names the whole list.
 *
 * jsdom has no layout, so the marquee's motion and its measured `+N` badge CANNOT be asserted here —
 * they are verified in a real browser. What IS asserted is the contract the animation hangs off:
 * every label present, the full list as the accessible name, and a `.marquee-track` for the CSS
 * keyframe to move. Restore `t.labels.slice(0, 2)` in Board.tsx and the first test fails.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, cleanup, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { Board } from '../screens/Board.js';
import { Marquee } from '../components.js';
import type { Task } from '../types.js';

// The real-world case from the ticket: labels long enough that a 300px column cannot show them.
const LABELS = ['context-sync', 'cross-task-memory', 'needs-design-review'];

const task: Task = {
  id: 'T-1',
  projectId: 'p1',
  boardId: 'b1',
  columnId: 'c1',
  title: 'Ship the parser',
  description: '',
  status: 'TODO',
  priority: 'high',
  assignee: null,
  labels: LABELS,
  requiredCapabilities: [],
  estimate: null,
  dueAt: null,
  customFields: {},
  parentTaskId: null,
  order: 'a0',
  blockedBy: [],
  blocks: [],
  contextSummary: '',
  version: 1,
  claim: null,
  createdBy: 'alice@example.com',
  createdAt: '2026-08-01T00:00:00.000Z',
  updatedAt: new Date().toISOString(),
};

const board = {
  id: 'b1',
  projectId: 'p1',
  name: 'Board',
  columns: [{ id: 'c1', name: 'To do', order: 1, status: 'TODO', wipLimit: null }],
};

beforeEach(() => {
  // The board opens an SSE stream on mount; jsdom has no EventSource.
  vi.stubGlobal(
    'EventSource',
    class {
      readyState = 1;
      addEventListener(): void {}
      close(): void {}
    },
  );
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      const routes: Array<[RegExp, unknown]> = [
        [/\/boards\/b1\/tasks\?/, { tasks: [task] }],
        [/\/boards\/b1\/feed$/, { events: [] }],
        [/\/boards\/b1$/, { board, taskCounts: {} }],
        [/\/boards$/, { boards: [board] }],
        [/\/agents\/sessions/, { sessions: [] }],
      ];
      const hit = routes.find(([re]) => re.test(url));
      return { ok: true, status: 200, json: async () => hit?.[1] ?? {} } as Response;
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function renderBoard() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/projects/p1/boards/b1']}>
        <Routes>
          <Route path="/projects/:projectId/boards/:boardId" element={<Board />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('board card labels (T-51)', () => {
  it('renders EVERY label — none is dropped behind a "+N" dead end', async () => {
    renderBoard();
    // Scoped to the card's chip row: the labels also populate the filter bar's <select>.
    const row = await screen.findByRole('group', { name: /^Labels:/ });
    for (const l of LABELS) expect(within(row).getByText(l)).toBeInTheDocument();
    expect(within(row).queryByText(/^\+\d/)).toBeNull(); // no "+1" placeholder standing in for content
  });

  it('names the chip row with the full label list, so truncation never hides it from a screen reader', async () => {
    renderBoard();
    await waitFor(() => expect(screen.getByRole('group', { name: `Labels: ${LABELS.join(', ')}` })).toBeInTheDocument());
  });

  it('gives the row a marquee track for the CSS keyframe to translate', async () => {
    renderBoard();
    const row = await screen.findByRole('group', { name: /^Labels:/ });
    expect(row).toHaveClass('marquee');
    expect(row.firstElementChild).toHaveClass('marquee-track');
  });
});

describe('Marquee', () => {
  it('exposes its full text as the accessible name and as a tooltip', () => {
    render(<Marquee label="a very long agent name@example.com">agent</Marquee>);
    const el = screen.getByRole('group', { name: 'a very long agent name@example.com' });
    expect(el).toHaveAttribute('title', 'a very long agent name@example.com');
  });

  it('stays presentational (no duplicate name) when an enclosing control already names it', () => {
    render(
      <button title="Assigned to alice">
        <Marquee>alice</Marquee>
      </button>,
    );
    expect(screen.queryByRole('group')).toBeNull();
    expect(screen.getByRole('button', { name: 'alice' })).toBeInTheDocument();
  });
});
