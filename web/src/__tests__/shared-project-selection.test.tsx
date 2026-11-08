/**
 * SETTINGS AND ON-CALL FOLLOW THE SIDEBAR'S PROJECT (P-434), AND ASK FOR ONE ON "ALL PROJECTS" (P-442).
 *
 * Both screens kept their own project pick, starting from the first project whatever the sidebar
 * said, and their picker never moved the sidebar. Now there is one selection: the sidebar's
 * `ProjectSwitcher` and each screen's picker read and write the same `ProjectProvider` value (stored
 * under `bd.projectId`). The sidebar's "All projects" has no meaning on a screen that configures one
 * project. P-434 took the first project there and stored it, which edited a project nobody chose and
 * moved the sidebar off "All projects"; now the screen shows its picker with nothing chosen and the
 * prompt "Pick a project to configure", and loads, edits and stores nothing until one is picked.
 *
 * Every case renders the REAL sidebar switcher beside the real screen, so "follows" is observed on
 * both controls rather than inferred from the context value.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { Link, MemoryRouter, Route, Routes } from 'react-router';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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

/**
 * Answers the reads both screens make; records every request (`METHOD url`) so a test can see which
 * project loaded and that nothing was written.
 */
function stub(projects = PROJECTS): string[] {
  const calls: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push(`${init?.method ?? 'GET'} ${url}`);
      if (url.endsWith('/api/session')) return json({ identity: 'U-1', csrfToken: 'c', isAdmin: true, permissions: [], subscriptionsEnabled: false, uiSettings: {} });
      if (/\/api\/projects$/.test(url)) return json({ projects });
      const one = /\/api\/projects\/(p[12])$/.exec(url);
      if (one) return json({ project: { id: one[1], name: one[1] === 'p1' ? 'Roadmap' : 'Ops', members: { 'U-1': 'admin' }, settings: {} }, memberDisplay: {} });
      if (/\/boards$/.test(url)) return json({ boards: [] });
      return json({});
    }),
  );
  return calls;
}

