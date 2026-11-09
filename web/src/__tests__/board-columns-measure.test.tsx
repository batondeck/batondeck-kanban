/**
 * THE BOARD RE-MEASURES WHERE ITS COLUMNS START WHEN THE HEADER CHANGES HEIGHT (P-448).
 *
 * P-446 sized the columns from where they start (`--columns-top`), measured after each render and on
 * window resize. A header that changes height with neither (a web font arriving late) left them
 * mis-sized. A ResizeObserver on the header now re-measures, and is disconnected on unmount.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { Board } from '../screens/Board.js';
import { ProjectProvider } from '../projectContext.js';
import { ShortcutProvider } from '../shortcuts.js';

class FakeES {
  readyState = 0;
  addEventListener(): void {}
  close(): void {
    this.readyState = 2;
  }
}

/** The one ResizeObserver the board makes: what it observes, its callback, and whether it was disconnected. */
const observers: Array<{ targets: Element[]; cb: ResizeObserverCallback; disconnected: boolean }> = [];
class FakeRO {
  rec: { targets: Element[]; cb: ResizeObserverCallback; disconnected: boolean };
  constructor(cb: ResizeObserverCallback) {
    this.rec = { targets: [], cb, disconnected: false };
    observers.push(this.rec);
  }
  observe(t: Element): void {
    this.rec.targets.push(t);
  }
  unobserve(): void {}
  disconnect(): void {
    this.rec.disconnected = true;
  }
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  observers.length = 0;
  localStorage.clear();
});

const json = (b: unknown) => ({ ok: true, status: 200, json: async () => b }) as Response;
const board = { id: 'b1', projectId: 'p1', name: 'Board', columns: [{ id: 'c1', name: 'To do', order: 1, status: 'TODO', wipLimit: null }] };

async function renderBoard() {
  vi.stubGlobal('EventSource', FakeES);
  vi.stubGlobal('ResizeObserver', FakeRO);
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    const routes: Array<[RegExp, unknown]> = [
      [/\/api\/projects$/, { projects: [{ projectId: 'p1', name: 'P1', role: 'master' }] }],
      [/\/sprints\?boardId=b1/, { sprints: [] }],
      [/\/boards\/b1\/tasks\?/, { tasks: [] }],
      [/\/boards\/b1\/feed$/, { events: [] }],
      [/\/boards\/b1$/, { board, taskCounts: {} }],
      [/\/boards$/, { boards: [board] }],
      [/\/projects\/p1$/, { project: { id: 'p1', name: 'P1', members: {} } }],
    ];
    return json(routes.find(([re]) => re.test(url))?.[1] ?? {});
  }));
  const view = render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <ProjectProvider>
        <ShortcutProvider>
          <MemoryRouter initialEntries={['/projects/p1/boards/b1']}>
            <div className="main">
              <Routes>
                <Route path="/projects/:projectId/boards/:boardId" element={<Board />} />
              </Routes>
            </div>
          </MemoryRouter>
        </ShortcutProvider>
      </ProjectProvider>
    </QueryClientProvider>,
  );
  await screen.findByText('To do');
  return view;
}

describe('the board columns follow the header’s height (P-448)', () => {
  it('a resize of the header re-measures --columns-top, and unmounting disconnects the observer', async () => {
    const view = await renderBoard();
    const header = document.querySelector('.main > .topbar');
    expect(header).not.toBeNull();
    const ro = observers.find((o) => o.targets.includes(header!));
    expect(ro, 'the header is observed').toBeDefined();

    const columns = document.querySelectorAll<HTMLElement>('.columns');
    const real = columns[columns.length - 1]!;
    // The header grew: the columns now start 200 px down. No render, no window resize, only the observer.
    vi.spyOn(real, 'getBoundingClientRect').mockReturnValue({ top: 200, bottom: 600, left: 0, right: 800, width: 800, height: 400, x: 0, y: 200, toJSON: () => ({}) });
    await act(async () => {
      ro!.cb([], ro as unknown as ResizeObserver);
    });
    expect(real.style.getPropertyValue('--columns-top')).toBe('200px');

    view.unmount();
    expect(ro!.disconnected).toBe(true);
  });
});
