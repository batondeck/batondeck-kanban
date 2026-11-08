/**
 * Runs section: the race-start form must survive a failed start (guard() swallows the rejection), and
 * a second action while one is in flight must not reach the server at all.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, cleanup, waitFor } from '@testing-library/react';
import { Runs } from '../screens/Runs.js';
import type { Task } from '../types.js';

const task: Task = {
  id: 'T-1', projectId: 'p1', boardId: 'b1', columnId: 'c1',
  title: 'Ship it', description: '', status: 'TODO', priority: 'normal',
  assignee: null, labels: [], requiredCapabilities: [], estimate: null, dueAt: null,
  customFields: {}, parentTaskId: null, order: 'a0', blockedBy: [], blocks: [],
  contextSummary: '', version: 1, claim: null, createdBy: 'me', createdAt: '2026-08-01T00:00:00.000Z', updatedAt: new Date().toISOString(),
};

/** Two submitted runs — both render a /^\s*Pick\s*$/ button while the parent is racing. */
const RUNS = [
  { taskId: 'T-2', agent: 'a1', status: 'REVIEW', hasDeliverable: true, deliverable: 'one' },
  { taskId: 'T-3', agent: 'a2', status: 'REVIEW', hasDeliverable: true, deliverable: 'two' },
];

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('Runs — race start', () => {
  it('keeps the start form open and shows the error when starting the race fails', async () => {
    vi.stubGlobal('fetch', vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'POST') return { ok: false, status: 500, json: async () => ({ code: 'INTERNAL', message: 'race exploded' }) } as Response;
      return { ok: true, status: 200, json: async () => ({ runs: [] }) } as Response;
    }));
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <Runs projectId="p1" canRace task={task} />
      </QueryClientProvider>,
    );
    fireEvent.click(await screen.findByText('Race this task'));
    fireEvent.click(screen.getByText('Start'));
    expect(await screen.findByText('race exploded')).toBeInTheDocument();
    // form not closed on failure — the count input is still there
    expect(screen.getByLabelText('Number of runs')).toBeInTheDocument();
  });

  it('REFUSAL: a second Pick while one is in flight sends NO request', async () => {
    // `guard` used to take an already-constructed promise, so `api.pickRun(...)` had fired before the
    // busy check could refuse it: the second Pick hit the server with a stale `task.version` and its
    // rejection went nowhere (guard returned before its await), leaving `err` unset.
    let resolveFirst: (r: Response) => void = () => {};
    const posts: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method !== 'POST') {
        return { ok: true, status: 200, json: async () => ({ runs: RUNS }) } as Response;
      }
      posts.push(String(input));
      return new Promise<Response>((res) => { resolveFirst = res; });
    }));
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <Runs projectId="p1" canRace task={{ ...task, racing: true }} />
      </QueryClientProvider>,
    );
    const picks = await screen.findAllByText(/^\s*Pick\s*$/);
    expect(picks.length, 'need two pickable runs or this check is dark').toBe(2);
    fireEvent.click(picks[0]!);
    await waitFor(() => expect(posts).toHaveLength(1));
    fireEvent.click(picks[1]!);

    // NO FIXED SLEEP. The claim is an ABSENCE — the second Pick sent nothing — and an absence is
    // trivially true before the click has been processed. The `setTimeout(10)` version passed with
    // this very `fireEvent.click` DELETED (mutation-proven, T-342).
    //
    // The guard is TEMPORAL, so the discrimination is temporal too: while the first request is still
    // in flight the count must stay at 1, and once it RESOLVES a further Pick must go through. A
    // guard stuck permanently on would pass the first half and fail the second; a guard that never
    // engages fails the first. Only a correct busy guard passes both.
    expect(posts, 'the busy guard must refuse the second Pick BEFORE the request is built').toHaveLength(1);

    resolveFirst({ ok: true, status: 200, json: async () => ({ task }) } as Response);
    await waitFor(() => expect(screen.queryAllByText(/^\s*Pick\s*$/).length).toBeGreaterThan(0));
    fireEvent.click((await screen.findAllByText(/^\s*Pick\s*$/))[0]!);
    await waitFor(() =>
      expect(posts, 'once the first request settles the guard must RELEASE — otherwise it is a deadlock, not a guard')
        .toHaveLength(2),
    );
  });

  it('POSITIVE CONTROL: a Pick on an idle section still sends its request', async () => {
    const posts: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method !== 'POST') return { ok: true, status: 200, json: async () => ({ runs: RUNS }) } as Response;
      posts.push(String(input));
      return { ok: true, status: 200, json: async () => ({ task }) } as Response;
    }));
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <Runs projectId="p1" canRace task={{ ...task, racing: true }} />
      </QueryClientProvider>,
    );
    fireEvent.click((await screen.findAllByText(/^\s*Pick\s*$/))[0]!);
    await waitFor(() => expect(posts).toHaveLength(1));
    expect(posts[0]).toContain('/runs/pick');
  });

  it('lists what list_runs returns: the racing count is its length, no discarded state, a DONE run without a deliverable not pickable', async () => {
    const listed = [
      { taskId: 'T-2', status: 'IN_PROGRESS', assignee: 'a1', leaseHolder: 'a1', hasDeliverable: false, version: 2 },
      { taskId: 'T-3', status: 'REVIEW', assignee: null, leaseHolder: null, hasDeliverable: true, deliverable: 'draft', version: 3 },
      // cancel_task closes a run DONE with the cancelled label and no deliverable (ADR-0001).
      { taskId: 'T-4', status: 'DONE', assignee: null, leaseHolder: null, hasDeliverable: false, version: 4 },
    ];
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ runs: listed }) }) as Response));
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <Runs projectId="p1" canRace task={{ ...task, racing: true }} />
      </QueryClientProvider>,
    );
    expect(await screen.findByText(/Racing · 3 runs open/)).toBeInTheDocument();
    expect(screen.getByText('3 runs')).toBeInTheDocument();
    expect(screen.queryByText('Discarded')).toBeNull();
    expect(screen.getAllByText(/^\s*Pick\s*$/)).toHaveLength(1);
    expect(screen.getAllByText('Submitted')).toHaveLength(1);
    expect(screen.getByText('deletes the others.', { exact: false })).toBeInTheDocument();
  });

  it('REFUSAL (P-208): a cancelled run that had delivered is Running, not Submitted, and offers no Pick', async () => {
    // cancel_task on a REVIEW run keeps its deliverable and lands it DONE with the cancelled label;
    // pick_run refuses it, so the drawer must not offer it. list_runs carries no labels: the drawer
    // passes the cancelled ids from the board's task list.
    const listed = [
      { taskId: 'T-2', status: 'DONE', assignee: 'a1', leaseHolder: null, hasDeliverable: true, deliverable: 'wrong approach', version: 4 },
      { taskId: 'T-3', status: 'REVIEW', assignee: 'a2', leaseHolder: null, hasDeliverable: true, deliverable: 'the fix', version: 3 },
    ];
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ runs: listed }) }) as Response));
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const { container } = render(
      <QueryClientProvider client={qc}>
        <Runs projectId="p1" canRace task={{ ...task, racing: true }} cancelled={new Set(['T-2'])} />
      </QueryClientProvider>,
    );
    expect(await screen.findByText('wrong approach')).toBeInTheDocument();
    expect(screen.getAllByText(/^\s*Pick\s*$/)).toHaveLength(1);
    expect(screen.getAllByText('Submitted')).toHaveLength(1);
    expect(screen.getAllByText('Running')).toHaveLength(1);
    const cards = [...container.querySelectorAll('.mono')].filter((n) => /^T-[23]$/.test(n.textContent ?? ''));
    const cardOf = (id: string): HTMLElement => cards.find((n) => n.textContent === id)!.closest('div')!.parentElement as HTMLElement;
    expect(cardOf('T-2').textContent).toContain('Running');
    expect(cardOf('T-2').textContent).not.toMatch(/Pick/);
    expect(cardOf('T-3').textContent).toMatch(/Pick/);
  });

  it('after a pick the one run left is the winner', async () => {
    const left = [{ taskId: 'T-3', status: 'REVIEW', assignee: null, leaseHolder: null, hasDeliverable: true, deliverable: 'draft', version: 3 }];
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ runs: left }) }) as Response));
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <Runs projectId="p1" canRace task={task} />
      </QueryClientProvider>,
    );
    expect(await screen.findByText('Win')).toBeInTheDocument();
    expect(screen.getByText('1 run')).toBeInTheDocument();
    expect(screen.queryByText(/Racing ·/)).toBeNull();
  });
});
