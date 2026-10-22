/**
 * T-429 — the "All projects" scope LISTS every visible project's Chronicle instead of asking the
 * reader to pick one first.
 *
 * The states this pins, each distinct: records grouped and labeled by project name; an empty project
 * contributes nothing; ONE project's fetch failing renders an inline error in its own group and never
 * blanks the rest; "no visible projects" and "projects but zero records" are different sentences.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { Chronicle } from '../screens/Chronicle.js';
import { ProjectProvider } from '../projectContext.js';

const rec = (projectId: string, n: number, over: Record<string, unknown> = {}) => ({
  id: `CP-${projectId}-${n}`,
  projectId,
  kind: 'adr' as const,
  slug: `adr/000${n}-x`,
  title: `${projectId} decision ${n}`,
  headVersionId: `CV-${n}`,
  sourcePath: null,
  createdAt: '2026-07-31T00:00:00Z',
  updatedAt: '2026-07-31T00:00:00Z',
  ...over,
});

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

/** Serves per-project chronicle bodies keyed by project id; a missing key answers 403. */
function stub(projects: Array<{ projectId: string; name: string }>, byProject: Record<string, unknown>) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/api/session')) return json({ identity: 'a@b.c', csrfToken: 't', isAdmin: false });
      const m = /\/api\/projects\/([^/]+)\/chronicle/.exec(url);
      if (m) {
        const body = byProject[m[1]!];
        return body ? json(body) : json({ code: 'FORBIDDEN', message: 'not a member' }, 403);
      }
      if (url.endsWith('/api/projects')) return json({ projects: projects.map((p) => ({ ...p, role: 'member' })) });
      return json({});
    }),
  );
}

function mount() {
  localStorage.clear(); // no stored pick — the "All projects" default
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/chronicle']}>
        <ProjectProvider>
          <Routes>
            <Route path="/chronicle" element={<Chronicle />} />
          </Routes>
        </ProjectProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('Chronicle at the "All projects" scope (T-429)', () => {
  it('lists every visible project’s records, labeled; an empty project contributes nothing; a failing one errors inline without blanking the list', async () => {
    stub(
      [
        { projectId: 'P-1', name: 'Alpha' },
        { projectId: 'P-2', name: 'Beta' },
        { projectId: 'P-3', name: 'Gamma' },
      ],
      {
        'P-1': { pages: [rec('P-1', 1), rec('P-1', 2)] },
        'P-2': { pages: [] },
        // P-3 absent -> its fetch answers 403
      },
    );
    mount();

    // Alpha's records, under Alpha's name, each row a link to the existing page route.
    await waitFor(() => expect(screen.getByText('P-1 decision 1')).toBeTruthy());
    expect(screen.getByText('Alpha')).toBeTruthy();
    expect(screen.getByText('P-1 decision 2')).toBeTruthy();
    expect(screen.getByText('P-1 decision 1').closest('a')?.getAttribute('href')).toBe('/chronicle/CP-P-1-1');

    // Gamma's failure is INLINE, named, and the rest of the screen survived it.
    await waitFor(() => expect(screen.getByText(/could not load this project/i)).toBeTruthy());
    expect(screen.getByText('Gamma')).toBeTruthy();
    expect(screen.getByText(/not a member/i)).toBeTruthy();
    expect(screen.getByText('P-1 decision 1')).toBeTruthy();

    // Beta is empty and contributes nothing — no group, and no whole-screen empty state either.
    expect(screen.queryByText('Beta')).toBeNull();
    expect(screen.queryByText(/no records yet/i)).toBeNull();

    // The retired prompt must be gone.
    expect(screen.queryByText(/pick a project/i)).toBeNull();
  });

  /**
   * T-514 — the supersession tag at THIS scope, which no test reached.
   *
   * The per-project screen and the all-projects fanout render the row twice, in two branches, and
   * only the first was covered: every other suite's `mount()` writes `bd.projectId`, so the fanout
   * branch was never driven and deleting its tag left the suite green. A record that has been
   * superseded must not read as current anywhere it is listed.
   */
  it('a superseded record is tagged in the all-projects fanout too, not only under one project', async () => {
    stub([{ projectId: 'P-1', name: 'Alpha' }], {
      'P-1': { pages: [rec('P-1', 1, { supersededBy: ['ADR-0044'] }), rec('P-1', 2)] },
    });
    mount();
    await waitFor(() => expect(screen.getByText('P-1 decision 1')).toBeTruthy());
    // Exactly one tag: the row that carries a successor, and not the row beside it.
    expect(screen.getAllByText('superseded')).toHaveLength(1);
    expect(screen.getByText('P-1 decision 1').closest('a')?.textContent).toContain('superseded');
    expect(screen.getByText('P-1 decision 2').closest('a')?.textContent).not.toContain('superseded');
  });

  it('projects but ZERO records everywhere says "no records yet", not "pick a project"', async () => {
    stub([{ projectId: 'P-1', name: 'Alpha' }], { 'P-1': { pages: [] } });
    mount();
    await waitFor(() => expect(screen.getByText(/no records yet/i)).toBeTruthy());
    expect(screen.queryByText(/pick a project/i)).toBeNull();
  });

  it('NO visible projects is its own sentence — membership, not emptiness', async () => {
    stub([], {});
    mount();
    await waitFor(() => expect(screen.getByText(/not a member of any project yet/i)).toBeTruthy());
    expect(screen.queryByText(/no records yet/i)).toBeNull();
  });

  it('a partial first page says so per project instead of truncating in silence', async () => {
    stub([{ projectId: 'P-1', name: 'Alpha' }], { 'P-1': { pages: [rec('P-1', 1)], nextCursor: 'c1' } });
    mount();
    await waitFor(() => expect(screen.getByText('P-1 decision 1')).toBeTruthy());
    expect(screen.getByRole('status').textContent).toMatch(/partial — more records exist in Alpha/i);
  });
});
