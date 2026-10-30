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
import { Comments } from '../screens/Comments.js';
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
    wrap(<Comments projectId="p1" taskId="T-1" mentionCandidates={[]} />);
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
  function routed(comments: unknown[]): Array<[string, Record<string, unknown>]> {
    const posts: Array<[string, Record<string, unknown>]> = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const p = String(input).split('?')[0]!;
        if (init?.method === 'POST') posts.push([p, JSON.parse(String(init.body)) as Record<string, unknown>]);
        const routes: Record<string, unknown> = { '/api/projects/p1': project, '/api/projects/p1/tasks/T-1/comments': { comments } };
        return { ok: true, status: 200, json: async () => routes[p] ?? {} } as Response;
      }),
    );
    return posts;
  }
  const type = (ta: HTMLTextAreaElement, text: string): void => {
    fireEvent.change(ta, { target: { value: text, selectionStart: text.length } });
  };

  it('suggests people by name, and a picked name posts a mention the server resolves', async () => {
    const posts = routed([]);
    wrap(<Comments projectId="p1" taskId="T-1" mentionCandidates={[ALICE, BOB, GHOST]} />);
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
    // "bob" is one token, which the server resolves as his alias: it is posted as is.
    expect(ta.value).toBe('see @Alice Doe and @bob ');
    // A member nothing names is offered by its shortened id, and posted by the whole one.
    type(ta, `${ta.value}and @U-9f`);
    fireEvent.click(await screen.findByRole('button', { name: new RegExp(GHOST_SHORT) }));
    expect(ta.value).toBe(`see @Alice Doe and @bob and @${GHOST_SHORT} `);
    type(ta, `see @Alice Doe and @bob, and @${GHOST_SHORT} please.`);
    noRawIds(document.body);
    fireEvent.click(screen.getByRole('button', { name: 'Comment' }));
    await waitFor(() => expect(posts.length).toBe(1));
    expect(posts[0]).toEqual(['/api/projects/p1/tasks/T-1/comments', { body: `see @${ALICE} and @bob, and @${GHOST} please.` }]);
  });

  it("render a body's account-id mentions by name, and shorten an unnamed one", async () => {
    routed([{ id: 'c1', taskId: 'T-1', author: BOB, body: `@${ALICE} please look, and @${GHOST} too`, parentCommentId: null, mentions: [ALICE, GHOST], ts: TS }]);
    wrap(<Comments projectId="p1" taskId="T-1" mentionCandidates={[]} />);
    await screen.findByText(/please look/);
    await waitFor(() => expect(screen.getByText(/please look/).textContent).toBe(`@Alice Doe please look, and @${GHOST_SHORT} too`));
    noRawIds(document.body);
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
