/**
 * T-295 (CH-16) — the two Chronicle history gaps, and the invariants that guard them.
 *
 * 1. (14, applied to history) A SILENTLY TRUNCATED HISTORY IS A FALSE ABSENCE CLAIM — "there is no
 *    earlier version", believed by a reader who was never told the list stopped at a page boundary.
 *    Same contract as the index (chronicle-paging.test.tsx), same visible indicator:
 *
 *        GET /api/projects/:projectId/chronicle/:pageId/versions?cursor=<opaque>
 *          -> { versions: ChronicleVersionMeta[], nextCursor?: string }
 *
 * 2. CHS §7 promises a diff between ANY two versions. The UI offered only "Compare with latest"
 *    (b hardcoded to the head); the two pickers make an arbitrary pair reachable without hand-editing
 *    the `?a=&b=` search params.
 *
 * 3. The (13) invariant, translated to this screen: the state a person BUILT here is the open diff,
 *    and a pagination fetch must not evict it. The literal (13) subject — ChroniclePageView's open
 *    editor — cannot be constructed against fetchNextPage at all: the editor and the history list
 *    live on different routes (`/chronicle/:pageId` vs `/chronicle/:pageId/history`), are never
 *    mounted together, and share no query key, so the closest reachable hazard is tested instead.
 *
 * Asserted on visible words and on the URLs that reach the wire, never on props.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { render, screen, cleanup, waitFor, fireEvent } from '@testing-library/react';
import { ChronicleHistory } from '../screens/Chronicle.js';
import { ProjectProvider } from '../projectContext.js';

/** Version METADATA, as the history list serves it — distinct authors so rows are tellable apart. */
const meta = (n: number) => ({
  id: `CV-${n}`,
  parentVersionId: null,
  source: 'human' as const,
  author: `u${n}@x`,
  agent: null,
  ts: '2026-07-31T00:00:00Z',
  blockCount: 1,
});

/** A full version BODY, as the per-version diff read serves it. */
const body = (id: string) => ({
  id,
  pageId: 'CP-1',
  parentVersionId: null,
  source: 'human' as const,
  author: 'a@b.c',
  agent: null,
  ts: '2026-07-31T00:00:00Z',
  blocks: [{ id: 'b1', state: 'derived' as const, text: `text of ${id}`, citations: [], origin: null }],
});

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200 });

interface Server {
  /** Every versions-LIST url that reached the wire, in order. */
  listUrls: string[];
  /** Every single-version (diff) url that reached the wire, in order. */
  versionUrls: string[];
  /** Parks the next versions-list fetch until `release()` — how an IN-FLIGHT fetchNextPage is observed. */
  hold: boolean;
  release: (() => void) | null;
}

/** Serves `pages[cursor]`; every entry but the last hands back the next cursor. */
function stub(pages: Array<{ versions: unknown[]; nextCursor?: string }>): Server {
  const s: Server = { listUrls: [], versionUrls: [], hold: false, release: null };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/api/session')) return json({ identity: 'a@b.c', csrfToken: 't', isAdmin: false });
      // The single-version read carries an id segment, so it must be matched BEFORE the list.
      const one = /\/versions\/(CV-\d+)$/.exec(url);
      if (one) {
        s.versionUrls.push(url);
        return json({ version: body(one[1]!) });
      }
      if (url.includes('/versions')) {
        s.listUrls.push(url);
        if (s.hold) await new Promise<void>((r) => { s.release = r; });
        const cursor = new URL(url, 'http://x').searchParams.get('cursor');
        return json(pages[cursor ? Number(cursor) : 0]);
      }
      if (url.endsWith('/api/projects')) return json({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] });
      return json({});
    }),
  );
  return s;
}

