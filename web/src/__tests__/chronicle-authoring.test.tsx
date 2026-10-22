/**
 * CHS-7 — writing a record by hand.
 *
 * Chronicle could be READ and it could be corrected, but it could not be WRITTEN. There was no way to
 * start a document, and the editor could only retype the text of blocks that already existed — so for
 * a hand-written page it was not an editor at all, it was a spell-checker. These cover the three
 * things that changed and the one that is now destructive.
 *
 * The load-bearing assertion in here is not "add works". It is that a block's ID SURVIVES an edit:
 * ids are what make the diff, the history and the three-way merge work, so an editor that re-derived
 * them (splitting a document into paragraphs on save, say) would make every version read as
 * "everything deleted, everything added" and leave the sweep nothing to match against. That failure
 * is invisible on screen and total on the wire, which is why it is asserted on the wire.
 *
 * Asserted on what a PERSON sees and on what the wire carries — never on props or class names.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { render, screen, cleanup, waitFor, fireEvent } from '@testing-library/react';
import { Chronicle, ChronicleNew, ChroniclePageView } from '../screens/Chronicle.js';
import { ProjectProvider } from '../projectContext.js';

const PAGE = {
  id: 'CP-1',
  projectId: 'P-1',
  kind: 'topic' as const,
  slug: 'notes/how-we-deploy',
  title: 'How we deploy',
  headVersionId: 'CV-1',
  sourcePath: null,
  createdAt: '2026-08-12T00:00:00Z',
  updatedAt: '2026-08-12T00:00:00Z',
};

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

interface Server {
  /** Bodies of every create POST that reached the wire. */
  created: Array<{
    kind: string;
    slug: string;
    title: string;
    blocks: Array<{ id: string; text: string }>;
    idempotencyKey?: string;
  }>;
  /** Block lists of every edit POST that reached the wire, in order. */
  edited: Array<Array<{ id: string; text: string }>>;
  /** Page ids of every DELETE that reached the wire. */
  deleted: string[];
  /** Full URLs of every DELETE, so the head pin (querystring) is assertable. */
  deletedUrls: string[];
  /** The head's blocks, so a test can put markdown on the page. */
  blocks: Array<{ id: string; state: string; text: string; citations: never[]; origin: null }>;
  /** Repo-derived pages warn that the sweep will recreate them; null means hand-written. */
  sourcePath: string | null;
  createFails: string | null;
}

function stub(): Server {
  const s: Server = {
    created: [],
    edited: [],
    deleted: [],
    deletedUrls: [],
    blocks: [{ id: 'a-1', state: 'authored', text: 'first paragraph', citations: [], origin: null }],
    sourcePath: null,
    createFails: null,
  };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url.includes('/api/session')) return json({ identity: 'a@b.c', csrfToken: 't', isAdmin: false });
      if (url.endsWith('/api/projects')) return json({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] });
      // `/edit` first — it contains the page path as a prefix.
      if (url.includes('/chronicle/CP-1/edit')) {
        const body = JSON.parse(String(init?.body)) as { blocks: Array<{ id: string; text: string }> };
        s.edited.push(body.blocks.map((b) => ({ id: b.id, text: b.text })));
        return json({ versionId: 'CV-2', drifted: [] });
      }
      if (url.includes('/chronicle/CP-1') && method === 'DELETE') {
        s.deleted.push('CP-1');
        s.deletedUrls.push(url);
        return json({ ok: true });
      }
      if (url.includes('/chronicle/CP-1'))
        return json({
          page: { ...PAGE, sourcePath: s.sourcePath },
          head: {
            id: 'CV-1',
            pageId: 'CP-1',
            parentVersionId: null,
            source: 'human',
            author: 'a@b.c',
            agent: null,
            ts: '2026-08-12T00:00:00Z',
            blocks: s.blocks,
          },
          drifted: [],
          conflicts: null,
        });
      if (url.endsWith('/chronicle') && method === 'POST') {
        s.created.push(JSON.parse(String(init?.body)) as Server['created'][number]);
        if (s.createFails) return json({ code: 'VALIDATION', message: s.createFails }, 400);
        return json({ pageId: 'CP-1', versionId: 'CV-1' });
      }
      if (url.includes('/chronicle')) return json({ pages: [] });
      return json({});
    }),
  );
  return s;
}

