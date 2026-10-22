/**
 * The Projects grid renders from ONE `list_projects` call and must not fan out per project.
 *
 * It used to issue a `useQueries` of one `getProject` per row. Measured in prod with 19 projects: 19
 * concurrent `/api/projects/P-*` requests fired inside 18ms, all served by ONE core instance
 * (containerConcurrency 80 >> 19, so Cloud Run never scales out), every one taking 1.52-2.12s against
 * 0.086-0.233s for the same endpoints once the burst drained. Self-inflicted, on every mount.
 *
 * THE GUARD IS THE ABSENCE ASSERTION, not the rendering ones. A test that only checked the avatars
 * appear would pass just as happily with the fan-out restored — it is the *source* of the data that
 * this change is about, so the test has to watch the network, not the DOM alone. The rendering
 * assertions are the positive control (rule 5): they stop "no detail requests" from being satisfied by
 * a grid that simply renders nothing.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

const ALICE = 'U-81bb37ce-9de5-4345-8c8d-c35db36fbc1f';
const UNRESOLVED = 'U-00000000-0000-4000-8000-000000000000';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('Projects grid — one list call, no per-project fan-out', () => {
  it('renders members, labels and colour from list_projects, and requests no project detail', async () => {
    const seen: string[] = [];
    const routes: Record<string, unknown> = {
      '/api/session': {
        identity: 'alice@example.com',
        csrfToken: null,
        isAdmin: false,
        subscriptionsEnabled: false,
        uiSettings: {},
      },
      '/api/projects': {
        projects: [
          {
            projectId: 'P-1',
            name: 'Apollo',
            role: 'admin',
            members: { [ALICE]: 'admin', [UNRESOLVED]: 'member' },
            memberDisplay: { [ALICE]: 'alice@x.test' },
            color: '#ff0000',
          },
        ],
      },
    };
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const path = String(input).split('?')[0]!;
        seen.push(path);
        const body = routes[path] ?? {
          notifications: [],
          unread: 0,
          sessions: [],
          boards: [],
          projects: [],
        };
        return { ok: true, status: 200, json: async () => body } as Response;
      }),
    );

    const { QueryClient, QueryClientProvider } = await import('@tanstack/react-query');
    const { AuthProvider } = await import('../auth.js');
    const { App } = await import('../App.js');
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/projects']}>
          <AuthProvider>
            <App />
          </AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    // POSITIVE CONTROL — the card actually rendered its members, from the LIST payload.
    // Asserted on the accessible name a person hears, not on props: `Avatar` composes
    // `<person> · <id>` into aria-label, so this fails if either half is dropped.
    expect(await screen.findByLabelText(`human alice@x.test · ${ALICE}`)).toBeInTheDocument();
    // ...and a member with no resolvable display name still renders, keyed on its raw id.
    expect(screen.getByLabelText(`human ${UNRESOLVED}`)).toBeInTheDocument();
    expect(screen.getByText(/2 members/)).toBeInTheDocument();
    // The project's own accent colour came from the same payload (absent → palette hash fallback).
    const accent = document.querySelector('.proj-card .accent') as HTMLElement | null;
    expect(accent?.style.background).toBe('rgb(255, 0, 0)');

    // THE GUARD. `/api/projects` is the list; `/api/projects/P-1` is the per-project detail the grid
    // must no longer ask for. Give any late request a chance to land before asserting absence.
    await waitFor(() => expect(seen.filter((p) => p === '/api/projects').length).toBeGreaterThan(0));
    expect(seen.filter((p) => /^\/api\/projects\/P-/.test(p))).toEqual([]);
  });
});
