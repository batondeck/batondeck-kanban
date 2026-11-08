/**
 * NO SURFACE PRINTS A RAW ACCOUNT ID WHERE IT MEANS A PERSON (P-314).
 *
 * Since the identity cutover a human is an opaque `U-<hex>` account id. Six surfaces printed it as is:
 * a comment's author (and "Replying to"), a follow-up's author and target, a notification's actor
 * ("U-282f… handed you T-2"), the ticket's "created by" on the drawer and the page, the Agents
 * "Recent Handoffs" list and the sidebar identity chip. Each is driven here with real components and a
 * routed `fetch`, fed account ids and the `memberDisplay` the server already sends
 * (`internal/service/projects.go`), and asserted on what renders: the name, and never the full id.
 * An id nothing names renders shortened, as `eventActor` shortens it.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { render, screen, cleanup, fireEvent, waitFor, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { Comments, movePicks, wireBody, type Pick } from '../screens/Comments.js';
import { FollowUps } from '../screens/FollowUps.js';
import { Notifications } from '../screens/Notifications.js';
import { TaskDrawer } from '../screens/TaskDrawer.js';
import { TaskPage } from '../screens/TaskPage.js';
import { Agents } from '../screens/Agents.js';
import { App } from '../App.js';
import { AuthProvider } from '../auth.js';
import { ProjectProvider } from '../projectContext.js';
import { routeFetch, task } from './taskFixtures.js';
import type { FollowUp, Notification, Task } from '../types.js';

const ALICE = 'U-282f4c9e01d7a3b5';
const BOB = 'U-6b0e7d21c4f8a9e2';
const GHOST = 'U-9f9f9f9f9f9f9f9f'; // no loaded data names this one
const GHOST_SHORT = 'U-9f9f9f9f…';
const memberDisplay = { [ALICE]: 'Alice Doe', [BOB]: 'bob' };
const TS = '2026-10-30T10:00:00.000Z';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  try { localStorage.clear(); } catch { /* jsdom always has it */ }
});

/** A `fetch` answering by exact path (query string ignored); unknown paths get `{}`. */
function byPath(routes: Record<string, unknown>): void {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const p = String(input).split('?')[0]!;
      return { ok: true, status: 200, json: async () => routes[p] ?? {} } as Response;
    }),
  );
}

const qc = () => new QueryClient({ defaultOptions: { queries: { retry: false } } });

function wrap(children: ReactNode, path = '/'): void {
  render(
    <QueryClientProvider client={qc()}>
      <MemoryRouter initialEntries={[path]}>{children}</MemoryRouter>
    </QueryClientProvider>,
  );
}

/** Neither person's full account id is anywhere in the rendered text or the attributes. */
function noRawIds(el: HTMLElement): void {
  const html = el.outerHTML;
  for (const id of [ALICE, BOB, GHOST]) expect(html, `raw ${id} rendered`).not.toContain(id);
}

const project = {
  project: { id: 'p1', name: 'P1', members: { [ALICE]: 'admin', [BOB]: 'worker' }, settings: { reviewEnabled: true, quotaEnabled: false, txnCap: null } },
  memberDisplay,
};

describe('comments', () => {
  it('name the author, the reply target and the mentioned, and shorten an unnamed id', async () => {
    byPath({
      '/api/projects/p1': project,
      '/api/projects/p1/tasks/T-1/comments': {
        comments: [
          { id: 'c1', taskId: 'T-1', author: ALICE, body: 'first', parentCommentId: null, mentions: [BOB], ts: TS },
          { id: 'c2', taskId: 'T-1', author: GHOST, body: 'second', parentCommentId: 'c1', mentions: [], ts: TS },
        ],
      },
    });
    wrap(<Comments projectId="p1" taskId="T-1" mentionCandidates={[]} canComment />);
    await screen.findByText('Alice Doe');
    expect(screen.getByText(GHOST_SHORT)).toBeInTheDocument();
    expect(screen.getByTitle('mentioned bob')).toBeInTheDocument();
    expect(screen.getByLabelText('human Alice Doe')).toBeInTheDocument();
    fireEvent.click(screen.getAllByRole('button', { name: 'Reply' })[0]!);
    expect(screen.getByText('Replying to Alice Doe')).toBeInTheDocument();
    noRawIds(document.body);
  });
});

