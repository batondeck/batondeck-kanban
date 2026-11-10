/**
 * TaskDrawer integration tests (jsdom + mocked fetch). Cover the ticket-details behaviors added in the
 * drawer revamp: markdown description, header metadata chips, linked-task navigation (dep chips,
 * subtasks), Escape-blurs-instead-of-closing, and the bad-deep-link error state.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, cleanup, within, waitFor } from '@testing-library/react';
import { TaskDrawer } from '../screens/TaskDrawer.js';
import { AuthProvider } from '../auth.js';
import { routeFetch, task } from './taskFixtures.js';
import type { Task } from '../types.js';

const now = Date.now();

function renderDrawer(props: Partial<Parameters<typeof TaskDrawer>[0]> = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const onClose = vi.fn();
  const onOpenTask = vi.fn();
  const utils = render(
    <QueryClientProvider client={qc}>
      <AuthProvider>
        <TaskDrawer projectId="p1" taskId="T-1" onClose={onClose} onOpenTask={onOpenTask} {...props} />
      </AuthProvider>
    </QueryClientProvider>,
  );
  return { qc, onClose, onOpenTask, ...utils };
}

beforeEach(() => {
  vi.stubGlobal('fetch', routeFetch());
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('TaskDrawer — ticket details', () => {
  it('renders the title, markdown description, and header metadata chips', async () => {
    renderDrawer();
    expect(await screen.findByText('Ship the parser')).toBeInTheDocument();
    // markdown: **tokenizer** renders as <strong>; links open in a new tab (SPA must not navigate away)
    const strong = await screen.findByText('tokenizer');
    expect(strong.tagName).toBe('STRONG');
    const link = screen.getByRole('link', { name: 'the spec' });
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noreferrer');
    // overdue due-date chip, estimate chip, label + capability chips, provenance line
    expect(screen.getByText(/Overdue/)).toBeInTheDocument();
    expect(screen.getByText('est 3')).toBeInTheDocument();
    // A label renders in the header chips AND as a Details chip now, so this asserts presence
    // rather than uniqueness — the header row is the thing under test here.
    expect(screen.getAllByText('backend').length).toBeGreaterThan(0);
    // Capability chip. Like `backend` above it now appears in the header AND in Details, so this
    // asserts presence rather than uniqueness. (The gear that used to disambiguate it was an emoji;
    // it is an aria-hidden <Icon> now, so it is correctly absent from the accessible text.)
    expect(screen.getAllByText(/^\s*python\s*$/).length).toBeGreaterThan(0);
    // model-hint chip: model + effort, rationale as tooltip
    expect(screen.getByText(/claude-opus-4-8 · high/).closest('[title]')).toHaveAttribute('title', 'deep refactor across modules');
    // provenance shows the email's local part; the tooltip keeps the full address
    const prov = screen.getByText(/Created by alice/);
    expect(prov).not.toHaveTextContent('@example.com');
    expect(prov).toHaveAttribute('title', 'alice@example.com');
  });

  it('renders the section nav and scrolls to an existing anchor on click', async () => {
    renderDrawer();
    await screen.findByText('Ship the parser');
    const nav = screen.getByRole('navigation', { name: 'Sections' });
    for (const s of ['Details', 'Discussion', 'Context', 'Files', 'Activity']) {
      expect(within(nav).getByRole('button', { name: s })).toBeInTheDocument();
    }
    // jsdom has no scrollIntoView — stub it to prove the click reaches a real, mounted anchor.
    const spy = vi.fn();
    Element.prototype.scrollIntoView = spy;
    fireEvent.click(within(nav).getByRole('button', { name: 'Files' }));
    expect(spy).toHaveBeenCalledWith({ behavior: 'smooth', block: 'start' });
  });

  it('moves the task via the header status select (board-gated targets)', async () => {
    // IN_PROGRESS → REVIEW, BLOCKED, TODO; the fixture board has To do and Building columns only.
    const fetchMock = routeFetch({ '/tasks/T-1$': { json: { task: { ...task, status: 'IN_PROGRESS', columnId: 'c2' } } } });
    vi.stubGlobal('fetch', fetchMock);
    renderDrawer();
    await screen.findByText('Ship the parser');
    const sel = screen.getByLabelText('Status');
    expect(sel).toHaveValue('IN_PROGRESS');
    // once the board loads, only statuses with a column remain (REVIEW and BLOCKED drop out)
    await waitFor(() => expect(within(sel).queryByRole('option', { name: 'REVIEW' })).not.toBeInTheDocument());
    expect(within(sel).queryByRole('option', { name: 'BLOCKED' })).not.toBeInTheDocument();
    await within(sel).findByRole('option', { name: 'TODO' });
    // The fixture's session is alice, the project's admin: the select opens once her role loads (P-209).
    await waitFor(() => expect(sel).toBeEnabled());
    fireEvent.change(sel, { target: { value: 'TODO' } });
    await waitFor(() => {
      const call = (fetchMock as ReturnType<typeof vi.fn>).mock.calls.find(([u]) => String(u).endsWith('/tasks/T-1/move'));
      expect(call).toBeTruthy();
      expect(JSON.parse((call![1] as RequestInit).body as string)).toMatchObject({ version: 3, toStatus: 'TODO' });
    });
  });

  it('surfaces effort/rank/model customFields as meta chips instead of deletable Fields rows', async () => {
    vi.stubGlobal('fetch', routeFetch({ '\\/tasks\\/T-1$': { json: { task: { ...task, customFields: { effort: 'M', rank: 7, repo: 'web' } } } } }));
    renderDrawer();
    await screen.findByText('Ship the parser');
    expect(screen.getByText(/^\s*effort M\s*$/)).toBeInTheDocument();
    expect(screen.getByText(/^\s*rank 7\s*$/)).toBeInTheDocument();
    expect(screen.queryByText('effort')).not.toBeInTheDocument(); // no bare Fields row for effort
    // the ordinary custom field keeps its Fields row + delete affordance
    expect(screen.getByText('repo')).toBeInTheDocument();
    // The delete control is now named for WHAT it removes rather than being a bare '×' — the same
    // affordance, with an accessible name a screen reader can act on.
    expect(screen.getByRole('button', { name: 'Remove repo' })).toBeInTheDocument();
  });

  it('renders no model-hint chip when the task has none', async () => {
    vi.stubGlobal('fetch', routeFetch({ '\\/tasks\\/T-1$': { json: { task: { ...task, modelHint: undefined } } } }));
    renderDrawer();
    await screen.findByText('Ship the parser');
    expect(screen.queryByText(/claude-opus-4-8/)).not.toBeInTheDocument();
  });

  it('prefers the creating agent over the human identity in the provenance line', async () => {
    vi.stubGlobal('fetch', routeFetch({ '\\/tasks\\/T-1$': { json: { task: { ...task, createdByAgent: 'worthy-gayal' } } } }));
    renderDrawer();
    await screen.findByText('Ship the parser');
    const line = screen.getByText(/Created by/);
    expect(line).toHaveTextContent('worthy-gayal');
    expect(line).not.toHaveTextContent('alice@example.com');
  });

  it('resolves dependency chips to the linked task and navigates on click', async () => {
    const { onOpenTask } = renderDrawer();
    // The blocked-by chip shows the linked task's title (resolved from the board cache)…
    const chip = await screen.findByTitle('Upstream schema — open T-9');
    expect(chip).toHaveTextContent('T-9');
    expect(chip).toHaveTextContent('Upstream schema');
    // …and clicking it walks the drawer to that task.
    fireEvent.click(chip);
    expect(onOpenTask).toHaveBeenCalledWith('T-9');
  });

  it('navigates to a subtask from its title and shows its status pill', async () => {
    const { onOpenTask } = renderDrawer();
    const sub = await screen.findByRole('button', { name: 'Write tests' });
    // IN_PROGRESS subtasks surface a status pill (DONE/TODO stay quiet) — scoped to the row
    const row = sub.closest('div')!; // button → span → row div
    expect(within(row).getByText('IN PROGRESS')).toBeInTheDocument();
    // the checkbox is separately labeled (no row-wide <label> that would toggle on any click)
    expect(within(row).getByRole('checkbox', { name: 'Mark T-2 done' })).not.toBeChecked();
    fireEvent.click(sub);
    expect(onOpenTask).toHaveBeenCalledWith('T-2');
  });

  it('Escape while typing blurs the field instead of closing the drawer', async () => {
    const { onClose } = renderDrawer();
    await screen.findByText('Ship the parser');
    const note = screen.getByPlaceholderText('Add a note…');
    note.focus();
    fireEvent.keyDown(note, { key: 'Escape' });
    expect(onClose).not.toHaveBeenCalled();
    expect(document.activeElement).not.toBe(note);
    // a second Escape (no field focused) closes
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('Escape cancels a pending Fields edit instead of committing it on blur', async () => {
    const fetchMock = routeFetch();
    vi.stubGlobal('fetch', fetchMock);
    renderDrawer();
    await screen.findByText('Ship the parser');
    // Labels moved to a CHIP field (a value is committed by Enter/comma, removed by its own ✕), so
    // the save-on-blur inputs this guards are now Assignee, Due and Estimate. Due is the one with the
    // subtle failure — see below — so it carries the assertion.
    const patchCount = (): number =>
      (fetchMock as ReturnType<typeof vi.fn>).mock.calls.filter(([, init]) => (init as RequestInit | undefined)?.method === 'PATCH').length;
    const assignee = screen.getByPlaceholderText('agent name, email, or SA');
    assignee.focus();
    fireEvent.change(assignee, { target: { value: 'oops' } });
    fireEvent.keyDown(assignee, { key: 'Escape' });
    expect(document.activeElement).not.toBe(assignee); // the blur really happened
    expect(patchCount()).toBe(0);
    expect(assignee).toHaveValue('');
    // Due field: the fixture's dueAt carries a TIME component; Escape (reset + blur) must not fire a
    // silent midnight-UTC normalization PATCH — the blur compares the displayed day, not the ISO.
    const due = document.querySelector('input[type="date"]') as HTMLInputElement;
    due.focus();
    fireEvent.keyDown(due, { key: 'Escape' });
    expect(document.activeElement).not.toBe(due);
    expect(patchCount()).toBe(0);
  });

  it('Escape with the @mention popup open dismisses only the popup (keeps the caret)', async () => {
    const { onClose } = renderDrawer();
    await screen.findByText('Ship the parser');
    const ta = screen.getByPlaceholderText(/Write a comment/);
    ta.focus();
    fireEvent.change(ta, { target: { value: '@ali', selectionStart: 4 } });
    // The suggestion is the member's name as the comments show it (P-314): a login's local part.
    expect(await screen.findByRole('button', { name: /^human alice\s+alice$/ })).toBeInTheDocument(); // suggestion appears
    fireEvent.keyDown(ta, { key: 'Escape' });
    expect(screen.queryByRole('button', { name: /^human alice\s+alice$/ })).not.toBeInTheDocument(); // popup gone…
    expect(document.activeElement).toBe(ta); // …composer keeps focus
    expect(onClose).not.toHaveBeenCalled();
  });

  it('shows an error state (not an infinite skeleton) when the task fails to load', async () => {
    vi.stubGlobal('fetch', routeFetch({ '\\/tasks\\/T-1$': { status: 404, json: { code: 'NOT_FOUND', message: 'no such task' } } }));
    const { onClose } = renderDrawer();
    expect(await screen.findByText(/Couldn.t open T-1/)).toBeInTheDocument();
    expect(screen.getByText('no such task')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Back to board'));
    expect(onClose).toHaveBeenCalled();
  });

  it('does not clear the note input when adding a context item fails', async () => {
    vi.stubGlobal('fetch', routeFetch({ '\\/tasks\\/T-1\\/context-items$': { status: 500, json: { code: 'INTERNAL', message: 'kaboom' } } }));
    renderDrawer();
    await screen.findByText('Ship the parser');
    const note = screen.getByPlaceholderText('Add a note…');
    fireEvent.change(note, { target: { value: 'important detail' } });
    fireEvent.keyDown(note, { key: 'Enter' });
    expect(await screen.findByText('kaboom')).toBeInTheDocument();
    expect(note).toHaveValue('important detail');
  });

  it('partitions link items into Links (URL card / plain-text fallback) and gates the Attach input', async () => {
    const ts = new Date(now).toISOString();
    vi.stubGlobal('fetch', routeFetch({
      '\\/tasks\\/T-1\\/context$': {
        json: {
          summary: '', fields: {},
          items: [
            { id: 'ci1', kind: 'note', body: 'plain note', author: 'alice@example.com', ts },
            { id: 'ci2', kind: 'link', body: 'https://www.figma.com/file/xyz', author: 'alice@example.com', ts },
            { id: 'ci3', kind: 'link', body: 'Design: https://lucid.app/x', author: 'alice@example.com', ts }, // legacy non-bare-URL body
          ],
          memory: [], deps: { blockedBy: [], blocks: [], edges: [] },
        },
      },
    }));
    renderDrawer();
    await screen.findByText('Ship the parser');
    // URL body → clickable card, new tab; recognized app name shown.
    const card = await screen.findByRole('link', { name: /Figma/ });
    expect(card).toHaveAttribute('href', 'https://www.figma.com/file/xyz');
    expect(card).toHaveAttribute('target', '_blank');
    // Non-URL body is NOT silently dropped — renders as plain text.
    expect(screen.getByText(/Design: https:\/\/lucid\.app\/x/)).toBeInTheDocument();
    // The note stays in Context (not in Links).
    expect(screen.getByText('plain note')).toBeInTheDocument();
    // Attach gate: prose disables + hints; a bare domain is accepted (https:// gets prefixed).
    const input = screen.getByPlaceholderText(/Paste a link/);
    fireEvent.change(input, { target: { value: 'not a url' } });
    expect(screen.getByRole('button', { name: 'Attach' })).toBeDisabled();
    expect(screen.getByText(/Not a valid URL/)).toBeInTheDocument();
    fireEvent.change(input, { target: { value: 'figma.com/file/2' } });
    expect(screen.getByRole('button', { name: 'Attach' })).not.toBeDisabled();
  });
});

describe('TaskDrawer — Delivered (T-52)', () => {
  it('renders the deliverable and the produced artifacts, including a ref with no forge URL', async () => {
    const delivered: Task = {
      ...task,
      status: 'DONE',
      deliverable: 'Parser rewritten; 12 tests added.',
      artifacts: [
        { kind: 'pr', url: 'https://gitlab.com/acme/app/-/merge_requests/9', ref: null, repo: 'gitlab.com/acme/app', title: null, addedBy: 'alice@example.com', addedByAgent: null, ts: new Date(now).toISOString() },
        { kind: 'commit', url: null, ref: '9f1c2c6', repo: null, title: null, addedBy: 'alice@example.com', addedByAgent: null, ts: new Date(now).toISOString() },
      ],
    };
    vi.stubGlobal('fetch', routeFetch({ '/tasks/T-1$': { json: { task: delivered } } }));
    renderDrawer();
    // The richest field on a finished ticket used to be invisible here.
    expect(await screen.findByText('Parser rewritten; 12 tests added.')).toBeInTheDocument();
    // A url artifact becomes a smart link card — GitLab is recognized, not rendered as a bare host.
    const card = await screen.findByRole('link', { name: /GitLab/ });
    expect(card).toHaveAttribute('href', 'https://gitlab.com/acme/app/-/merge_requests/9');
    // A url-less artifact (no remote) still renders its ref rather than vanishing.
    expect(screen.getByText('9f1c2c6')).toBeInTheDocument();
  });

  it('renders no Delivered section on a ticket with neither', async () => {
    renderDrawer();
    await screen.findByText('Ship the parser');
    expect(screen.queryByText('Delivered')).not.toBeInTheDocument();
  });
});

describe('TaskDrawer — Review ownership (T-52 Part B)', () => {
  it('shows who owns the review and how long it has waited', async () => {
    const inReview: Task = {
      ...task,
      status: 'REVIEW',
      assignee: 'bob@example.com',
      review: {
        reviewer: 'bob@example.com',
        completedBy: 'alice@example.com',
        completedByAgent: null,
        requestedBy: 'alice@example.com',
        requestedAt: new Date(now - 3 * 3600_000).toISOString(),
      },
    };
    vi.stubGlobal('fetch', routeFetch({ '/tasks/T-1$': { json: { task: inReview } } }));
    renderDrawer();
    expect(await screen.findByText(/owns this review/)).toBeInTheDocument();
    expect(screen.getByText(/requested .* · completed by alice/)).toBeInTheDocument();
  });

  it('calls out an UNOWNED review rather than rendering nothing', async () => {
    const unowned: Task = {
      ...task,
      status: 'REVIEW',
      review: {
        reviewer: null,
        completedBy: 'alice@example.com',
        completedByAgent: null,
        requestedBy: 'alice@example.com',
        requestedAt: new Date(now - 60_000).toISOString(),
      },
    };
    vi.stubGlobal('fetch', routeFetch({ '/tasks/T-1$': { json: { task: unowned } } }));
    renderDrawer();
    expect(await screen.findByText(/Nobody owns this review/)).toBeInTheDocument();
  });

  /**
   * P-431: `completedBy` is the account id the core stored, and the panel printed it as given
   * ("completed by U-4e68…"). It names the person the way the rest of the ticket does: the member's
   * name, else their login, else the shortened id when nothing names the account.
   */
  describe('names who completed it (P-431)', () => {
    const WES = 'U-4e68a1b2c3d4e5f6a7b8';
    const completedBy = (who: string): Task => ({
      ...task,
      status: 'REVIEW',
      review: { reviewer: null, completedBy: who, completedByAgent: null, requestedBy: who, requestedAt: new Date(now - 60_000).toISOString() },
    });
    const projectWith = (memberDisplay: Record<string, string>) => ({
      json: {
        project: { id: 'p1', name: 'P1', members: { 'alice@example.com': 'admin', [WES]: 'worker' }, settings: { reviewEnabled: true, quotaEnabled: false, txnCap: null } },
        memberDisplay,
      },
    });

    it("shows the member's name, never the account id", async () => {
      vi.stubGlobal('fetch', routeFetch({ '/tasks/T-1$': { json: { task: completedBy(WES) } }, '/projects/p1$': projectWith({ [WES]: 'Wes Worker' }) }));
      renderDrawer();
      expect(await screen.findByText(/completed by Wes Worker$/)).toBeInTheDocument();
      expect(screen.queryByText(new RegExp(`completed by ${WES.slice(0, 6)}`))).not.toBeInTheDocument();
    });

    it('falls back to the login (its local part, as the header does) when the member has no name', async () => {
      vi.stubGlobal('fetch', routeFetch({ '/tasks/T-1$': { json: { task: completedBy(WES) } }, '/projects/p1$': projectWith({ [WES]: 'wes@example.com' }) }));
      renderDrawer();
      expect(await screen.findByText(/completed by wes$/)).toBeInTheDocument();
    });

    it('shows the shortened id only when nothing names the account', async () => {
      vi.stubGlobal('fetch', routeFetch({ '/tasks/T-1$': { json: { task: completedBy(WES) } }, '/projects/p1$': projectWith({}) }));
      renderDrawer();
      expect(await screen.findByText(`requested 1m ago · completed by ${WES.slice(0, 10)}…`, { exact: false })).toBeInTheDocument();
    });

    // P-457: on this board the one who finished is usually an agent working with a person's token.
    // The payload names it (review.completedByAgent); the panel showed only the account.
    it('leads with the agent that completed it, then the account in brackets', async () => {
      const byAgent: Task = { ...completedBy(WES), review: { ...completedBy(WES).review!, completedByAgent: 'codex-cli' } };
      vi.stubGlobal('fetch', routeFetch({ '/tasks/T-1$': { json: { task: byAgent } }, '/projects/p1$': projectWith({ [WES]: 'Wes Worker' }) }));
      renderDrawer();
      expect(await screen.findByText(/completed by codex-cli \(Wes Worker\)$/)).toBeInTheDocument();
      expect(screen.queryByText(new RegExp(WES))).not.toBeInTheDocument();
    });
  });
});

