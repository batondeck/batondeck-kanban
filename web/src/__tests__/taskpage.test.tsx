/**
 * TASKPAGE HAD NO TESTS AT ALL. This is its first coverage, added when the status control moved into
 * the shared `TicketActions` — restructuring an untested screen is how a regression ships silently.
 *
 * The assertions deliberately MIRROR `taskdrawer.test.tsx`'s status-select case, against the same
 * fixture, because that is the claim being made: the page and the drawer now drive one control
 * through one code path. Two surfaces asserted against two different fakes would prove nothing about
 * whether they agree.
 *
 * The page previously had NO status control at all — moving a ticket meant finding the "Move to"
 * buttons in the middle of the content column. So this is also new behaviour, not just a refactor.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { render, screen, cleanup, fireEvent, waitFor, within } from '@testing-library/react';
import { TaskPage } from '../screens/TaskPage.js';
import { ProjectProvider } from '../projectContext.js';
import { routeFetch } from './taskFixtures.js';

/** `panels.tsx` source, for the one assertion whose subject is a PROP rather than rendered output. */
const PANELS_SRC = (): string => {
  const mods = import.meta.glob('../screens/task/panels.tsx', { query: '?raw', import: 'default', eager: true }) as Record<string, string>;
  const hit = Object.values(mods)[0];
  if (!hit) throw new Error('panels.tsx not found — this assertion would be vacuous');
  return hit;
};

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/projects/p1/boards/b1/tasks/T-1']}>
        <ProjectProvider>
          <Routes>
            <Route path="/projects/:projectId/boards/:boardId/tasks/:taskId" element={<TaskPage />} />
          </Routes>
        </ProjectProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.stubGlobal('fetch', routeFetch());
});
afterEach(() => {
  vi.unstubAllGlobals();
  cleanup();
});

describe('TaskPage', () => {
  it('renders the ticket record', async () => {
    renderPage();
    expect(await screen.findByText('Ship the parser')).toBeTruthy();
  });

  /**
   * THE ID AND TITLE ARE PRINTED ONCE EACH.
   *
   * The page used to render both twice — `PageHeader` carried `task.id` as its title and
   * `task.title` as its subtitle, and a `.task-head` block below repeated them as a `Board › T-1`
   * crumb and an `<h1>`. Reported from staging as "duplicate information".
   *
   * The old comment on the test above ACKNOWLEDGED the duplication and worked around it by querying
   * the heading role — a test shaped to tolerate the defect rather than name it (CONTRIBUTING rule 4).
   */
  it('prints the id and the title exactly once each', async () => {
    renderPage();
    await screen.findByText('Ship the parser');
    expect(screen.getAllByText('Ship the parser')).toHaveLength(1);
    // The id survives as the header's title; the crumb that repeated it is gone.
    expect(screen.getAllByText('T-1')).toHaveLength(1);
    expect(screen.queryByText(/Board ›/)).toBeNull();
  });

  it('moves the task via the status control (board-gated targets)', async () => {
    const fetchMock = routeFetch();
    vi.stubGlobal('fetch', fetchMock);
    renderPage();
    await screen.findByText('Ship the parser');

    const sel = await screen.findByLabelText('Status');
    expect(sel).toHaveValue('READY');
    // Once the board loads, only statuses that HAVE a column remain — READY→BACKLOG/CANCELLED drop
    // out, because a move to a column that does not exist is not a move.
    await within(sel).findByRole('option', { name: 'IN PROGRESS' });
    await waitFor(() =>
      expect(within(sel).queryByRole('option', { name: 'BACKLOG' })).not.toBeInTheDocument(),
    );

    fireEvent.change(sel, { target: { value: 'IN_PROGRESS' } });

    await waitFor(() => {
      const move = (fetchMock as unknown as { mock: { calls: Array<[string, RequestInit?]> } }).mock.calls.find(
        ([url]) => String(url).endsWith('/tasks/T-1/move'),
      );
      expect(move, 'no /move request was sent').toBeTruthy();
      expect(JSON.parse(String(move![1]?.body))).toEqual({ version: 3, toStatus: 'IN_PROGRESS' });
    });
  });

  it('renders the incident brief as markdown when the ticket is an incident', async () => {
    // The page and the drawer share `panels.tsx`, so this is a cheap check that the fix reaches BOTH
    // surfaces rather than only the one that was screenshotted.
    vi.stubGlobal(
      'fetch',
      routeFetch({
        '/tasks/T-1$': {
          json: {
            task: {
              id: 'T-1', projectId: 'p1', boardId: 'b1', columnId: 'c1',
              title: 'CheckoutLatencyHigh', description: '', status: 'READY', priority: 'urgent',
              assignee: null, labels: [], requiredCapabilities: [], estimate: null, dueAt: null,
              customFields: { 'incident.fingerprint': '9f2ac41d', 'incident.source': 'grafana' },
              parentTaskId: null, order: 'a0', blockedBy: [], blocks: [], contextSummary: '',
              version: 1, claim: null, createdBy: 'ingest:ing_x',
              updatedAt: new Date().toISOString(),
            },
          },
        },
        '/tasks/T-1/context$': {
          json: {
            summary: '', fields: {}, memory: [],
            deps: { blockedBy: [], blocks: [], edges: [] },
            items: [
              {
                id: 'CI-1', taskId: 'T-1', kind: 'note',
                body: '**FIRING** CheckoutLatencyHigh',
                author: 'ingest:ing_x', ts: '2026-08-01T00:00:00.000Z',
              },
            ],
          },
        },
      }),
    );
    renderPage();
    expect((await screen.findByText('FIRING')).tagName).toBe('STRONG');
  });
});

