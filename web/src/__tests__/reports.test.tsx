/**
 * The Reports screen.
 *
 * Three properties here are about HONESTY rather than rendering, and each was a real defect on the
 * screen this one is modelled on:
 *
 *  1. The census PAGES the cursor. `Analytics.tsx` asks for 200 tasks and discards `nextCursor`, so
 *     every figure there is silently truncated at 200 — a headline count cannot ship on that.
 *  2. When the census stops early it SAYS SO. A partial number that looks total is worse than none.
 *  3. A failed `list_tasks` renders an ERROR, not zeros. Today an all-zeros dashboard is
 *     indistinguishable from an empty board, which is a signal that cannot mean anything.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { Reports } from '../screens/Reports.js';
import type { Task } from '../types.js';

/**
 * The project scope is MOCKED rather than provided.
 *
 * `ProjectProvider` derives `projectId` from a fetched roster and writes '' back to localStorage on
 * its first effect — before that fetch resolves — so a test that seeds localStorage and renders
 * immediately watches its own seed get erased, and the screen sits behind its `projectsLoading`
 * guard. The drawer tests avoid this by taking `projectId` as a prop; this screen reads it from
 * context, so the scope is what gets stubbed. What is under test here is the census and the charts,
 * not project selection.
 */
vi.mock('../projectContext.js', () => ({
  useProject: () => ({
    projectId: 'p1',
    setProjectId: () => {},
    projects: [{ projectId: 'p1', name: 'P1', role: 'admin' }],
    loading: false,
  }),
}));

const mkTask = (i: number, over: Partial<Task> = {}): Task =>
  ({
    id: `T-${i}`, projectId: 'p1', boardId: 'b1', columnId: 'c1',
    title: `task ${i}`, description: '', status: 'TODO', priority: 'normal',
    assignee: null, labels: [], requiredCapabilities: [], estimate: null, dueAt: null,
    customFields: {}, parentTaskId: null, order: 'a0', blockedBy: [], blocks: [],
    contextSummary: '', version: 1, claim: null, createdBy: 'u',
    createdAt: new Date(Date.now() - 86_400_000).toISOString(),
    updatedAt: new Date().toISOString(),
    ...over,
  }) as Task;

/** Routes the screen's reads; `tasks` may be a function of the cursor so paging can be observed. */
function stub(opts: {
  pages?: Array<{ tasks: Task[]; nextCursor?: string }>;
  tasksStatus?: number;
  ops?: unknown;
  opsStatus?: number;
} = {}) {
  const calls: string[] = [];
  const pages = opts.pages ?? [{ tasks: [mkTask(1)] }];
  let pageIdx = 0;
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    calls.push(url);
    const json = (body: unknown, status = 200) =>
      ({ ok: status < 400, status, json: async () => body }) as Response;

    // MOST SPECIFIC FIRST. `/api/projects` (the roster ProjectProvider reads) is a prefix of every
    // project-scoped URL, so a loose check here swallows the boards and tasks reads and the screen
    // renders its "no projects" branch forever.
    if (url.endsWith('/api/projects')) {
      return json({ projects: [{ projectId: 'p1', name: 'P1', role: 'admin' }] });
    }
    if (url.endsWith('/boards')) {
      return json({ boards: [{ id: 'b1', projectId: 'p1', name: 'Board', columns: [] }] });
    }
    if (url.includes('/tasks?')) {
      if (opts.tasksStatus) return json({ error: `request failed: ${opts.tasksStatus}` }, opts.tasksStatus);
      const page = pages[Math.min(pageIdx, pages.length - 1)]!;
      pageIdx += 1;
      return json(page);
    }
    if (url.includes('/analytics/operations')) {
      if (opts.opsStatus) return json({ error: `request failed: ${opts.opsStatus}` }, opts.opsStatus);
      return json(opts.ops ?? { projectId: 'p1', days: 30, total: 0, errors: 0, errorRate: 0, avgLatencyMs: 0, byTool: [], byDay: [], byAgent: [] });
    }
    return json({});
  }) as unknown as typeof fetch;
  vi.stubGlobal('fetch', fetchMock);
  return { calls, fetchMock };
}

function renderReports() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/reports']}>
        <Reports />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
  cleanup();
});
beforeEach(() => {
  vi.clearAllMocks();
});

