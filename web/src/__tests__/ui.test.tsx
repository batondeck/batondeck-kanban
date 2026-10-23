import { describe, it, expect, vi, afterEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { StatusPill, Avatar, PriorityDot } from '../components.js';
import {
  Chip, Chips, Cluster, Cols, Dot, Empty, KV, LineRow, Note, Panel, Pill, Prov, RoleBadge,
  Section, Spacer, Tag, Tile, roleColor, roleOf,
} from '../ui.js';
import type { Status } from '../types.js';
import { STATUSES } from '../types.js';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('design-system components', () => {
  it('StatusPill renders the status label', () => {
    render(<StatusPill status="IN_PROGRESS" />);
    expect(screen.getByText('IN PROGRESS')).toBeInTheDocument();
  });

  it('Avatar distinguishes agents (square) from humans (round)', () => {
    const { container: human } = render(<Avatar id="alice" />);
    expect(human.querySelector('.avatar.human')).toBeTruthy();
    const { container: agent } = render(<Avatar id="agent-7" />);
    expect(agent.querySelector('.avatar.agent')).toBeTruthy();
  });

  it('PriorityDot renders with a title', () => {
    render(<PriorityDot priority="urgent" />);
    expect(screen.getByTitle('urgent')).toBeInTheDocument();
  });
});

describe('SignIn screen (the local login form, R1.15)', () => {
  const REFUSED = 'The login or password is wrong, or the account is disabled.';

  /** The real AuthProvider and App at `path`, signed out, with `/api/login` answering `login`. */
  async function mountSignedOut(path: string, login: (body: URLSearchParams) => Response): Promise<ReturnType<typeof vi.fn>> {
    let signedIn = false;
    const spy = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const p = String(input).split('?')[0];
      if (p === '/api/login') {
        const r = login(new URLSearchParams(String(init?.body)));
        signedIn = r.status === 204;
        return r;
      }
      if (p === '/api/session') {
        return signedIn
          ? ({ ok: true, status: 200, json: async () => ({ identity: 'bob', csrfToken: 'c', isAdmin: false, permissions: [], subscriptionsEnabled: false, uiSettings: {} }) } as Response)
          : ({ ok: false, status: 401, json: async () => ({ code: 'UNAUTHENTICATED', message: 'unauthenticated' }) } as Response);
      }
      return { ok: true, status: 200, json: async () => ({ notifications: [], unread: 0, sessions: [], boards: [], projects: [] }) } as Response;
    });
    vi.stubGlobal('fetch', spy);
    const { QueryClient, QueryClientProvider } = await import('@tanstack/react-query');
    const { AuthProvider } = await import('../auth.js');
    const { App } = await import('../App.js');
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={[path]}>
          <AuthProvider>
            <App />
          </AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    await screen.findByRole('button', { name: 'Sign in' });
    return spy;
  }

  function submit(login: string, password: string): void {
    fireEvent.change(screen.getByLabelText('Login'), { target: { value: login } });
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: password } });
    fireEvent.click(screen.getByRole('button', { name: 'Sign in' }));
  }

  it('renders the brand and a login form, and none of the hosted ways in', async () => {
    await mountSignedOut('/projects', () => new Response(null, { status: 204 }));
    expect(screen.getByRole('img', { name: 'BatonDeck' })).toBeInTheDocument();
    expect(screen.getByText(/task board your agents/i)).toBeInTheDocument();
    expect(screen.getByLabelText('Login')).toBeInTheDocument();
    expect(screen.getByLabelText('Password')).toHaveAttribute('type', 'password');
    // REFUSAL: no WorkOS, Google or demo door is left, and nothing links to a hosted /auth/ route.
    expect(screen.queryByRole('button', { name: /workos|google|demo/i })).toBeNull();
    expect(document.body.innerHTML).not.toMatch(/\/auth\//);
  });

  it('ERROR STATE: a refused login shows the BFF message in the form and stays signed out', async () => {
    const spy = await mountSignedOut('/projects', () =>
      ({ ok: false, status: 401, json: async () => ({ code: 'UNAUTHENTICATED', message: REFUSED }) }) as Response,
    );
    submit('bob', 'wrong');
    expect(await screen.findByRole('alert')).toHaveTextContent(REFUSED);
    expect(screen.getByLabelText('Password')).toHaveValue('');
    const login = spy.mock.calls.find(([p]) => String(p) === '/api/login');
    expect(new URLSearchParams(String((login?.[1] as RequestInit).body)).get('password')).toBe('wrong');
  });

  it('a correct login renders the route asked for before it, with no navigation', async () => {
    await mountSignedOut('/notifications', (body) =>
      body.get('password') === 'right' ? new Response(null, { status: 204 }) : ({ ok: false, status: 401, json: async () => ({ code: 'UNAUTHENTICATED', message: REFUSED }) }) as Response,
    );
    submit('bob', 'right');
    expect(await screen.findByRole('heading', { name: 'Notifications' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Sign in' })).toBeNull();
  });
});

describe('App shell', () => {
  it('renders the global project switcher in the sidebar', async () => {
    // Stub the gateway: a signed-in session + one project; everything else returns benign empties.
    const routes: Record<string, unknown> = {
      '/api/session': { identity: 'alice@example.com', csrfToken: null, isAdmin: false, subscriptionsEnabled: false, uiSettings: {} },
      '/api/projects': { projects: [{ projectId: 'P-1', name: 'Apollo', role: 'admin' }] },
    };
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input).split('?')[0];
      const body = routes[path] ?? { notifications: [], unread: 0, sessions: [], boards: [], projects: [] };
      return { ok: true, status: 200, json: async () => body } as Response;
    }));
    const { QueryClient, QueryClientProvider } = await import('@tanstack/react-query');
    const { AuthProvider } = await import('../auth.js');
    const { App } = await import('../App.js');
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/notifications']}>
          <AuthProvider>
            <App />
          </AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    const switcher = await screen.findByLabelText('Project');
    expect(switcher.closest('nav')).toBeTruthy(); // lives in the sidebar
    expect(screen.getByRole('option', { name: 'Apollo' })).toBeInTheDocument();
  });

  /** Mounts the REAL `App` shell and opens the avatar menu. */
  async function mountShellMenu(sessionOver: Record<string, unknown> = {}): Promise<ReturnType<typeof vi.fn>> {
    const routes: Record<string, unknown> = {
      '/api/session': {
        identity: 'alice@example.com', csrfToken: null, isAdmin: false, subscriptionsEnabled: false, uiSettings: {},
        ...sessionOver,
      },
      '/api/projects': { projects: [] },
    };
    const spy = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input).split('?')[0];
      const body = routes[path] ?? { notifications: [], unread: 0, sessions: [], boards: [], projects: [] };
      return { ok: true, status: 200, json: async () => body } as Response;
    });
    vi.stubGlobal('fetch', spy);
    const { QueryClient, QueryClientProvider } = await import('@tanstack/react-query');
    const { AuthProvider } = await import('../auth.js');
    const { App } = await import('../App.js');
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={['/notifications']}>
          <AuthProvider><App /></AuthProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    const row = await screen.findByText('alice@example.com');
    row.closest('button')?.click();
    return spy;
  }

  it('REFUSAL: the retired themes are gone from the menu', async () => {
    // Owner decision 2026-08-10. Bone/NeoPOP/NeoPOP light were exploration leftovers offered to every
    // user as though supported. Intent is the shipped design and a one-option chooser is not a choice,
    // so the whole Theme section went with them.
    await mountShellMenu();
    await screen.findByText('Sign out');
    for (const gone of ['Bone', 'NeoPOP', 'NeoPOP light', 'Theme']) {
      expect(screen.queryByText(gone), `${gone} must no longer be offered`).not.toBeInTheDocument();
    }
    // POSITIVE CONTROL: the menu still works — the items that should remain are still there.
    expect(screen.getByText('Sign out')).toBeInTheDocument();
  });
});

