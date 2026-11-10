/**
 * R2.7 — the four chronicle screens over the BFF's JSON (internal/api/chronicle.go).
 *
 * The fixtures are the Go server's answers, key for key: captured from `batondeck-kanban serve` on
 * 2026-10-24 (GET /api/projects/:p/chronicle, …/:pageId, …/versions, …/versions/:v) with the ids and
 * timestamps shortened. Those carry keys the older chronicle tests' fixtures never had
 * (lastDerivedObject, conflict, topics, coverage, supersededBy, updatedBy, blocksObject, blockCount),
 * so a screen that broke on the real shape fails here.
 *
 * MERMAID IS THE REAL LIBRARY. jsdom has no layout, so `getBBox` and `getComputedTextLength` are
 * given fixed sizes below; everything else — the parse, the layout, the SVG mermaid writes — is
 * mermaid's own, and the view must put that `<svg>` in the DOM.
 */
import { describe, it, expect, vi, afterEach, beforeAll } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { render, screen, cleanup, waitFor, fireEvent } from '@testing-library/react';
import { Chronicle, ChronicleHistory, ChronicleNew, ChroniclePageView } from '../screens/Chronicle.js';
import { ProjectProvider } from '../projectContext.js';

beforeAll(() => {
  const svg = window.SVGElement.prototype as unknown as Record<string, unknown>;
  svg.getBBox = () => ({ x: 0, y: 0, width: 80, height: 16 });
  svg.getComputedTextLength = () => 80;
});

const PID = 'P-40472f400dfd';
const SIGNED = '/blobs/7d23964a17487d76fd9ccd9ceefab62ec6a2b4c6825e09c84192b064b8fc8b4f?exp=1792825519&sig=c2lnbmF0dXJl';

const page = (id: string, over: Record<string, unknown> = {}) => ({
  id,
  projectId: PID,
  kind: 'adr',
  slug: 'adr/r2-7-observed',
  title: 'R2.7 observed: routes and screens',
  headVersionId: 'CV-e8bdeeed55a3',
  sourcePath: 'docs/adr/r2-7.md',
  lastDerivedObject: `${id}/56caecc6d77de2090d9a813a5152918cb3cb1ef6414dacfba21e5ca4faa7e6dd`,
  conflict: null,
  topics: ['chronicle'],
  coverage: 'full',
  supersededBy: [],
  updatedBy: { author: 'U-f7cf5e7fefc3c03e', agent: null },
  createdAt: '2026-10-24T06:50:19.083Z',
  updatedAt: '2026-10-24T06:50:19.088Z',
  ...over,
});

const INDEX = {
  pages: [
    page('CP-46a5d90303fe'),
    page('CP-c9374f93302e', {
      kind: 'topic', slug: 'notes/written-in-the-browser', title: 'Written in the browser', sourcePath: null,
      topics: [], coverage: null, headVersionId: 'CV-7128e2a80c36',
    }),
  ],
};

const block = (id: string, text: string) => ({ id, state: 'authored', text, citations: [], origin: null });

const HEAD = {
  id: 'CV-e8bdeeed55a3',
  pageId: 'CP-46a5d90303fe',
  parentVersionId: 'CV-7d3b9ae8453f',
  source: 'human',
  author: 'U-f7cf5e7fefc3c03e',
  agent: null,
  ts: '2026-10-24T06:50:19.088Z',
  blocks: [
    block('ctx1/1', '## Context\n\nAn agent wrote this record through ingest_chronicle_page, then edited it.'),
    block('ctx1/2', '```mermaid\ngraph TD; Agent-->Store; Store-->Screen;\n```'),
    block('ctx1/3', `The attached image, through its signed URL:\n\n![red square](${SIGNED})`),
  ],
  blocksObject: 'CP-46a5d90303fe/3478b0b60d944081565c72a5ed1983bcd07d7df8c592fdf69c9aa3f099a5897d',
  blockCount: 3,
};