/**
 * T-51 follow-through: the drawer's three `Marquee` sites (dependency-chip title, parent breadcrumb,
 * activity actor) were shipped without ever being SEEN rendered — the demo tasks had no dependencies,
 * parent or history. The dep chip is already covered above ("resolves dependency chips…", which asserts
 * the linked title reaches the DOM through Marquee); these close the other two. jsdom has no layout, so
 * what is proven is that the component mounts and keeps the full text — not the motion.
 */
describe('TaskDrawer — Marquee sites (T-51)', () => {
  it('renders the parent breadcrumb and the activity actor through Marquee, text intact', async () => {
    const child: Task = { ...task, parentTaskId: 'T-9' };
    vi.stubGlobal('fetch', routeFetch({
      '/tasks/T-1$': { json: { task: child } },
      '\\/boards\\/b1\\/feed$': {
        json: {
          events: [{
            id: 'ev1', projectId: 'p1', boardId: 'b1', taskId: 'T-1',
            type: 'task.updated', actor: 'a-very-long-agent-name@example.com',
            data: {}, ts: new Date(now - 30_000).toISOString(),
          }],
        },
      },
    }));
    renderDrawer();
    // Parent breadcrumb: resolved from the board cache, rendered inside a marquee track (no aria-label
    // — the enclosing button already names it), with the parent's FULL title present.
    // The dep chip renders the same title, so pick the breadcrumb by its leading em dash.
    const crumb = (await screen.findAllByText(/Upstream schema/)).find((el) => el.textContent?.startsWith('\u2014 '));
    expect(crumb, 'parent breadcrumb did not render').toBeDefined();
    expect(crumb!).toHaveClass('marquee-track');
    expect(crumb!.parentElement).toHaveClass('marquee');
    // Activity actor: the rail is collapsed by default, so open it first.
    fireEvent.click(await screen.findByRole('button', { name: /Recent Activity/ }));
    // Labelled, so a screen reader gets the whole identity however narrow the rail is.
    const actor = await screen.findByRole('group', { name: 'a-very-long-agent-name@example.com' });
    expect(actor).toHaveClass('marquee');
    expect(actor).toHaveTextContent('a-very-long-agent-name@example.com');
  });
});