function mount(entry = '/chronicle/CP-1/history') {
  localStorage.setItem('bd.projectId', 'P-1');
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[entry]}>
        <ProjectProvider>
          <Routes>
            <Route path="/chronicle/:pageId/history" element={<ChronicleHistory />} />
          </Routes>
        </ProjectProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return qc;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('(14, applied to history) the version list never truncates in silence', () => {
  it('says the list is PARTIAL, and fetches the rest with the cursor it was given', async () => {
    const s = stub([
      { versions: [meta(3), meta(2)], nextCursor: '1' },
      { versions: [meta(1)] },
    ]);
    mount();

    await waitFor(() => expect(screen.getByLabelText('human u3')).toBeTruthy());
    // The truncation stated in words — a reader who never scrolls to the button is still told.
    expect(screen.getByRole('status').textContent).toMatch(/showing 2 versions — this list is partial, more exist/i);

    screen.getByText('Load more').click();

    await waitFor(() => expect(screen.getByLabelText('human u1')).toBeTruthy());
    // The earlier page is still on screen: "more" appends, it does not replace.
    expect(screen.getByLabelText('human u3')).toBeTruthy();
    // The cursor is opaque and must be sent back verbatim on the documented parameter.
    expect(s.listUrls[1]).toContain('/versions?cursor=1');
    // Complete now — so the partial claim is withdrawn rather than left standing.
    expect(screen.queryByText('Load more')).toBeNull();
    expect(screen.queryByText(/this list is partial/i)).toBeNull();
  });

  it('POSITIVE CONTROL: a COMPLETE non-empty history claims nothing — no "more", every version shown', async () => {
    stub([{ versions: [meta(3), meta(2), meta(1)] }]);
    mount();

    await waitFor(() => expect(screen.getByLabelText('human u3')).toBeTruthy());
    expect(screen.getByLabelText('human u2')).toBeTruthy();
    expect(screen.getByLabelText('human u1')).toBeTruthy();
    expect(screen.queryByText('Load more')).toBeNull();
    expect(screen.queryByText(/this list is partial/i)).toBeNull();
    // …and it is NOT the empty state either — a complete history of three is not "no versions yet".
    expect(screen.queryByText(/no versions yet/i)).toBeNull();
  });

  it('POSITIVE CONTROL: a genuinely empty history still says "no versions yet", not "partial"', async () => {
    stub([{ versions: [] }]);
    mount();
    await waitFor(() => expect(screen.getByText(/no versions yet/i)).toBeTruthy());
    expect(screen.queryByText('Load more')).toBeNull();
  });

  it('the FIRST request carries no cursor — the opaque token is the server’s to issue', async () => {
    const s = stub([{ versions: [meta(1)] }]);
    mount();
    await waitFor(() => expect(screen.getByLabelText('human u1')).toBeTruthy());
    expect(s.listUrls[0]).toMatch(/\/versions$/);
  });
});

describe('any two versions are comparable — the second picker (CHS §7)', () => {
  it('picking From and To loads EXACTLY those two versions and shows both texts', async () => {
    const s = stub([{ versions: [meta(3), meta(2), meta(1)] }]);
    mount();
    await waitFor(() => expect(screen.getByLabelText('human u1')).toBeTruthy());

    // Neither side involves the head: the pair "Compare with latest" could never reach.
    fireEvent.change(screen.getByLabelText('From version'), { target: { value: 'CV-1' } });
    fireEvent.change(screen.getByLabelText('To version'), { target: { value: 'CV-2' } });

    await waitFor(() => expect(screen.getByText('text of CV-1')).toBeTruthy());
    expect(screen.getByText('text of CV-2')).toBeTruthy();
    // The wire carried the two picked versions and nothing else — a diff is exactly two reads.
    expect(s.versionUrls.filter((u) => u.includes('/versions/CV-1'))).toHaveLength(1);
    expect(s.versionUrls.filter((u) => u.includes('/versions/CV-2'))).toHaveLength(1);
    expect(s.versionUrls).toHaveLength(2);
    // REFUSAL: the head's body was never fetched — CV-3 is not part of this pair.
    expect(s.versionUrls.some((u) => u.includes('/versions/CV-3'))).toBe(false);
  });

  it('POSITIVE CONTROL: "Compare with latest" still works, and the pickers agree with it', async () => {
    stub([{ versions: [meta(3), meta(2), meta(1)] }]);
    mount();
    await waitFor(() => expect(screen.getByLabelText('human u1')).toBeTruthy());

    // The last row is the OLDEST version (newest first) — its shortcut diffs CV-1 against the head.
    screen.getAllByText('Compare with latest')[2]!.click();

    await waitFor(() => expect(screen.getByText('text of CV-1')).toBeTruthy());
    expect(screen.getByText('text of CV-3')).toBeTruthy();
    // Both paths write the same URL state, so the pickers now SHOW the pair the shortcut chose.
    expect((screen.getByLabelText('From version') as HTMLSelectElement).value).toBe('CV-1');
    expect((screen.getByLabelText('To version') as HTMLSelectElement).value).toBe('CV-3');
  });

  it('a hand-written ?a=&b= URL still renders the diff — the params remain the contract', async () => {
    stub([{ versions: [meta(3), meta(2), meta(1)] }]);
    mount('/chronicle/CP-1/history?a=CV-2&b=CV-1');
    await waitFor(() => expect(screen.getByText('text of CV-2')).toBeTruthy());
    expect(screen.getByText('text of CV-1')).toBeTruthy();
  });
});

