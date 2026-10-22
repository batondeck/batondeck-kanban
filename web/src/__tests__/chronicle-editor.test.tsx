/**
 * CHS-4 — the open editor holds text that exists NOWHERE ELSE.
 *
 * Not in the react-query cache, not on the server, not in localStorage: only in this component's
 * state. Two defects took it away, and both of them looked like correct fixes for something else.
 *
 *  (13) The whole-screen early returns (`isFetching` -> "Loading…", `isError` -> a Note) fired
 *       unconditionally. With `staleTime: 30_000` + react-query's default `refetchOnWindowFocus`,
 *       switching tab mid-edit swapped the editor for a spinner; with `retry: false`, one failed
 *       background refetch pinned `isError` and left an error message with no Save button at all.
 *       Those returns were ADDED to fix real bugs (a null page spinning forever, an unscoped project
 *       rendering nothing) — so they stay, they just no longer outrank somebody's unsaved words.
 *
 *  (12) A STALE save said "Your text is still here" and then offered two doors that both lost it:
 *       Save retried the SAME pinned parent (STALE forever — it was never rebased) and Cancel flipped
 *       `editing` off, which re-seeded the draft from the refetched head.
 *
 * Asserted on what a PERSON sees and on what the wire carries — never on props or class names.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { render, screen, cleanup, waitFor, fireEvent } from '@testing-library/react';
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

const version = (id: string, text: string) => ({
  id,
  pageId: 'CP-1',
  parentVersionId: null,
  source: 'human' as const,
  author: 'a@b.c',
  agent: null,
  ts: '2026-07-31T00:00:00Z',
  blocks: [{ id: 'b1', state: 'derived' as const, text, citations: [], origin: null }],
});

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

/** A tiny mutable server. Every test drives it by reassigning fields, so timing stays explicit. */
interface Server {
  head: ReturnType<typeof version>;
  /** GET fails — a background refresh that dies under an open editor. */
  getFails: boolean;
  /** GET parks until `release()` is called — the only way to observe an IN-FLIGHT refetch. */
  hold: boolean;
  release: (() => void) | null;
  /** The edit POST fails with something that is NOT a conflict. */
  saveFails: boolean;
  /** `expectedParentVersionId` of every edit that reached the wire, in order. */
  posted: string[];
  /** Block text of every edit that reached the wire, in order. */
  postedText: string[];
  /** Block IDS of every edit that reached the wire — a resolution can DELETE a block (T-298). */
  postedIds: string[][];
  /** The persisted sweep-conflict state the page GET serves (T-298). Null = none stands. */
  conflicts: {
    versionId: string;
    ts: string;
    items: Array<{ blockId: string; kind: string; theirs: { id: string; state: string; text: string; citations: never[]; origin: null } | null }>;
  } | null;
}

function stub(): Server {
  const s: Server = {
    head: version('CV-2', 'original text'),
    getFails: false,
    hold: false,
    release: null,
    saveFails: false,
    posted: [],
    postedText: [],
    postedIds: [],
    conflicts: null,
  };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes('/api/session'))
        return json({ identity: 'a@b.c', csrfToken: 't', isAdmin: false });
      // `/edit` must be matched BEFORE the page GET — it contains the page path as a prefix.
      if (url.includes('/chronicle/CP-1/edit')) {
        const body = JSON.parse(String(init?.body)) as { expectedParentVersionId: string; blocks: Array<{ id: string; text: string }> };
        s.posted.push(body.expectedParentVersionId);
        s.postedText.push(body.blocks[0]?.text ?? '');
        s.postedIds.push(body.blocks.map((b) => b.id));
        if (s.saveFails) return json({ code: 'INTERNAL', message: 'save blew up' }, 500);
        // The server's own optimistic-concurrency check: landing on anything but the live head is STALE.
        if (body.expectedParentVersionId !== s.head.id) return json({ code: 'STALE', message: 'version moved' }, 409);
        // A human-landed version resolves any standing sweep conflict — mirrors the store (T-298).
        s.conflicts = null;
        return json({ versionId: 'CV-9', drifted: [] });
      }
      if (url.includes('/chronicle/CP-1')) {
        if (s.hold) await new Promise<void>((r) => { s.release = r; });
        if (s.getFails) return json({ code: 'INTERNAL', message: 'gateway exploded' }, 500);
        return json({ page: { ...PAGE, headVersionId: s.head.id }, head: s.head, drifted: [], conflicts: s.conflicts });
      }
      if (url.endsWith('/api/projects'))
        return json({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] });
      return json({});
    }),
  );
  return s;
}