/* ---- ui.tsx — the Intent component vocabulary --------------------------------------------- */
describe('roleOf — the status → role map (PRD P-3)', () => {
  // Roles exist so analytics never key on a column name or ordinal. If someone adds a status and
  // forgets a role, every derived measure silently loses those tickets — so assert TOTALITY.
  const ALL: readonly Status[] = STATUSES;

  it('maps every status to a role', () => {
    for (const s of ALL) expect(roleOf(s), `no role for ${s}`).toBeTruthy();
  });

  it('maps the lifecycle statuses to their PRD roles', () => {
    expect(roleOf('TODO')).toBe('ready');
    expect(roleOf('IN_PROGRESS')).toBe('doing');
    expect(roleOf('REVIEW')).toBe('handover');
    // BLOCKED → `parked` is the role EXCLUDED from the cost clock. Getting this wrong silently
    // inflates every cycle-time and cost figure in the product.
    expect(roleOf('BLOCKED')).toBe('parked');
    expect(roleOf('DONE')).toBe('accepted');
  });
});

describe('ui vocabulary', () => {
  // PRD P-1: facts and opinions must never render as the same visual class. A regression here is
  // easy to miss by eye but changes what a reviewer trusts.
  it('gives fact and opinion tags different classes', () => {
    const { container } = render(<><Tag kind="fact">a</Tag><Tag kind="opinion">b</Tag></>);
    const [fact, opinion] = Array.from(container.querySelectorAll('.tag'));
    expect(fact.className).toContain('tag-fact');
    expect(opinion.className).toContain('tag-opinion');
    expect(fact.className).not.toBe(opinion.className);
  });

  it('renders a role badge carrying its role class', () => {
    const { container } = render(<RoleBadge role="parked" />);
    expect(container.querySelector('.role-parked')).toBeTruthy();
  });

  it('renders a panel with its banded header, rows and footnote', () => {
    render(
      <Panel title="Cost record" right="R-034" footnote="excluded from the clock">
        <KV k="tokens">184k</KV>
        <LineRow label="doing" value="1h 38m" />
      </Panel>,
    );
    expect(screen.getByText('Cost record')).toBeInTheDocument();
    expect(screen.getByText('R-034')).toBeInTheDocument();
    expect(screen.getByText('184k')).toBeInTheDocument();
    expect(screen.getByText('1h 38m')).toBeInTheDocument();
    expect(screen.getByText('excluded from the clock')).toBeInTheDocument();
  });

  // Regression: deleting Settings' local Panel stripped `style={{padding:16}}` from its call
  // sites, so prose sat flush against the 1.5px rule and read as a rendering fault. `pad` is
  // the supported way to say "this body is prose, not self-padding rows".
  it('pads the body only when asked, so prose never sits flush against the rule', () => {
    const bare = render(<Panel title="t">x</Panel>).container;
    expect(bare.querySelector('.record-body')).toBeNull();
    const padded = render(<Panel title="t" pad>x</Panel>).container;
    expect(padded.querySelector('.record-body')).toBeTruthy();
  });

  it('marks a danger panel so it cannot be confused with an ordinary record', () => {
    const { container } = render(<Panel title="Guardrail" danger>x</Panel>);
    expect(container.querySelector('.record.danger')).toBeTruthy();
  });

  it('uses the gap step on Cluster rather than a free number', () => {
    const { container } = render(<Cluster gap={12}><span>a</span></Cluster>);
    expect((container.firstChild as HTMLElement).style.gap).toBe('12px');
  });

  it('renders a tile with its delta direction', () => {
    const { container } = render(<Tile label="Cycle time" value="3.8h" delta={{ dir: 'up', text: '39%' }} />);
    expect(container.querySelector('.tile-d.up')).toBeTruthy();
  });

  // These primitives are deliberately built AHEAD of the screens that will consume them (see the
  // banner in ui.tsx) — which means nothing else in the app imports them yet, so a typo in a class
  // name would ship silently and knip would call them dead. This pins the class hooks the CSS keys
  // on. Delete a case here only when the primitive itself goes.
  it('the not-yet-consumed primitives render the class hooks the CSS keys on', () => {
    const { container } = render(
      <Section>
        <Cols n={3} min={220}>
          <Chips><Chip on>a</Chip></Chips>
          <Pill tone="ok">ok</Pill>
          <Dot role="doing" />
          <Prov>chat</Prov>
          <Note>why</Note>
          <Empty>nothing here</Empty>
          <Spacer />
        </Cols>
      </Section>,
    );
    for (const sel of ['.record-section', '.cols', '.chips', '.chip.on', '.pill-ok', '.dot.doing',
      '.prov', '.record-note', '.empty']) {
      expect(container.querySelector(sel), sel).toBeTruthy();
    }
    // `min` is what stops columns collapsing below a readable width — a plain repeat(n, 1fr) reads
    // identically in a snapshot and is the bug this asserts against.
    expect((container.querySelector('.cols') as HTMLElement).style.gridTemplateColumns)
      .toBe('repeat(3, minmax(220px, 1fr))');
    expect(container.querySelector('.chip')?.getAttribute('aria-pressed')).toBe('true');
    expect(roleColor('parked')).toBe('var(--backlog)');
  });
});

