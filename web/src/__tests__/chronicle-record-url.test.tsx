/**
 * A record's URL names its project.
 *
 * A page id is a hash of the slug alone, so two projects recording the same slug share it, and every
 * lookup is keyed by project too (internal/domain/chronicle.go). The record URL was `/chronicle/<id>`
 * and the page read the project from the sidebar: a shared link opened "Pick a project" for a reader
 * on "All projects", "No such record" for one on another project, or ANOTHER project's record with
 * the same slug. Record links now carry `?project=`, and the page reads it before the sidebar.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { render, screen, cleanup, waitFor, fireEvent } from '@testing-library/react';
import { Chronicle, ChronicleHistory, ChroniclePageView } from '../screens/Chronicle.js';
import { ProjectProvider, useProject } from '../projectContext.js';

const PROJECTS = [
  { projectId: 'P-1', name: 'Alpha', role: 'admin' },
  { projectId: 'P-2', name: 'Beta', role: 'admin' },
];
const page = (projectId: string) => ({
  page: { id: 'CP-1', projectId, slug: 'adr/shared', title: `${projectId} decision`, kind: 'adr', headVersionId: 'CV-1' },
  head: { id: 'CV-1', pageId: 'CP-1', parentId: null, source: 'human', author: 'U-1', agent: null, ts: '2026-10-30T00:00:00Z', blocks: [{ id: 'b1', text: `written in ${projectId}`, state: 'authored', citations: [] }] },
  drifted: [],
  conflicts: null,
});

let fetched: string[] = [];
function stub() {
  fetched = [];
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    fetched.push(url);
    const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200 });
    if (url.endsWith('/api/projects')) return json({ projects: PROJECTS });
    const rec = /\/api\/projects\/(P-\d)\/chronicle\/CP-1(\/versions)?(\?|$)/.exec(url);
    const version = (id: string) => ({ id, source: 'human', author: 'U-1', agent: null, ts: '2026-10-30T00:00:00Z', blockCount: 1 });
    if (rec) return rec[2] ? json({ versions: [version('CV-2'), version('CV-1')] }) : json(page(rec[1]!));
    const list = /\/api\/projects\/(P-\d)\/chronicle(\?|$)/.exec(url);
    if (list) return json({ pages: [{ id: 'CP-1', slug: 'adr/shared', title: `${list[1]} decision`, kind: 'adr', conflict: false }] });
    return json({});
  }));
}

function Sidebar() {
  const { projectId } = useProject();
  return <output aria-label="sidebar project">{projectId || 'all'}</output>;
}

function Where() {
  const l = useLocation();
  return <output aria-label="location">{l.pathname + l.search}</output>;
}

function renderAt(path: string, element: React.ReactNode, route: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <ProjectProvider>
          <Sidebar />
          <Where />
          <Routes><Route path={route} element={element} /></Routes>
        </ProjectProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => { cleanup(); vi.unstubAllGlobals(); localStorage.clear(); });

describe('a shared record link', () => {
  it('opens its own project’s record for a reader on "All projects", and scopes the sidebar to it', async () => {
    stub();
    renderAt('/chronicle/CP-1?project=P-2', <ChroniclePageView />, '/chronicle/:pageId');
    expect(await screen.findByText('written in P-2')).toBeTruthy();
    expect(screen.queryByText(/pick a project/i)).toBeNull();
    await waitFor(() => expect(screen.getByLabelText('sidebar project').textContent).toBe('P-2'));
  });

  it('opens its own project’s record for a reader on ANOTHER project with the same slug', async () => {
    localStorage.setItem('bd.projectId', 'P-1');
    stub();
    renderAt('/chronicle/CP-1?project=P-2', <ChroniclePageView />, '/chronicle/:pageId');
    expect(await screen.findByText('written in P-2')).toBeTruthy();
    expect(fetched.some((u) => u.includes('/api/projects/P-1/chronicle/CP-1')), 'read the sidebar project’s record').toBe(false);
  });

  it('a link with no project still falls back to the sidebar (links made before this change)', async () => {
    localStorage.setItem('bd.projectId', 'P-1');
    stub();
    renderAt('/chronicle/CP-1', <ChroniclePageView />, '/chronicle/:pageId');
    expect(await screen.findByText('written in P-1')).toBeTruthy();
  });

  it('history reads the URL’s project too', async () => {
    localStorage.setItem('bd.projectId', 'P-1');
    stub();
    renderAt('/chronicle/CP-1/history?project=P-2', <ChronicleHistory />, '/chronicle/:pageId/history');
    await waitFor(() => expect(fetched.some((u) => u.includes('/api/projects/P-2/chronicle/CP-1/versions'))).toBe(true));
    expect(screen.getByRole('link', { name: 'Back to page' }).getAttribute('href')).toBe('/chronicle/CP-1?project=P-2');
    // Picking versions to compare keeps the project in the URL.
    fireEvent.click((await screen.findAllByRole('button', { name: 'Compare with latest' }))[1]!);
    await waitFor(() => expect(screen.getByLabelText('location').textContent).toBe('/chronicle/CP-1/history?project=P-2&a=CV-1&b=CV-2'));
  });
});

describe('the links the app makes carry the project', () => {
  it('the index of one project', async () => {
    localStorage.setItem('bd.projectId', 'P-2');
    stub();
    renderAt('/chronicle', <Chronicle />, '/chronicle');
    const link = await screen.findByRole('link', { name: /P-2 decision/ });
    expect(link.getAttribute('href')).toBe('/chronicle/CP-1?project=P-2');
  });

  it('the "All projects" index, one group per project', async () => {
    stub();
    renderAt('/chronicle', <Chronicle />, '/chronicle');
    const a = await screen.findByRole('link', { name: /P-1 decision/ });
    const b = await screen.findByRole('link', { name: /P-2 decision/ });
    expect(a.getAttribute('href')).toBe('/chronicle/CP-1?project=P-1');
    expect(b.getAttribute('href')).toBe('/chronicle/CP-1?project=P-2');
  });

  it('the page’s History link', async () => {
    stub();
    renderAt('/chronicle/CP-1?project=P-2', <ChroniclePageView />, '/chronicle/:pageId');
    expect((await screen.findByRole('link', { name: 'History' })).getAttribute('href')).toBe('/chronicle/CP-1/history?project=P-2');
  });
});
