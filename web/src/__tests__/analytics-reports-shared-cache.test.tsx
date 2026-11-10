/**
 * `/analytics` and `/reports` share ONE QueryClient with the board, and their cache entries must not
 * collide with each other's or with the board's.
 *
 *  1. The Analytics census is an INFINITE query (`{pages, pageParams}`). It was keyed
 *     `['tasks', projectId, boardId]`, under the prefix every optimistic board write walks assuming a
 *     flat `{tasks}` page, so for the five minutes the entry outlived the screen every card move,
 *     assign or create in that project threw before its request was sent.
 *  2. The two screens asked for different operations windows (7 and 30 days) under one key, so the
 *     second screen opened painted the first one's window under its own heading.
 *
 * One client per test, configured like main.tsx (retry off, staleTime 30 s), because both defects
 * only exist when the screens share a cache.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import { render, renderHook, screen, cleanup, waitFor, act } from '@testing-library/react';
import { Analytics } from '../screens/Analytics.js';
import { Reports } from '../screens/Reports.js';
import { boardTasksKey, patchTaskCaches, useTaskAction } from '../mutations.js';
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

/** The operations answer is a function of the window asked for: total = days x 10. */
function stub() {
  const calls: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      calls.push(url);
      const json = (b: unknown) => ({ ok: true, status: 200, json: async () => b }) as Response;
      if (url.endsWith('/api/projects')) return json({ projects: [{ projectId: 'p1', name: 'P1', role: 'admin' }] });
      if (url.endsWith('/boards')) return json({ boards: [{ id: 'b1', projectId: 'p1', name: 'Board', columns: [] }] });
      if (url.includes('/tasks?')) return json({ tasks: [mkTask(1), mkTask(2)] });
      if (url.includes('/analytics/operations')) {
        const days = Number(new URL(url, 'http://x').searchParams.get('days'));
        return json({
          projectId: 'p1', days, total: days * 10, errors: 0, errorRate: 0, avgLatencyMs: 5,
          byTool: [{ tool: 'claim_next', calls: days * 10, errors: 0, avgMs: 5 }],
          byDay: [{ day: '2026-11-09', calls: days * 10, errors: 0 }],
          byAgent: [{ agent: 'a', calls: days * 10 }],
        });
      }
      return json({ sessions: [], events: [], board: { id: 'b1', columns: [] } });
    }) as unknown as typeof fetch,
  );
  return calls;
}

const client = () => new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } });
const at = (qc: QueryClient, path: string, el: React.ReactNode) =>
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>{el}</MemoryRouter>
    </QueryClientProvider>,
  );

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
  cleanup();
});

describe('the Analytics census stays out of the board cache an optimistic write walks', () => {
  it('after /analytics, a board move still patches the board and does not throw', async () => {
    stub();
    const qc = client();
    const view = at(qc, '/analytics', <Analytics />);
    await waitFor(() => expect(qc.getQueryCache().findAll().some((q) => q.state.status === 'success' && JSON.stringify(q.queryKey).includes('"b1"'))).toBe(true));
    await waitFor(() => expect(document.body.textContent).toMatch(/Total Tasks\s*2/));
    view.unmount();

    // The board's own entry, as Board.tsx caches it.
    qc.setQueryData(boardTasksKey('p1', 'b1', 200), { tasks: [mkTask(1), mkTask(2)] });
    for (const [key, data] of qc.getQueriesData({ queryKey: ['tasks', 'p1'] })) {
      if (data === undefined) continue; // a disabled entry that never fetched
      expect(Array.isArray((data as { tasks?: unknown } | undefined)?.tasks), `a non-page entry under the board prefix: ${JSON.stringify(key)}`).toBe(true);
    }
    expect(() => patchTaskCaches(qc, 'p1', 'T-1', (t) => ({ ...t, status: 'REVIEW' }))).not.toThrow();
    expect(qc.getQueryData<{ tasks: Task[] }>(boardTasksKey('p1', 'b1', 200))!.tasks[0]!.status).toBe('REVIEW');
  });
});

describe('a board write marks the census stale', () => {
  // The census left the `['tasks', projectId]` prefix, and with it the invalidation every settled
  // board write sends there. Without its own, Analytics reopened within the 30 s staleTime after a
  // card move counted the board as it was before the move.
  it('a settled task action invalidates the census of that project', async () => {
    stub();
    const qc = client();
    const view = at(qc, '/analytics', <Analytics />);
    await waitFor(() => expect(document.body.textContent).toMatch(/Total Tasks\s*2/));
    view.unmount();
    const census = () => qc.getQueryCache().find({ queryKey: ['task-census', 'p1', 'b1'] })!;
    expect(census().state.isInvalidated).toBe(false);

    const { result } = renderHook(
      () => useTaskAction<'DONE'>('p1', (to, t) => ({ ...t, status: to }), async () => ({})),
      { wrapper: ({ children }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider> },
    );
    await act(async () => {
      await result.current.mutateAsync({ vars: 'DONE', task: mkTask(1) });
    });
    await waitFor(() => expect(census().state.isInvalidated, 'the census kept its pre-move count').toBe(true));
  });
});

describe('the operations window is part of its cache key', () => {
  it('Reports (30 days) then Analytics: Analytics asks for and shows its own 7 days', async () => {
    const calls = stub();
    const qc = client();
    const reports = at(qc, '/reports', <Reports />);
    await waitFor(() => expect(calls.some((u) => u.includes('days=30'))).toBe(true));
    await waitFor(() => expect(qc.getQueryCache().findAll().some((q) => JSON.stringify(q.queryKey).includes('analytics-ops') && q.state.status === 'success')).toBe(true));
    reports.unmount();

    at(qc, '/analytics', <Analytics />);
    await waitFor(() => expect(calls.some((u) => u.includes('days=7')), 'Analytics reused the 30-day answer').toBe(true));
    const heading = await screen.findByRole('heading', { name: /Operations · last 7 days/ });
    const card = heading.closest('.card')!;
    await waitFor(() => expect(card.textContent).toMatch(/Operations70/));
    expect(card.textContent).not.toMatch(/Operations300/);
  });

  it('Analytics (7 days) then Reports: Reports asks for its own 30 days', async () => {
    const calls = stub();
    const qc = client();
    const analytics = at(qc, '/analytics', <Analytics />);
    await waitFor(() => expect(calls.some((u) => u.includes('days=7'))).toBe(true));
    await waitFor(() => expect(qc.getQueryCache().findAll().some((q) => JSON.stringify(q.queryKey).includes('analytics-ops') && q.state.status === 'success')).toBe(true));
    analytics.unmount();

    at(qc, '/reports', <Reports />);
    await waitFor(() => expect(calls.some((u) => u.includes('days=30')), 'Reports reused the 7-day answer').toBe(true));
  });
});