/**
 * GFM ON THE TICKET SURFACES.
 *
 * `remark-gfm` was a declared dependency that only the Chronicle passed, so the same bytes rendered
 * as a table there and as a flat paragraph on a ticket. Agents write pipe tables in briefs — that
 * asymmetry made a brief unreadable on the surface it was written for.
 */
describe('markdown on a ticket is GFM', () => {
  const TABLE = ['| check | result |', '| --- | --- |', '| lint | pass |'].join('\n');

  it('renders a pipe table in the description', async () => {
    vi.stubGlobal('fetch', routeFetch({ '/tasks/T-1$': { json: { task: { ...task, description: TABLE } } } }));
    renderDrawer();
    await screen.findByText('Ship the parser');
    const table = document.querySelector('.md table');
    expect(table, 'a GFM pipe table must render as a table, not a paragraph').toBeTruthy();
    expect(table!.querySelectorAll('th')).toHaveLength(2);
  });

  it('renders a pipe table in a comment', async () => {
    vi.stubGlobal(
      'fetch',
      routeFetch({
        '/tasks/T-1/comments$': {
          json: { comments: [{ id: 'C-1', taskId: 'T-1', author: 'a@x.test', body: TABLE, parentCommentId: null, mentions: [], ts: new Date().toISOString() }] },
        },
      }),
    );
    renderDrawer();
    await screen.findByText('Ship the parser');
    await waitFor(() => expect(document.querySelectorAll('.md table').length).toBeGreaterThan(0));
  });
});