/** Mount the whole chronicle sub-app, so a navigation lands somewhere observable. */
function mount(at: string) {
  localStorage.setItem('bd.projectId', 'P-1');
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[at]}>
        <ProjectProvider>
          <Routes>
            <Route path="/chronicle" element={<Chronicle />} />
            <Route path="/chronicle/new" element={<ChronicleNew />} />
            <Route path="/chronicle/:pageId" element={<ChroniclePageView />} />
          </Routes>
        </ProjectProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return qc;
}

const field = (label: RegExp | string) => screen.getByLabelText(label) as HTMLInputElement;
const createBtn = () => screen.getByText('Create record').closest('button') as HTMLButtonElement;

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('writing a record by hand', () => {
  it('POSITIVE CONTROL: title, slug, kind and blocks reach the wire, then it navigates to the record', async () => {
    const s = stub();
    mount('/chronicle/new');
    await waitFor(() => expect(field('Title')).toBeTruthy());

    fireEvent.change(field('Title'), { target: { value: 'How we deploy' } });
    fireEvent.change(screen.getByLabelText(/^Block /), { target: { value: 'Deploys are CI/CD only.' } });
    fireEvent.change(screen.getByLabelText('Kind'), { target: { value: 'topic' } });
    // Enabled only once the projects query has resolved (the fast-submit guard below pins this).
    await waitFor(() => expect(createBtn().disabled).toBe(false));
    createBtn().click();

    await waitFor(() => expect(s.created).toHaveLength(1));
    expect(s.created[0]).toMatchObject({ kind: 'topic', title: 'How we deploy', slug: 'how-we-deploy' });
    expect(s.created[0]!.blocks.map((b) => b.text)).toEqual(['Deploys are CI/CD only.']);
    // Landed ON the new record rather than back at the index — the thing just written is the thing
    // the writer wants to look at.
    await waitFor(() => expect(screen.getByText('first paragraph')).toBeTruthy());
  });

  it('the slug follows the title until someone edits it, and then STOPS', async () => {
    const s = stub();
    mount('/chronicle/new');
    await waitFor(() => expect(field('Title')).toBeTruthy());

    fireEvent.change(field('Title'), { target: { value: 'How We Deploy!' } });
    // Lowercased, punctuation collapsed to dashes — the shape the server accepts.
    expect(field(/^Slug/).value).toBe('how-we-deploy');

    fireEvent.change(field(/^Slug/), { target: { value: 'notes/deploys' } });
    fireEvent.change(field('Title'), { target: { value: 'How We Deploy, Actually' } });
    // A slug is an IDENTITY and cannot be renamed later, so a chosen one must not be silently
    // rewritten by a later word in the heading.
    expect(field(/^Slug/).value).toBe('notes/deploys');

    fireEvent.change(screen.getByLabelText(/^Block /), { target: { value: 'x' } });
    await waitFor(() => expect(createBtn().disabled).toBe(false));
    createBtn().click();
    await waitFor(() => expect(s.created[0]?.slug).toBe('notes/deploys'));
  });

  it('REFUSAL: an empty title or an empty body cannot be submitted, and blank blocks never travel', async () => {
    const s = stub();
    mount('/chronicle/new');
    await waitFor(() => expect(field('Title')).toBeTruthy());

    // Nothing typed at all.
    expect(createBtn().disabled).toBe(true);
    // A title with no words in the body is still nothing — a record of a heading is not a record.
    fireEvent.change(field('Title'), { target: { value: 'Empty' } });
    expect(createBtn().disabled).toBe(true);

    fireEvent.change(screen.getByLabelText(/^Block /), { target: { value: 'something' } });
    await waitFor(() => expect(createBtn().disabled).toBe(false));
    // A second, untouched block exists in the editor and must NOT be posted — every record would
    // otherwise open with a blank paragraph nobody typed.
    screen.getAllByText('+ Add below')[0]!.click();
    createBtn().click();
    await waitFor(() => expect(s.created).toHaveLength(1));
    expect(s.created[0]!.blocks).toHaveLength(1);
  });

  it('REFUSAL: cannot submit before the projects query resolves — no POST into an empty projectId', async () => {
    // Bespoke stub: /api/projects HANGS until released, which is every screen's first paint.
    let releaseProjects!: () => void;
    const gate = new Promise<void>((r) => (releaseProjects = r));
    const created: unknown[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.includes('/api/session')) return json({ identity: 'a@b.c', csrfToken: 't', isAdmin: false });
        if (url.endsWith('/api/projects')) {
          await gate;
          return json({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] });
        }
        if (url.endsWith('/chronicle') && (init?.method ?? 'GET') === 'POST') {
          created.push(url);
          return json({ pageId: 'CP-1', versionId: 'CV-1' });
        }
        return json({});
      }),
    );
    localStorage.setItem('bd.projectId', 'P-1');
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/chronicle/new']}>
          <ProjectProvider>
            <Routes>
              <Route path="/chronicle/new" element={<ChronicleNew />} />
            </Routes>
          </ProjectProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    await waitFor(() => expect(field('Title')).toBeTruthy());
    fireEvent.change(field('Title'), { target: { value: 'Fast fingers' } });
    fireEvent.change(screen.getByLabelText(/^Block /), { target: { value: 'typed before load' } });
    // The fast submit: everything typed, projects still in flight — the button MUST be disabled,
    // because projectId is '' until the fetched list validates the stored id and a click here used
    // to POST into `/api/projects//chronicle`.
    expect(createBtn().disabled).toBe(true);
    createBtn().click();
    expect(created).toEqual([]);

    releaseProjects();
    await waitFor(() => expect(createBtn().disabled).toBe(false));
    createBtn().click();
    await waitFor(() => expect(created).toHaveLength(1));
    expect(String(created[0])).toContain('/api/projects/P-1/chronicle');
  });

  it('mints ONE idempotency key per form instance, so a retried submit replays rather than re-creates', async () => {
    const s = stub();
    s.createFails = 'flaky';
    mount('/chronicle/new');
    await waitFor(() => expect(field('Title')).toBeTruthy());
    fireEvent.change(field('Title'), { target: { value: 'Retried' } });
    fireEvent.change(screen.getByLabelText(/^Block /), { target: { value: 'x' } });
    await waitFor(() => expect(createBtn().disabled).toBe(false));
    createBtn().click();
    await waitFor(() => expect(s.created).toHaveLength(1));
    expect(s.created[0]!.idempotencyKey).toMatch(/^[0-9a-f-]{36}$/);

    // The retry from the SAME form carries the SAME key — that is what lets the server replay a
    // create whose response was lost instead of refusing the slug as taken.
    createBtn().click();
    await waitFor(() => expect(s.created).toHaveLength(2));
    expect(s.created[1]!.idempotencyKey).toBe(s.created[0]!.idempotencyKey);
  });

  it('reports the server refusing a taken slug instead of pretending it landed', async () => {
    const s = stub();
    s.createFails = 'a Chronicle record with this slug already exists';
    mount('/chronicle/new');
    await waitFor(() => expect(field('Title')).toBeTruthy());
    fireEvent.change(field('Title'), { target: { value: 'Taken' } });
    fireEvent.change(screen.getByLabelText(/^Block /), { target: { value: 'x' } });
    await waitFor(() => expect(createBtn().disabled).toBe(false));
    createBtn().click();

    await waitFor(() => expect(screen.getByText(/already exists/i)).toBeTruthy());
    // Still on the form, with the words still in it — a failed create must not cost the draft.
    expect(field('Title').value).toBe('Taken');
  });
});