describe('(13, translated) a pagination fetch must not evict the state a person built here', () => {
  it('REFUSAL: an IN-FLIGHT fetchNextPage leaves the open diff and the list standing', async () => {
    const s = stub([
      { versions: [meta(3), meta(2)], nextCursor: '1' },
      { versions: [meta(1)] },
    ]);
    mount();
    await waitFor(() => expect(screen.getByLabelText('human u3')).toBeTruthy());

    // A person builds state: an open diff between the two loaded versions.
    fireEvent.change(screen.getByLabelText('From version'), { target: { value: 'CV-2' } });
    fireEvent.change(screen.getByLabelText('To version'), { target: { value: 'CV-3' } });
    await waitFor(() => expect(screen.getByText('text of CV-2')).toBeTruthy());

    // Park the next-page fetch so it is observably in flight — waiting for it to FINISH would prove
    // nothing, because the eviction (a spinner, a blanked list) only exists during the fetch.
    s.hold = true;
    screen.getByText('Load more').click();
    await waitFor(() => expect(s.listUrls.some((u) => u.includes('cursor=1'))).toBe(true));

    // The diff is still on screen, the pickers still hold the pair, the list did not blank.
    expect(screen.getByText('text of CV-2')).toBeTruthy();
    expect(screen.getByText('text of CV-3')).toBeTruthy();
    expect((screen.getByLabelText('From version') as HTMLSelectElement).value).toBe('CV-2');
    expect(screen.getByLabelText('human u3')).toBeTruthy();

    s.release?.();
    // …and when the page lands, the older version APPENDS under the still-open diff.
    await waitFor(() => expect(screen.getByLabelText('human u1')).toBeTruthy());
    expect(screen.getByText('text of CV-2')).toBeTruthy();
  });

  it('POSITIVE CONTROL: the loaded page can join the diff — an OLDER version is pickable once fetched', async () => {
    stub([
      { versions: [meta(3), meta(2)], nextCursor: '1' },
      { versions: [meta(1)] },
    ]);
    mount();
    await waitFor(() => expect(screen.getByLabelText('human u3')).toBeTruthy());

    // CV-1 is beyond the first page: before "Load more" the picker cannot offer it…
    expect(screen.queryByText(/CV-1 · edited/)).toBeNull();
    screen.getByText('Load more').click();
    await waitFor(() => expect(screen.getByLabelText('human u1')).toBeTruthy());

    // …after it, the 51st-version case is diffable like any other.
    fireEvent.change(screen.getByLabelText('From version'), { target: { value: 'CV-1' } });
    fireEvent.change(screen.getByLabelText('To version'), { target: { value: 'CV-3' } });
    await waitFor(() => expect(screen.getByText('text of CV-1')).toBeTruthy());
    expect(screen.getByText('text of CV-3')).toBeTruthy();
  });
});