/**
 * A COLLAPSED PANEL KEEPS ITS CHILDREN MOUNTED.
 *
 * `collapsible` renders a native `<details>` rather than `{open && children}`, and the difference is
 * not cosmetic: the Details fold holds the ticket's editable fields. Conditional rendering would
 * discard a half-typed value the moment someone folded the panel, and would make any control inside
 * unreachable to an accessible-name query while shut.
 *
 * `defaultOpen={false}` is used HERE and nowhere in the app yet — Details ships open. Without this
 * case the unmount-vs-hide distinction has no coverage at all: I mutated `Panel` to unmount on
 * collapse and the entire 19-test drawer suite stayed green, because nothing ever closes one.
 */
describe('Panel — collapsible', () => {
  it('renders a <details> whose summary carries the title', () => {
    const { container } = render(
      <Panel title="Details" collapsible>
        <p>field</p>
      </Panel>,
    );
    const box = container.querySelector('details.record');
    expect(box, 'collapsible must render a native <details>').toBeTruthy();
    expect(box!.hasAttribute('open')).toBe(true); // "collapsible" is not "collapsed"
    expect(container.querySelector('summary.record-head .record-title')?.textContent).toBe('Details');
  });

  it('keeps children in the DOM when closed, so a pending edit survives the fold', () => {
    render(
      <Panel title="Details" collapsible defaultOpen={false}>
        <input aria-label="Assignee" defaultValue="half-typed" />
      </Panel>,
    );
    // Present and queryable while shut — this is the assertion `{open && children}` would fail.
    expect(screen.getByLabelText('Assignee')).toHaveValue('half-typed');
  });

  it('is a plain <section> when not collapsible — the default is unchanged', () => {
    const { container } = render(<Panel title="Actions"><p>x</p></Panel>);
    expect(container.querySelector('section.record')).toBeTruthy();
    expect(container.querySelector('details.record')).toBeNull();
  });
});
