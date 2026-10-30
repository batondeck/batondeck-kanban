/**
 * P-197: a live device session (a `device:<pid>` row from a presence frame) stays online on the Board
 * and in task detail when `/api/agents/sessions` answers. The list never carries device rows, and a
 * frame comes on open and then only when the directory changes, so a list answer that replaced the
 * cache entry showed the agent offline from load, and again after any refetch, until the next change.
 * Both screens share the ['agentsessions'] entry, so both queryFns are driven here.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, renderHook, screen, cleanup, waitFor, act } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { Board } from '../screens/Board.js';
import { useTaskDetail } from '../screens/task/useTaskDetail.js';
import { TaskPage } from '../screens/TaskPage.js';
import * as streamApi from '../api/stream.js';
import type { AgentSession, Task } from '../types.js';

class FakeES {
  static instances: FakeES[] = [];
  readyState = 0;
  listeners = new Map<string, Array<(e: { data: string; lastEventId: string }) => void>>();
  constructor(public url: string) {
    FakeES.instances.push(this);
  }
  addEventListener(t: string, fn: (e: { data: string; lastEventId: string }) => void): void {
    this.listeners.set(t, [...(this.listeners.get(t) ?? []), fn]);
  }
  close(): void {
    this.readyState = 2;
  }
  emit(t: string, data: unknown = {}): void {
    for (const fn of this.listeners.get(t) ?? []) fn({ data: JSON.stringify(data), lastEventId: '' });
  }
}

const board = { id: 'b1', projectId: 'p1', name: 'Board', columns: [{ id: 'c1', name: 'To do', order: 1, status: 'TODO', wipLimit: null }] };
const task = {
  id: 'T-1', projectId: 'p1', boardId: 'b1', columnId: 'c1', title: 'Ship it', description: '', status: 'TODO', priority: 'normal',
  assignee: null, labels: [], requiredCapabilities: [], estimate: null, dueAt: null, customFields: {}, parentTaskId: null, order: 'a0',
  blockedBy: [], blocks: [], contextSummary: '', version: 1, claim: null, createdBy: 'alice@example.com',
  createdAt: '2026-10-30T00:00:00.000Z', updatedAt: '2026-10-30T00:00:00.000Z',
} as unknown as Task;
// One live CLI process, as the directory's presence frame lists it (api/stream.ts DeviceSession).
const frame = { sessions: [{ pid: 4242, role: 'worker', identityId: 'U-1', agentId: 'obs-alice', registeredAt: '2026-10-30T00:00:00.000Z' }] };

// The list answers are held until the test releases them, so it decides whether a frame lands first.
let pending: Array<() => void> = [];
let listed: AgentSession[] = []; // what /api/agents/sessions answers (never a device: row)
const release = (): void => {
  for (const r of pending.splice(0)) r();
};

beforeEach(() => {
  FakeES.instances = [];
  pending = [];
  listed = [];
  vi.stubGlobal('EventSource', FakeES);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      const body = (b: unknown) => ({ ok: true, status: 200, json: async () => b }) as Response;
      if (/\/agents\/sessions/.test(url)) return new Promise<Response>((r) => pending.push(() => r(body({ sessions: listed }))));
      const routes: Array<[RegExp, unknown]> = [
        [/\/boards\/b1\/tasks\?/, { tasks: [task] }],
        [/\/boards\/b1\/feed$/, { events: [] }],
        [/\/boards\/b1$/, { board, taskCounts: {} }],
        [/\/boards$/, { boards: [board] }],
        [/\/tasks\/T-1$/, { task }],
        [/\/tasks\/T-1\/context$/, { summary: '', fields: {}, items: [], deps: { blockedBy: [], blocks: [], edges: [] }, memory: [] }],
        [/\/tasks\/T-1\/subtasks$/, { tasks: [] }],
        [/\/tasks\/T-1\/memory$/, { entries: [] }],
        [/\/projects\/p1$/, { project: { id: 'p1', name: 'P', members: {} } }],
      ];
      return body(routes.find(([re]) => re.test(url))?.[1] ?? {});
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const newClient = () => new QueryClient({ defaultOptions: { queries: { retry: false } } });
// TanStack Query tells its observers on a timer (notifyManager), not inside setQueryData, so a screen
// is read only after that timer has run: `settled` waits out the request and then that tick.
const tick = () => act(() => new Promise<void>((r) => setTimeout(r, 10)));
const settled = async (qc: QueryClient) => {
  await waitFor(() => expect(qc.isFetching({ queryKey: ['agentsessions'] })).toBe(0));
  await tick();
};
const pill = () => screen.getByText(/agents? online$/);

describe('the Board keeps a live device session online across list answers (P-197)', () => {
  it('a frame before the first list answer, then a refetch, then a frame that drops it', async () => {
    const qc = newClient();
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/projects/p1/boards/b1']}>
          <Routes>
            <Route path="/projects/:projectId/boards/:boardId" element={<Board />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    await waitFor(() => expect(FakeES.instances.length).toBeGreaterThan(0));
    await waitFor(() => expect(pending.length).toBeGreaterThan(0)); // the list request is in flight
    const es = FakeES.instances.at(-1)!;
    act(() => {
      es.readyState = 1;
      es.emit('open');
      es.emit('presence', frame); // the frame a stream sends on open
    });
    await waitFor(() => expect(pill()).toHaveTextContent('1 agent online'));

    act(release); // the list answers after the frame, without the device row
    await settled(qc);
    expect(pill()).toHaveTextContent('1 agent online');

    // A refetch later (a focus or visibilitychange refetch, an invalidation), with no directory change.
    act(() => void qc.invalidateQueries({ queryKey: ['agentsessions'] }));
    await waitFor(() => expect(pending.length).toBeGreaterThan(0));
    act(release);
    await settled(qc);
    expect(pill()).toHaveTextContent('1 agent online');

    // CONTROL: the process leaves the directory; the next frame is its whole list, so the row goes.
    act(() => es.emit('presence', { sessions: [] }));
    await waitFor(() => expect(pill()).toHaveTextContent('no agents online'));
  });
});

const session = (sessionKey: string, kind: AgentSession['kind'] = 'dev'): AgentSession => ({
  sessionKey, agentId: 'obs-alice', agentName: 'obs-alice', identityId: 'U-1', kind,
  firstSeenAt: '2026-10-30T00:00:00.000Z', lastSeenAt: new Date().toISOString(), sessionCount: 1, requestCount: 0, toolCounts: {}, lastDeliveredCursor: 0,
});
const keys = (qc: QueryClient) => qc.getQueryData<{ sessions: AgentSession[] }>(['agentsessions'])?.sessions.map((s) => s.sessionKey);
const hookWrapper = (qc: QueryClient) =>
  function Wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={qc}>
        <MemoryRouter>{children}</MemoryRouter>
      </QueryClientProvider>
    );
  };

describe('task detail shares the entry and keeps the device rows the same way (P-197)', () => {
  it('its list answer keeps the device rows the cache holds when the answer lands', async () => {
    const qc = newClient();
    qc.setQueryData(['agentsessions'], { sessions: [session('device:4242')] }, { updatedAt: 0 }); // stale: the hook fetches
    const { result } = renderHook(() => useTaskDetail('p1', 'T-1'), { wrapper: hookWrapper(qc) });
    await waitFor(() => expect(pending.length).toBeGreaterThan(0)); // the list request is in flight
    // Meanwhile the directory changed: a frame replaced 4242 with 5151. The answer must keep 5151, so
    // the cache is read when the answer lands, not when the request left (which held 4242).
    act(() => qc.setQueryData(['agentsessions'], { sessions: [session('device:5151')] }));
    act(release);
    await settled(qc);
    expect(result.current.agentsQ.data?.sessions.map((s) => s.sessionKey)).toEqual(['device:5151']);
  });

  it('a non-device session the list stops answering is dropped (a disconnect_agent invalidation)', async () => {
    const qc = newClient();
    qc.setQueryData(['agentsessions'], { sessions: [session('device:4242')] }, { updatedAt: 0 });
    listed = [session('S-mcp', 'service_account')];
    renderHook(() => useTaskDetail('p1', 'T-1'), { wrapper: hookWrapper(qc) });
    await waitFor(() => expect(pending.length).toBeGreaterThan(0));
    act(release);
    await settled(qc);
    expect(keys(qc)).toEqual(expect.arrayContaining(['S-mcp', 'device:4242']));
    // Settings' disconnect_agent invalidates the entry; the server no longer lists the session.
    listed = [];
    act(() => void qc.invalidateQueries({ queryKey: ['agentsessions'] }));
    await waitFor(() => expect(pending.length).toBeGreaterThan(0));
    act(release);
    await settled(qc);
    expect(keys(qc)).toEqual(['device:4242']);
  });
});

describe('the full task page retires a device session that left (P-197)', () => {
  it('a frame without the session, then a refetch: the row is gone', async () => {
    const qc = newClient();
    // The Board put the live session in the cache, then the user went to the full task page.
    qc.setQueryData(['agentsessions'], { sessions: [session('device:4242')] }, { updatedAt: 0 });
    // Another subscriber of the shared socket (the app's own userStream), so a socket exists whether or
    // not the page joins it: what this test asks is whether the page writes the frames it carries.
    const other = streamApi.userStream({});
    try {
      render(
        <QueryClientProvider client={qc}>
          <MemoryRouter initialEntries={['/projects/p1/boards/b1/tasks/T-1']}>
            <Routes>
              <Route path="/projects/:projectId/boards/:boardId/tasks/:taskId" element={<TaskPage />} />
            </Routes>
          </MemoryRouter>
        </QueryClientProvider>,
      );
      await waitFor(() => expect(pending.length).toBeGreaterThan(0));
      act(release);
      await settled(qc);
      expect(keys(qc)).toEqual(['device:4242']); // kept across the page's own list answer
      const es = FakeES.instances.at(-1)!;
      act(() => {
        es.readyState = 1;
        es.emit('open');
        es.emit('presence', { sessions: [] }); // the process left the directory
      });
      await tick();
      act(() => void qc.invalidateQueries({ queryKey: ['agentsessions'] })); // a later refetch
      await waitFor(() => expect(pending.length).toBeGreaterThan(0));
      act(release);
      await settled(qc);
      expect(keys(qc)).toEqual([]);
    } finally {
      other();
    }
  });
});
