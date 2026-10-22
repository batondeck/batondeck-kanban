/**
 * Chronicle search and information architecture.
 *
 * WHY THE ASSERTIONS ARE AT THIS LAYER (rule 12). The thing under test is not "a matcher returns an
 * array" — it is that pressing ⌘K on the Chronicle screen finds a record, and that a kind chip hides
 * the kinds it excludes. Both are only observable once the screen has published its records into the
 * app's ONE command palette and cmdk has rendered them, so the tests drive the real screen and the
 * real palette through the real shortcut dispatcher. A test of `searchChronicle()` alone would stay
 * green with the publish deleted, which is exactly the green-but-useless shape rule 12 names.
 *
 * cmdk needs two browser APIs jsdom does not ship (`ResizeObserver`, `Element.scrollIntoView`). They
 * are stubbed here rather than in `test-setup.ts` so no other suite silently gains them.
 */
import { describe, it, expect, vi, afterEach, beforeAll } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { render, screen, cleanup, waitFor, fireEvent } from '@testing-library/react';
import { Chronicle } from '../screens/Chronicle.js';
import { CommandPalette } from '../palette.js';
import { ShortcutProvider } from '../shortcuts.js';
import { ProjectProvider } from '../projectContext.js';
import type { ChroniclePage, ChroniclePageKind } from '../types.js';

beforeAll(() => {
  (globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = class {
    observe(): void {}
    unobserve(): void {}
    disconnect(): void {}
  };
  Element.prototype.scrollIntoView = (): void => {};
});

const rec = (
  id: string,
  kind: ChroniclePageKind,
  slug: string,
  title: string,
  createdAt = '2026-07-31T00:00:00Z',
  over: Partial<ChroniclePage> = {},
): ChroniclePage => ({
  id,
  projectId: 'P-1',
  kind,
  slug,
  title,
  headVersionId: `CV-${id}`,
  sourcePath: null,
  createdAt,
  updatedAt: createdAt,
  ...over,
});

/** Three kinds, and deliberately NO shared words: a hit can only come from the field under test. */
const RECORDS = [
  rec('CP-1', 'adr', 'adr/t-162', 'Mint the doorbell', '2026-07-01T00:00:00Z'),
  rec('CP-2', 'topic', 'topic/authz', 'Authorization and identity', '2026-08-01T00:00:00Z'),
  rec('CP-3', 'feature', 'feature/t-115', 'Feature: t-115', '2026-08-02T00:00:00Z'),
];

/**
 * T-514 fixtures: ADR rows that DECLARE topics, plus the topic page they join to. Kept separate from
 * RECORDS so every assertion above still runs against rows with no topics at all — which is what 238
 * already-ingested pages look like, and the reason the fields are optional.
 */
const TOPICED: ChroniclePage[] = [
  rec('CP-2', 'topic', 'topic/authz', 'Authorization and identity'),
  rec('CP-10', 'adr', 'adr/t-500', 'Collapse the auth modes', '2026-07-02T00:00:00Z', {
    topics: ['authz'],
    coverage: 'full',
    updatedBy: { author: 'alice@example.com', agent: null },
  }),
  rec('CP-11', 'adr', 'adr/t-501', 'Resolve accounts by wuid', '2026-07-03T00:00:00Z', {
    topics: ['authz'],
    coverage: 'partial',
    supersededBy: ['ADR-0044'],
    updatedBy: { author: 'svc@example.com', agent: 'chronicle-bot' },
  }),
  // NO record fields at all — the 238-page case. It must render, and must count toward nothing.
  // `topics: []` is the shape the resource actually projects for a page ingested before T-514; the
  // matcher's `?? []` makes absent and empty equivalent, and this row states the real one.
  rec('CP-12', 'adr', 'adr/t-502', 'Mint the doorbell shape', '2026-07-04T00:00:00Z', { topics: [] }),
  // A NON-ADR page that declares a topic. `hosted_index_pages` does not emit topics today, so this
  // row is constructed rather than observed — deliberately, because the ADR restriction in `railsOf`
  // is exactly what stops the rail disagreeing with the page it links to (`topics/<key>.md`'s table
  // is built from ADR records only). Without a row like this the restriction is unprovable, and an
  // unprovable guard is one a later change deletes without noticing.
  rec('CP-13', 'feature', 'feature/t-777', 'Feature: t-777', '2026-07-05T00:00:00Z', {
    topics: ['authz'],
  }),
];

const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200 });

