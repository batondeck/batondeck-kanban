/**
 * P-244 review B3 — the server signs every embedded attachment URL afresh on each read
 * (`/blobs/<id>?exp=…&sig=…`, valid 15 minutes from that read), so two reads of a block nobody
 * touched differ in `exp` and `sig`. The two places the SPA compares block text across reads (the
 * STALE conflict panel and the history diff) must not call that block changed, and must still call
 * a real change changed.
 *
 * Asserted on the row highlight a person sees (`is-changed` is the only thing that paints it) and
 * on the texts on screen.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { render, screen, cleanup, waitFor, fireEvent } from '@testing-library/react';
import { ChronicleHistory, ChroniclePageView } from '../screens/Chronicle.js';
import { ProjectProvider } from '../projectContext.js';
import { sameBlockText } from '../chronicleBlocks.js';

const BLOB = 'a'.repeat(64);
const OTHER = 'b'.repeat(64);

/** A download URL as a read signs it: the n-th read gets its own expiry and signature. */
let reads = 0;
const signed = (id = BLOB, host = 'http://127.0.0.1:8080') => {
  reads++;
  return `${host}/blobs/${id}?exp=${1793368000 + reads}&sig=${reads.toString(16).padStart(64, '0')}`;
};

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

type Texts = Record<string, () => string>;

/** A version whose block texts are computed per read, as the server's re-signing does. */
const version = (id: string, texts: Texts) => ({
  id,
  pageId: 'CP-1',
  parentVersionId: null,
  source: 'human' as const,
  author: 'a@b.c',
  agent: null,
  ts: '2026-07-31T00:00:00Z',
  blocks: Object.entries(texts).map(([bid, t]) => ({ id: bid, state: 'authored' as const, text: t(), citations: [], origin: null })),
});

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

function serve(s: { head: string; versions: Record<string, Texts> }) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/api/session')) return json({ identity: 'a@b.c', csrfToken: 't', isAdmin: false });
      if (url.endsWith('/api/projects')) return json({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] });
      if (url.includes('/chronicle/CP-1/edit')) {
        const body = JSON.parse(String(init?.body)) as { expectedParentVersionId: string };
        if (body.expectedParentVersionId !== s.head) return json({ code: 'STALE', message: 'version moved' }, 409);
        return json({ versionId: 'CV-9', drifted: [] });
      }
      const one = /\/versions\/(CV-\d+)$/.exec(url);
      if (one) return json({ version: version(one[1]!, s.versions[one[1]!]!) });
      if (url.includes('/versions'))
        return json({
          versions: Object.keys(s.versions).map((id) => ({
            id, parentVersionId: null, source: 'human', author: 'a@b.c', agent: null, ts: '2026-07-31T00:00:00Z', blockCount: 2,
          })),
        });
      if (url.includes('/chronicle/CP-1'))
        return json({ page: { ...PAGE, headVersionId: s.head }, head: version(s.head, s.versions[s.head]!), drifted: [], conflicts: null });
      return json({});
    }),
  );
}

function mount(entry: string) {
  localStorage.setItem('bd.projectId', 'P-1');
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[entry]}>
        <ProjectProvider>
          <Routes>
            <Route path="/chronicle/:pageId" element={<ChroniclePageView />} />
            <Route path="/chronicle/:pageId/history" element={<ChronicleHistory />} />
          </Routes>
        </ProjectProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

const rowOf = (el: HTMLElement) => el.closest('.chr-diff-row')!;

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('sameBlockText', () => {
  it("masks only a signed blob URL's exp and sig", () => {
    const img = (u: string) => `See ![x](${u}) here.`;
    expect(sameBlockText(img(signed()), img(signed()))).toBe(true);
    expect(sameBlockText(`  ${img(signed())}\n`, img(signed()))).toBe(true);
    // A different blob, host, or word is a change.
    expect(sameBlockText(img(signed()), img(signed(OTHER)))).toBe(false);
    expect(sameBlockText(img(signed()), img(signed(BLOB, 'https://elsewhere.example')))).toBe(false);
    expect(sameBlockText(img(signed()), `See ![y](${signed()}) here.`)).toBe(false);
    // Anything that is not a signed blob URL compares as written.
    expect(sameBlockText('/blobs/x?exp=1&sig=2', '/blobs/x?exp=3&sig=4')).toBe(false);
    expect(sameBlockText(undefined, '')).toBe(true);
  });
});

