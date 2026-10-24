/**
 * The Analytics and Reports screens over GET /api/analytics/operations (R2.11), rendered.
 *
 * Analytics shows the summary's total and error rate, and asks the BFF for exactly the reads it
 * composes: the summary, the board, its feed and the agent sessions. A cancelled task (DONE with the
 * `cancelled` label, ADR-0001) is in the total and in no done figure, as conductor's CANCELLED was
 * (P-208). Reports renders the summary's calls and window.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { Analytics } from '../screens/Analytics.js';
import { Reports } from '../screens/Reports.js';
import type { OpsSummary, Task } from '../types.js';

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

const ops = (days: number): OpsSummary => ({
  projectId: 'p1', days, total: 25, errors: 5, errorRate: 0.2, avgLatencyMs: 12.5,
  byTool: [{ tool: 'claim_next', calls: 25, avgMs: 12.5 }],
  byDay: [{ day: '2026-10-24', calls: 25 }],
  byAgent: [{ agent: 'claude', calls: 25 }],
  retention: { days: 45, oldestDay: '2026-09-09' },
});

const tasks = [
  mkTask(1, { status: 'DONE' }),
  mkTask(2, { status: 'DONE', labels: ['cancelled'] }),
  mkTask(3),
];

function stub(): string[] {
  const calls: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      calls.push(url);
      const json = (b: unknown) => ({ ok: true, status: 200, json: async () => b }) as Response;
      if (url.includes('/analytics/operations')) return json(ops(Number(new URL(url, 'http://x').searchParams.get('days'))));
      if (url.endsWith('/api/projects')) return json({ projects: [{ projectId: 'p1', name: 'P1', role: 'admin' }] });
      if (url.endsWith('/boards')) return json({ boards: [{ id: 'b1', projectId: 'p1', name: 'Board', columns: [] }] });
      if (url.endsWith('/boards/b1')) return json({ board: { id: 'b1', projectId: 'p1', name: 'Board', columns: [] }, taskCounts: {} });
      if (url.endsWith('/boards/b1/feed')) return json({ events: [] });
      if (url.includes('/agents/sessions')) return json({ sessions: [] });
      if (url.includes('/tasks?')) return json({ tasks });
      return json({});
    }) as unknown as typeof fetch,
  );
  return calls;
}

function renderAt(path: string, el: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>{el}</MemoryRouter>
    </QueryClientProvider>,
  );
}

/** The smallest element whose text starts with label and holds more than it. */
function card(label: string): string {
  const el = [...document.querySelectorAll('div')]
    .filter((d) => d.textContent?.startsWith(label) && d.textContent !== label)
    .sort((a, b) => (a.textContent?.length ?? 0) - (b.textContent?.length ?? 0))[0];
  expect(el, `no "${label}" card rendered`).toBeTruthy();
  return el!.textContent ?? '';
}

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
  cleanup();
});

describe('/analytics', () => {
  it('shows the summary over the BFF and counts a cancelled task in the total only', async () => {
    const calls = stub();
    renderAt('/analytics', <Analytics />);
    await screen.findByText('Error rate', {}, { timeout: 3_000 });
    expect(card('Operations')).toBe('Operations25');
    expect(card('Error rate')).toBe('Error rate20%');
    await waitFor(() => expect(card('Total Tasks')).toMatch(/^Total Tasks3/), { timeout: 3_000 });
    expect(card('Done'), 'Done 1 at 33%: the cancelled task is not done').toBe('Done133% throughput');
    for (const read of ['/analytics/operations?projectId=p1&days=7', '/boards/b1', '/boards/b1/feed', '/agents/sessions']) {
      expect(calls.some((u) => u.includes(read)), `no ${read} read`).toBe(true);
    }
  });
});

describe('/reports', () => {
  it('shows the summary over the BFF for its 30-day window', async () => {
    const calls = stub();
    renderAt('/reports', <Reports />);
    await screen.findByText('25 calls over 30 days', {}, { timeout: 3_000 });
    expect(calls.some((u) => u.includes('/analytics/operations?projectId=p1&days=30'))).toBe(true);
  });
});