/**
 * REPORTED FROM STAGING — controls that existed but did not read as controls.
 */
describe('labels and capabilities are chips', () => {
  it('renders each value as its own chip with its own remove control', async () => {
    renderDrawer();
    await screen.findByText('Ship the parser');
    // The fixture carries labels ['backend'] and capabilities ['python'].
    expect(screen.getByRole('button', { name: 'Remove backend' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Remove python' })).toBeTruthy();
  });

  it('removing one chip PATCHes the remaining list, not a re-parsed string', async () => {
    const fetchMock = routeFetch();
    vi.stubGlobal('fetch', fetchMock);
    renderDrawer();
    await screen.findByText('Ship the parser');
    fireEvent.click(screen.getByRole('button', { name: 'Remove backend' }));
    await waitFor(() => {
      const patch = (fetchMock as ReturnType<typeof vi.fn>).mock.calls.find(
        ([, init]) => (init as RequestInit | undefined)?.method === 'PATCH',
      );
      expect(patch, 'removing a chip should save').toBeTruthy();
      expect(JSON.parse(String((patch![1] as RequestInit).body)).patch).toMatchObject({ labels: [] });
    });
  });
});

/**
 * P-208: a cancelled task is DONE with the cancelled label but was not delivered. The drawer's subtask
 * progress counts a cancelled subtask in the total and not in done, and the Runs section reads the
 * cancelled label from the board's task list (list_runs carries none), so a cancelled run that had
 * delivered offers no Pick.
 */
describe('TaskDrawer — a cancelled task is not done (P-208)', () => {
  const sub = (id: string, over: Partial<Task>): Task => ({ ...task, id, title: `sub ${id}`, parentTaskId: 'T-1', blockedBy: [], dueAt: null, ...over });

  it('subtask progress counts a cancelled subtask in the total only', async () => {
    vi.stubGlobal('fetch', routeFetch({
      '/tasks/T-1/subtasks$': { json: { tasks: [sub('T-2', { status: 'DONE' }), sub('T-3', { status: 'DONE', labels: ['cancelled'] }), sub('T-4', { status: 'TODO' })] } },
    }));
    renderDrawer();
    expect(await screen.findByText('1 of 3 complete')).toBeInTheDocument();
  });

  it('REFUSAL: a cancelled run that had delivered offers no Pick; the delivered one does', async () => {
    const racing: Task = { ...task, racing: true };
    const cancelledRun = sub('T-5', { status: 'DONE', labels: ['cancelled'], parentTaskId: null });
    vi.stubGlobal('fetch', routeFetch({
      '/tasks/T-1$': { json: { task: racing } },
      '/tasks/T-1/runs$': { json: { runs: [
        { taskId: 'T-5', status: 'DONE', assignee: 'a1', leaseHolder: null, hasDeliverable: true, deliverable: 'wrong approach', version: 4 },
        { taskId: 'T-6', status: 'REVIEW', assignee: 'a2', leaseHolder: null, hasDeliverable: true, deliverable: 'the fix', version: 3 },
      ] } },
      '/boards/b1/tasks\\?': { json: { tasks: [racing, cancelledRun] } },
    }));
    renderDrawer();
    expect(await screen.findByText('wrong approach')).toBeInTheDocument();
    await waitFor(() => expect(screen.getAllByText(/^\s*Pick\s*$/)).toHaveLength(1));
    expect(screen.getAllByText('Submitted')).toHaveLength(1);
  });
});

/**
 * Escape in a chip field cancels the half-typed value. The drawer's Escape handler blurs the focused
 * field, and the chip input commits its draft on blur, so Escape used to SAVE "wip" as a label (a
 * real update_task write) from the key that means cancel.
 */
describe('Escape in a chip field cancels the draft', () => {
  for (const name of ['Labels', 'Capabilities']) {
    it(`${name}: Escape drops the typed value and writes nothing`, async () => {
      const fetchMock = routeFetch();
      vi.stubGlobal('fetch', fetchMock);
      const { onClose } = renderDrawer();
      await screen.findByText('Ship the parser');
      const input = screen.getByRole('textbox', { name });
      await waitFor(() => expect(input).toBeEnabled());
      input.focus();
      fireEvent.change(input, { target: { value: 'wip' } });
      fireEvent.keyDown(input, { key: 'Escape' });
      expect(document.activeElement).not.toBe(input); // the drawer's Escape blurred it
      await new Promise((r) => setTimeout(r, 20));
      const patches = (fetchMock as ReturnType<typeof vi.fn>).mock.calls.filter(([, init]) => (init as RequestInit | undefined)?.method === 'PATCH');
      expect(patches).toHaveLength(0);
      expect(input).toHaveValue('');
      expect(onClose).not.toHaveBeenCalled();
      // A value committed the ordinary way still saves.
      input.focus();
      fireEvent.change(input, { target: { value: 'wip' } });
      fireEvent.keyDown(input, { key: 'Enter' });
      await waitFor(() => expect((fetchMock as ReturnType<typeof vi.fn>).mock.calls.some(([, init]) => (init as RequestInit | undefined)?.method === 'PATCH')).toBe(true));
    });
  }
});
