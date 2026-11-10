/**
 * THE BOARD'S KEYS ARE INERT BEHIND THE DRAWER (web-board#15).
 *
 * j/k already gave way to the drawer; `c` did not: pressed while reading a ticket, it opened the
 * board's new-task row behind the modal and moved focus into it, where typing went unseen.
 */
import { describe, it, expect, vi, afterEach, beforeAll } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { Board } from '../screens/Board.js';
import { AuthProvider } from '../auth.js';
import { ProjectProvider } from '../projectContext.js';
import { ShortcutProvider } from '../shortcuts.js';
import { CommandPalette } from '../palette.js';
import { routeFetch } from './taskFixtures.js';

beforeAll(() => {
  (globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = class {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  };
  Element.prototype.scrollIntoView = (): void => {};
});

class FakeES {
  readyState = 0;
  addEventListener(): void {}
  close(): void {
    this.readyState = 2;
  }
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

function boardAt(path: string, withPalette = false): void {
  vi.stubGlobal('EventSource', FakeES);
  vi.stubGlobal('fetch', routeFetch({
    '/api/projects$': { json: { projects: [{ projectId: 'p1', name: 'P1', role: 'admin' }] } },
    '/boards$': { json: { boards: [{ id: 'b1', projectId: 'p1', name: 'Board' }] } },
  }));
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <AuthProvider>
        <ProjectProvider>
          <ShortcutProvider>
            <MemoryRouter initialEntries={[path]}>
              {withPalette && <CommandPalette />}
              <Routes><Route path="/projects/:projectId/boards/:boardId" element={<Board />} /></Routes>
            </MemoryRouter>
          </ShortcutProvider>
        </ProjectProvider>
      </AuthProvider>
    </QueryClientProvider>,
  );
}

const settle = () => act(() => new Promise<void>((r) => setTimeout(r, 30)));

describe('the board behind an open drawer', () => {
  it('`c` opens no new-task row behind the drawer', async () => {
    boardAt('/projects/p1/boards/b1?task=T-1');
    const dialog = await screen.findByRole('dialog', { name: 'Task T-1' });
    await screen.findByRole('button', { name: '+ New task' });
    await settle();
    fireEvent.keyDown(dialog, { key: 'c' });
    await settle();
    expect(screen.queryByPlaceholderText('Task title…')).not.toBeInTheDocument();
  });

  it('`c` still opens the new-task row with no drawer open', async () => {
    boardAt('/projects/p1/boards/b1');
    await screen.findByRole('button', { name: '+ New task' });
    await settle();
    fireEvent.keyDown(document.body, { key: 'c' });
    expect(await screen.findByPlaceholderText('Task title…')).toBeInTheDocument();
  });

  it('Tab inside the command palette, opened over the drawer, stays in the palette', async () => {
    boardAt('/projects/p1/boards/b1?task=T-1', true);
    const dialog = await screen.findByRole('dialog', { name: 'Task T-1' });
    await screen.findByRole('button', { name: 'Edit' });
    await settle();
    fireEvent.keyDown(window, { key: 'k', metaKey: true });
    const input = await screen.findByPlaceholderText(/type a command or search/i);
    input.focus();
    expect(dialog.contains(document.activeElement)).toBe(false);
    const tab = fireEvent.keyDown(input, { key: 'Tab' });
    expect(tab).toBe(true); // not prevented: the palette keeps its own Tab
    expect(document.activeElement).toBe(input);
  });
});
