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
  vi.doMock('../projectContext.js', () => ({
    useProject: () => ({
      projectId: 'p1',
      setProjectId: () => {},
      loading: false,
      projects: [{ projectId: 'p1', name: 'P1', role }],
    }),
  }));
}

/**
 * Records every URL the screen asks for, and answers enough for it to finish rendering.
 *
 * `role` must match the one `mockProject` feeds. `Oncall.tsx` reads only `projectId` off this
 * response today, so a mismatch is inert — but a fixture whose membership row contradicts its own
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
});

const integrationCalls = (calls: string[]) => calls.filter((u) => /\/integrations/.test(u));

describe('Oncall screen', () => {
  it('a worker never REQUESTS integrations — the refusal is what pages', async () => {
    mockProject('worker');
    const calls = stub('worker');
    const { default: Oncall } = await import('../screens/Oncall.js');
    render(<Oncall />, { wrapper });
    // Wait for the screen to settle, so "no call" means "did not call", not "has not called YET".
    await waitFor(() => expect(calls.some((u) => u.includes('/projects'))).toBe(true));
    await new Promise((r) => setTimeout(r, 50));
    expect(integrationCalls(calls), 'a sub-master role must not call a master-tier tool').toEqual([]);
  });

  it('POSITIVE CONTROL: a master DOES request them, so the gate is not refusing everyone', async () => {
    mockProject('master');
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
  async function renderSetup(role: string) {
    mockProject(role);
    const calls = stub(role);
    const { OncallSetup } = await import('../screens/oncall/OncallSetup.js');
    render(<OncallSetup projectId="p1" integrations={[]} onClose={() => {}} />, { wrapper });
    const btn = await screen.findByText('Show deleted');
    fireEvent.click(btn);
    await new Promise((r) => setTimeout(r, 50));
    return calls;
  }

  it('a worker pressing Show deleted issues no request', async () => {
    const calls = await renderSetup('worker');
    expect(integrationCalls(calls), 'the deleted read is master-tier too').toEqual([]);
  });

  it('POSITIVE CONTROL: a master pressing Show deleted does issue one', async () => {
    const calls = await renderSetup('master');
    expect(
      integrationCalls(calls).length,
      'if this is 0 the worker assertion above proves nothing — the button did not fire',
    ).toBeGreaterThan(0);
  });
});
