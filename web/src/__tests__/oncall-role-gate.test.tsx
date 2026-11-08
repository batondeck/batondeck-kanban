/**
 * NO SCREEN REQUESTS `list_integrations` BELOW MASTER — asserted by watching `fetch`, not by reading
 * the source.
 *
 * WHY THIS FILE EXISTS. The gate was guarded lexically (`ui-polish.test.tsx`), and two adversarial
 * passes defeated that guard SEVEN ways without touching a single assertion's text:
 *
 *   - a second, entirely ungated `useQuery` in an already-enumerated file (per-file, not per-call)
 *   - `const mayReadIntegrations = true`, then `let` + reassign when the `const` spelling was pinned
 *   - a decoy `enabled:` in a nested object, because `.exec` reads only the FIRST match in a block
 *   - `(a && mayReadIntegrations) || a` — the regex forbids nothing after the conjunction it requires
 *   - ONE `{` inside a string literal, which unbalances the brace matcher so a block runs to EOF and
 *     inherits the next query's `enabled:`
 *
 * Each of those shipped a live, ungated, auto-refetching master-tier call with the whole suite green.
 * That is not a guard that needs another regex; it is the wrong layer. `web/` is ALSO not linted at
 * all (`eslint.config.js` ignores it, `web/package.json` has no lint script), so nothing sits behind
 * it either.
 *
 * CONTRACTING RULE 12: this guards a CALL SITE, so drive the real component and watch the real edge.
 * None of the five defeats above survives here, because none of them changes what `fetch` sees.
 *
 * WHY IT MATTERS AT ALL (rule 19): `list_integrations` is `requireMaster`. A refusal is not an inert
 * miss — the core answers `Requires master … (have: worker)`, `signals.ts` matches `/^Requires /`,
 * and `escalation-deny.yaml` pages CRITICAL on >3 in 5 minutes. `Oncall.tsx` refetches every 60s, so
 * sub-master readers with the page open page the project owner while touching nothing.
 *
 * THIS TEST DOES NOT SUBSUME THE LEXICAL ONE, and a sixth pass proved it by deleting the lexical
 * assertions and then landing a defect this file cannot see: a second `useQuery` in `Oncall.tsx`
 * gated `enabled: setupOpen && Boolean(activeProject)` — no role gate, behind the Setup gear. This
 * suite never clicks that gear, so it stayed green at 527/527 while the deleted assertion caught it
 * outright. A render test observes only what its FIXTURES REACH.
 *
 * So the two are kept and they cover different things: this one proves the gate HOLDS on the paths
 * it drives; `ui-polish.test.tsx` enumerates every call site and every new caller file. When a defeat
 * gets past both, the answer is a new fixture HERE — not a tighter regex there.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, cleanup, waitFor, fireEvent } from '@testing-library/react';
import type { ReactNode } from 'react';

const ING = 'ing_ba3c6ec8c12cc970c3ae16e2f62ddbaf';

function mockProject(role: string) {
  const ctx = () => ({
    projectId: 'p1',
    setProjectId: () => {},
    loading: false,
    projects: [{ projectId: 'p1', name: 'P1', role }],
  });
  vi.doMock('../projectContext.js', () => ({ useProject: ctx, useSingleProject: ctx }));
}

/**
 * The session half of the gate (P-248): `list_integrations` is workspace-admin only BEFORE it is
 * master-tier (`internal/service/authz.go`), so a fixture states both. Only `useAuth` is replaced;
 * the rest of the module is the real one.
 */
function mockSession(isAdmin: boolean) {
  vi.doMock('../auth.js', async () => {
    const actual = await vi.importActual<typeof import('../auth.js')>('../auth.js');
    return { ...actual, useAuth: () => ({ ...actual.useAuth(), identity: 'U-1', isAdmin }) };
  });
}

/** A caller: their role in p1 and whether the session is a workspace admin. */
function mockCaller(role: string, isAdmin: boolean) {
  mockProject(role);
  mockSession(isAdmin);
}

/**
 * Records every URL the screen asks for, and answers enough for it to finish rendering.
 *
 * `role` must match the one `mockProject` feeds. `Oncall.tsx` takes its project from the shared
 * selection (P-434), not this response, so a mismatch is inert — but a fixture whose membership row contradicts its own
 * premise is one refactor away from making the worker assertion vacuous.
 */
function stub(role: string): string[] {
  const calls: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      calls.push(url);
      const json = (b: unknown) => ({ ok: true, status: 200, json: async () => b }) as Response;
      if (url.includes('/integrations')) {
        return json({ integrations: [{ id: ING, name: 'prod-grafana', enabled: true, source: 'grafana' }] });
      }
      if (url.includes('/oncall/sources') || url.includes('/sources')) {
        return json({
          sources: [
            { id: 'grafana', label: 'Grafana', authModes: ['hmac'], docsPage: 'grafana', authNote: '' },
          ],
        });
      }
      if (url.includes('/projects')) {
        return json({ projects: [{ projectId: 'p1', name: 'P1', role }] });
      }
      return json({});
    }) as unknown as typeof fetch,
  );
  return calls;
}