const VIEW = { page: page('CP-46a5d90303fe'), head: HEAD, drifted: [], conflicts: null };

const HISTORY = {
  versions: [
    { id: 'CV-e8bdeeed55a3', parentVersionId: 'CV-7d3b9ae8453f', source: 'human', author: 'U-f7cf5e7fefc3c03e', agent: null, ts: '2026-10-24T06:50:19.088Z', blockCount: 3 },
    { id: 'CV-7d3b9ae8453f', parentVersionId: null, source: 'sweep', author: 'U-f7cf5e7fefc3c03e', agent: null, ts: '2026-10-24T06:50:19.083Z', blockCount: 3 },
  ],
};

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

interface Wire {
  calls: Array<{ method: string; url: string; body: unknown }>;
}

/** The BFF's answers, by route; every request is recorded as it reached the wire. */
function serve(): Wire {
  const w: Wire = { calls: [] };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      w.calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined });
      const pc = `/api/projects/${PID}/chronicle`;
      if (url.includes('/api/session')) return json({ identity: 'U-f7cf5e7fefc3c03e', csrfToken: 't', isAdmin: false });
      if (url.endsWith('/api/projects')) return json({ projects: [{ projectId: PID, name: 'Chronicle observation', role: 'admin' }] });
      if (url === pc && method === 'POST') return json({ pageId: 'CP-c9374f93302e', versionId: 'CV-7128e2a80c36' });
      if (url === pc || url.startsWith(`${pc}?`)) return json(INDEX);
      if (url.endsWith('/revert')) return json({ versionId: 'CV-29131560f9c8' });
      if (url.includes('/versions/')) return json({ version: HEAD });
      if (url.includes('/versions')) return json(HISTORY);
      if (url.includes(`${pc}/CP-c9374f93302e`))
        return json({
          ...VIEW,
          page: INDEX.pages[1],
          head: { ...HEAD, id: 'CV-7128e2a80c36', pageId: 'CP-c9374f93302e', parentVersionId: null, blocks: [block('a-193e57fa6015', 'A person wrote this.')], blockCount: 1 },
        });
      if (url.includes(`${pc}/CP-46a5d90303fe`)) return json(VIEW);
      return json({});
    }),
  );
  return w;
}

function mount(at: string) {
  localStorage.setItem('bd.projectId', PID);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[at]}>
        <ProjectProvider>
          <Routes>
            <Route path="/chronicle" element={<Chronicle />} />
            <Route path="/chronicle/new" element={<ChronicleNew />} />
            <Route path="/chronicle/:pageId" element={<ChroniclePageView />} />
            <Route path="/chronicle/:pageId/history" element={<ChronicleHistory />} />
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

describe('/chronicle — the index', () => {
  it('lists every page the BFF answers, each linking to its view, under its kind', async () => {
    const w = serve();
    mount('/chronicle');
    await waitFor(() => expect(screen.getByText('R2.7 observed: routes and screens')).toBeTruthy());
    expect(screen.getByText('Written in the browser')).toBeTruthy();
    const links = [...document.querySelectorAll('a.line-row')].map((a) => a.getAttribute('href'));
    expect(links).toEqual(expect.arrayContaining([`/chronicle/CP-46a5d90303fe?project=${PID}`, `/chronicle/CP-c9374f93302e?project=${PID}`]));
    // The coverage tag is read off the real `coverage` key; the null one renders nothing.
    expect(screen.getByText('full')).toBeTruthy();
    expect(screen.getByText('New record').getAttribute('href')).toBe('/chronicle/new');
    expect(w.calls.some((c) => c.method === 'GET' && c.url === `/api/projects/${PID}/chronicle`)).toBe(true);
  });
});

