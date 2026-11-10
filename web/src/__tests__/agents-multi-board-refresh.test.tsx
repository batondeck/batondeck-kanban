/**
 * The Agents aggregate (claims, leases, handoffs) covers every board of the project, and it must
 * refresh when ANY of them changes.
 *
 * It subscribed to each board's stream, but the tab shares one socket scoped to ONE board (the first
 * a subscriber names, api/stream.ts), and the server sends only that board's changes. Claims and
 * handoffs on a second board never refreshed the page, although its comment promised a refresh on
 * any board's update. One board is covered by the stream; past one, the aggregate polls.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import { render, screen, cleanup, waitFor, act } from '@testing-library/react';
import { Agents } from '../screens/Agents.js';
import { ProjectProvider } from '../projectContext.js';

class FakeES {
  static urls: string[] = [];
  readyState = 0;
  constructor(public url: string) {
    FakeES.urls.push(url);
  }
  addEventListener(): void {}
  close(): void {
    this.readyState = 2;
  }
}

let fetched: string[] = [];
function stub(boards: string[]) {
  fetched = [];
  FakeES.urls = [];
  vi.stubGlobal('EventSource', FakeES);
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    fetched.push(url);
    const p = url.split('?')[0]!;
    const body: unknown =
      p === '/api/projects' ? { projects: [{ projectId: 'p1', name: 'P1', role: 'admin' }] }
        : p === '/api/projects/p1' ? { project: { id: 'p1', name: 'P1', members: {} }, memberDisplay: {} }
          : p === '/api/projects/p1/boards' ? { boards: boards.map((id) => ({ id, projectId: 'p1', name: id })) }
            : /\/tasks$/.test(p) ? { tasks: [] }
              : /\/feed$/.test(p) ? { events: [] }
                : { sessions: [] };
    return { ok: true, status: 200, json: async () => body } as Response;
  }));
}

const tasksReads = (board: string) => fetched.filter((u) => u.includes(`/boards/${board}/tasks`)).length;

function mount() {
  localStorage.setItem('bd.projectId', 'p1');
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter initialEntries={['/agents']}>
        <ProjectProvider><Agents /></ProjectProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('Agents refreshes for every board', () => {
  it('two boards: the second board’s changes reach the page within the poll interval', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    stub(['b1', 'b2']);
    mount();
    await waitFor(() => expect(tasksReads('b2')).toBe(1));
    await screen.findByText(/Recent Handoffs/);
    await act(async () => { await vi.advanceTimersByTimeAsync(31_000); });
    await waitFor(() => expect(tasksReads('b2'), 'nothing refreshed the second board’s claims').toBeGreaterThan(1));
  });

  it('one board: the stream covers it, so nothing polls', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    stub(['b1']);
    mount();
    await waitFor(() => expect(tasksReads('b1')).toBe(1));
    await waitFor(() => expect(FakeES.urls.some((u) => u.includes('boardId=b1'))).toBe(true));
    await act(async () => { await vi.advanceTimersByTimeAsync(31_000); });
    expect(tasksReads('b1')).toBe(1);
  });
});
