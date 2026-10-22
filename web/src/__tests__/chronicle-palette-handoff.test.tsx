/**
 * Chronicle is now a SECOND publisher into the one palette store, and `publishPalette` REPLACES the
 * whole context rather than merging into it — so leaving Chronicle could plausibly wipe the actions
 * another screen had published (`Board.tsx` publishes `boardId`, `actions` and `openTask`).
 *
 * It does not, and the reason is ordering: React runs a removed subtree's effect cleanup before the
 * arriving subtree's effects, so Chronicle's `clearPalette()` always lands BEFORE the next screen's
 * publish. This test pins that, because nothing else in the codebase states it and the invariant is
 * only load-bearing now that a second publisher exists.
 *
 * The other publisher is a FAKE on purpose (rule 12): what is under test is the store and the effect
 * ordering — real code on both counts — not Board's own logic. Proven live by making Chronicle
 * publish on a `setTimeout`, which reproduces the clobber and turns this red.
 */
import { describe, it, expect, vi, afterEach, beforeAll } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes, Link } from 'react-router';
import { render, screen, cleanup, waitFor, fireEvent } from '@testing-library/react';
import { useEffect } from 'react';
import { Chronicle } from '../screens/Chronicle.js';
import { CommandPalette, publishPalette, clearPalette } from '../palette.js';
import { ShortcutProvider } from '../shortcuts.js';
import { ProjectProvider } from '../projectContext.js';

beforeAll(() => {
  (globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = class {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  };
  Element.prototype.scrollIntoView = (): void => {};
});

const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200 });

/** Stands in for Board: publishes an action on mount, clears on unmount — the same contract. */
function FakeBoard() {
  useEffect(() => {
    publishPalette({ projectId: 'P-1', boardId: 'B-1', actions: [{ id: 'x', label: 'Board action' }] });
    return () => clearPalette();
  }, []);
  return <Link to="/chronicle">to chronicle</Link>;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('palette ownership across a route change', () => {
  it('going board -> chronicle -> board leaves the board action in ⌘K', async () => {
    localStorage.setItem('bd.projectId', 'P-1');
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('/api/session')) return json({ identity: 'a@b.c', csrfToken: 't', isAdmin: false });
        if (url.includes('/chronicle')) return json({ pages: [] });
        if (url.endsWith('/api/projects')) return json({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] });
        return json({});
      }),
    );
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/board']}>
          <ProjectProvider>
            <ShortcutProvider>
              <CommandPalette />
              <Routes>
                <Route path="/board" element={<FakeBoard />} />
                <Route path="/chronicle" element={<><Link to="/board">back</Link><Chronicle /></>} />
              </Routes>
            </ShortcutProvider>
          </ProjectProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    fireEvent.keyDown(window, { key: 'k', metaKey: true });
    await waitFor(() => expect(screen.getByRole('option', { name: /Board action/ })).toBeTruthy());
    fireEvent.keyDown(screen.getByPlaceholderText(/type a command or search/i), { key: 'Escape' });

    fireEvent.click(screen.getByText('to chronicle'));
    await waitFor(() => expect(screen.getByText('back')).toBeTruthy());
    fireEvent.click(screen.getByText('back'));
    await waitFor(() => expect(screen.getByText('to chronicle')).toBeTruthy());

    fireEvent.keyDown(window, { key: 'k', metaKey: true });
    await waitFor(() => expect(screen.getByRole('option', { name: /Board action/ })).toBeTruthy());
  });
});