describe('the STALE conflict panel', () => {
  it('does not mark an image block changed when neither side touched it; marks the edited one', async () => {
    const s = {
      head: 'CV-2',
      versions: {
        'CV-2': { b1: () => `![shot](${signed()})`, b2: () => 'prose as it was' },
        'CV-3': { b1: () => `![shot](${signed()})`, b2: () => 'theirs: a REST edit of b2' },
      } as Record<string, Texts>,
    };
    serve(s);
    mount('/chronicle/CP-1');
    (await waitFor(() => screen.getByText('Edit'))).click();
    await waitFor(() => expect(screen.getByText('Save version')).toBeTruthy());
    fireEvent.change(screen.getByLabelText('Block b2'), { target: { value: 'mine: a UI edit of b2' } });

    s.head = 'CV-3';
    screen.getByText('Save version').click();
    await waitFor(() => expect(screen.getByLabelText('Theirs b1')).toBeTruthy());

    const theirs = screen.getByLabelText('Theirs b1').textContent!;
    const mine = screen.getByLabelText('Mine b1').textContent!;
    // The premise: the two sides' texts really differ, in the signature alone.
    expect(theirs).not.toBe(mine);
    expect(rowOf(screen.getByLabelText('Theirs b1')).classList.contains('is-changed')).toBe(false);
    expect(rowOf(screen.getByLabelText('Theirs b2')).classList.contains('is-changed')).toBe(true);
  });

  it('POSITIVE CONTROL: theirs swapped the image for another blob — that row is changed', async () => {
    const s = {
      head: 'CV-2',
      versions: {
        'CV-2': { b1: () => `![shot](${signed()})`, b2: () => 'prose' },
        'CV-3': { b1: () => `![shot](${signed(OTHER)})`, b2: () => 'prose' },
      } as Record<string, Texts>,
    };
    serve(s);
    mount('/chronicle/CP-1');
    (await waitFor(() => screen.getByText('Edit'))).click();
    await waitFor(() => expect(screen.getByText('Save version')).toBeTruthy());
    fireEvent.change(screen.getByLabelText('Block b2'), { target: { value: 'mine' } });
    s.head = 'CV-3';
    screen.getByText('Save version').click();
    await waitFor(() => expect(screen.getByLabelText('Theirs b1')).toBeTruthy());
    expect(rowOf(screen.getByLabelText('Theirs b1')).classList.contains('is-changed')).toBe(true);
  });
});

describe('the history diff', () => {
  it('does not mark an image block changed between two versions that both hold it; marks the edit', async () => {
    serve({
      head: 'CV-2',
      versions: {
        'CV-2': { b1: () => `![shot](${signed()})`, b2: () => 'the new words' },
        'CV-1': { b1: () => `![shot](${signed()})`, b2: () => 'the old words' },
      },
    });
    mount('/chronicle/CP-1/history?a=CV-1&b=CV-2');
    await waitFor(() => expect(screen.getByText('the old words')).toBeTruthy());
    const [left, right] = [...rowOf(screen.getByText('the old words')).parentElement!.querySelectorAll('.chr-diff-row')]
      .find((r) => r.textContent!.includes('/blobs/'))!
      .querySelectorAll('pre');
    expect(left!.textContent).not.toBe(right!.textContent);
    expect(left!.closest('.chr-diff-row')!.classList.contains('is-changed')).toBe(false);
    expect(rowOf(screen.getByText('the old words')).classList.contains('is-changed')).toBe(true);
  });

  it('POSITIVE CONTROL: a version that swapped the blob is changed in the diff', async () => {
    serve({
      head: 'CV-2',
      versions: {
        'CV-2': { b1: () => `![shot](${signed(OTHER)})`, b2: () => 'same' },
        'CV-1': { b1: () => `![shot](${signed()})`, b2: () => 'same' },
      },
    });
    mount('/chronicle/CP-1/history?a=CV-1&b=CV-2');
    await waitFor(() => expect(screen.getAllByText('same')).toHaveLength(2));
    const row = rowOf(screen.getAllByText(/blobs/)[0]!);
    expect(row.classList.contains('is-changed')).toBe(true);
    expect(rowOf(screen.getAllByText('same')[0]!).classList.contains('is-changed')).toBe(false);
  });
});
