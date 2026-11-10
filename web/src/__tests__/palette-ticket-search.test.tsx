/**
 * The ⌘K palette keeps filtering its commands once the ticket search has answered.
 *
 * It used to switch cmdk's filtering off whenever the search had data (`shouldFilter={!searchQ.data}`),
 * so the list became every command, unfiltered, with the server's hits at the bottom and the
 * selection on the first command: typing a ticket's title and pressing Enter ran the board's "Open
 * T-9" action (or went to Projects), and typing "settings" past the debounce did the same.
 */
import { describe, it, expect, vi, afterEach, beforeAll } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useEffect } from 'react';
import { CommandPalette, clearPalette, publishPalette } from '../palette.js';
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

afterEach(() => {
  cleanup();
  clearPalette();
  vi.unstubAllGlobals();
  localStorage.clear();
});

const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200 });
const hit = (id: string, title: string) => ({ task: { id, title, boardId: 'B-1', status: 'TODO' }, score: 1 });

function Where() {
  return <span data-testid="where">{useLocation().pathname + useLocation().search}</span>;
}
function Publisher({ run }: { run: () => void }) {
  useEffect(() => {
    publishPalette({ projectId: 'P-1', boardId: 'B-1', actions: [{ id: 'open', label: 'Open T-9', kbd: '↵', run }] });
    return () => clearPalette();
  }, [run]);
  return null;
}

function setup(results: (q: string) => unknown[]) {
  localStorage.setItem('bd.projectId', 'P-1');
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), 'http://x');
      if (url.pathname.endsWith('/search')) return json({ results: results(url.searchParams.get('query') ?? '') });
      if (url.pathname === '/api/projects') return json({ projects: [{ projectId: 'P-1', name: 'Alpha', role: 'admin' }] });
      return json({});
    }),
  );
  const openT9 = vi.fn();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/start']}>
        <ProjectProvider>
          <ShortcutProvider>
            <Publisher run={openT9} />
            <CommandPalette />
            <Routes>
              <Route path="*" element={<Where />} />
            </Routes>
          </ShortcutProvider>
        </ProjectProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { openT9, fetchSpy: globalThis.fetch as unknown as ReturnType<typeof vi.fn> };
}
const input = () => screen.getByPlaceholderText(/type a command or search/i);
const searched = (f: ReturnType<typeof vi.fn>, q: string) =>
  f.mock.calls.some(([u]) => new URL(String(u), 'http://x').searchParams.get('query') === q);

describe('the palette with ticket search answering', () => {
  it('typing a ticket title and pressing Enter opens that ticket, not the board action', async () => {
    const { openT9 } = setup((q) => (q === 'wake' ? [hit('T-7', 'wake the sleeper')] : []));
    fireEvent.keyDown(window, { key: 'k', metaKey: true });
    await waitFor(() => expect(screen.getByRole('option', { name: /Open T-9/ })).toBeTruthy());
    fireEvent.change(input(), { target: { value: 'wake' } });
    await waitFor(() => expect(screen.getByRole('option', { name: /wake the sleeper/ })).toBeTruthy());
    // Filtering stays on: the commands that do not match are gone.
    expect(screen.queryByRole('option', { name: /Open T-9/ })).toBeNull();
    expect(screen.queryByRole('option', { name: /Go to Projects/ })).toBeNull();
    fireEvent.keyDown(input(), { key: 'Enter' });
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('/projects/P-1/boards/B-1?task=T-7'));
    expect(openT9).not.toHaveBeenCalled();
  });

  it('a server hit whose title does not contain the query is still listed', async () => {
    setup((q) => (q === 'flaky' ? [hit('T-3', 'Retry the uploader')] : []));
    fireEvent.keyDown(window, { key: 'k', metaKey: true });
    fireEvent.change(input(), { target: { value: 'flaky' } });
    await waitFor(() => expect(screen.getByRole('option', { name: /Retry the uploader/ })).toBeTruthy());
  });

  it('typing a command past the debounce still runs that command', async () => {
    const { openT9, fetchSpy } = setup(() => []);
    fireEvent.keyDown(window, { key: 'k', metaKey: true });
    fireEvent.change(input(), { target: { value: 'sett' } });
    await waitFor(() => expect(searched(fetchSpy, 'sett')).toBe(true));
    await act(() => new Promise<void>((r) => setTimeout(r, 50)));
    fireEvent.change(input(), { target: { value: 'settings' } });
    await waitFor(() => expect(screen.getByRole('option', { name: /Go to Settings/ })).toBeTruthy());
    expect(screen.queryByRole('option', { name: /Open T-9/ })).toBeNull();
    fireEvent.keyDown(input(), { key: 'Enter' });
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent('/settings'));
    expect(openT9).not.toHaveBeenCalled();
  });
});