function mount() {
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
  return qc;
}

const editor = () => screen.getByLabelText('Block b1') as HTMLTextAreaElement;

/** Open the editor and type `text` into its one block. */
async function typeInto(text: string) {
  const edit = await waitFor(() => screen.getByText('Edit'));
  edit.click();
  await waitFor(() => expect(screen.getByText('Save version')).toBeTruthy());
  fireEvent.change(editor(), { target: { value: text } });
  expect(editor().value).toBe(text);
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('(13) an open editor outranks every whole-screen state', () => {
  it('a background refetch IN FLIGHT does not replace the editor with a spinner', async () => {
    const s = stub();
    const qc = mount();
    await typeInto('the words I typed');

    // Park the refetch so it is observably in flight. Waiting for it to FINISH would prove nothing:
    // `editing` survives the round trip, so the buggy version renders the editor again afterwards and
    // the assertion passes on the defect. The spinner only exists during the fetch, so that is when
    // this has to look.
    s.hold = true;
    void qc.invalidateQueries({ queryKey: ['chronicle-page', 'P-1', 'CP-1'] });
    await waitFor(() => expect(qc.isFetching()).toBeGreaterThan(0));

    expect(screen.queryByText(/loading/i)).toBeNull();
    expect(editor().value).toBe('the words I typed');

    s.release?.();
  });

  it('a background refetch that FAILS leaves the editor standing, and reports itself inline', async () => {
    const s = stub();
    const qc = mount();
    await typeInto('the words I typed');

    s.getFails = true;
    await qc.invalidateQueries({ queryKey: ['chronicle-page', 'P-1', 'CP-1'] });

    // The failure is said out loud — it is just not allowed to be the whole screen.
    await waitFor(() => expect(screen.getByText(/gateway exploded/i)).toBeTruthy());
    expect(editor().value).toBe('the words I typed');
    expect(screen.getByText('Save version')).toBeTruthy();
  });

  it('POSITIVE CONTROL: with NO editor open, a failed fetch is still a full-screen error', async () => {
    const s = stub();
    s.getFails = true;
    mount();
    await waitFor(() => expect(screen.getByText(/gateway exploded/i)).toBeTruthy());
    // Nothing to protect here, so the reader gets the plain error screen — no editor, no stale page.
    expect(screen.queryByLabelText('Block b1')).toBeNull();
    expect(screen.queryByText('Edit')).toBeNull();
  });

  it('POSITIVE CONTROL: the FIRST load still says "Loading…" — the spinner was narrowed, not deleted', async () => {
    const s = stub();
    s.hold = true;
    mount();
    await waitFor(() => expect(screen.getByText(/loading/i)).toBeTruthy());
    s.release?.();
    await waitFor(() => expect(screen.getByText('original text')).toBeTruthy());
  });
});

describe('(12) a STALE save is recoverable — theirs, mine, and a way forward', () => {
  it('shows BOTH versions and then lands my text on top of theirs', async () => {
    const s = stub();
    mount();
    await typeInto('my words');

    // Someone else lands CV-3 while the editor is open.
    s.head = version('CV-3', 'their new text');
    screen.getByText('Save version').click();

    // The save is rejected against the version this person actually edited — that check is correct
    // and stays. What follows it is the fix.
    await waitFor(() => expect(s.posted).toEqual(['CV-2']));
    await waitFor(() => expect(screen.getByText(/someone else saved while you were editing/i)).toBeTruthy());

    // THEIRS is on screen. This is what makes the next click informed rather than a silent overwrite:
    // `editingFrom` exists to stop a save landing on a version its author never saw, so the way out is
    // to show them the version — not to drop the check.
    expect(screen.getByLabelText('Theirs b1').textContent).toBe('their new text');
    // MINE is on screen, and still in the editor — unsent, unlost.
    expect(screen.getByLabelText('Mine b1').textContent).toBe('my words');
    expect(editor().value).toBe('my words');

    // REFUSAL: the button that retried the same pinned parent forever is gone. Its only possible
    // outcome was the error already being read.
    expect(screen.queryByText('Save version')).toBeNull();

    screen.getByText(/keep my text — save it on top of CV-3/i).click();

    // Rebased ONTO the version that was displayed, carrying the words that were typed.
    await waitFor(() => expect(s.posted).toEqual(['CV-2', 'CV-3']));
    expect(s.postedText).toEqual(['my words', 'my words']);
    // Landed: the editor closes and the page is a reader's page again.
    await waitFor(() => expect(screen.queryByLabelText('Block b1')).toBeNull());
  });

  it('REFUSAL: with theirs unloadable, the rebase is offered but DISABLED — no landing on an unseen version', async () => {
    const s = stub();
    mount();
    await typeInto('my words');

    s.head = version('CV-3', 'their new text');
    s.getFails = true; // the "go and read theirs" fetch dies
    screen.getByText('Save version').click();

    await waitFor(() => expect(screen.getByText(/someone else saved while you were editing/i)).toBeTruthy());
    expect(screen.getByText(/their version is not on screen yet/i)).toBeTruthy();

    const rebase = screen.getByText(/keep my text/i).closest('button')!;
    expect(rebase.disabled).toBe(true);
    // …and the text is still here, which is the only promise this screen is allowed to make.
    expect(editor().value).toBe('my words');
    expect(s.posted).toEqual(['CV-2']);

    // A disabled rebase beside a discard is this finding wearing a different hat, so the dead end has
    // to have a door. `refetchOnWindowFocus` would eventually rescue this on its own — a recovery
    // nobody can see is not one.
    s.getFails = false;
    screen.getByText(/try again/i).click();
    await waitFor(() => expect(screen.getByLabelText('Theirs b1').textContent).toBe('their new text'));
    const rebased = screen.getByText(/keep my text/i).closest('button')!;
    expect(rebased.disabled).toBe(false);
    rebased.click();
    await waitFor(() => expect(s.posted).toEqual(['CV-2', 'CV-3']));
    expect(s.postedText).toEqual(['my words', 'my words']);
  });

  it('the only discard is EXPLICIT: "Cancel" becomes "Discard my changes", and taking theirs shows theirs', async () => {
    const s = stub();
    mount();
    await typeInto('my words');

    s.head = version('CV-3', 'their new text');
    screen.getByText('Save version').click();
    await waitFor(() => expect(screen.getByLabelText('Theirs b1')).toBeTruthy());

    // The old exit said "Cancel" beside a message promising the text was safe, and then ate it.
    expect(screen.queryByText('Cancel')).toBeNull();
    expect(screen.getByText('Discard my changes')).toBeTruthy();

    screen.getByText(/take theirs instead/i).click();
    // Chosen, not inflicted: the editor stays open, re-seeded from the version just read.
    await waitFor(() => expect(editor().value).toBe('their new text'));
    expect(screen.queryByText(/someone else saved while you were editing/i)).toBeNull();
    expect(s.posted).toEqual(['CV-2']); // nothing was written by taking theirs
  });

  it('POSITIVE CONTROL: an ordinary save posts once, closes, and never mentions a conflict', async () => {
    stub();
    mount();
    await typeInto('my words');
    screen.getByText('Save version').click();

    await waitFor(() => expect(screen.queryByLabelText('Block b1')).toBeNull());
    expect(screen.queryByText(/someone else saved while you were editing/i)).toBeNull();
    expect(screen.queryByText('Discard my changes')).toBeNull();
  });

  it('POSITIVE CONTROL: a non-STALE save error keeps the editor AND keeps Save — it is retryable', async () => {
    const s = stub();
    mount();
    await typeInto('my words');
    // Not a conflict: nothing to compare, so no conflict panel — just the message and another go.
    s.saveFails = true;
    screen.getByText('Save version').click();

    await waitFor(() => expect(screen.getByText('save blew up')).toBeTruthy());
    expect(screen.queryByText(/someone else saved while you were editing/i)).toBeNull();
    expect(screen.getByText('Save version')).toBeTruthy();
    expect(editor().value).toBe('my words');
  });
});

describe('(T-298) a sweep conflict reaches a human, and the human lands the final version', () => {
  const theirs = (id: string, text: string) => ({ id, state: 'authored', text, citations: [] as never[], origin: null });
  const conflicted = (s: Server) => {
    s.conflicts = {
      versionId: 'CV-2',
      ts: '2026-08-04T00:00:00Z',
      items: [{ blockId: 'b1', kind: 'conflict', theirs: theirs('b1', 'generator text') }],
    };
  };
  const panel = () => screen.queryByText(/a sweep conflicted with edits on this page/i);

  it('BOTH sides are on screen, and landing untouched CONFIRMS the auto-pick (kept-and-labeled)', async () => {
    const s = stub();
    conflicted(s);
    mount();

    await waitFor(() => expect(panel()).toBeTruthy());
    // Ours and theirs, per block — the diff view CHS §5 promises.
    expect(screen.getByLabelText('Current b1').textContent).toBe('original text');
    expect(screen.getByLabelText('Sweep b1').textContent).toBe('generator text');

    // Landing with every choice at its default posts the head content on the head parent: an
    // explicit human confirmation of what the merge kept. The server clears the flag on it.
    screen.getByText('Land the final version').click();
    await waitFor(() => expect(s.posted).toEqual(['CV-2']));
    expect(s.postedText).toEqual(['original text']);
    await waitFor(() => expect(panel()).toBeNull());
  });

  it("taking the generator's side lands THEIRS text through the ordinary edit path", async () => {
    const s = stub();
    conflicted(s);
    mount();
    await waitFor(() => expect(panel()).toBeTruthy());

    fireEvent.click(screen.getByText("Take the generator's"));
    // The choice must have LANDED in the UI before the land click reads it — a toggle is state.
    await waitFor(() =>
      expect(screen.getByText("Take the generator's").closest('button')!.getAttribute('aria-pressed')).toBe('true'),
    );
    screen.getByText('Land the final version').click();

    await waitFor(() => expect(s.posted).toEqual(['CV-2']));
    expect(s.postedText).toEqual(['generator text']);
    await waitFor(() => expect(panel()).toBeNull());
  });

  it('an ORPHANED block can be dropped: accepting the generator\'s deletion removes it from the landed version', async () => {
    const s = stub();
    s.head = {
      ...version('CV-2', 'original text'),
      blocks: [
        { id: 'b1', state: 'derived' as const, text: 'original text', citations: [], origin: null },
        { id: 'b2', state: 'derived' as const, text: 'stale section', citations: [], origin: null },
      ],
    };
    s.conflicts = {
      versionId: 'CV-2',
      ts: '2026-08-04T00:00:00Z',
      items: [{ blockId: 'b2', kind: 'kept-ours-orphaned', theirs: null }],
    };
    mount();
    await waitFor(() => expect(panel()).toBeTruthy());

    fireEvent.click(screen.getByText('Drop it'));
    await waitFor(() =>
      expect(screen.getByText('Drop it').closest('button')!.getAttribute('aria-pressed')).toBe('true'),
    );
    screen.getByText('Land the final version').click();

    await waitFor(() => expect(s.posted).toEqual(['CV-2']));
    expect(s.postedIds).toEqual([['b1']]); // b2 is gone from the landed version
    // POSITIVE CONTROL inside the same flow: the untouched sibling block still travelled.
    expect(s.postedText).toEqual(['original text']);
  });

  it('REFUSAL: the sweep panel is HIDDEN while the editor is open — nothing outranks an open draft (13)', async () => {
    const s = stub();
    conflicted(s);
    mount();
    await waitFor(() => expect(panel()).toBeTruthy());

    await typeInto('mid-edit words');
    expect(panel()).toBeNull();

    // Closing the editor brings the doorway back — hidden is not lost.
    screen.getByText('Cancel').click();
    await waitFor(() => expect(panel()).toBeTruthy());
  });

  it('a land that hits STALE reports and refetches instead of silently overwriting', async () => {
    const s = stub();
    conflicted(s);
    mount();
    await waitFor(() => expect(panel()).toBeTruthy());

    // Someone lands CV-3 between the render and the click.
    s.head = version('CV-3', 'their new text');
    screen.getByText('Land the final version').click();

    await waitFor(() => expect(s.posted).toEqual(['CV-2']));
    await waitFor(() => expect(screen.getByText(/someone landed a version while you were deciding/i)).toBeTruthy());
    // Nothing was landed on the moved head, and the refetched reality is what the panel now shows —
    // by LABEL, because the same text also renders in the reader's block list.
    await waitFor(() => expect(screen.getByLabelText('Current b1').textContent).toBe('their new text'));
  });

  it('POSITIVE CONTROL: with no standing conflict the panel never renders', async () => {
    stub();
    mount();
    await waitFor(() => expect(screen.getByText('original text')).toBeTruthy());
    expect(panel()).toBeNull();
  });
});