/** The screen at `/screen`, the real sidebar switcher beside it, and a link away to `/elsewhere`. */
function mount(screenUnderTest: ReactElement): void {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/screen']}>
        <AuthProvider>
          <ProjectProvider>
            <ProjectSwitcher />
            <Link to="/elsewhere">Leave</Link>
            <Routes>
              <Route path="/screen" element={screenUnderTest} />
              <Route path="/elsewhere" element={<p>Elsewhere</p>} />
            </Routes>
          </ProjectProvider>
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

const settle = () => act(() => new Promise<void>((r) => setTimeout(r, 50)));
const loaded = (calls: string[], re: RegExp) => calls.some((c) => re.test(c));
const wrote = (calls: string[]) => calls.filter((c) => !c.startsWith('GET '));

const sidebar = () => screen.getByRole('combobox', { name: 'Project' });
const value = (el: HTMLElement) => (el as HTMLSelectElement).value;
const prompt = () => screen.queryByRole('heading', { name: 'Pick a project to configure' });

describe('Settings on "All projects" asks for a project and stores nothing (P-442)', () => {
  it('shows its picker with no project chosen and the prompt; loads, edits and stores nothing; the sidebar stays on All', async () => {
    const calls = stub();
    mount(<Settings />);
    const picker = await screen.findByRole('combobox', { name: 'Project to configure' });
    await within(picker).findByRole('option', { name: 'Ops' });
    await settle();
    expect(value(picker)).toBe('');
    expect(screen.getByRole('option', { name: 'Pick a project' })).toBeDisabled();
    expect(prompt()).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Columns' })).toBeNull();
    expect(screen.queryByRole('heading', { name: 'Members' })).toBeNull();
    expect(screen.queryByRole('heading', { name: 'Workflow Settings' })).toBeNull();
    expect(value(sidebar())).toBe('');
    expect(localStorage.getItem('bd.projectId') ?? '').toBe('');
    expect(loaded(calls, /\/api\/projects\/p[12]/), 'no project is loaded').toBe(false);
    expect(wrote(calls)).toEqual([]);
    // The personal sections stay: they follow the person, not the project.
    expect(screen.getByRole('heading', { name: 'Your MCP sessions' })).toBeInTheDocument();
  });

  it('leaving Settings on "All projects" leaves the sidebar on "All projects"', async () => {
    stub();
    mount(<Settings />);
    const picker = await screen.findByRole('combobox', { name: 'Project to configure' });
    await within(picker).findByRole('option', { name: 'Ops' });
    await settle();
    fireEvent.click(screen.getByRole('link', { name: 'Leave' }));
    await screen.findByText('Elsewhere');
    await settle();
    expect(value(sidebar())).toBe('');
    expect(localStorage.getItem('bd.projectId') ?? '').toBe('');
  });

  it('a project already picked in the sidebar is the one Settings opens on, with no prompt', async () => {
    localStorage.setItem('bd.projectId', 'p2');
    const calls = stub();
    mount(<Settings />);
    const picker = await screen.findByRole('combobox', { name: 'Project to configure' });
    await waitFor(() => expect(value(picker)).toBe('p2'));
    await waitFor(() => expect(loaded(calls, /\/api\/projects\/p2$/)).toBe(true));
    expect(loaded(calls, /\/api\/projects\/p1$/), 'the first project is never loaded').toBe(false);
    expect(prompt()).toBeNull();
    expect(screen.queryByRole('option', { name: 'Pick a project' })).toBeNull();
  });

  it('picking a project in the sidebar: Settings loads it and the prompt goes', async () => {
    const calls = stub();
    mount(<Settings />);
    const picker = await screen.findByRole('combobox', { name: 'Project to configure' });
    await within(picker).findByRole('option', { name: 'Ops' });
    expect(prompt()).toBeInTheDocument();
    fireEvent.change(sidebar(), { target: { value: 'p2' } });
    await waitFor(() => expect(value(picker)).toBe('p2'));
    await waitFor(() => expect(loaded(calls, /\/api\/projects\/p2$/)).toBe(true));
    expect(prompt()).toBeNull();
    expect(screen.getByRole('heading', { name: 'Columns' })).toBeInTheDocument();
    expect(localStorage.getItem('bd.projectId')).toBe('p2');
  });

  it('picking a project in the Settings picker sets the sidebar and the stored selection', async () => {
    stub();
    mount(<Settings />);
    const picker = await screen.findByRole('combobox', { name: 'Project to configure' });
    await within(picker).findByRole('option', { name: 'Ops' });
    fireEvent.change(picker, { target: { value: 'p2' } });
    await waitFor(() => expect(value(sidebar())).toBe('p2'));
    expect(localStorage.getItem('bd.projectId')).toBe('p2');
    expect(prompt()).toBeNull();
  });

  it('"All projects" picked in the sidebar while on Settings brings the prompt back, not the first project', async () => {
    localStorage.setItem('bd.projectId', 'p2');
    stub();
    mount(<Settings />);
    const picker = await screen.findByRole('combobox', { name: 'Project to configure' });
    await waitFor(() => expect(value(sidebar())).toBe('p2'));
    fireEvent.change(sidebar(), { target: { value: '' } });
    await waitFor(() => expect(prompt()).toBeInTheDocument());
    await settle();
    expect(value(picker)).toBe('');
    expect(value(sidebar())).toBe('');
    expect(localStorage.getItem('bd.projectId')).toBe('');
  });
});

describe('On-call on "All projects" asks for a project and stores nothing (P-442)', () => {
  it('shows its picker with no project chosen and the prompt; reads no integrations, Setup is closed, the sidebar stays on All', async () => {
    const calls = stub();
    mount(<Oncall />);
    const picker = await screen.findByRole('combobox', { name: 'On-call project' });
    await within(picker).findByRole('option', { name: 'Ops' });
    await settle();
    expect(value(picker)).toBe('');
    expect(screen.getByText('Pick a project to configure')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'On-call setup' })).toBeDisabled();
    expect(value(sidebar())).toBe('');
    expect(localStorage.getItem('bd.projectId') ?? '').toBe('');
    expect(loaded(calls, /\/api\/projects\/p[12]\//), 'no project is read').toBe(false);
    expect(wrote(calls)).toEqual([]);
  });

  it('with a single project the picker still shows on "All projects", so it can be picked', async () => {
    stub([PROJECTS[0]!]);
    mount(<Oncall />);
    const picker = await screen.findByRole('combobox', { name: 'On-call project' });
    await within(picker).findByRole('option', { name: 'Roadmap' });
    expect(value(picker)).toBe('');
    expect(screen.getByText('Pick a project to configure')).toBeInTheDocument();
    fireEvent.change(picker, { target: { value: 'p1' } });
    await waitFor(() => expect(value(sidebar())).toBe('p1'));
    expect(screen.queryByText('Pick a project to configure')).toBeNull();
    // One project, chosen: the picker has nothing left to offer.
    expect(screen.queryByRole('combobox', { name: 'On-call project' })).toBeNull();
  });

  it('picking a project in the sidebar: On-call follows and the prompt goes', async () => {
    stub();
    mount(<Oncall />);
    const picker = await screen.findByRole('combobox', { name: 'On-call project' });
    await within(picker).findByRole('option', { name: 'Ops' });
    fireEvent.change(sidebar(), { target: { value: 'p2' } });
    await waitFor(() => expect(value(picker)).toBe('p2'));
    expect(screen.queryByText('Pick a project to configure')).toBeNull();
    expect(screen.getByRole('button', { name: 'On-call setup' })).toBeEnabled();
  });

  it('picking a project in the On-call picker sets the sidebar and the stored selection', async () => {
    stub();
    mount(<Oncall />);
    const picker = await screen.findByRole('combobox', { name: 'On-call project' });
    await within(picker).findByRole('option', { name: 'Ops' });
    fireEvent.change(picker, { target: { value: 'p2' } });
    await waitFor(() => expect(value(sidebar())).toBe('p2'));
    expect(localStorage.getItem('bd.projectId')).toBe('p2');
  });

  it('"All projects" picked in the sidebar while on On-call brings the prompt back', async () => {
    localStorage.setItem('bd.projectId', 'p2');
    stub();
    mount(<Oncall />);
    const picker = await screen.findByRole('combobox', { name: 'On-call project' });
    await waitFor(() => expect(value(picker)).toBe('p2'));
    fireEvent.change(sidebar(), { target: { value: '' } });
    await waitFor(() => expect(screen.getByText('Pick a project to configure')).toBeInTheDocument());
    expect(value(picker)).toBe('');
    expect(value(sidebar())).toBe('');
  });
});