function stub(pages: ChroniclePage[] = RECORDS) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/api/session')) return json({ identity: 'a@b.c', csrfToken: 't', isAdmin: false });
      if (url.includes('/chronicle')) return json({ pages });
      // THE PALETTE ALSO RUNS A SERVER TASK SEARCH, debounced 250ms behind the same input. Every
      // assertion in this file used to land inside that window, so the request never fired and the
      // catch-all `json({})` below was never read as a search response. A test that waits longer —
      // any test asserting an ABSENCE — trips it: `searchQ.data.results` is read unguarded
      // (palette.tsx:224), so `{}` crashes <CommandPalette> and the screen renders empty, which
      // presents as "chronicle search is broken". Answering the real shape keeps the harness honest.
      // The unguarded read itself is a product fragility, filed rather than fixed here (T-513 owns
      // no part of it either) — it needs the response type, not a test.
      if (url.includes('/search')) return json({ results: [] });
      if (url.endsWith('/api/projects')) return json({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] });
      return json({});
    }),
  );
}

/** The real screen, the real palette, the real dispatcher — plus a page route to observe a nav. */
function mount() {
  localStorage.setItem('bd.projectId', 'P-1');
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/chronicle']}>
        <ProjectProvider>
          <ShortcutProvider>
            <CommandPalette />
            <Routes>
              <Route path="/chronicle" element={<Chronicle />} />
              <Route path="/chronicle/:pageId" element={<div>page view CP-2</div>} />
            </Routes>
          </ShortcutProvider>
        </ProjectProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

/** `sentinel` is a row that proves the index has RENDERED before ⌘K is pressed — parametrised
 *  because the T-514 cases mount a different fixture and would otherwise wait for a row that the
 *  screen was never given (a timeout that reads as "search is broken"). */
async function openSearch(query: string, sentinel = 'Mint the doorbell') {
  await waitFor(() => expect(screen.getByText(sentinel)).toBeTruthy());
  fireEvent.keyDown(window, { key: 'k', metaKey: true });
  const input = await screen.findByPlaceholderText(/type a command or search/i);
  fireEvent.change(input, { target: { value: query } });
  return input;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('⌘K finds a Chronicle record', () => {
  it('matches by TITLE, and groups the hit under its kind with the record id', async () => {
    stub();
    mount();
    await openSearch('doorbell');

    await waitFor(() => expect(screen.getByRole('option', { name: /Mint the doorbell/ })).toBeTruthy());
    expect(screen.getByText('Chronicle · adr (1)')).toBeTruthy();
    // The record id is shown: two records can share a title, and the id is what addresses the page.
    expect(screen.getByRole('option', { name: /CP-1/ })).toBeTruthy();
    // NEGATIVE: the other two records match nothing in this query and must not be listed.
    expect(screen.queryByRole('option', { name: /Authorization and identity/ })).toBeNull();
  });

  it('matches by SLUG, which no rendered title contains', async () => {
    stub();
    mount();
    // `t-162` appears in the slug only — the title is "Mint the doorbell". A search that read titles
    // alone, or a cmdk pass filtering on the visible label, would both find nothing here.
    await openSearch('t-162');

    await waitFor(() => expect(screen.getByRole('option', { name: /Mint the doorbell/ })).toBeTruthy());
    expect(screen.queryByRole('option', { name: /Feature: t-115/ })).toBeNull();
  });

  // A topic PAGE is found BY NAME, and the name is its slug's own segment (`topic/authz`).
  // THE SECOND SENTENCE HERE USED TO SAY there is no separate topic axis "and there cannot be one
  // client-side, because an ADR row does not say which topics its record carries". T-514 put `topics`
  // on the row, so that is now false and the axis exists — see the ADR-by-topic case below. This case
  // still earns its place: it pins the GROUPING, which is how a reader tells the topic page itself
  // from the records that merely declare it.
  it('matches a TOPIC by name and groups it under `topic`', async () => {
    stub();
    mount();
    await openSearch('authz');

    await waitFor(() => expect(screen.getByRole('option', { name: /Authorization and identity/ })).toBeTruthy());
    expect(screen.getByText('Chronicle · topic (1)')).toBeTruthy();
    expect(screen.queryByRole('option', { name: /Mint the doorbell/ })).toBeNull();
  });

  /**
   * T-514 — searching by TOPIC, which is the axis this suite could not test before.
   *
   * `topic` used to mean only "the facet of a topic PAGE" (a slice of its own slug), so a query could
   * never reach an ADR through its topics — the join did not exist on the row. It does now, and this
   * drives the REAL palette so it also covers the trap the module documents: cmdk scores every row a
   * second time on `searchValue`, so a topic-only hit that the matcher selected is silently dropped
   * unless both haystacks carry topics. A unit test of `matchesChronicle` would miss exactly that.
   */
  it('matches an ADR by a TOPIC it declares, which its title and slug do not contain', async () => {
    stub(TOPICED);
    mount();
    await openSearch('authz', 'Collapse the auth modes');
    // SCOPED TO THE PALETTE'S OPTIONS, never `getByText`: the index list is still mounted behind the
    // overlay, so a bare text query finds every row on the screen and would pass with search deleted.
    await waitFor(() =>
      expect(screen.getByRole('option', { name: /Collapse the auth modes/ })).toBeTruthy(),
    );
    // Both records that DECLARE authz — neither says "authz" in its title or its slug.
    expect(screen.getByRole('option', { name: /Resolve accounts by wuid/ })).toBeTruthy();
    // The topic PAGE still matches too, by its own slug — the new axis adds, it does not replace.
    expect(screen.getByRole('option', { name: /Authorization and identity/ })).toBeTruthy();
    // NEGATIVE within the positive: the ADR declaring no topics is NOT dragged in.
    expect(screen.queryByRole('option', { name: /Mint the doorbell shape/ })).toBeNull();
  });

  /**
   * THE cmdk SECOND PASS, which is the trap `chronicleSearch.ts` documents and which the other cases
   * cannot reach.
   *
   * `<Command shouldFilter={!searchQ.data}>` (palette.tsx:154) turns cmdk's own scoring OFF as soon as
   * the server task-search has answered. Every case above stubs that endpoint, so the second pass is
   * disabled and a `searchValue` that had drifted from the matcher would still pass — proven by
   * mutation: dropping topics from `searchValue` alone changed nothing. Here the search FAILS, so
   * `searchQ.data` stays undefined, `shouldFilter` is true, and cmdk re-scores every row on
   * `searchValue`. A topic-only hit then survives only if BOTH haystacks carry topics.
   *
   * This is also the realistic first moment of any search: 250ms of debounce before the server has
   * said anything, with cmdk filtering on its own.
   */
  it('a topic-only hit survives cmdk re-scoring when the server search has not answered', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('/api/session'))
          return json({ identity: 'a@b.c', csrfToken: 't', isAdmin: false });
        if (url.includes('/chronicle')) return json({ pages: TOPICED });
        if (url.endsWith('/api/projects'))
          return json({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] });
        // The task search never answers -> searchQ.data stays undefined -> cmdk filters for itself.
        if (url.includes('/search')) return new Response('boom', { status: 500 });
        return json({});
      }),
    );
    mount();
    await openSearch('authz', 'Collapse the auth modes');
    // `authz` is in this record's TOPICS and in neither its title nor its slug, so cmdk's own pass is
    // the only thing that can veto it.
    await waitFor(() =>
      expect(screen.getByRole('option', { name: /Collapse the auth modes/ })).toBeTruthy(),
    );
    expect(screen.getByRole('option', { name: /Resolve accounts by wuid/ })).toBeTruthy();
  });

  it('NEGATIVE: a topic nothing declares returns nothing', async () => {
    stub(TOPICED);
    mount();
    await openSearch('metering', 'Collapse the auth modes');
    // POSITIVE CONTROL that the palette is actually open and filtering, so the three nulls below are
    // "no hit" rather than "no palette" — an empty dialog would satisfy them either way.
    await waitFor(() => expect(screen.getByPlaceholderText(/type a command or search/i)).toBeTruthy());
    expect(screen.queryByRole('option', { name: /Collapse the auth modes/ })).toBeNull();
    expect(screen.queryByRole('option', { name: /Resolve accounts by wuid/ })).toBeNull();
    expect(screen.queryByRole('option', { name: /Authorization and identity/ })).toBeNull();
  });

  it('NEGATIVE: a query matching nothing lists no record at all', async () => {
    stub();
    mount();
    await openSearch('zzzznotarecord');

    await waitFor(() => expect(screen.getByText('No results.')).toBeTruthy());
    for (const t of ['Mint the doorbell', 'Authorization and identity', 'Feature: t-115'])
      expect(screen.queryByRole('option', { name: new RegExp(t) })).toBeNull();
  });

  it('Escape closes it', async () => {
    stub();
    mount();
    const input = await openSearch('doorbell');
    await waitFor(() => expect(screen.getByRole('option', { name: /Mint the doorbell/ })).toBeTruthy());

    fireEvent.keyDown(input, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByPlaceholderText(/type a command or search/i)).toBeNull());
  });
});