describe('the comment composer and comment bodies', () => {
  /** Posts recorded from the routed fetch: [path, the JSON body]. */
  function routed(comments: unknown[], proj: unknown = project): Array<[string, Record<string, unknown>]> {
    const posts: Array<[string, Record<string, unknown>]> = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const p = String(input).split('?')[0]!;
        if (init?.method === 'POST') posts.push([p, JSON.parse(String(init.body)) as Record<string, unknown>]);
        const routes: Record<string, unknown> = { '/api/projects/p1': proj, '/api/projects/p1/tasks/T-1/comments': { comments } };
        return { ok: true, status: 200, json: async () => routes[p] ?? {} } as Response;
      }),
    );
    return posts;
  }
  /**
   * The draft becomes `text` with the selection at [caret, end] (both at the end of `text` by default),
   * as one keystroke, paste or deletion would leave it.
   */
  const type = (ta: HTMLTextAreaElement, text: string, caret = text.length, end = caret): void => {
    fireEvent.change(ta, { target: { value: text, selectionStart: caret, selectionEnd: end } });
  };
  /** Delete [from, to) from the draft, as Backspace over that selection does. */
  const cut = (ta: HTMLTextAreaElement, from: number, to: number): void =>
    type(ta, ta.value.slice(0, from) + ta.value.slice(to), from);
  /** Type `text` at the end of the draft. */
  const append = (ta: HTMLTextAreaElement, text: string): void => type(ta, ta.value + text);
  /** Type `text` at `pos` in the draft, the caret after it. */
  const insertAt = (ta: HTMLTextAreaElement, pos: number, text: string): void =>
    type(ta, ta.value.slice(0, pos) + text + ta.value.slice(pos), pos + text.length);
  /** The menu's entries as [name, hint]. */
  const menu = (): Array<[string, string | null]> =>
    [...document.querySelectorAll('.assign-menu .assign-item')].map((b) => [
      b.querySelector('.mention-name')!.textContent!,
      b.querySelector('.mention-name + .muted')?.textContent?.trim() ?? null,
    ]);
  /** Open the menu with `@<query>` typed at the end, and pick the entry whose name and hint match. */
  const pick = async (ta: HTMLTextAreaElement, query: string, name: string, hint: string | null = null): Promise<void> => {
    append(ta, `@${query}`);
    await waitFor(() => expect(menu().some(([n, h]) => n === name && h === hint)).toBe(true));
    const i = menu().findIndex(([n, h]) => n === name && h === hint);
    fireEvent.click(document.querySelectorAll('.assign-menu .assign-item')[i]!);
  };
  const draft = async (proj: unknown, candidates: string[]) => {
    const posts = routed([], proj);
    wrap(<Comments projectId="p1" taskId="T-1" mentionCandidates={candidates} canComment />);
    const ta = (await screen.findByPlaceholderText(/@mention a teammate/)) as HTMLTextAreaElement;
    await waitFor(() => expect(screen.queryByText('No comments yet.')).toBeInTheDocument());
    const send = async (): Promise<unknown> => {
      fireEvent.click(screen.getByRole('button', { name: 'Comment' }));
      await waitFor(() => expect(posts.length).toBe(1));
      return posts[0]![1].body;
    };
    return { ta, send };
  };
  /** A project whose members are `display` (id → memberDisplay), all workers. */
  const team = (display: Record<string, string>) => ({
    project: { ...project.project, members: Object.fromEntries(Object.keys(display).map((id) => [id, 'worker'])) },
    memberDisplay: display,
  });

  it('suggests people by name, and a picked name posts a mention the server resolves', async () => {
    const posts = routed([]);
    wrap(<Comments projectId="p1" taskId="T-1" mentionCandidates={[ALICE, BOB, GHOST]} canComment />);
    const ta = (await screen.findByPlaceholderText(/@mention a teammate/)) as HTMLTextAreaElement;
    await waitFor(() => expect(screen.queryByText('No comments yet.')).toBeInTheDocument());
    // The names load with the project read; by name is how a person is found.
    type(ta, 'see @ali');
    const alice = await screen.findByRole('button', { name: /Alice Doe/ });
    expect(screen.queryByRole('button', { name: /bob/ })).toBeNull();
    noRawIds(document.body);
    fireEvent.click(alice);
    // "Alice Doe" is two words, which the server's mention pattern cannot read as one handle: the
    // composer shows the name and the post carries her id.
    expect(ta.value).toBe('see @Alice Doe ');
    type(ta, `${ta.value}and @bo`);
    fireEvent.click(await screen.findByRole('button', { name: /bob/ }));
    // "bob" is one word, but another member can answer to it too: a pick posts the id, always.
    expect(ta.value).toBe('see @Alice Doe and @bob ');
    // A member nothing names is offered by its shortened id, and posted by the whole one.
    type(ta, `${ta.value}and @U-9f`);
    fireEvent.click(await screen.findByRole('button', { name: new RegExp(GHOST_SHORT) }));
    expect(ta.value).toBe(`see @Alice Doe and @bob and @${GHOST_SHORT} `);
    insertAt(ta, 'see @Alice Doe and @bob'.length, ',');
    append(ta, 'please.');
    expect(ta.value).toBe(`see @Alice Doe and @bob, and @${GHOST_SHORT} please.`);
    noRawIds(document.body);
    fireEvent.click(screen.getByRole('button', { name: 'Comment' }));
    await waitFor(() => expect(posts.length).toBe(1));
    expect(posts[0]).toEqual(['/api/projects/p1/tasks/T-1/comments', { body: `see @${ALICE} and @${BOB}, and @${GHOST} please.` }]);
  });

  // Two people with one name: the menu tells them apart, and each pick posts that person's id.
  const SAM1 = 'U-5a5a5a5a5a5a5a5a';
  const SAM2 = 'U-5b5b5b5b5b5b5b5b';
  it('posts two different ids for two "Sam Lee" members picked one after the other', async () => {
    const { ta, send } = await draft(team({ [SAM1]: 'Sam Lee', [SAM2]: 'Sam Lee' }), [SAM1, SAM2]);
    append(ta, 'cc ');
    await pick(ta, 'sam', 'Sam Lee', 'U-5a5a5a5a…');
    // A display name is all the web holds for these two, so the shortened id tells them apart.
    expect(menu()).toEqual([]);
    append(ta, 'and ');
    await pick(ta, 'sam', 'Sam Lee', 'U-5b5b5b5b…');
    expect(ta.value).toBe('cc @Sam Lee and @Sam Lee ');
    expect(await send()).toBe(`cc @${SAM1} and @${SAM2}`);
  });

  it('posts only the picked id when two members are both "sam"', async () => {
    const SAMA = 'U-5c5c5c5c5c5c5c5c';
    const SAMB = 'U-5d5d5d5d5d5d5d5d';
    // No display name: the server sends each login, and the web shows its local part.
    const { ta, send } = await draft(team({ [SAMA]: 'sam@a.example', [SAMB]: 'sam@b.example' }), [SAMA, SAMB]);
    append(ta, '@sam');
    await waitFor(() => expect(menu()).toEqual([['sam', 'sam@a.example'], ['sam', 'sam@b.example']]));
    type(ta, '');
    await pick(ta, 'sam', 'sam', 'sam@a.example');
    append(ta, 'over to you');
    expect(ta.value).toBe('@sam over to you');
    expect(await send()).toBe(`@${SAMA} over to you`);
  });

  it('keeps a name with $1, $& and regex characters as written, and posts its id', async () => {
    const REX = 'U-7e7e7e7e7e7e7e7e';
    const name = 'Rex (ops) $1.* $& [x]+?';
    const { ta, send } = await draft(team({ [REX]: name }), [REX]);
    append(ta, 'cc ');
    await pick(ta, 'rex', name);
    expect(ta.value).toBe(`cc @${name} `);
    append(ta, 'thanks');
    expect(await send()).toBe(`cc @${REX} thanks`);
  });

  // A name that begins another: each pick is its own, where it was put.
  const ANN = 'U-a1a1a1a1a1a1a1a1';
  const ANNLEE = 'U-a2a2a2a2a2a2a2a2';
  const anns = team({ [ANN]: 'Ann', [ANNLEE]: 'Ann Lee' });
  it('posts "Ann Lee" and "Ann" as two people when one name begins the other', async () => {
    const { ta, send } = await draft(anns, [ANN, ANNLEE]);
    await pick(ta, 'ann', 'Ann Lee');
    append(ta, 'and ');
    await pick(ta, 'ann', 'Ann');
    expect(ta.value).toBe('@Ann Lee and @Ann ');
    // Text typed before both picks moves them; it does not lose them.
    insertAt(ta, 0, 'cc ');
    expect(ta.value).toBe('cc @Ann Lee and @Ann ');
    expect(await send()).toBe(`cc @${ANNLEE} and @${ANN}`);
  });

  it('posts a picked name the author then ran into a longer word as the text they typed', async () => {
    const { ta, send } = await draft(anns, [ANN, ANNLEE]);
    append(ta, 'hi ');
    await pick(ta, 'ann', 'Ann Lee');
    // "@Ann Lee" became "@Ann Leeds": that is not who was picked, and no id is posted for it.
    insertAt(ta, 'hi @Ann Lee'.length, 'ds');
    expect(ta.value).toBe('hi @Ann Leeds ');
    expect(await send()).toBe('hi @Ann Leeds');
  });

  it('posts no id for a pick whose name the author changed', async () => {
    const { ta, send } = await draft(anns, [ANN, ANNLEE]);
    await pick(ta, 'ann', 'Ann Lee');
    // Deleting " Lee" leaves "@Ann": the edit touched the pick, so it is dropped, not posted as Ann Lee.
    type(ta, '@Ann ', 4);
    append(ta, 'ok');
    expect(await send()).toBe('@Ann ok');
  });

  it('posts no id for a pick changed and changed back: an edited pick is no longer a pick', async () => {
    const { ta, send } = await draft(anns, [ANN, ANNLEE]);
    await pick(ta, 'ann', 'Ann Lee');
    type(ta, '@Ann Lex ', 8);
    type(ta, '@Ann Lee ', 8);
    append(ta, 'ok');
    expect(await send()).toBe('@Ann Lee ok');
  });

  // Deleting one of two mentions whose text repeats what follows it: the caret, not the text, says
  // which one went (review round 3, B7).
  it('posts Ann Lee, not Ann, after the first "@Ann " of "@Ann @Ann Lee" is deleted', async () => {
    const { ta, send } = await draft(anns, [ANN, ANNLEE]);
    append(ta, 'C7 ');
    await pick(ta, 'ann', 'Ann');
    await pick(ta, 'ann', 'Ann Lee');
    append(ta, 'ok');
    expect(ta.value).toBe('C7 @Ann @Ann Lee ok');
    cut(ta, 3, 8);
    expect(ta.value).toBe('C7 @Ann Lee ok');
    expect(await send()).toBe(`C7 @${ANNLEE} ok`);
  });

  it('posts the "Sam Lee" left, not the one deleted', async () => {
    const { ta, send } = await draft(team({ [SAM1]: 'Sam Lee', [SAM2]: 'Sam Lee' }), [SAM1, SAM2]);
    append(ta, 'C8 ');
    await pick(ta, 'sam', 'Sam Lee', 'U-5a5a5a5a…');
    append(ta, 'and ');
    await pick(ta, 'sam', 'Sam Lee', 'U-5b5b5b5b…');
    append(ta, 'ok');
    cut(ta, 3, 3 + '@Sam Lee and '.length);
    expect(ta.value).toBe('C8 @Sam Lee ok');
    expect(await send()).toBe(`C8 @${SAM2} ok`);
  });

  it('posts Ann Lee after "@Ann " is deleted one Backspace at a time', async () => {
    const { ta, send } = await draft(anns, [ANN, ANNLEE]);
    await pick(ta, 'ann', 'Ann');
    await pick(ta, 'ann', 'Ann Lee');
    append(ta, 'ok');
    // The caret after "@Ann ", then five Backspaces.
    for (let i = 5; i > 0; i--) cut(ta, i - 1, i);
    expect(ta.value).toBe('@Ann Lee ok');
    expect(await send()).toBe(`@${ANNLEE} ok`);
  });

  it('posts neither id when a deleted selection spans both picks', async () => {
    const { ta, send } = await draft(anns, [ANN, ANNLEE]);
    append(ta, 'hi ');
    await pick(ta, 'ann', 'Ann');
    append(ta, 'and ');
    await pick(ta, 'ann', 'Ann Lee');
    append(ta, 'ok');
    expect(ta.value).toBe('hi @Ann and @Ann Lee ok');
    // From inside "@Ann" to inside "@Ann Lee": what is left names neither.
    cut(ta, 5, 18);
    expect(ta.value).toBe('hi @Aee ok');
    expect(await send()).toBe('hi @Aee ok');
  });

  it('keeps a pick before text an undo restores and selects', async () => {
    const { ta, send } = await draft(anns, [ANN, ANNLEE]);
    await pick(ta, 'ann', 'Ann');
    append(ta, 'ok');
    // An undo puts back "@Ann Lee " after "@Ann " and selects it: the selection's end is where the
    // restored text ends, so "@Ann" before it is untouched.
    type(ta, '@Ann @Ann Lee ok', 5, 14);
    expect(await send()).toBe(`@${ANN} @Ann Lee ok`);
  });

  it('keeps a pick where it was when the same text is pasted before it', async () => {
    const { ta, send } = await draft(anns, [ANN, ANNLEE]);
    await pick(ta, 'ann', 'Ann');
    append(ta, 'ok');
    // The paste starts where the caret was, at 0: the pick moves past it, onto its own text.
    insertAt(ta, 0, '@Ann ');
    expect(ta.value).toBe('@Ann @Ann ok');
    expect(await send()).toBe(`@Ann @${ANN} ok`);
  });

  it('moves a pick when a mention is picked before it', async () => {
    const { ta, send } = await draft(anns, [ANN, ANNLEE]);
    await pick(ta, 'ann', 'Ann');
    append(ta, 'ok');
    insertAt(ta, 0, '@ann');
    await waitFor(() => expect(menu().some(([n]) => n === 'Ann Lee')).toBe(true));
    fireEvent.click(document.querySelectorAll('.assign-menu .assign-item')[menu().findIndex(([n]) => n === 'Ann Lee')]!);
    expect(ta.value).toBe('@Ann Lee @Ann ok');
    expect(await send()).toBe(`@${ANNLEE} @${ANN} ok`);
  });

  it('posts no id for a pick whose "@" a replaced selection took', async () => {
    const { ta, send } = await draft(anns, [ANN, ANNLEE]);
    append(ta, 'hi ');
    await pick(ta, 'ann', 'Ann Lee');
    append(ta, 'ok');
    // "hi @" selected and replaced by a typed "@": the edit touched the pick, so it is dropped.
    type(ta, '@Ann Lee ok', 1);
    expect(await send()).toBe('@Ann Lee ok');
  });

  it('posts an agent label holding "@" as inserted', async () => {
    const { ta, send } = await draft(project, ['bot@ci']);
    append(ta, '@bo');
    const item = await waitFor(() => {
      const b = document.querySelector('.assign-menu .assign-item');
      expect(b).not.toBeNull();
      return b!;
    });
    fireEvent.click(item);
    expect(ta.value).toBe('@bot@ci ');
    append(ta, 'ok');
    expect(await send()).toBe('@bot@ci ok');
  });

  it('posts no id for a pick the author joined onto the word before it', async () => {
    const { ta, send } = await draft(anns, [ANN, ANNLEE]);
    append(ta, 'hi ');
    await pick(ta, 'ann', 'Ann Lee');
    // "hi@Ann Lee" is not a mention to the server, so no id is posted into it.
    type(ta, 'hi@Ann Lee ', 2);
    expect(await send()).toBe('hi@Ann Lee');
  });

  it("render a body's account-id mentions by name, and shorten an unnamed one", async () => {
    routed([{ id: 'c1', taskId: 'T-1', author: BOB, body: `@${ALICE} please look, and @${GHOST} too`, parentCommentId: null, mentions: [ALICE, GHOST], ts: TS }]);
    wrap(<Comments projectId="p1" taskId="T-1" mentionCandidates={[]} canComment />);
    await screen.findByText(/please look/);
    await waitFor(() => expect(screen.getByText(/please look/).textContent).toBe(`@Alice Doe please look, and @${GHOST_SHORT} too`));
    noRawIds(document.body);
  });

  it('render a name as text: markdown and HTML in it are not read', async () => {
    const MO = 'U-3c3c3c3c3c3c3c3c';
    const HAL = 'U-4d4d4d4d4d4d4d4d';
    const display = { ...memberDisplay, [MO]: 'Mo *ops* [link](http://evil.example)', [HAL]: '<i>Hal</i> <script>x</script>' };
    routed([{ id: 'c1', taskId: 'T-1', author: BOB, body: `ping @${MO} and @${HAL} now`, parentCommentId: null, mentions: [MO, HAL], ts: TS }],
      { ...project, memberDisplay: display });
    wrap(<Comments projectId="p1" taskId="T-1" mentionCandidates={[]} canComment />);
    const md = await waitFor(() => {
      const el = document.querySelector('.md')!;
      expect(el.textContent).toBe('ping @Mo *ops* [link](http://evil.example) and @<i>Hal</i> <script>x</script> now');
      return el;
    });
    for (const tag of ['em', 'a', 'i', 'script']) expect(md.querySelector(tag), `<${tag}> rendered`).toBeNull();
  });

  it("leave an id the author wrote as code, and one the server does not read as that id, as written", async () => {
    const body = [
      `Use \`@${ALICE}\` here, and @${ALICE}x, @${ALICE}_y, @${ALICE}.z and @${ALICE}@ex.example stay, as does mail@${ALICE}; @${ALICE}. ends.`,
      '',
      '```',
      `@${ALICE}`,
      '```',
    ].join('\n');
    routed([{ id: 'c1', taskId: 'T-1', author: BOB, body, parentCommentId: null, mentions: [ALICE], ts: TS }]);
    wrap(<Comments projectId="p1" taskId="T-1" mentionCandidates={[]} canComment />);
    const md = await waitFor(() => {
      const el = document.querySelector('.md')!;
      expect(el.querySelector('pre')).not.toBeNull();
      return el;
    });
    expect(md.querySelector('p')!.textContent).toBe(
      `Use @${ALICE} here, and @${ALICE}x, @${ALICE}_y, @${ALICE}.z and @${ALICE}@ex.example stay, as does mail@${ALICE}; @Alice Doe. ends.`,
    );
    expect(md.querySelector('p code')!.textContent).toBe(`@${ALICE}`);
    expect(md.querySelector('pre')!.textContent!.trim()).toBe(`@${ALICE}`);
  });
});