/**
 * THE PAGE TOLD YOU TO CLICK A BUTTON IT DID NOT HAVE.
 *
 * `DescriptionPanel`'s empty state reads "click Edit (or double-click) to add the brief". The drawer
 * carries an Edit button in its header; the page never did, so on the surface with the most room the
 * instruction pointed at nothing. Nothing failed — no test asserted the button existed, which is
 * exactly how it stayed missing.
 */
describe('editing from the page', () => {
  it('has an Edit button, and it opens the description editor', async () => {
    renderPage();
    await screen.findByText('Ship the parser');
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    // Scoped to the DESCRIPTION editor: `document.querySelector('textarea')` also matches the
    // comment composer further down the page, so a loose query passes whether Edit worked or not.
    const descBox = (): HTMLTextAreaElement | null =>
      [...document.querySelectorAll('textarea')].find((t) => t.value.includes('tokenizer')) ?? null;
    await waitFor(() => expect(descBox(), 'Edit should open the description editor').toBeTruthy());
    // …and it toggles back out without saving.
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(descBox()).toBeNull());
  });
});

/* ── two staging reports, 2026-08-30 ───────────────────────────────────────────────────────────── */

describe('the header leads with the title, not the id', () => {
  /**
   * Reported from staging: "ticket title needs to be in bigger font size, board / ticket no should be
   * smaller". The page passed `title={task.id}` and `subtitle={task.title}`, so the 16px `<h1>` read
   * "T-17" and the sentence describing the ticket sat under it at 12px muted.
   *
   * ASSERTED AS DOM IDENTITY, not as pixels — jsdom resolves no layout and no custom properties, so
   * "bigger" is not provable here. What IS provable, and what actually regressed, is WHICH STRING is
   * the heading. A swap back would put the id in the h1 again and fail this.
   */
  it('the h1 is the ticket title; the id is the eyebrow beside the board', async () => {
    renderPage();
    // Wait for the LOADED header. Before the ticket resolves the page renders its own fallback
    // (`title={taskId}`), so reading the h1 immediately asserts against the loading state.
    await screen.findByText('Ship the parser');
    const h1 = await screen.findByRole('heading', { level: 1 });
    expect(h1.textContent, 'the h1 must be the title, not the identifier').toBe('Ship the parser');
    expect(h1.textContent, 'the id must not BE the heading').not.toMatch(/^T-\d+$/);

    const ref = document.querySelector('.ticket-ref');
    expect(ref, 'the board / id eyebrow is missing').toBeTruthy();
    expect(ref!.textContent, 'the eyebrow carries the ticket id').toContain('T-1');
  });

  it('the eyebrow offers a copy control, reachable without a pointer', async () => {
    renderPage();
    await screen.findByText('Ship the parser');
    const copy = document.querySelector('.ticket-ref .ticket-ref-copy');
    expect(copy, 'no copy control beside the id').toBeTruthy();
    // A hover-revealed control that carries no accessible name is unreachable for anyone not using a
    // mouse — the reveal is CSS, the name is the contract.
    expect(copy!.getAttribute('aria-label')).toMatch(/copy link/i);
  });
});

describe('the Incident panel pads its body', () => {
  /**
   * Reported from staging as a "markdown rendering gutter" fault. It is not the markdown: `Panel`
   * takes a `pad` prop for exactly this ("without it they sit flush against the 1.5px rule, which
   * reads as a rendering fault rather than a design choice"), and `IncidentPanel` was the one prose
   * panel that omitted it — its chips, fingerprint line and whole markdown brief sat ~27px left of
   * the "INCIDENT" head above them.
   *
   * Lexical because the padding is a CSS class jsdom will not resolve; what regressed is the PROP.
   */
  it('passes `pad`, like every other panel that renders prose', () => {
    const src = PANELS_SRC();
    const incident = src.slice(src.indexOf('export function IncidentPanel'));
    const open = incident.slice(incident.indexOf('<Panel'), incident.indexOf('>', incident.indexOf('<Panel')) + 1);
    expect(open, 'IncidentPanel renders prose and must pad its body').toMatch(/\bpad\b/);
  });
});
