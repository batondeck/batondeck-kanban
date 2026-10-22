/**
 * THE PAGE AND THE DRAWER MUST PRESENT THE SAME RECORD IN THE SAME ORDER.
 *
 * They did not. The page's tab list ordered Context → Discussion; the drawer's nav ordered
 * Discussion → Context; each surface's panel bodies matched its own nav. Two hand-maintained arrays,
 * both internally consistent, so neither typecheck nor any existing test could see the disagreement —
 * only a human opening the same ticket twice and noticing it moved.
 *
 * This is the fence against re-inlining panels into a screen instead of going through the shared
 * `SECTIONS` order. It compares the RENDERED panel sequences, not the constants, so it fails on the
 * thing a reader would actually notice.
 *
 * Scoped by `.record-title` inside each surface's own container: the page puts its rail AFTER the
 * body in the DOM, the drawer inlines everything in one column, so a document-wide query would
 * compare two different things and pass or fail for the wrong reason.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { render, screen, cleanup } from '@testing-library/react';
import { TaskPage } from '../screens/TaskPage.js';
import { TaskDrawer } from '../screens/TaskDrawer.js';
import { ProjectProvider } from '../projectContext.js';
import { SECTIONS } from '../screens/task/TicketBody.js';
import { routeFetch } from './taskFixtures.js';

const titles = (root: ParentNode): string[] =>
  [...root.querySelectorAll('.record-title')].map((e) => e.textContent?.trim() ?? '');

/**
 * The CONTENT column only. The rail is deliberately positioned differently on the two surfaces — the
 * page puts it in a 420px column beside the body (so it lands AFTER `.side-main` in the DOM), the
 * drawer stacks it above the body at 520px. Comparing raw document order would therefore fail on the
 * placement difference that is the whole point of the layout, while saying nothing about whether the
 * two agree on the record.
 */
const bodyTitles = (root: HTMLElement): string[] => {
  const main = root.querySelector('.side-main');
  if (main) return titles(main); // TaskPage: content column
  // Drawer: everything except the rail, which is the run of panels before the first section anchor.
  const rail = root.querySelector('.drawer > div > .record');
  const railTitles = new Set<string>();
  for (let el = rail; el && el.classList.contains('record'); el = el.nextElementSibling) {
    railTitles.add(el.querySelector('.record-title')?.textContent?.trim() ?? '');
  }
  return titles(root).filter((t) => !railTitles.has(t));
};

