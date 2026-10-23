import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { StatusPill, Avatar, PriorityDot } from '../components.js';
import { SignIn } from '../screens/SignIn.js';
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

describe('SignIn screen', () => {
  /**
   * Google sign-in was removed on 2026-07-29 — WorkOS is the only front door, and it brokers Google
   * itself. This used to assert a "Continue with Google" button; that assertion is now the exact
   * thing that must NOT be true, so it is inverted rather than deleted.
   *
   * jsdom gives `window.location.replace` no navigation, so the auto-redirect path renders the
   * fallback markup here. That is what makes the "no provider buttons" assertion below meaningful:
   * it inspects the branch a real browser would only reach on an error / access message / demo build.
   */
  it('renders the brand and tagline, and offers exactly ONE way in', () => {
    render(<SignIn />);
    // The brand is the LOCKUP now, not a text node — so this asserts the ACCESSIBLE NAME, which is
    // what a screen reader announces and the only thing that was ever worth checking here. A
    // `getByText` would have gone green again the moment someone dropped a stray "BatonDeck" string
    // anywhere on the page, and red for a purely visual change that broke nothing.
    expect(screen.getByRole('img', { name: 'BatonDeck' })).toBeInTheDocument();
    expect(screen.getByText(/task board your agents/i)).toBeInTheDocument();

    // REFUSAL: no provider chooser, and no direct-Google path back.
    expect(screen.queryByRole('button', { name: /continue with google/i })).toBeNull();
    expect(screen.queryByRole('button', { name: /continue with workos/i })).toBeNull();
    expect(document.body.innerHTML).not.toContain('/auth/google');

    // POSITIVE CONTROL: there IS still a way in, and it goes to WorkOS. Without this the refusal
    // above would pass just as happily on a screen that renders no sign-in at all.
    expect(screen.getByRole('button', { name: /^sign in$/i })).toBeInTheDocument();
  });

  /**
   * T-270: the human tenant gate's refusal has to be READABLE. The gateway redirects a tenant-less
   * sign-in to `/login?error=no_tenant`, and this screen is the whole user-visible half of that — a
   * refusal that renders a blank page, or that bounces straight back to the IdP that just refused,
   * is the failure mode the `error` branch exists to prevent.
   *
   * `window.location` is stubbed because both halves are read off it: `search` decides the copy, and
   * `replace` is the auto-redirect that must NOT fire. jsdom's own `location` is not assignable.
   */
  it('REFUSAL rendered: ?error=no_tenant names the remedy and does NOT bounce back to the IdP', () => {
    const replace = vi.fn();
    vi.stubGlobal('location', { search: '?error=no_tenant', replace, href: '' });
    render(<SignIn />);

    // T-387 NARROWED WHO REACHES `no_tenant`, so the copy this asserts moved with it. A user with NO
    // membership is now onboarded; the only people left here HOLD a membership that is not usable —
    // overwhelmingly an unaccepted invitation. The old copy ("not a member of any organization") would
    // be false for them and would send them to ask for access they have already been granted.
    expect(screen.getByText(/membership is not active yet/i)).toBeInTheDocument();
    // The actionable half — still the point of the message, but now naming the remedy that applies.
    expect(screen.getByText(/accept the invitation/i)).toBeInTheDocument();
    expect(replace, 'a refused sign-in must not be redirected back to the IdP').not.toHaveBeenCalled();
  });

  /**
   * T-388: the OTHER cause of a tenant-less sign-in, where the message above would be actively harmful.
   *
   * The gateway can now tell "WorkOS says you belong to nothing" from "WorkOS could not be reached", and
   * sends `?error=tenant_unavailable` for the second. An existing customer hitting a WorkOS incident
   * must not be told they belong to no organization — that is false — nor sent to ask an administrator
   * for membership they already hold. Retrying is the correct remedy here and the WRONG one above,
   * which is why the two messages must not converge.
   */
  it('REFUSAL rendered: ?error=tenant_unavailable says RETRY, not "ask an administrator"', () => {
    const replace = vi.fn();
    vi.stubGlobal('location', { search: '?error=tenant_unavailable', replace, href: '' });
    render(<SignIn />);

    expect(screen.getByText(/could not verify your organization/i)).toBeInTheDocument();
    expect(screen.getByText(/try signing in again/i)).toBeInTheDocument();
    // T-393's two codes must not be mistaken for this one: all three say "try again", and a screen that
    // rendered one copy for every retryable code would pass the assertion above.
    expect(screen.queryByText(/had already been used/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/slow first request/i)).not.toBeInTheDocument();
    // The discriminating assertions: neither of `no_tenant`'s two claims may appear. Without these the
    // test passes against a screen that renders the un-onboarded copy for both codes — which is exactly
    // the pre-T-388 behaviour and the thing being fixed.
    expect(screen.queryByText(/membership is not active yet/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/accept the invitation/i)).not.toBeInTheDocument();
    // It is still a refusal: no bounce back to the IdP that just failed.
    expect(replace).not.toHaveBeenCalled();
  });

  /**
   * T-393: the two failures a cold start produces. The load-bearing behaviour is not the wording but the
   * BUTTON — `?error=` keeps `autoRedirect` off, so the user mints a FRESH code, the only thing that
   * works once one is spent. An auto-redirect here would replay the dead code forever.
   */
  it('REFUSAL rendered: ?error=signin_expired blames the LINK, not the account, and offers a fresh one', () => {
    const replace = vi.fn();
    vi.stubGlobal('location', { search: '?error=signin_expired', replace, href: '' });
    render(<SignIn />);

    expect(screen.getByText(/had already been used/i)).toBeInTheDocument();
    expect(screen.getByText(/sign in again to get a fresh one/i)).toBeInTheDocument();
    // The discriminating half: the old 401 read like a broken account, so the copy must NOT suggest the
    // account is the problem, and must not be the un-onboarded message either.
    expect(screen.queryByText(/membership is not active yet/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/not permitted to sign in/i)).not.toBeInTheDocument();
    // The whole fix: a manual button, so the next attempt carries a NEW code.
    expect(replace, 'auto-redirecting here would replay the spent code').not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: /sign in/i })).toBeInTheDocument();
  });

  it('REFUSAL rendered: ?error=access_unavailable says it is us, not them, and does not bounce', () => {
    const replace = vi.fn();
    vi.stubGlobal('location', { search: '?error=access_unavailable', replace, href: '' });
    render(<SignIn />);

    expect(screen.getByText(/slow first request/i)).toBeInTheDocument();
    expect(screen.getByText(/not a problem with your account/i)).toBeInTheDocument();
    // Must not converge with `signin_expired`: nothing has been "used" here — the first attempt never
    // got far enough to spend anything, and telling the user otherwise sends them looking for a link.
    expect(screen.queryByText(/had already been used/i)).not.toBeInTheDocument();
    expect(replace).not.toHaveBeenCalled();
  });

  /**
   * A clean sign-in does NOT redirect. This binary has no `/auth/workos`, so it answers that path
   * with the SPA itself; a redirect there renders this screen again and reloads the page forever.
   * The manual button is the way on.
   */
  it('REFUSAL: a clean sign-in renders the card and does not redirect', () => {
    const replace = vi.fn();
    vi.stubGlobal('location', { search: '', replace, href: '' });
    render(<SignIn />);
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeInTheDocument();
    expect(replace).not.toHaveBeenCalled();
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