describe('movePicks after deleting one of two mentions (review round 3 probe)', () => {
  const A = 'U-aaaaaaaaaaaaaaaa';
  const B = 'U-bbbbbbbbbbbbbbbb';
  /** Delete [from, to) from `prev`, as Backspace over that selection does: the caret is left at `from`. */
  const del = (picks: Pick[], prev: string, from: number, to: number) => {
    const next = prev.slice(0, from) + prev.slice(to);
    return { next, picks: movePicks(picks, prev, next, from) };
  };
  it('prefix names: delete "@Ann " (A) of "@Ann @Ann Lee ok"; what is left is Ann Lee (B)', () => {
    const r = del([{ at: 0, name: 'Ann', id: A }, { at: 5, name: 'Ann Lee', id: B }], '@Ann @Ann Lee ok', 0, 5);
    expect(r.next).toBe('@Ann Lee ok');
    expect(wireBody(r.next, r.picks)).toBe(`@${B} ok`);
  });
  it('same name: delete the first "@Sam Lee and " (A); what is left is B', () => {
    const r = del([{ at: 0, name: 'Sam Lee', id: A }, { at: 13, name: 'Sam Lee', id: B }], '@Sam Lee and @Sam Lee ok', 0, 13);
    expect(r.next).toBe('@Sam Lee ok');
    expect(wireBody(r.next, r.picks)).toBe(`@${B} ok`);
  });
  it('same name mid-text: delete "@Sam Lee " (A) of "hi @Sam Lee @Sam Lee ok"', () => {
    const r = del([{ at: 3, name: 'Sam Lee', id: A }, { at: 12, name: 'Sam Lee', id: B }], 'hi @Sam Lee @Sam Lee ok', 3, 12);
    expect(r.next).toBe('hi @Sam Lee ok');
    expect(wireBody(r.next, r.picks)).toBe(`hi @${B} ok`);
  });
});

