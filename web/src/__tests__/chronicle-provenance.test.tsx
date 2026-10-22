/**
 * CHS-4 — the one UI property the feature's central claim depends on.
 *
 * `derived-then-edited` must be VISIBLE, not merely stored. If a reader cannot see that a sentence
 * has drifted from the evidence it still cites, then "full editing is safe here because provenance
 * is preserved" is true in the database and false on screen — which is exactly the Confluence failure
 * this product exists to beat.
 *
 * So this renders the page and asserts on what a person would actually see, rather than on props.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { ChroniclePageView } from '../screens/Chronicle.js';
import { ProjectProvider } from '../projectContext.js';

const PAGE = {
  id: 'CP-1',
  projectId: 'P-1',
  kind: 'adr' as const,
  slug: 'adr/0001-x',
  title: 'A decision',
  headVersionId: 'CV-2',
  sourcePath: null,
  createdAt: '2026-07-31T00:00:00Z',
  updatedAt: '2026-07-31T00:00:00Z',
};

const head = (blocks: unknown[]) => ({
  id: 'CV-2',
  pageId: 'CP-1',
  parentVersionId: 'CV-1',
  source: 'human' as const,
  author: 'a@b.c',
  agent: null,
  ts: '2026-07-31T00:00:00Z',
  blocks,
});

function stub(blocks: unknown[], drifted: string[]) {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes('/api/session')) {
      return new Response(JSON.stringify({ identity: 'a@b.c', csrfToken: 't', isAdmin: false }), { status: 200 });
    }
    if (url.includes('/api/projects') && url.includes('/chronicle/CP-1')) {
      return new Response(JSON.stringify({ page: PAGE, head: head(blocks), drifted }), { status: 200 });
    }
    if (url.endsWith('/api/projects')) {
      return new Response(JSON.stringify({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] }), { status: 200 });
    }
    return new Response(JSON.stringify({}), { status: 200 });
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

function renderPage() {
  // ProjectProvider only honours a stored id that is still in the roster, so both halves are needed
  // or `useProject()` yields '' and every query stays disabled behind `enabled: !!projectId`.
  localStorage.setItem('bd.projectId', 'P-1');
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/chronicle/CP-1']}>
        <ProjectProvider>
          <Routes>
            <Route path="/chronicle/:pageId" element={<ChroniclePageView />} />
          </Routes>
        </ProjectProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('CHS-4 provenance is visible, not just stored', () => {
  it('an edited block SAYS its citation may no longer hold', async () => {
    stub(
      [
        { id: 'b1', state: 'derived-then-edited', text: 'someone rewrote this', citations: [{ projectId: 'P-1', boardId: 'B-1', taskId: 'T-9' }], origin: { adrId: 'ADR-1', blockHash: 'abc' } },
        { id: 'b2', state: 'derived', text: 'untouched', citations: [], origin: { adrId: 'ADR-1', blockHash: 'def' } },
      ],
      ['b1'],
    );
    renderPage();

    // The warning a reader must not miss — asserted on the words, not a class name. It appears
    // TWICE by design: once on the block itself and once in the page banner, so a reader who scans
    // the top and a reader who scans the prose both hit it.
    await waitFor(() => expect(screen.getAllByText(/citation may no longer hold/i).length).toBe(2));
    expect(screen.getByRole('status').textContent).toMatch(/1 block .*has been edited since generation/i);
  });

  it('POSITIVE CONTROL: a clean page shows NO drift warning, so the warning means something', async () => {
    stub(
      [
        { id: 'b1', state: 'derived', text: 'generated', citations: [], origin: { adrId: 'ADR-1', blockHash: 'abc' } },
        { id: 'b2', state: 'authored', text: 'a human note', citations: [], origin: null },
      ],
      [],
    );
    renderPage();

    await waitFor(() => expect(screen.getByText('generated')).toBeTruthy());
    expect(screen.queryAllByText(/citation may no longer hold/i)).toHaveLength(0);
    expect(screen.queryByRole('status')).toBeNull();
    // T-491 (owner decision, 2026-08-21): a derived block carries NO chip — on a generated record
    // ~95% of blocks are derived, so the label was noise; its provenance IS the citation rendered
    // beneath the block. The states a reader must be WARNED about keep their labels: `authored`
    // asserted here, `derived-then-edited` by the drift cases above.
    expect(screen.queryByText('derived')).toBeNull();
    expect(screen.getByText('authored')).toBeTruthy();
  });

  it('a citation link carries the FULL triple, so a bare task id can never be what resolves', async () => {
    stub(
      [{ id: 'b1', state: 'derived', text: 'x', citations: [{ projectId: 'P-1', boardId: 'B-1', taskId: 'T-9' }], origin: { adrId: 'A', blockHash: 'h' } }],
      [],
    );
    renderPage();
    const link = await waitFor(() => screen.getByTitle('T-9 — open the ticket this is derived from'));
    // The href carries the WHOLE triple including the task — a citation exists to answer "where did
    // this sentence come from", and a board link made the reader hunt for the ticket the record had
    // already named (owner, 2026-08-22). The assertion's intent is unchanged: a bare task id is still
    // never what resolves.
    expect(link.getAttribute('href')).toBe('/projects/P-1/boards/B-1/tasks/T-9');
  });
});

describe('CHS-4 a missing page is not a spinner, and an edit is pinned to what was edited', () => {
  /** `{page: null}` is a real answer — the core returns it rather than leaking whether a page exists. */
  it('a page that reads back as null says so instead of loading forever', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/api/session'))
        return new Response(JSON.stringify({ identity: 'a@b.c', csrfToken: 't', isAdmin: false }), { status: 200 });
      if (url.includes('/chronicle/CP-1'))
        return new Response(JSON.stringify({ page: null, head: null, drifted: [] }), { status: 200 });
      if (url.endsWith('/api/projects'))
        return new Response(JSON.stringify({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] }), { status: 200 });
      return new Response(JSON.stringify({}), { status: 200 });
    });
    vi.stubGlobal('fetch', fetchMock);
    renderPage();

    // Previously `if (!page || !head)` rendered "Loading…", which never resolved: a spinner that is
    // really a 404 makes the reader wait instead of fixing the link.
    await waitFor(() => expect(screen.getByText(/no such record/i)).toBeTruthy());
    expect(screen.queryByText(/loading/i)).toBeNull();
  });

  it('the edit posts the version the editor OPENED on, not whatever the head has drifted to', async () => {
    const posted: string[] = [];
    // The head MOVES under the open editor — someone else saved. This is the whole point of the
    // test: without a refetch mid-edit, reading the head at save time and reading it at open time
    // are indistinguishable and the assertion proves nothing.
    let served = 'CV-2';
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/api/session'))
        return new Response(JSON.stringify({ identity: 'a@b.c', csrfToken: 't', isAdmin: false }), { status: 200 });
      if (url.includes('/chronicle/CP-1/edit')) {
        posted.push((JSON.parse(String(init?.body)) as { expectedParentVersionId: string }).expectedParentVersionId);
        return new Response(JSON.stringify({ versionId: 'CV-9', drifted: [] }), { status: 200 });
      }
      if (url.includes('/chronicle/CP-1'))
        return new Response(
          JSON.stringify({
            page: { ...PAGE, headVersionId: served },
            head: { ...head([{ id: 'b1', state: 'derived', text: 'x', citations: [], origin: null }]), id: served },
            drifted: [],
          }),
          { status: 200 },
        );
      if (url.endsWith('/api/projects'))
        return new Response(JSON.stringify({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] }), { status: 200 });
      return new Response(JSON.stringify({}), { status: 200 });
    });
    vi.stubGlobal('fetch', fetchMock);

    localStorage.setItem('bd.projectId', 'P-1');
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/chronicle/CP-1']}>
          <ProjectProvider>
            <Routes>
              <Route path="/chronicle/:pageId" element={<ChroniclePageView />} />
            </Routes>
          </ProjectProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    // Open the editor on CV-2 …
    const edit = await waitFor(() => screen.getByText('Edit'));
    edit.click();
    await waitFor(() => expect(screen.getByText('Save version')).toBeTruthy());

    // … then someone else commits CV-3 and a background refetch picks it up.
    served = 'CV-3';
    await qc.invalidateQueries({ queryKey: ['chronicle-page', 'P-1', 'CP-1'] });
    await waitFor(() => expect(qc.getQueryState(['chronicle-page', 'P-1', 'CP-1'])?.isInvalidated).toBe(false));

    screen.getByText('Save version').click();

    // `expectedParentVersionId` means "the version I edited". Reading the LIVE head here would post
    // CV-3 — a version this user never saw — so the STALE check matches and their save silently
    // overwrites the other person's work: the exact lost update the check exists to prevent, made
    // invisible by the mechanism meant to surface it.
    await waitFor(() => expect(posted).toEqual(['CV-2']));
  });
});