describe('the block editor: add, remove, reorder', () => {
  /** Open the inline editor on the page and return the current textarea labels, in order. */
  async function openEditor() {
    const edit = await waitFor(() => screen.getByText('Edit'));
    edit.click();
    await waitFor(() => expect(screen.getByText('Save version')).toBeTruthy());
  }
  const blockLabels = () =>
    screen.getAllByPlaceholderText(/^Markdown/).map((el) => el.getAttribute('aria-label')!);

  it('THE ONE THAT MATTERS: a block keeps its id across an edit', async () => {
    const s = stub();
    s.blocks = [
      { id: 'a-1', state: 'authored', text: 'one', citations: [], origin: null },
      { id: 'a-2', state: 'authored', text: 'two', citations: [], origin: null },
    ];
    mount('/chronicle/CP-1');
    await openEditor();

    fireEvent.change(screen.getByLabelText('Block a-1'), { target: { value: 'one, edited' } });
    screen.getByText('Save version').click();

    await waitFor(() => expect(s.edited).toHaveLength(1));
    // Ids UNCHANGED. Re-deriving them would make this a wholesale delete-and-recreate on every save:
    // the diff would show two removals and two additions, the history would be unreadable, and the
    // sweep's three-way merge would have nothing to match its own blocks against.
    expect(s.edited[0]).toEqual([
      { id: 'a-1', text: 'one, edited' },
      { id: 'a-2', text: 'two' },
    ]);
  });

  it('adds a block below, and the new one is a NEW id — never a reused or colliding one', async () => {
    const s = stub();
    mount('/chronicle/CP-1');
    await openEditor();

    screen.getAllByText('+ Add below')[0]!.click();
    await waitFor(() => expect(blockLabels()).toHaveLength(2));
    const [, second] = blockLabels();
    fireEvent.change(screen.getByLabelText(second!), { target: { value: 'a new paragraph' } });
    screen.getByText('Save version').click();

    await waitFor(() => expect(s.edited).toHaveLength(1));
    const ids = s.edited[0]!.map((b) => b.id);
    expect(ids[0]).toBe('a-1');
    expect(ids[1]).not.toBe('a-1');
    // `a-` prefixed and structurally disjoint from the sweep's `<sectionKey><n>/<m>` scheme, so a
    // hand-written block can never collide with a derived one (a duplicate id is refused by the tool,
    // and were it not, the merge indexes by id and one of the pair would silently vanish).
    expect(ids[1]).toMatch(/^a-[0-9a-f]{12}$/);
    expect(s.edited[0]![1]!.text).toBe('a new paragraph');
  });

  it('removes a block — it is simply absent from the next version', async () => {
    const s = stub();
    s.blocks = [
      { id: 'a-1', state: 'authored', text: 'keep', citations: [], origin: null },
      { id: 'a-2', state: 'authored', text: 'drop', citations: [], origin: null },
    ];
    mount('/chronicle/CP-1');
    await openEditor();

    screen.getByLabelText('Remove a-2').click();
    await waitFor(() => expect(blockLabels()).toEqual(['Block a-1']));
    screen.getByText('Save version').click();
    await waitFor(() => expect(s.edited).toEqual([[{ id: 'a-1', text: 'keep' }]]));
  });

  it('reorders, and the ends of the list cannot be moved off it', async () => {
    const s = stub();
    s.blocks = [
      { id: 'a-1', state: 'authored', text: 'one', citations: [], origin: null },
      { id: 'a-2', state: 'authored', text: 'two', citations: [], origin: null },
    ];
    mount('/chronicle/CP-1');
    await openEditor();

    // The first block cannot go up and the last cannot go down — disabled, not silently ignored.
    expect((screen.getByLabelText('Move a-1 up').closest('button') as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByLabelText('Move a-2 down').closest('button') as HTMLButtonElement).disabled).toBe(true);

    screen.getByLabelText('Move a-2 up').click();
    await waitFor(() => expect(blockLabels()).toEqual(['Block a-2', 'Block a-1']));
    screen.getByText('Save version').click();
    // Same ids, new order — the text travelled with the block rather than with the position.
    await waitFor(() =>
      expect(s.edited).toEqual([
        [
          { id: 'a-2', text: 'two' },
          { id: 'a-1', text: 'one' },
        ],
      ]),
    );
  });

  it('an emptied page can be saved — a record that says nothing is still a record with a history', async () => {
    const s = stub();
    mount('/chronicle/CP-1');
    await openEditor();
    screen.getByLabelText('Remove a-1').click();
    // The empty case needs its own door: with no rows left there is no "add below" to press.
    await waitFor(() => expect(screen.getByText('Add the first block')).toBeTruthy());
    screen.getByText('Save version').click();
    await waitFor(() => expect(s.edited).toEqual([[]]));
  });
});