describe('/chronicle/new — writing a record', () => {
  it('POSTs kind, slug, title, the typed block and an idempotency key, then opens the new page', async () => {
    const w = serve();
    mount('/chronicle/new');
    await waitFor(() => expect(screen.getByLabelText('Title')).toBeTruthy());
    fireEvent.change(screen.getByLabelText('Title'), { target: { value: 'Written in the browser' } });
    fireEvent.change(screen.getByLabelText(/^Slug/), { target: { value: 'notes/written-in-the-browser' } });
    fireEvent.change(screen.getByLabelText(/^Block /), { target: { value: 'A person wrote this.' } });
    const create = () => screen.getByText('Create record').closest('button') as HTMLButtonElement;
    await waitFor(() => expect(create().disabled).toBe(false));
    create().click();
    await waitFor(() => expect(screen.getByText('A person wrote this.')).toBeTruthy());
    const post = w.calls.find((c) => c.method === 'POST' && c.url === `/api/projects/${PID}/chronicle`);
    expect(post?.body).toMatchObject({ kind: 'topic', slug: 'notes/written-in-the-browser', title: 'Written in the browser' });
    const body = post!.body as { blocks: Array<{ id: string; text: string }>; idempotencyKey: string };
    expect(body.blocks).toHaveLength(1);
    expect(body.blocks[0]!.id).toMatch(/^a-[0-9a-f]{12}$/);
    expect(body.blocks[0]!.text).toBe('A person wrote this.');
    expect(body.idempotencyKey).toBeTruthy();
    // The create carries exactly the keys create_chronicle_page's schema admits (call() refuses others).
    expect(Object.keys(body).sort()).toEqual(['blocks', 'idempotencyKey', 'kind', 'slug', 'title']);
  });
});

describe('/chronicle/:pageId — reading a record', () => {
  it('renders the body, the mermaid block as mermaid’s own <svg>, and the image by its signed URL', async () => {
    serve();
    mount('/chronicle/CP-46a5d90303fe');
    await waitFor(() => expect(screen.getByText(/An agent wrote this record/)).toBeTruthy());
    expect(screen.getByText('R2.7 observed: routes and screens')).toBeTruthy();
    await waitFor(() => expect(document.querySelector('.chr-mermaid svg')).toBeTruthy(), { timeout: 10000 });
    const svg = document.querySelector('.chr-mermaid svg')!;
    expect(svg.getAttribute('aria-roledescription')).toMatch(/^flowchart/);
    expect(svg.textContent).toContain('Agent');
    expect(svg.textContent).toContain('Screen');
    expect(document.querySelector('pre.chr-mermaid-src'), 'the fence fell back to its source').toBeNull();
    // The signed download URL reaches the <img> unaltered: exp and sig are the authority (blobs.go).
    expect(screen.getByAltText('red square').getAttribute('src')).toBe(SIGNED);
    expect(screen.getByText('History').getAttribute('href')).toBe(`/chronicle/CP-46a5d90303fe/history?project=${PID}`);
  });
});

describe('/chronicle/:pageId/history — the versions', () => {
  it('lists each version newest first, and Revert POSTs the chosen version', async () => {
    const w = serve();
    mount('/chronicle/CP-46a5d90303fe/history');
    await waitFor(() => expect(screen.getAllByText('Revert to this')).toHaveLength(2));
    expect(screen.getByText(/v e8bdeee · .* · 3 blocks/)).toBeTruthy();
    expect(screen.getByText(/v 7d3b9ae · .* · 3 blocks/)).toBeTruthy();
    expect(screen.getByText('generated')).toBeTruthy();
    expect(screen.getByText('edited')).toBeTruthy();
    fireEvent.click(screen.getAllByText('Revert to this')[1]!);
    await waitFor(() => expect(w.calls.some((c) => c.url.endsWith('/revert'))).toBe(true));
    const revert = w.calls.find((c) => c.url.endsWith('/revert'))!;
    expect(revert.method).toBe('POST');
    expect(revert.url).toBe(`/api/projects/${PID}/chronicle/CP-46a5d90303fe/revert`);
    expect(revert.body).toEqual({ versionId: 'CV-7d3b9ae8453f' });
  });
});
