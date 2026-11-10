/**
 * A key a focused control acts on itself is the control's, not a shortcut's.
 *
 * The dispatcher listens on `window` and exempted only text fields, so with a card selected the
 * board's `Enter` ("open the selected task") ran on Enter pressed on ANY focused button or link, and
 * its `preventDefault` cancelled that control's own activation: "Hide activity", "Load more", a nav
 * link or "Sign out" opened the previously selected card instead. A focused card took Enter for the
 * j/k-selected card, not itself.
 */
import { describe, it, expect, vi, beforeAll, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { ShortcutProvider, useShortcuts } from '../shortcuts.js';
import { Board } from '../screens/Board.js';
import type { Task } from '../types.js';

beforeAll(() => {
  Element.prototype.scrollIntoView = (): void => {}; // jsdom has none; j/k scrolls the selection into view
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

function Bound({ run }: { run: () => void }) {
  useShortcuts('board', [{ keys: 'Enter', label: 'Open selected task', run: () => run() }], [run]);
  return (
    <>
      <button type="button">Hide activity</button>
      <a href="/elsewhere">a link</a>
      <div role="button" tabIndex={0}>a role button</div>
      <div role="menu"><div role="menuitem" tabIndex={-1}>an item</div></div>
    </>
  );
}

describe('the dispatcher leaves a focused control its own keys', () => {
  it('Enter and Space on a button, a link or a role=button do not run the board binding or cancel the press', () => {
    const run = vi.fn();
    render(<ShortcutProvider><Bound run={run} /></ShortcutProvider>);
    for (const el of [screen.getByText('Hide activity'), screen.getByText('a link'), screen.getByText('a role button')]) {
      el.focus();
      expect(fireEvent.keyDown(el, { key: 'Enter' }), `${el.textContent}: its activation was cancelled`).toBe(true);
      expect(fireEvent.keyDown(el, { key: ' ' })).toBe(true);
    }
    expect(run).not.toHaveBeenCalled();
  });

  it('an event a component already handled is not handled again', () => {
    const run = vi.fn();
    render(<ShortcutProvider><Bound run={run} /></ShortcutProvider>);
    const ev = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true });
    ev.preventDefault();
    document.body.dispatchEvent(ev);
    expect(run).not.toHaveBeenCalled();
  });

  it('CONTROL: Enter with nothing focused (the body) still runs it', () => {
    const run = vi.fn();
    render(<ShortcutProvider><Bound run={run} /></ShortcutProvider>);
    expect(fireEvent.keyDown(document.body, { key: 'Enter' })).toBe(false);
    expect(run).toHaveBeenCalledTimes(1);
  });

  it('CONTROL: a modified chord (⌘K) on a focused button still reaches the dispatcher', () => {
    const run = vi.fn();
    function K() {
      useShortcuts('global', [{ keys: '⌘k', label: 'Command palette', run: () => run() }], []);
      return <button type="button">b</button>;
    }
    render(<ShortcutProvider><K /></ShortcutProvider>);
    fireEvent.keyDown(screen.getByText('b'), { key: 'k', metaKey: true });
    expect(run).toHaveBeenCalledTimes(1);
  });
});

class FakeES {
  readyState = 0;
  addEventListener(): void {}
  close(): void {
    this.readyState = 2;
  }
}
const board = { id: 'b1', projectId: 'p1', name: 'Board', columns: [{ id: 'c1', name: 'To do', order: 1, status: 'TODO', wipLimit: null }] };
const mk = (i: number): Task =>
  ({
    id: `T-${i}`, projectId: 'p1', boardId: 'b1', columnId: 'c1', title: `Task ${i}`, description: '', status: 'TODO', priority: 'normal',
    assignee: null, labels: [], requiredCapabilities: [], estimate: null, dueAt: null, customFields: {}, parentTaskId: null, order: `a${i}`,
    blockedBy: [], blocks: [], contextSummary: '', version: 1, claim: null, createdBy: 'alice@example.com',
    createdAt: '2026-10-30T00:00:00.000Z', updatedAt: '2026-10-30T00:00:00.000Z',
  }) as unknown as Task;

function Where() {
  const l = useLocation();
  return <span data-testid="where">{l.search}</span>;
}

describe('on the board', () => {
  beforeEach(() => {
    vi.stubGlobal('EventSource', FakeES);
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        const routes: Array<[RegExp, unknown]> = [
          [/\/boards\/b1\/tasks\?/, { tasks: [mk(1), mk(2)] }],
          [/\/boards\/b1\/feed$/, { events: [] }],
          [/\/boards\/b1$/, { board, taskCounts: { c1: 2 } }],
          [/\/boards$/, { boards: [board] }],
          [/\/agents\/sessions/, { sessions: [] }],
        ];
        return { ok: true, status: 200, json: async () => routes.find(([re]) => re.test(url))?.[1] ?? {} } as Response;
      }),
    );
  });

  it('with a card selected, Enter on a focused button does not open it; Enter on a focused card opens THAT card', async () => {
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ShortcutProvider>
          <MemoryRouter initialEntries={['/projects/p1/boards/b1']}>
            <Routes>
              <Route path="/projects/:projectId/boards/:boardId" element={<><Board /><Where /></>} />
            </Routes>
          </MemoryRouter>
        </ShortcutProvider>
      </QueryClientProvider>,
    );
    await waitFor(() => expect(document.querySelectorAll('.card[data-task-id]').length).toBe(2));
    act(() => void fireEvent.keyDown(document.body, { key: 'j' })); // selects T-1
    await waitFor(() => expect(document.querySelector('.card.selected')?.getAttribute('data-task-id')).toBe('T-1'));

    const activity = screen.getByRole('button', { name: 'Activity' });
    activity.focus();
    expect(fireEvent.keyDown(activity, { key: 'Enter' }), 'the button press was cancelled').toBe(true);
    expect(screen.getByTestId('where')).toHaveTextContent(/^$/);

    // The focusable card wrapper (dnd-kit gives it role=button and tabIndex=0).
    const card2 = document.querySelector('.card[data-task-id="T-2"]')!.parentElement as HTMLElement;
    expect(card2.getAttribute('role')).toBe('button');
    card2.focus();
    fireEvent.keyDown(card2, { key: 'Enter' });
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('?task=T-2'));
  });
});