describe('follow-ups', () => {
  it("name the author, the target and the composer's holder", async () => {
    const fu: FollowUp = { id: 'f1', taskId: 'T-1', projectId: 'p1', body: 'please rebase', author: ALICE, target: BOB, status: 'open', reopened: false, ts: TS };
    byPath({ '/api/projects/p1': project, '/api/projects/p1/tasks/T-1/follow-ups': { followUps: [fu] } });
    wrap(<FollowUps projectId="p1" task={{ ...task, assignee: BOB }} identity={null} />);
    await screen.findByText('Alice Doe');
    expect(screen.getByText('→ bob')).toBeInTheDocument();
    expect(screen.getByTitle('directed at bob')).toBeInTheDocument();
    await waitFor(() => expect(screen.getByPlaceholderText('Add a follow-up for bob…')).toBeInTheDocument());
    noRawIds(document.body);
  });
});

describe('notifications', () => {
  it("name the actor from the caller's projects, and shorten an unnamed one", async () => {
    const n = (id: string, type: string, actor: string, taskId: string): Notification => (
      { id, identityId: BOB, projectId: 'p1', boardId: 'b1', type, taskId, actor, read: false, ts: TS });
    byPath({
      '/api/projects': { projects: [{ projectId: 'p1', name: 'P1', role: 'worker', memberDisplay }] },
      '/api/notifications': { notifications: [n('n1', 'handoff', ALICE, 'T-2'), n('n2', 'mention', GHOST, 'T-3')], unread: 2 },
    });
    wrap(<ProjectProvider><Notifications /></ProjectProvider>);
    await screen.findByText('Alice Doe handed you T-2');
    expect(screen.getByText(`${GHOST_SHORT} mentioned you in a comment`)).toBeInTheDocument();
    expect(screen.getByLabelText('human Alice Doe')).toBeInTheDocument();
    noRawIds(document.body);
  });
});