const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    {children}
  </QueryClientProvider>
);

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.resetModules();
  vi.doUnmock('../projectContext.js');
  vi.doUnmock('../auth.js');
});

const integrationCalls = (calls: string[]) => calls.filter((u) => /\/integrations/.test(u));

describe('Oncall screen', () => {
  async function renderOncall(role: string, isAdmin: boolean) {
    mockCaller(role, isAdmin);
    const calls = stub(role);
    const { default: Oncall } = await import('../screens/Oncall.js');
    render(<Oncall />, { wrapper });
    // Wait for the screen to settle, so "no call" means "did not call", not "has not called YET".
    // The project comes from the shared selection (P-434), which `mockProject` holds already, so the
    // screen decides its integrations read on its first render; the heading says that render happened.
    await screen.findByRole('heading', { name: 'On-call' });
    await new Promise((r) => setTimeout(r, 50));
    return calls;
  }

  it('a worker never REQUESTS integrations, even as a workspace admin', async () => {
    const calls = await renderOncall('worker', true);
    expect(integrationCalls(calls), 'a sub-master role must not call a master-tier tool').toEqual([]);
  });

  it('a project master who is not a workspace admin never requests them (P-248)', async () => {
    const calls = await renderOncall('master', false);
    expect(integrationCalls(calls), 'list_integrations is workspace-admin only before it is master-tier').toEqual([]);
  });

  it('POSITIVE CONTROL: a master who is a workspace admin DOES request them, so the gate is not refusing everyone', async () => {
    mockCaller('master', true);
    const calls = stub('master');
    const { default: Oncall } = await import('../screens/Oncall.js');
    render(<Oncall />, { wrapper });
    await waitFor(() => expect(integrationCalls(calls).length).toBeGreaterThan(0));
  });
});

describe('OncallSetup deleted-integrations read', () => {
  /**
   * This one is gated on `showDeleted && mayReadIntegrations`, so the button must actually be pressed
   * — otherwise the assertion passes because nothing ever asked, which is rule 18's vacuous zero. The
   * positive control below is what proves the press works.
   */
  async function renderSetup(role: string, isAdmin: boolean) {
    mockCaller(role, isAdmin);
    const calls = stub(role);
    const { OncallSetup } = await import('../screens/oncall/OncallSetup.js');
    render(<OncallSetup projectId="p1" integrations={[]} onClose={() => {}} />, { wrapper });
    const btn = await screen.findByText('Show deleted');
    fireEvent.click(btn);
    await new Promise((r) => setTimeout(r, 50));
    return calls;
  }

  it('a worker pressing Show deleted issues no request, even as a workspace admin', async () => {
    const calls = await renderSetup('worker', true);
    expect(integrationCalls(calls), 'the deleted read is master-tier too').toEqual([]);
  });

  it('a project master who is not a workspace admin pressing Show deleted issues no request (P-248)', async () => {
    const calls = await renderSetup('master', false);
    expect(integrationCalls(calls), 'the deleted read is workspace-admin only too').toEqual([]);
  });

  it('POSITIVE CONTROL: a master who is a workspace admin pressing Show deleted does issue one', async () => {
    const calls = await renderSetup('master', true);
    expect(
      integrationCalls(calls).length,
      'if this is 0 the assertions above prove nothing — the button did not fire',
    ).toBeGreaterThan(0);
  });
});

describe('useDisplayNames (Board, Activity, Analytics, the ticket Activity panel)', () => {
  /**
   * The busiest call site: it mounts on four screens, so this is where a project master who is not a
   * workspace admin met the 403 (P-248). The project read must still go out, so "no integrations
   * request" cannot pass because the hook did nothing at all.
   */
  async function renderHook(role: string, isAdmin: boolean) {
    mockCaller(role, isAdmin);
    const calls = stub(role);
    const { useDisplayNames } = await import('../displayNames.js');
    function Probe() {
      const names = useDisplayNames('p1');
      return <span data-testid="names">{Object.values(names).join(',')}</span>;
    }
    render(<Probe />, { wrapper });
    await waitFor(() => expect(calls.some((u) => /\/projects\/p1$/.test(u))).toBe(true));
    await new Promise((r) => setTimeout(r, 50));
    return calls;
  }

  it('a project master who is not a workspace admin issues no integrations request (P-248)', async () => {
    const calls = await renderHook('master', false);
    expect(integrationCalls(calls)).toEqual([]);
  });

  it('POSITIVE CONTROL: a master who is a workspace admin does, and the name resolves', async () => {
    const calls = await renderHook('master', true);
    await waitFor(() => expect(integrationCalls(calls).length).toBeGreaterThan(0));
    await waitFor(() => expect(screen.getByTestId('names').textContent).toContain('prod-grafana'));
  });
});