describe('the census pages the cursor', () => {
  it('counts EVERY page, and sends the cursor it was given', async () => {
    const { calls } = stub({
      pages: [
        { tasks: [mkTask(1), mkTask(2)], nextCursor: 'c1' },
        { tasks: [mkTask(3)] },
      ],
    });
    renderReports();
    // 3 tasks across two pages — not 2. This is the assertion `Analytics.tsx` would fail.
    await waitFor(() => expect(calls.some((u) => u.includes('cursor=c1'))).toBe(true));
    await waitFor(() => {
      const tile = [...document.querySelectorAll('.tile')].find((t) => t.textContent?.includes('Tasks'));
      expect(tile?.textContent, 'the census tile must count BOTH pages').toContain('3');
    });
  });

  it('says so when it stops at the cap instead of implying it counted everything', async () => {
    // Every page carries a cursor, so the drain hits MAX_PAGES with more still available.
    stub({ pages: [{ tasks: [mkTask(1)], nextCursor: 'more' }] });
    renderReports();
    expect(await screen.findByText(/Census stopped at/i)).toBeTruthy();
    expect(screen.getByText(/counts only those/i)).toBeTruthy();
  });
});

describe('failures are told apart, per panel', () => {
  it('a failed task read renders an ERROR, never a zero', async () => {
    stub({ tasksStatus: 403 });
    renderReports();
    // The board panels say what went wrong…
    expect(await screen.findAllByText(/do not have access/i)).not.toHaveLength(0);
    // …and the tiles do NOT render a confident 0, which would read as "an empty board".
    //
    // READ THE VALUE NODES, NOT `.tiles.textContent`. The first version of this assertion was
    // `expect(tiles.textContent).not.toMatch(/\b0\b/)` and it COULD NOT FAIL: `Tile` renders label,
    // value and note as adjacent divs with no separator, so textContent glues them into
    // `Tasks0on this board` — and `\b0\b` needs a non-word character on BOTH sides, which no tile
    // ever produces. It passed against a screen that really did render four zeros (rule 18: the
    // measurement could not come back non-zero).
    const values = [...document.querySelectorAll('.tile-v')].map((n) => n.textContent);
    expect(values.length, 'no tiles rendered — this assertion would pass vacuously').toBe(4);
    expect(values, 'a failed census must not render a confident 0').toEqual(['—', '—', '—', '—']);
  });

  it('operations failing leaves every task-derived chart rendered', async () => {
    stub({ pages: [{ tasks: [mkTask(1), mkTask(2)] }], opsStatus: 503 });
    renderReports();
    // The ops panels degrade with their own copy…
    expect(await screen.findAllByText(/not configured in this environment/i)).not.toHaveLength(0);
    // …while the census charts are unaffected.
    expect(screen.getByText('By status')).toBeTruthy();
    await waitFor(() => expect(document.querySelectorAll('svg').length).toBeGreaterThan(0));
  });

  it('403 and 503 do NOT produce the same message', async () => {
    stub({ opsStatus: 403 });
    renderReports();
    const forbidden = await screen.findAllByText(/do not have access/i);
    expect(forbidden.length).toBeGreaterThan(0);
    expect(screen.queryByText(/not configured in this environment/i)).toBeNull();
  });
});

describe('every panel states its window', () => {
  it('no chart head is left without one', async () => {
    stub({ pages: [{ tasks: [mkTask(1)] }] });
    renderReports();
    await screen.findByText('By status');
    const heads = [...document.querySelectorAll('.record-head-right')];
    expect(heads.length).toBeGreaterThan(4);
    for (const h of heads) {
      expect(h.textContent?.trim(), 'a chart rendered with an empty window label').not.toBe('');
    }
  });

  it('uses the SERVER-reported ops window, not a hardcoded one', async () => {
    // The request asks for 30 days; the service clamps to its retention and reports what it used.
    stub({
      pages: [{ tasks: [mkTask(1)] }],
      ops: { projectId: 'p1', days: 45, total: 5, errors: 0, errorRate: 0, avgLatencyMs: 12, byTool: [{ tool: 'get_task', calls: 5, avgMs: 12 }], byDay: [{ day: '2026-08-26', calls: 5 }], byAgent: [] },
    });
    renderReports();
    await waitFor(() => expect(screen.getAllByText(/last 45 days/).length).toBeGreaterThan(0));
    expect(screen.queryByText(/last 30 days/)).toBeNull();
  });
});

describe('the charts render from the census', () => {
  it('draws a status donut whose description carries the counts', async () => {
    stub({
      pages: [{ tasks: [mkTask(1), mkTask(2), mkTask(3, { status: 'DONE' })] }],
    });
    renderReports();
    await screen.findByText('By status');
    await waitFor(() => {
      const donut = [...document.querySelectorAll('svg')].find((s2) =>
        s2.querySelector('title')?.textContent?.includes('tasks: 3'),
      );
      expect(donut, 'the status donut should total the census').toBeTruthy();
      // The <desc> carries the full breakdown — the same numbers the visible legend shows.
      expect(donut!.querySelector('desc')?.textContent).toBe('Todo 2 (67%), Done 1 (33%)');
    });
  });
});