describe('blocks render as markdown', () => {
  it('a heading, a list and emphasis all render as themselves', async () => {
    const s = stub();
    s.blocks = [
      { id: 'a-1', state: 'authored', text: '## Deploys\n\n- CI/CD **only**\n', citations: [], origin: null },
    ];
    mount('/chronicle/CP-1');

    // People writing documentation by hand expect headings, lists and emphasis; showing their
    // asterisks back to them is the difference between a document and a textarea's contents.
    const heading = await waitFor(() => screen.getByText('Deploys'));
    expect(heading.tagName).toBe('H2');
    expect(screen.getByText('only').tagName).toBe('STRONG');
    expect(document.querySelector('.chr-block li')).toBeTruthy();
    // And the raw syntax is nowhere on the page.
    expect(screen.queryByText(/## Deploys/)).toBeNull();
  });
});

describe('deleting a record', () => {
  const openPanel = async () => {
    const btn = await waitFor(() => screen.getByText('Delete'));
    btn.click();
    await waitFor(() => expect(screen.getByText(/delete this record permanently/i)).toBeTruthy());
  };
  const confirmBtn = () => screen.getByText('Delete permanently').closest('button') as HTMLButtonElement;

  it('REFUSAL: the confirm stays disabled until the SLUG is typed exactly', async () => {
    const s = stub();
    mount('/chronicle/CP-1');
    await openPanel();

    expect(confirmBtn().disabled).toBe(true);
    fireEvent.change(field(/Type/), { target: { value: 'notes/how-we-deplo' } });
    expect(confirmBtn().disabled).toBe(true);
    fireEvent.change(field(/Type/), { target: { value: 'How we deploy' } }); // the TITLE, not the slug
    expect(confirmBtn().disabled).toBe(true);
    expect(s.deleted).toEqual([]);

    fireEvent.change(field(/Type/), { target: { value: 'notes/how-we-deploy' } });
    expect(confirmBtn().disabled).toBe(false);
    confirmBtn().click();
    await waitFor(() => expect(s.deleted).toEqual(['CP-1']));
    // Left for the index: a page that resolves to nothing is the one state this screen cannot
    // honestly render.
    await waitFor(() => expect(screen.getByText(/no records yet/i)).toBeTruthy());
  });

  it('says what it destroys, and warns when the sweep will simply put it back', async () => {
    const s = stub();
    s.sourcePath = 'docs/chronicle/adr/0001-x.md';
    mount('/chronicle/CP-1');
    await openPanel();
    expect(screen.getByText(/cannot be undone/i)).toBeTruthy();
    // Deleting a GENERATED record is close to a no-op, and saying so beforehand is cheaper than
    // watching it reappear tomorrow and calling it a bug.
    expect(screen.getByText(/the next sweep will recreate it/i)).toBeTruthy();
  });

  it('POSITIVE CONTROL: a hand-written record carries NO sweep warning', async () => {
    stub();
    mount('/chronicle/CP-1');
    await openPanel();
    expect(screen.queryByText(/the next sweep will recreate it/i)).toBeNull();
  });

  it('the destroy pins the head the confirmation DISPLAYED (expectedHeadVersionId on the wire)', async () => {
    const s = stub();
    mount('/chronicle/CP-1');
    await openPanel();
    fireEvent.change(field(/Type/), { target: { value: 'notes/how-we-deploy' } });
    confirmBtn().click();
    await waitFor(() => expect(s.deleted).toEqual(['CP-1']));
    // The pin is what turns a version landed mid-confirm into a STALE instead of a destroy.
    expect(s.deletedUrls[0]).toContain('expectedHeadVersionId=CV-1');
  });

  it('REFUSAL: the delete-then-EDIT order kills the confirm panel — the draft outranks it', async () => {
    stub();
    mount('/chronicle/CP-1');
    await openPanel();
    // The order the earlier guard missed: the panel is OPEN, then the editor opens over it. The
    // armed "Delete permanently" must not survive next to a live draft.
    screen.getByText('Edit').click();
    await waitFor(() => expect(screen.getByText('Save version')).toBeTruthy());
    expect(screen.queryByText(/delete this record permanently/i)).toBeNull();
    expect(screen.queryByText('Delete permanently')).toBeNull();

    // And leaving the editor does not resurrect it: the confirmation must be re-opened, re-typed.
    screen.getByText('Cancel').click();
    await waitFor(() => expect(screen.getByText('Delete')).toBeTruthy());
    expect(screen.queryByText(/delete this record permanently/i)).toBeNull();
  });

  it('REFUSAL: Delete is not reachable while the editor is open — nothing outranks an open draft', async () => {
    stub();
    mount('/chronicle/CP-1');
    const edit = await waitFor(() => screen.getByText('Edit'));
    edit.click();
    await waitFor(() => expect(screen.getByText('Save version')).toBeTruthy());
    expect(screen.queryByText('Delete')).toBeNull();

    screen.getByText('Cancel').click();
    await waitFor(() => expect(screen.getByText('Delete')).toBeTruthy());
  });

  it('Cancel closes the panel and writes nothing', async () => {
    const s = stub();
    mount('/chronicle/CP-1');
    await openPanel();
    screen.getByText('Cancel').click();
    await waitFor(() => expect(screen.queryByText(/delete this record permanently/i)).toBeNull());
    expect(s.deleted).toEqual([]);
  });
});