describe('the index has navigation, not one wall of rows', () => {
  it('a TOPIC rail entry navigates to that topic page', async () => {
    stub();
    mount();
    await waitFor(() => expect(screen.getByText('Topics')).toBeTruthy());

    // The rail is keyed by the facet, and it is a LINK to the page — not a filter of this list.
    // UPDATED BY T-514, and the product changed rather than the test drifting: a topic rail now
    // carries the count of ADR records declaring it, so the chip's accessible name is `authz <n>`
    // and an exact-string match no longer finds it. Anchored rather than loosened to a substring, so
    // it still could not match a differently-keyed rail.
    fireEvent.click(screen.getByRole('link', { name: /^authz\b/ }));  // "authz, N records" (T-514)
    await waitFor(() => expect(screen.getByText('page view CP-2')).toBeTruthy());
  });

  /**
   * T-514 — rails count, and the count is SUPPRESSED while the index is partial.
   *
   * The suppression is the load-bearing half. The index pages at 100 against 238 records, so a count
   * over the loaded rows renders `authz (13)` on first paint and climbs as the reader pages — a wrong
   * number a reader believes, which is why T-512 shipped rails bare rather than guessing.
   */
  it('a topic rail counts the ADR records that declare it, once the index is complete', async () => {
    stub(TOPICED);
    mount();
    await waitFor(() => expect(screen.getByText('Collapse the auth modes')).toBeTruthy());
    // Two of the three ADRs declare `authz`; the third declares nothing and must not be counted.
    expect(screen.getByRole('link', { name: 'authz, 2 records' })).toBeTruthy();
  });

  it('NEGATIVE: while the index is PARTIAL the rail carries no count at all', async () => {
    // Same rows, but the response says more exist — so no honest count is available yet.
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('/api/session'))
          return json({ identity: 'a@b.c', csrfToken: 't', isAdmin: false });
        if (url.includes('/chronicle')) return json({ pages: TOPICED, nextCursor: 'adr/t-502' });
        if (url.endsWith('/api/projects'))
          return json({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] });
        return json({});
      }),
    );
    mount();
    await waitFor(() => expect(screen.getByText('Collapse the auth modes')).toBeTruthy());
    // The rail is still there and still navigates — it just does not claim a number.
    expect(screen.getByRole('link', { name: 'authz' })).toBeTruthy();
    expect(screen.queryByRole('link', { name: /authz, \d+ records/ })).toBeNull();
  });

  /**
   * THE 238-RECORD CASE, and it is today's production corpus rather than an edge.
   *
   * `complete` is satisfied the moment the list is not paginated — it says nothing about whether any
   * row carries `topics`. Every page ingested before T-514 normalises to `topics: []`, so between
   * deploying this and a re-sweep that re-ingests topics onto every ADR page, the join is empty on a
   * COMPLETE index. A `?? 0` there renders `authz (0)` and announces "authz, 0 records" — a rail
   * asserting a number that is false, which is the exact failure the suppression exists to prevent.
   */
  it('a complete index where NO record declares a topic renders the rail bare, not `(0)`', async () => {
    // RECORDS: one topic page, one ADR with no topics, and no `nextCursor` — so `complete` is true
    // and the count is nonetheless underivable.
    stub();
    mount();
    await waitFor(() => expect(screen.getByText('Mint the doorbell')).toBeTruthy());
    expect(screen.getByRole('link', { name: 'authz' })).toBeTruthy();
    expect(screen.queryByRole('link', { name: /authz, \d+ records?/ })).toBeNull();
  });

  /**
   * THE KIND RESTRICTION IN `railsOf`, which nothing could observe before.
   *
   * `TOPICED`'s feature page is `feature/t-777`, so its facet key matches no declared topic and
   * `counts.get('t-777')` is undefined either way — deleting `p.kind === 'topic'` changed nothing and
   * the guard was decoration. A feature page whose key COLLIDES with a declared topic is the only
   * shape that separates them: `topic/authz` must count 2, `feature/authz` must stay bare, because a
   * record's feature is the `feature:` frontmatter field and is not on the row at all. Counting it
   * from `topics` would be a different number wearing the right label.
   */
  it('a FEATURE rail whose key collides with a topic stays bare while the topic rail counts', async () => {
    stub([...TOPICED, rec('CP-14', 'feature', 'feature/authz', 'Feature: authz', '2026-07-06T00:00:00Z')]);
    mount();
    await waitFor(() => expect(screen.getByText('Collapse the auth modes')).toBeTruthy());
    // The topic rail counts the two ADRs declaring `authz`...
    expect(screen.getByRole('link', { name: 'authz, 2 records' })).toBeTruthy();
    // ...and the identically-keyed feature rail claims no number at all.
    expect(screen.getByRole('link', { name: 'authz' })).toBeTruthy();
    // Exactly one of the two carries a count — not both, and not the wrong one.
    expect(screen.getAllByRole('link', { name: /^authz(,|$)/ })).toHaveLength(2);
  });

  it('a row shows its coverage, its supersession and who landed the head version', async () => {
    stub(TOPICED);
    mount();
    await waitFor(() => expect(screen.getByText('Collapse the auth modes')).toBeTruthy());
    expect(screen.getByText('full')).toBeTruthy();
    expect(screen.getByText('partial')).toBeTruthy();
    // Names WHICH record replaced it — "superseded" with no successor is a dead end.
    expect(screen.getByText('superseded by ADR-0044')).toBeTruthy();
    // The byline resolves an agent by display name and a human by the email's name part.
    expect(screen.getByText(/· by alice/)).toBeTruthy();
    expect(screen.getByText(/· by chronicle-bot/)).toBeTruthy();
  });

  it('NEGATIVE: a row with NO record fields renders, and wears no badge it did not earn', async () => {
    // This is the shape of all 238 pages ingested before T-514, so it is the common case, not an edge.
    stub([rec('CP-12', 'adr', 'adr/t-502', 'Mint the doorbell shape')]);
    mount();
    await waitFor(() => expect(screen.getByText('Mint the doorbell shape')).toBeTruthy());
    expect(screen.queryByText('full')).toBeNull();
    expect(screen.queryByText('partial')).toBeNull();
    expect(screen.queryByText(/superseded/)).toBeNull();
    // Anchored on the row's own separator: a bare / by / also matches the screen SUBTITLE
    // ("written by hand"), which would have made this assertion pass for the wrong reason.
    expect(screen.queryByText(/· by /)).toBeNull();
  });

  it('the kind filter keeps the selected kind and REMOVES the others', async () => {
    stub();
    mount();
    await waitFor(() => expect(screen.getByText('Mint the doorbell')).toBeTruthy());
    // Positive control: all three kinds are on screen before the filter is touched.
    expect(screen.getByText('Authorization and identity')).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: 'topic' }));

    await waitFor(() => expect(screen.queryByText('Mint the doorbell')).toBeNull());
    expect(screen.getByText('Authorization and identity')).toBeTruthy();
    expect(screen.queryByText('Feature: t-115')).toBeNull();
  });

  it('under a kind filter the partial-list count names BOTH numbers', async () => {
    // The filter hides rows the loaded count still includes. "Showing 3 records" over one visible row
    // is the same false reading the unfiltered wording exists to prevent, so it names both.
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.includes('/api/session')) return json({ identity: 'a@b.c', csrfToken: 't', isAdmin: false });
        if (url.includes('/chronicle')) return json({ pages: RECORDS, nextCursor: '1' });
        if (url.endsWith('/api/projects')) return json({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] });
        return json({});
      }),
    );
    mount();
    await waitFor(() => expect(screen.getByText('Mint the doorbell')).toBeTruthy());
    // Positive control: unfiltered, the wording is the one the paging suite pins, verbatim.
    expect(screen.getByRole('status').textContent).toMatch(/showing 3 records — this list is partial/i);

    fireEvent.click(screen.getByRole('button', { name: 'topic' }));

    await waitFor(() =>
      expect(screen.getByRole('status').textContent).toMatch(
        /showing 1 topic record of 3 loaded — this list is partial/i,
      ),
    );
  });

  it('sorting by date actually reverses the order', async () => {
    // Two ADRs so the ordering is observable WITHIN one kind group — reordering across groups would
    // prove nothing, since the groups render in their own order regardless.
    stub([
      rec('CP-4', 'adr', 'adr/t-1', 'Older decision', '2026-07-01T00:00:00Z'),
      rec('CP-5', 'adr', 'adr/t-2', 'Newer decision', '2026-08-05T00:00:00Z'),
    ]);
    mount();

    const order = (): string[] =>
      [...document.querySelectorAll('.line-row-label')].map((n) => n.textContent?.trim() ?? '');
    await waitFor(() => expect(order()).toEqual(['Newer decision', 'Older decision']));

    // The direction toggle is its OWN button now — the /Added/ button switches which timestamp is
    // sorted on, so clicking it here would change the field and prove nothing about direction.
    fireEvent.click(screen.getByRole('button', { name: /newest first/ }));
    await waitFor(() => expect(order()).toEqual(['Older decision', 'Newer decision']));
  });

  it('sorts by UPDATED, which is a different order from added', async () => {
    // The record added first was updated last. If the field toggle did nothing, both orders would
    // match and this test would pass vacuously — so the two orders are asserted to DIFFER.
    stub([
      rec('CP-6', 'adr', 'adr/t-1', 'Added first', '2026-07-01T00:00:00Z', {
        updatedAt: '2026-08-30T00:00:00Z',
      }),
      rec('CP-7', 'adr', 'adr/t-2', 'Added second', '2026-08-05T00:00:00Z', {
        updatedAt: '2026-08-06T00:00:00Z',
      }),
    ]);
    mount();

    const order = (): string[] =>
      [...document.querySelectorAll('.line-row-label')].map((n) => n.textContent?.trim() ?? '');
    await waitFor(() => expect(order()).toEqual(['Added second', 'Added first']));

    fireEvent.click(screen.getByRole('button', { name: /^Added$/ }));
    await waitFor(() => expect(order()).toEqual(['Added first', 'Added second']));
    expect(screen.getByRole('button', { name: /^Updated$/ })).toBeTruthy();
  });
});
