/**
 * A ticket's side reads that fail say so, with Retry, instead of drawing the empty state.
 *
 * The drawer and the page read the task, then its context (digest, notes, links, dependencies),
 * subtasks, memory, attachments, comments, follow-ups, runs and the board feed, each on its own. Only
 * the task read had an error branch: every other one rendered `data ?? []`, so a 500 on, say, the
 * comments read printed "No comments yet." and a 500 on the context read printed "Blocked by: None"
 * on a task blocked by T-9. A reader could not tell "there are none" from "we could not read them".
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, cleanup, within, waitFor } from '@testing-library/react';
import { TaskDrawer } from '../screens/TaskDrawer.js';
import { AuthProvider } from '../auth.js';
import { routeFetch } from './taskFixtures.js';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const fail = { status: 500, json: { code: 'INTERNAL', message: 'database is locked' } };

/** Every side read of T-1 answers 500 until `heal()` is called; the task read itself succeeds. */
function failingFetch() {
  let healed = false;
  const broken = routeFetch({
    '/tasks/T-1/context$': fail,
    '/tasks/T-1/subtasks$': fail,
    '/tasks/T-1/memory$': fail,
    '/tasks/T-1/attachments$': fail,
    '/tasks/T-1/comments$': fail,
    '/tasks/T-1/follow-ups$': fail,
    '/tasks/T-1/runs$': fail,
    '/boards/b1/feed$': fail,
  });
  const healthy = routeFetch({
    '/tasks/T-1/comments$': { json: { comments: [{ id: 'CM-1', taskId: 'T-1', author: 'agent-ada', body: 'First pass is in', parentCommentId: null, mentions: [], ts: '2026-10-24T08:00:00.000Z' }] } },
  });
  const f = vi.fn((input: RequestInfo | URL, init?: RequestInit) => (healed ? healthy : broken)(input, init));
  return { fetch: f as unknown as typeof fetch, heal: () => { healed = true; } };
}

function renderDrawer() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <AuthProvider>
        <TaskDrawer projectId="p1" taskId="T-1" onClose={vi.fn()} onOpenTask={vi.fn()} />
      </AuthProvider>
    </QueryClientProvider>,
  );
}

/** The panel or section (the nearest `<section>`) whose heading is named `name`. */
function region(name: RegExp): HTMLElement {
  const box = screen.getByRole('heading', { name }).closest('section');
  if (!box) throw new Error(`no section headed ${name}`);
  return box;
}

describe('ticket side reads that fail', () => {
  it('say which read failed, with the server message and Retry, and draw no empty state', async () => {
    const f = failingFetch();
    vi.stubGlobal('fetch', f.fetch);
    renderDrawer();
    await screen.findByText('Ship the parser');

    for (const [panel, what] of [
      [/^Comments$/, /Couldn’t load the comments: database is locked/],
      [/^Follow-ups$/, /Couldn’t load the follow-ups: database is locked/],
      [/^Context items$/, /Couldn’t load the context: database is locked/],
      [/^Links$/, /Couldn’t load the context: database is locked/],
      [/^Dependencies$/, /Couldn’t load the context: database is locked/],
      [/^Subtasks$/, /Couldn’t load the subtasks: database is locked/],
      [/^Agent memory$/, /Couldn’t load the memory: database is locked/],
      [/^Artifacts & attachments$/, /Couldn’t load the attachments: database is locked/],
      [/Recent Activity/, /Couldn’t load the activity: database is locked/],
      [/^Runs$/, /Couldn’t load the runs: database is locked/],
    ] as const) {
      await waitFor(() => expect(within(region(panel)).getByRole('alert')).toHaveTextContent(what));
      expect(within(within(region(panel)).getByRole('alert')).getByRole('button', { name: 'Retry' })).toBeInTheDocument();
    }

    // None of the empty states that read as "there are none".
    for (const empty of ['No comments yet.', 'No follow-ups yet.', 'No memory entries.', 'None.', 'No activity yet.']) {
      expect(screen.queryByText(empty), empty).toBeNull();
    }
    expect(screen.queryByText(/0 events on this ticket/)).toBeNull();
    expect(within(region(/^Dependencies$/)).queryByText('None')).toBeNull();
  });

  it('Retry reads again and shows what the read returns', async () => {
    const f = failingFetch();
    vi.stubGlobal('fetch', f.fetch);
    renderDrawer();
    await screen.findByText('Ship the parser');
    const alert = await waitFor(() => within(region(/^Comments$/)).getByRole('alert'));
    f.heal();
    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('First pass is in')).toBeInTheDocument();
    expect(within(region(/^Comments$/)).queryByRole('alert')).toBeNull();
  });

  it('a read that succeeded empty still says so', async () => {
    vi.stubGlobal('fetch', routeFetch());
    renderDrawer();
    await screen.findByText('Ship the parser');
    expect(await screen.findByText('No comments yet.')).toBeInTheDocument();
    expect(await screen.findByText('No follow-ups yet.')).toBeInTheDocument();
    expect(await screen.findByText('No memory entries.')).toBeInTheDocument();
    expect(screen.queryAllByRole('alert')).toHaveLength(0);
  });
});
