/**
 * T-491 — the Chronicle reads as an architecture record, not a work log. Three defects, each pinned
 * at the layer a reader observes (the rendered DOM), each measured on the hosted page first:
 * GFM tables flattened to a paragraph; a DERIVED chip on ~95% of blocks; evidence unfolded noise.
 * Harness mirrors chronicle-editor.test.tsx (same route, same ProjectProvider, same fetch fake).
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { ChroniclePageView } from '../screens/Chronicle.js';
import { ProjectProvider } from '../projectContext.js';

const PAGE = {
  id: 'CP-1', projectId: 'P-1', kind: 'adr' as const, slug: 'adr/t-1', title: 'A record',
  headVersionId: 'CV-1', sourcePath: null,
  createdAt: '2026-08-21T00:00:00Z', updatedAt: '2026-08-21T00:00:00Z',
};
const HEAD = {
  id: 'CV-1', pageId: 'CP-1', parentVersionId: null, source: 'sweep' as const,
  author: 'a@b.c', agent: null, ts: '2026-08-21T00:00:00Z',
  blocks: [
    { id: 'ctx1/1', state: 'derived' as const, text: 'The decision text.',
      citations: [{ projectId: 'P-1', boardId: 'B-1', taskId: 'T-1' }], origin: null },
    { id: 'a-1', state: 'authored' as const, text: 'A human note.', citations: [], origin: null },
    { id: 'ev1/2', state: 'derived' as const,
      text: '| Claim | Source |\n|---|---|\n| C4 skipped | T-162 desc |', citations: [], origin: null },
  ],
};
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

function stubServer(): void {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes('/api/session')) return json({ identity: 'a@b.c', csrfToken: 't', isAdmin: false });
    if (url.includes('/chronicle/CP-1')) return json({ page: PAGE, head: HEAD, drifted: [], conflicts: null });
    if (url.endsWith('/api/projects')) return json({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] });
    return json({});
  }));
}

afterEach(() => { cleanup(); vi.unstubAllGlobals(); localStorage.clear(); });

async function mount(): Promise<void> {
  stubServer();
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
  await waitFor(() => screen.getByText('The decision text.'));
}

describe('Chronicle rendering (T-491)', () => {
  it('a GFM pipe table renders as a real table, not a flattened paragraph', async () => {
    await mount();
    const details = document.querySelector('details.chr-evidence')!;
    (details as HTMLDetailsElement).open = true;
    const table = document.querySelector('.chr-table-wrap table');
    expect(table, 'remark-gfm absent — the evidence table flattened to a paragraph again').toBeTruthy();
    expect(table!.querySelectorAll('th').length).toBe(2);
  });

  it('a derived block carries NO provenance chip — its citation is the reference', async () => {
    await mount();
    expect(screen.queryByText('derived')).toBeNull();
    // The tooltip stopped restating the triple (the href carries it) and now says what clicking
    // DOES, because the link opens the ticket rather than its board (owner, 2026-08-22).
    expect(screen.getByTitle('T-1 — open the ticket this is derived from')).toBeTruthy();
  });

  it('POSITIVE CONTROL: an authored block still carries its chip', async () => {
    await mount();
    expect(screen.getByText('authored')).toBeTruthy();
  });

  it('evidence folds behind a details element; context leads the page', async () => {
    await mount();
    const details = document.querySelector('details.chr-evidence');
    expect(details, 'the evidence fold is gone — records read as work logs again').toBeTruthy();
    expect(details!.textContent).toContain('Evidence & coverage');
    // The lead (context) block is OUTSIDE the fold.
    expect(details!.textContent).not.toContain('The decision text.');
  });
});
