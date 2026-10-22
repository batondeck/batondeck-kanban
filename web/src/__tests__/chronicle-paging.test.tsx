/**
 * (14, UI half) A SILENTLY TRUNCATED INDEX IS A FALSE ABSENCE CLAIM.
 *
 * Chronicle's whole pitch is checkable evidence. If the index renders one page of records and stops
 * without saying so, a reader who scans it and does not find the decision concludes it was never
 * recorded — and they are wrong, quietly, in the direction that loses trust. Being wrong loudly
 * ("there are more") is recoverable; being wrong silently is not.
 *
 * The contract this codes against (fixed centrally so the API and UI lanes cannot disagree):
 *
 *     GET /api/projects/:projectId/chronicle?cursor=<opaque>
 *       -> { pages: ChroniclePage[], nextCursor?: string }
 *
 * `nextCursor` present means MORE EXIST. Absent means complete. Asserted on visible words and on the
 * URL that reaches the wire, never on props.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { Chronicle } from '../screens/Chronicle.js';
import { ProjectProvider } from '../projectContext.js';

const rec = (n: number) => ({
  id: `CP-${n}`,
  projectId: 'P-1',
  kind: 'adr' as const,
  slug: `adr/000${n}-x`,
  title: `Decision ${n}`,
  headVersionId: `CV-${n}`,
  sourcePath: null,
  createdAt: '2026-07-31T00:00:00Z',
  updatedAt: '2026-07-31T00:00:00Z',
});

const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200 });

/** Serves `pages[cursor]`; every entry but the last hands back the next cursor. Records the URLs. */
function stub(pages: Array<{ pages: unknown[]; nextCursor?: string }>) {
  const urls: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/api/session')) return json({ identity: 'a@b.c', csrfToken: 't', isAdmin: false });
      if (url.includes('/chronicle')) {
        urls.push(url);
        const cursor = new URL(url, 'http://x').searchParams.get('cursor');
        return json(pages[cursor ? Number(cursor) : 0]);
      }
      if (url.endsWith('/api/projects')) return json({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] });
      return json({});
    }),
  );
  return urls;
}

function mount() {
  localStorage.setItem('bd.projectId', 'P-1');
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

describe('(14) the Chronicle index never truncates in silence', () => {
  it('says the list is PARTIAL, and fetches the rest with the cursor it was given', async () => {
    const urls = stub([
      { pages: [rec(1), rec(2)], nextCursor: '1' },
      { pages: [rec(3)] },
    ]);
    mount();

    await waitFor(() => expect(screen.getByText('Decision 1')).toBeTruthy());
    // The truncation stated in words — a reader who never scrolls to the button is still told.
    expect(screen.getByRole('status').textContent).toMatch(/showing 2 records — this list is partial, more exist/i);

    screen.getByText('Load more').click();

    await waitFor(() => expect(screen.getByText('Decision 3')).toBeTruthy());
    // The earlier page is still on screen: "more" appends, it does not replace.
    expect(screen.getByText('Decision 1')).toBeTruthy();
    // The cursor is opaque and must be sent back verbatim, url-encoded, on the documented parameter.
    expect(urls[1]).toContain('/chronicle?cursor=1');
    // Complete now — so the partial claim is withdrawn rather than left standing.
    expect(screen.queryByText('Load more')).toBeNull();
    expect(screen.queryByText(/this list is partial/i)).toBeNull();
  });

  it('POSITIVE CONTROL: a COMPLETE non-empty list claims nothing — no "more", and every record shown', async () => {
    // Non-empty on purpose. The same assertions on an empty list would pass vacuously: no records
    // means nothing to truncate, so "no more indicator" would prove only that the screen was blank.
    stub([{ pages: [rec(1), rec(2), rec(3)] }]);
    mount();

    await waitFor(() => expect(screen.getByText('Decision 1')).toBeTruthy());
    expect(screen.getByText('Decision 2')).toBeTruthy();
    expect(screen.getByText('Decision 3')).toBeTruthy();
    expect(screen.queryByText('Load more')).toBeNull();
    expect(screen.queryByText(/this list is partial/i)).toBeNull();
    // …and it is NOT the empty state either — a complete list of three is not "no records yet".
    expect(screen.queryByText(/no records yet/i)).toBeNull();
  });

  it('POSITIVE CONTROL: a genuinely empty list still says "no records yet", not "partial"', async () => {
    stub([{ pages: [] }]);
    mount();
    await waitFor(() => expect(screen.getByText(/no records yet/i)).toBeTruthy());
    expect(screen.queryByText('Load more')).toBeNull();
  });

  it('the FIRST request carries no cursor — the opaque token is the server’s to issue', async () => {
    const urls = stub([{ pages: [rec(1)] }]);
    mount();
    await waitFor(() => expect(screen.getByText('Decision 1')).toBeTruthy());
    expect(urls[0]).toMatch(/\/chronicle$/);
  });
});