/** The page, rendered far enough to read its section nav. Same mount as the parity test above. */
async function renderPageNav(): Promise<{ pageNavLabels: string[] }> {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const page = render(
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
  await screen.findByText('Ship the parser');
  const nav = page.container.querySelector('[role="navigation"], .tabs');
  const labels = [...(nav?.querySelectorAll('button, a') ?? [])].map((b) => b.textContent?.trim() ?? '');
  return { pageNavLabels: labels };
}

beforeEach(() => {
  vi.stubGlobal('fetch', routeFetch());
});
afterEach(() => {
  vi.unstubAllGlobals();
  cleanup();
});

describe('ticket IA parity', () => {
  it('renders the same panels in the same order on both surfaces', async () => {
    const qc1 = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const page = render(
      <QueryClientProvider client={qc1}>
        <MemoryRouter initialEntries={['/projects/p1/boards/b1/tasks/T-1']}>
          <ProjectProvider>
            <Routes>
              <Route path="/projects/:projectId/boards/:boardId/tasks/:taskId" element={<TaskPage />} />
            </Routes>
          </ProjectProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    await screen.findByText('Ship the parser');
    const pageBody = bodyTitles(page.container);
    const pageRail = titles(page.container.querySelector('.side-rail') as HTMLElement);
    cleanup();

    const qc2 = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const drawer = render(
      <QueryClientProvider client={qc2}>
        <TaskDrawer projectId="p1" taskId="T-1" onClose={() => {}} />
      </QueryClientProvider>,
    );
    await screen.findByText('Ship the parser');
    const drawerBody = bodyTitles(drawer.container);
    const drawerAll = titles(drawer.container);

    // Sanity first: a comparison of two empty lists would pass vacuously (rule 18).
    expect(pageBody.length).toBeGreaterThan(5);
    expect(pageRail.length).toBeGreaterThan(1);

    // 1. The CONTENT columns match exactly, in order. `Runs` is drawer-only by design (it always
    //    renders a section and fires its own read), so it is excluded rather than asserted away.
    expect(drawerBody.filter((t) => t !== 'Runs')).toEqual(pageBody);

    // 2. Every rail panel the page shows is also present on the drawer. Its POSITION differs on
    //    purpose — beside the body at 1fr+420px, above it at 520px — so only presence is asserted.
    for (const t of pageRail) expect(drawerAll, `drawer is missing the rail panel "${t}"`).toContain(t);

    // 3. …and the controls are NOT duplicated back into the content column.
    for (const t of pageRail) expect(pageBody).not.toContain(t);
  });

  /**
   * BOTH nav bars, and the name used to promise that while the body rendered only the drawer.
   * A review replaced the page's `<Tabs items={SECTIONS}>` with three hand-written tabs in a
   * different order — half the sections unreachable from the page nav — and all 527 tests passed.
   */
  it('both nav bars are built from SECTIONS, so they cannot list different sections', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <TaskDrawer projectId="p1" taskId="T-1" onClose={() => {}} />
      </QueryClientProvider>,
    );
    await screen.findByText('Ship the parser');
    const drawerNav = screen.getByRole('navigation', { name: 'Sections' });
    const drawerLabels = [...drawerNav.querySelectorAll('button')].map((b) => b.textContent?.trim());
    for (const { label } of SECTIONS) {
      expect(drawerLabels, `the drawer nav is missing the "${label}" section`).toContain(label);
    }
    cleanup();

    // …and the PAGE, which is the half that was only ever claimed.
    const { pageNavLabels } = await renderPageNav();
    for (const { label } of SECTIONS) {
      expect(pageNavLabels, `the page nav is missing the "${label}" section`).toContain(label);
    }
    // Order too: the page nav is `<Tabs items={SECTIONS}>`, so it must read in SECTIONS order.
    expect(
      pageNavLabels.filter((l) => SECTIONS.some((s) => s.label === l)),
      'the page nav lists the sections in a different order than SECTIONS declares',
    ).toEqual(SECTIONS.map((s) => s.label));
  });
});

/**
 * THE DETAILS FOLD IS OPEN ON THE PAGE AND SHUT IN THE DRAWER.
 *
 * Measured on staging: at 520px the rail sits ABOVE the content, and an open fold filled the entire
 * first screen — the Description, which is why you opened the ticket, was below the fold. On the
 * page the rail is BESIDE the content, so an open fold costs nothing.
 *
 * `<details>` keeps its children MOUNTED either way (that is why `Panel` uses it), so this asserts
 * the `open` attribute rather than presence — a query for the fields resolves in both cases.
 */
describe('the Details fold adapts to the surface', () => {
  const foldOf = (root: HTMLElement): HTMLDetailsElement | undefined =>
    [...root.querySelectorAll('details.record')].find(
      (el) => el.querySelector('.record-title')?.textContent?.trim() === 'Details',
    ) as HTMLDetailsElement | undefined;

  it('is OPEN on the page, where the rail sits beside the content', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const { container } = render(
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
    await screen.findByText('Ship the parser');
    const fold = foldOf(container);
    expect(fold, 'the page should render a Details fold').toBeTruthy();
    expect(fold!.open).toBe(true);
  });

  it('is SHUT in the drawer, where the rail sits above it', async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const { container } = render(
      <QueryClientProvider client={qc}>
        <TaskDrawer projectId="p1" taskId="T-1" onClose={() => {}} />
      </QueryClientProvider>,
    );
    await screen.findByText('Ship the parser');
    const fold = foldOf(container);
    expect(fold, 'the drawer should render a Details fold').toBeTruthy();
    expect(fold!.open).toBe(false);
    // …and its fields are still MOUNTED, so a half-typed edit survives and queries resolve. Asserted
    // on a control that only exists INSIDE the fold — the Assignee input — so a shut `<details>` that
    // unmounted its children would fail here.
    expect(screen.getByPlaceholderText('agent name, email, or SA')).toBeTruthy();
  });
});