describe("the ticket header's created by", () => {
  const mine: Task = { ...task, createdBy: ALICE };
  const routes = { '/tasks/T-1$': { json: { task: mine } }, '/projects/p1$': { json: project } };

  it('names the creator on the drawer', async () => {
    vi.stubGlobal('fetch', routeFetch(routes));
    render(
      <QueryClientProvider client={qc()}>
        <AuthProvider><TaskDrawer projectId="p1" taskId="T-1" onClose={vi.fn()} /></AuthProvider>
      </QueryClientProvider>,
    );
    const dlg = await screen.findByRole('dialog', { name: 'Task T-1' });
    await waitFor(() => expect(within(dlg).getByText('Created by Alice Doe')).toHaveAttribute('title', 'Alice Doe'));
    noRawIds(dlg);
  });

  it('names the creator on the page', async () => {
    vi.stubGlobal('fetch', routeFetch(routes));
    render(
      <QueryClientProvider client={qc()}>
        <MemoryRouter initialEntries={['/projects/p1/boards/b1/tasks/T-1']}>
          <AuthProvider><ProjectProvider>
            <Routes><Route path="/projects/:projectId/boards/:boardId/tasks/:taskId" element={<TaskPage />} /></Routes>
          </ProjectProvider></AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    await waitFor(() => expect(document.querySelector('.task-head')?.textContent).toContain('· created by Alice Doe'));
    noRawIds(document.querySelector('.task-head') as HTMLElement);
  });
});

describe('the Agents handoff list', () => {
  it('names both ends of a handoff', async () => {
    localStorage.setItem('bd.projectId', 'p1');
    byPath({
      '/api/projects': { projects: [{ projectId: 'p1', name: 'P1', role: 'admin', memberDisplay }] },
      '/api/projects/p1': project,
      '/api/projects/p1/boards': { boards: [{ id: 'b1', projectId: 'p1', name: 'Board' }] },
      '/api/projects/p1/boards/b1/tasks': { tasks: [] },
      '/api/projects/p1/boards/b1/feed': {
        events: [{ id: 'e1', type: 'task.handoff', actor: ALICE, agent: null, taskId: 'T-2', data: { toAgent: BOB, note: '' }, ts: TS }],
      },
    });
    wrap(<ProjectProvider><Agents /></ProjectProvider>);
    const heading = await screen.findByRole('heading', { name: /Recent Handoffs/ });
    const panel = heading.parentElement as HTMLElement;
    await waitFor(() => expect(panel.textContent).toContain('Alice Doe→bob'));
    noRawIds(panel);
  });
});

describe('the sidebar identity chip', () => {
  function mount(session: Record<string, unknown>, projects: unknown[]): void {
    byPath({
      '/api/session': { identity: ALICE, csrfToken: 'c', isAdmin: false, permissions: [], subscriptionsEnabled: false, uiSettings: {}, ...session },
      '/api/projects': { projects },
      '/api/me/settings': { maxTicketTokens: null, tokenPolicy: null, starredAssets: [], defaults: { maxTicketTokens: 50000, tokenPolicy: 'warn', charsPerToken: 4 }, estimated: true },
    });
    render(
      <QueryClientProvider client={qc()}>
        <MemoryRouter initialEntries={['/settings']}>
          <AuthProvider><App /></AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  }
  const chip = async () => (await screen.findByRole('button', { expanded: false, name: /Alice Doe|U-/ })) as HTMLElement;

  it("shows the session's displayName for an account in no project", async () => {
    mount({ displayName: 'Alice Doe' }, []);
    const c = await chip();
    await waitFor(() => expect(c.querySelector('.identity-email')?.textContent).toBe('Alice Doe'));
    expect(c).toHaveAttribute('title', 'Alice Doe');
    noRawIds(c);
  });

  it("falls back to a project's memberDisplay when the session has no displayName", async () => {
    mount({}, [{ projectId: 'p1', name: 'P1', role: 'worker', memberDisplay }]);
    const c = await chip();
    await waitFor(() => expect(c.querySelector('.identity-email')?.textContent).toBe('Alice Doe'));
    noRawIds(c);
  });
});
