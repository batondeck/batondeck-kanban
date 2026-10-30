/**
 * `useDisplayNames` IS TESTED BY WHAT IT RETURNS, not by grepping its call sites.
 *
 * The first guard for this was two source-text assertions — "no call site says `eventActor(e)`" and
 * "the file mentions `useDisplayNames`". An adversarial review killed it in one move: stub the hook
 * to return `{}` and the ENTIRE web suite still passed, 515/515, with the feature completely dead.
 * The first assertion matched nothing (every call site already passed the map) and the second was
 * satisfied by the import line.
 *
 * A lexical guard can say a name appears. Only a behavioural one can say the map is BUILT.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

const ADMIN = 'U-511987b3-26cd-413e-89ae-9b549945081a';
const ING = 'ing_ba3c6ec8c12cc970c3ae16e2f62ddbaf';

/** `role` decides whether the integrations read is even attempted — see the role gate in the hook. */
function mockProject(role: string) {
  vi.doMock('../projectContext.js', () => ({
    useProject: () => ({ projectId: 'p1', setProjectId: () => {}, loading: false,
      projects: [{ projectId: 'p1', name: 'P1', role }] }),
  }));
}

/**
 * The session half of the integrations gate (P-248): `list_integrations` is workspace-admin only
 * before it is master-tier (`internal/service/authz.go`). Only `useAuth` is replaced.
 */
function mockSession(isAdmin: boolean) {
  vi.doMock('../auth.js', async () => {
    const actual = await vi.importActual<typeof import('../auth.js')>('../auth.js');
    return { ...actual, useAuth: () => ({ ...actual.useAuth(), identity: ADMIN, isAdmin }) };
  });
}

function stub(): string[] {
  const calls: string[] = [];
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    calls.push(url);
    const json = (b: unknown) => ({ ok: true, status: 200, json: async () => b }) as Response;
    if (url.includes('/integrations')) {
      return json({ integrations: [{ id: ING, name: 'prod-grafana' }] });
    }
    // get_project carries `memberDisplay` as a SIBLING of `project`.
    return json({ project: { id: 'p1' }, memberDisplay: { [ADMIN]: 'sumit@batondeck.com' } });
  }) as unknown as typeof fetch);
  return calls;
}

const wrapper = ({ children }: { children: ReactNode }) => (
  <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    {children}
  </QueryClientProvider>
);

afterEach(() => { vi.unstubAllGlobals(); vi.resetModules(); vi.doUnmock('../projectContext.js'); vi.doUnmock('../auth.js'); });

describe('useDisplayNames builds a real map', () => {
  it('resolves an account id from memberDisplay', async () => {
    mockProject('admin');
    stub();
    const { useDisplayNames: hook } = await import('../displayNames.js');
    const { result } = renderHook(() => hook('p1'), { wrapper });
    await waitFor(() => expect(result.current[ADMIN]).toBe('sumit@batondeck.com'));
  });

  it('resolves an integration id by its BARE id, the shape eventActor looks up', async () => {
    mockProject('admin');
    mockSession(true);
    stub();
    const { useDisplayNames: hook } = await import('../displayNames.js');
    const { result } = renderHook(() => hook('p1'), { wrapper });
    // Keyed WITHOUT the `ingest:` prefix — eventActor strips it before looking up.
    await waitFor(() => expect(result.current[ING]).toBe('prod-grafana'));
  });

  /**
   * THE RULE-19 GUARD. `list_integrations` is `requireMaster`; a refusal answers `Requires master …`,
   * which `signals.ts` turns into an escalation signal and `escalation-deny.yaml` pages CRITICAL on.
   * A viewer opening a board must therefore not make the call at all.
   */
  it('a viewer never REQUESTS integrations — the refusal is what pages', async () => {
    mockProject('viewer');
    const calls = stub();
    const { useDisplayNames: hook } = await import('../displayNames.js');
    const { result } = renderHook(() => hook('p1'), { wrapper });
    await waitFor(() => expect(result.current[ADMIN]).toBe('sumit@batondeck.com'));
    expect(calls.filter((u) => u.includes('/integrations')),
      'a sub-master role must not call a master-tier tool').toEqual([]);
  });

  it('POSITIVE CONTROL: a master who is a workspace admin DOES request them, so the gate is not refusing everyone', async () => {
    mockProject('master');
    mockSession(true);
    const calls = stub();
    const { useDisplayNames: hook } = await import('../displayNames.js');
    const { result } = renderHook(() => hook('p1'), { wrapper });
    await waitFor(() => expect(result.current[ING]).toBe('prod-grafana'));
    expect(calls.some((u) => u.includes('/integrations'))).toBe(true);
  });
});
