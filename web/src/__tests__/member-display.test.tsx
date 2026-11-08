/**
 * T-319 — the member roster must name a PERSON, not the uuid they are stored under.
 *
 * The T-216 cutover re-keyed `project.members` from emails to opaque `U-<uuid>` account ids, and this
 * roster renders those keys straight at a human: before this fix a teammate showed up as
 * `U-81bb37ce-9de5-4345-8c8d-c35db36fbc1f` with a `U8` avatar, next to a Remove button.
 *
 * Asserted on the WORDS A PERSON READS, never on props — a test that checked `label={...}` was passed
 * would still pass if the screen dropped it on the floor.
 *
 * Both directions in one render (rule 5):
 *   - POSITIVE CONTROL: a member the server could resolve shows their name.
 *   - REFUSAL: a member it could NOT resolve still shows the raw id rather than a blank cell. That is
 *     the pre-existing rendering, which is the whole reason this fix needs no backfill.
 *
 * The id stays on screen for BOTH: this row carries role and Remove controls, and two accounts may
 * legitimately render the same name, so identity must never become unresolvable here.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, cleanup } from '@testing-library/react';
import { Settings } from '../screens/Settings.js';
import { ProjectProvider } from '../projectContext.js';

const NAMED = 'U-81bb37ce-9de5-4345-8c8d-c35db36fbc1f';
const UNRESOLVED = 'U-00000000-0000-4000-8000-000000000000';

const project = {
  id: 'P-1',
  name: 'Roadmap',
  createdBy: NAMED,
  createdAt: new Date().toISOString(),
  members: { [NAMED]: 'admin', [UNRESOLVED]: 'member' },
  settings: {},
};

function stubGateway(memberDisplay: Record<string, string>): void {
  // Settings configures the selected project; on "All projects" it only asks for one (P-442).
  localStorage.setItem('bd.projectId', 'P-1');
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      const body = url.includes('/boards')
        ? { boards: [] }
        : /\/api\/projects\/P-1$/.test(url)
          ? { project, memberDisplay }
          : url.includes('/api/projects')
            ? { projects: [{ projectId: 'P-1', name: 'Roadmap', role: 'admin' }] }
            : { ui: {}, subscriptionsEnabled: false };
      return { ok: true, status: 200, json: async () => body } as Response;
    }),
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  localStorage.clear();
});

describe('Settings member roster — account ids resolve to a person (T-319)', () => {
  it('shows the resolved name, keeps the id visible, and falls back to the id when unresolved', async () => {
    stubGateway({ [NAMED]: 'alice@x.test' });
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <ProjectProvider>
          <Settings />
        </ProjectProvider>
      </QueryClientProvider>,
    );

    // POSITIVE CONTROL — the person is on screen.
    expect(await screen.findByText('alice@x.test')).toBeInTheDocument();
    // ...and the id they are stored under is still readable next to it.
    expect(screen.getByText(NAMED)).toBeInTheDocument();
    // REFUSAL — the member with no resolvable account renders exactly as it does today: its raw id.
    expect(screen.getByText(UNRESOLVED)).toBeInTheDocument();
  });

  it('REFUSAL: an older core that sends no memberDisplay still renders every member', async () => {
    // A rolling deploy puts this SPA in front of a core that predates the field. Absent must degrade to
    // today's rendering, never to an empty roster.
    stubGateway({});
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <ProjectProvider>
          <Settings />
        </ProjectProvider>
      </QueryClientProvider>,
    );
    expect(await screen.findByText(NAMED)).toBeInTheDocument();
    expect(screen.getByText(UNRESOLVED)).toBeInTheDocument();
  });
});
