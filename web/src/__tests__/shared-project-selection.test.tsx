/**
 * SETTINGS AND ON-CALL FOLLOW THE SIDEBAR'S PROJECT (P-434).
 *
 * Both screens kept their own project pick, starting from the first project whatever the sidebar
 * said, and their picker never moved the sidebar. Now there is one selection: the sidebar's
 * `ProjectSwitcher` and each screen's picker read and write the same `ProjectProvider` value (stored
 * under `bd.projectId`). The sidebar's "All projects" has no meaning on a screen that configures one
 * project, so there the screen takes the first project, as it did, and makes that the selection.
 *
 * Every case renders the REAL sidebar switcher beside the real screen, so "follows" is observed on
 * both controls rather than inferred from the context value.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ReactElement } from 'react';
import { ProjectSwitcher } from '../App.js';
import { Settings } from '../screens/Settings.js';
import Oncall from '../screens/Oncall.js';
import { AuthProvider } from '../auth.js';
import { ProjectProvider } from '../projectContext.js';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

const json = (b: unknown) => ({ ok: true, status: 200, json: async () => b }) as Response;

const PROJECTS = [
  { projectId: 'p1', name: 'Roadmap', role: 'admin' },
  { projectId: 'p2', name: 'Ops', role: 'admin' },
];

/** Answers the reads both screens make; records every URL so a test can see which project loaded. */
function stub(): string[] {
  const calls: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      calls.push(url);
      if (url.endsWith('/api/session')) return json({ identity: 'U-1', csrfToken: 'c', isAdmin: false, permissions: [], subscriptionsEnabled: false, uiSettings: {} });
      if (/\/api\/projects$/.test(url)) return json({ projects: PROJECTS });
      const one = /\/api\/projects\/(p[12])$/.exec(url);
      if (one) return json({ project: { id: one[1], name: one[1] === 'p1' ? 'Roadmap' : 'Ops', members: { 'U-1': 'admin' }, settings: {} }, memberDisplay: {} });
      if (/\/boards$/.test(url)) return json({ boards: [] });
      return json({});
    }),
  );
  return calls;
}

function mount(screenUnderTest: ReactElement): void {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <AuthProvider>
          <ProjectProvider>
            <ProjectSwitcher />
            {screenUnderTest}
          </ProjectProvider>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

const sidebar = () => screen.getByRole('combobox', { name: 'Project' });

describe('Settings shares the sidebar project selection (P-434)', () => {
  it('"All projects" in the sidebar: Settings takes the first project and the sidebar then names it', async () => {
    stub();
    mount(<Settings />);
    const picker = await screen.findByRole('combobox', { name: 'Project to configure' });
    await waitFor(() => expect(picker).toHaveValue('p1'));
    await waitFor(() => expect(sidebar()).toHaveValue('p1'));
    expect(localStorage.getItem('bd.projectId')).toBe('p1');
  });

  it('a project already picked in the sidebar is the one Settings opens on, not the first', async () => {
    localStorage.setItem('bd.projectId', 'p2');
    const calls = stub();
    mount(<Settings />);
    const picker = await screen.findByRole('combobox', { name: 'Project to configure' });
    await waitFor(() => expect(picker).toHaveValue('p2'));
    await waitFor(() => expect(calls.some((u) => /\/api\/projects\/p2$/.test(u))).toBe(true));
    expect(calls.some((u) => /\/api\/projects\/p1$/.test(u)), 'the first project is never loaded').toBe(false);
  });

  it('changing the sidebar changes Settings, and Settings loads that project', async () => {
    const calls = stub();
    mount(<Settings />);
    const picker = await screen.findByRole('combobox', { name: 'Project to configure' });
    await waitFor(() => expect(sidebar()).toHaveValue('p1'));
    fireEvent.change(sidebar(), { target: { value: 'p2' } });
    await waitFor(() => expect(picker).toHaveValue('p2'));
    await waitFor(() => expect(calls.some((u) => /\/api\/projects\/p2$/.test(u))).toBe(true));
  });

  it('changing the Settings picker changes the sidebar and the stored selection', async () => {
    stub();
    mount(<Settings />);
    const picker = await screen.findByRole('combobox', { name: 'Project to configure' });
    await waitFor(() => expect(sidebar()).toHaveValue('p1'));
    fireEvent.change(picker, { target: { value: 'p2' } });
    await waitFor(() => expect(sidebar()).toHaveValue('p2'));
    expect(localStorage.getItem('bd.projectId')).toBe('p2');
  });

  it('"All projects" picked in the sidebar while on Settings comes back as the first project', async () => {
    localStorage.setItem('bd.projectId', 'p2');
    stub();
    mount(<Settings />);
    const picker = await screen.findByRole('combobox', { name: 'Project to configure' });
    await waitFor(() => expect(sidebar()).toHaveValue('p2'));
    fireEvent.change(sidebar(), { target: { value: '' } });
    await waitFor(() => expect(picker).toHaveValue('p1'));
    await waitFor(() => expect(sidebar()).toHaveValue('p1'));
  });
});

describe('On-call shares the sidebar project selection (P-434)', () => {
  it('"All projects" in the sidebar: On-call takes the first project and the sidebar then names it', async () => {
    stub();
    mount(<Oncall />);
    const picker = await screen.findByRole('combobox', { name: 'On-call project' });
    await waitFor(() => expect(picker).toHaveValue('p1'));
    await waitFor(() => expect(sidebar()).toHaveValue('p1'));
  });

  it('changing the sidebar changes On-call', async () => {
    stub();
    mount(<Oncall />);
    const picker = await screen.findByRole('combobox', { name: 'On-call project' });
    await waitFor(() => expect(sidebar()).toHaveValue('p1'));
    fireEvent.change(sidebar(), { target: { value: 'p2' } });
    await waitFor(() => expect(picker).toHaveValue('p2'));
  });

  it('changing the On-call picker changes the sidebar and the stored selection', async () => {
    stub();
    mount(<Oncall />);
    const picker = await screen.findByRole('combobox', { name: 'On-call project' });
    await waitFor(() => expect(sidebar()).toHaveValue('p1'));
    fireEvent.change(picker, { target: { value: 'p2' } });
    await waitFor(() => expect(sidebar()).toHaveValue('p2'));
    expect(localStorage.getItem('bd.projectId')).toBe('p2');
  });
});
