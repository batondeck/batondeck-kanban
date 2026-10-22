/**
 * The DEFAULT state of the app must not render a blank screen.
 *
 * `useProject()` starts at '' — "All projects" — and Chronicle's query is `enabled: !!projectId`. So
 * on a fresh sign-in the fetch never fires, `q.data` stays undefined, and the "No records yet" branch
 * (which requires `q.data`) never renders either. The screen showed a heading and nothing else,
 * permanently, with no error and no spinner.
 *
 * It was reported twice as "chronicle has no content" while the records existed and were being served
 * correctly the whole time — the UI simply never asked for them. Every other Chronicle test seeds a
 * project into localStorage, which is exactly why none of them could see this.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { Chronicle, ChroniclePageView } from '../screens/Chronicle.js';
import { ProjectProvider } from '../projectContext.js';

function stub() {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes('/api/session'))
      return new Response(JSON.stringify({ identity: 'a@b.c', csrfToken: 't', isAdmin: false }), { status: 200 });
    if (url.includes('/chronicle'))
      return new Response(JSON.stringify({ pages: [] }), { status: 200 });
    if (url.endsWith('/api/projects'))
      return new Response(JSON.stringify({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] }), { status: 200 });
    return new Response(JSON.stringify({}), { status: 200 });
  }));
}

function renderAt(path: string, element: React.ReactNode, route: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <ProjectProvider>
          <Routes><Route path={route} element={element} /></Routes>
        </ProjectProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => { cleanup(); vi.unstubAllGlobals(); localStorage.clear(); });

describe('Chronicle at the default "All projects" scope', () => {
  it('the INDEX renders a real answer instead of nothing (T-429: it lists, it no longer asks)', async () => {
    localStorage.clear();                       // no stored pick — the fresh-sign-in state
    stub();
    renderAt('/chronicle', <Chronicle />, '/chronicle');
    // The one project is fetched and genuinely empty — so the honest answer is "no records yet".
    await waitFor(() => expect(screen.getByText(/no records yet/i)).toBeTruthy());
    expect(screen.queryByText(/pick a project/i)).toBeNull();
  });

  it('the PAGE view does not sit on a spinner that can never resolve', async () => {
    localStorage.clear();
    stub();
    renderAt('/chronicle/CP-1', <ChroniclePageView />, '/chronicle/:pageId');
    // A disabled react-query stays `isPending` forever, so "Loading…" here would never clear.
    await waitFor(() => expect(screen.getByText(/pick a project/i)).toBeTruthy());
    expect(screen.queryByText(/loading/i)).toBeNull();
  });

  it('POSITIVE CONTROL: WITH a project scoped, the prompt is gone and the real fetch happens', async () => {
    localStorage.setItem('bd.projectId', 'P-1');
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/api/session'))
        return new Response(JSON.stringify({ identity: 'a@b.c', csrfToken: 't', isAdmin: false }), { status: 200 });
      if (url.includes('/chronicle'))
        return new Response(JSON.stringify({ pages: [] }), { status: 200 });
      if (url.endsWith('/api/projects'))
        return new Response(JSON.stringify({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] }), { status: 200 });
      return new Response(JSON.stringify({}), { status: 200 });
    });
    vi.stubGlobal('fetch', fetchMock);
    renderAt('/chronicle', <Chronicle />, '/chronicle');

    // The genuinely-empty message, NOT the pick-a-project one — they mean different things and the
    // whole defect was showing neither.
    await waitFor(() => expect(screen.getByText(/no records yet/i)).toBeTruthy());
    expect(screen.queryByText(/pick a project/i)).toBeNull();
    expect(fetchMock.mock.calls.some(([u]) => String(u).includes('/chronicle')), 'the fetch must fire').toBe(true);
  });
});
