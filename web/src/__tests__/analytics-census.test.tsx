/**
 * `/analytics` PAGES ITS CENSUS — asserted by rendering the screen, not by grepping it.
 *
 * WHY THIS FILE EXISTS. The fix for "Analytics counted one page of 200 and called it the total" was
 * guarded by two source regexes in `ui-polish.test.tsx` (`getNextPageParam:…nextCursor`,
 * `cursor:\s*pageParam`). A review deleted the drain entirely — replacing the `fetchNextPage` effect
 * body with `if (false) {…}` — and all 527 web tests stayed green, because both regexes still
 * matched the untouched declaration above it. The regexes guard that the CODE IS PRESENT, which is
 * not the property; the property is that page two is actually fetched and counted.
 *
 * `/reports` already had this harness (`reports.test.tsx`) for the same property. The sibling screen
 * got a lexical stand-in, and the two screens are now one click apart via the Tabs strip — so they
 * can report different totals for the same board with only one of them carrying a caveat.
 *
 * Rule 12: a behaviour is guarded at the layer that can observe it. Rule 17: the cheapest check that
 * would catch its absence — which a regex over the source demonstrably is not.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { Analytics } from '../screens/Analytics.js';
import type { Task } from '../types.js';

vi.mock('../projectContext.js', () => ({
  useProject: () => ({
    projectId: 'p1',
    setProjectId: () => {},
    projects: [{ projectId: 'p1', name: 'P1', role: 'admin' }],
    loading: false,
  }),
}));

const mkTask = (i: number): Task =>
  ({
    id: `T-${i}`, projectId: 'p1', boardId: 'b1', columnId: 'c1',
    title: `task ${i}`, description: '', status: 'TODO', priority: 'normal',
    assignee: null, labels: [], requiredCapabilities: [], estimate: null, dueAt: null,
    customFields: {}, parentTaskId: null, order: 'a0', blockedBy: [], blocks: [],
    contextSummary: '', version: 1, claim: null, createdBy: 'u',
    createdAt: new Date(Date.now() - 86_400_000).toISOString(),
    updatedAt: new Date().toISOString(),
  }) as Task;

function stub(opts: { pages?: Array<{ tasks: Task[]; nextCursor?: string }>; tasksStatus?: number } = {}) {
  const calls: string[] = [];
  const pages = opts.pages ?? [{ tasks: [mkTask(1)] }];
  let pageIdx = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      calls.push(url);
      const json = (b: unknown, status = 200) =>
        ({ ok: status < 400, status, json: async () => b }) as Response;
      // Most specific first — `/api/projects` is a prefix of every project-scoped URL.
      if (url.endsWith('/api/projects')) return json({ projects: [{ projectId: 'p1', name: 'P1', role: 'admin' }] });
      if (url.endsWith('/boards')) return json({ boards: [{ id: 'b1', projectId: 'p1', name: 'Board', columns: [] }] });
      if (url.includes('/tasks?')) {
        if (opts.tasksStatus) return json({ error: `request failed: ${opts.tasksStatus}` }, opts.tasksStatus);
        const page = pages[Math.min(pageIdx, pages.length - 1)]!;
        pageIdx += 1;
        return json(page);
      }
      return json({ projectId: 'p1', days: 30, total: 0, errors: 0, errorRate: 0, avgLatencyMs: 0, byTool: [], byDay: [], byAgent: [] });
    }) as unknown as typeof fetch,
  );
  return calls;
}

function renderAnalytics() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/analytics']}>
        <Analytics />
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

describe('the /analytics census pages the cursor', () => {
  it('sends the cursor it was given, so page two is actually fetched', async () => {
    const calls = stub({
      pages: [
        { tasks: [mkTask(1), mkTask(2)], nextCursor: 'c1' },
        { tasks: [mkTask(3)] },
      ],
    });
    renderAnalytics();
    await waitFor(
      () => expect(calls.some((u) => u.includes('cursor=c1')), 'the drain never asked for page two').toBe(true),
      { timeout: 3_000 },
    );
  });

  it('counts EVERY page, not just the first', async () => {
    stub({
      pages: [
        { tasks: [mkTask(1), mkTask(2)], nextCursor: 'c1' },
        { tasks: [mkTask(3)] },
      ],
    });
    renderAnalytics();
    // 3 across two pages, not 2. This is the assertion the pre-fix screen failed.
    // `Stat`, not `Tile` — Analytics predates the Reports tile row. Find the card by its label and
    // read the whole card, so a markup change inside `Stat` does not silently empty the population.
    await waitFor(
      () => {
        const card = [...document.querySelectorAll('div')].find(
          (d) => d.textContent?.startsWith('Total Tasks') && d.children.length > 0 && d.children.length < 6,
        );
        expect(card, 'no "Total Tasks" card rendered — this assertion would pass vacuously').toBeTruthy();
        expect(card!.textContent, 'the total must count BOTH pages, not just the first').toMatch(/3/);
      },
      { timeout: 3_000 },
    );
  });

  /**
   * A failed census and an empty board both produce 0 in every card — `Total Tasks 0`, `Blocked 0
   * "clear"`, `Done 0 "0% throughput"`. `isError` was assigned into the compat shim and then read
   * nowhere, so the failure was noticed in code and invisible on screen. `/reports` renders an em
   * dash for the same reason; this screen's cards are `Stat`, so the notice carries it.
   */
  it('a failed census SAYS SO, instead of rendering zeros that read as an empty board', async () => {
    stub({ tasksStatus: 403 });
    renderAnalytics();
    const note = await screen.findByRole('alert', {}, { timeout: 3_000 });
    expect(note.textContent).toMatch(/not because the board is empty/i);
  });

  it('POSITIVE CONTROL: a healthy census shows no failure notice', async () => {
    stub({ pages: [{ tasks: [mkTask(1)] }] });
    renderAnalytics();
    await waitFor(() => expect(document.querySelectorAll('.tile, .card, [role="alert"]').length).toBeGreaterThan(0));
    await new Promise((r) => setTimeout(r, 100));
    expect(screen.queryByRole('alert'), 'a healthy board must not claim a failure').toBeNull();
  });

  it('POSITIVE CONTROL: a single page needs no second request', async () => {
    // Without this, the two assertions above pass on a screen that requests pages forever.
    const calls = stub({ pages: [{ tasks: [mkTask(1)] }] });
    renderAnalytics();
    await waitFor(() => expect(calls.some((u) => u.includes('/tasks?'))).toBe(true));
    await new Promise((r) => setTimeout(r, 100));
    expect(
      calls.filter((u) => u.includes('cursor=')),
      'no cursor is offered, so nothing should be requested with one',
    ).toEqual([]);
  });
});
