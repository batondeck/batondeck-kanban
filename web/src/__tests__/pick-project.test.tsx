/**
 * ON "ALL PROJECTS" A ONE-PROJECT SCREEN ASKS FOR A PROJECT; IT DOES NOT CLAIM THERE IS NONE (P-457).
 *
 * "All projects" is the sidebar's default after a first sign-in. Analytics said "No projects yet",
 * Reports "No projects yet — create one", the activity feed "No activity yet" and the Agents roster
 * "No identities in this project yet", each although the caller had projects. They now show "Pick a
 * project" with the caller's projects as buttons; a click sets the sidebar's selection (the same
 * `bd.projectId`) and the screen loads that project. With no project at all the old message stands.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ReactElement } from 'react';
import { Analytics } from '../screens/Analytics.js';
import { Reports } from '../screens/Reports.js';
import { ActivityLog } from '../screens/ActivityLog.js';
import { Agents } from '../screens/Agents.js';
import { AuthProvider } from '../auth.js';
import { ProjectProvider } from '../projectContext.js';

class FakeES {
  readyState = 0;
  addEventListener(): void {}
  close(): void {
    this.readyState = 2;
  }
}

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

function stub(projects: typeof PROJECTS): string[] {
  const calls: string[] = [];
  vi.stubGlobal('EventSource', FakeES);
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    calls.push(`${init?.method ?? 'GET'} ${url}`);
    if (url.endsWith('/api/session')) return json({ identity: 'U-1', csrfToken: 'c', isAdmin: true, permissions: [], subscriptionsEnabled: false, uiSettings: {} });
    if (/\/api\/projects$/.test(url)) return json({ projects });
    const one = /\/api\/projects\/(p[12])$/.exec(url);
    if (one) return json({ project: { id: one[1], name: 'x', members: { 'U-1': 'admin' }, settings: {} }, memberDisplay: {} });
    if (/\/boards$/.test(url)) return json({ boards: [] });
    if (/\/agents\/sessions/.test(url)) return json({ sessions: [] });
    if (/\/audit/.test(url)) return json({ entries: [] });
    return json({});
  }));
  return calls;
}

function show(screenEl: ReactElement) {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <AuthProvider>
        <ProjectProvider>
          <MemoryRouter>{screenEl}</MemoryRouter>
        </ProjectProvider>
      </AuthProvider>
    </QueryClientProvider>,
  );
}

const SCREENS: Array<[string, () => ReactElement, RegExp]> = [
  ['Analytics', () => <Analytics />, /No projects yet\. Create a project/],
  ['Reports', () => <Reports />, /No projects yet — create one/],
  ['the activity feed', () => <ActivityLog />, /No activity yet/],
  ['the Agents roster', () => <Agents />, /No identities in this project yet/],
];

describe('on "All projects" with projects', () => {
  it.each(SCREENS)('%s asks for a project and offers each one, instead of its old empty message', async (_name, el, old) => {
    stub(PROJECTS);
    show(el());
    expect(await screen.findByText('Pick a project')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Roadmap' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Ops' })).toBeInTheDocument();
    expect(screen.queryByText(old)).toBeNull();
    expect(screen.queryByText('No projects yet')).toBeNull();
  });

  it('picking one sets the shared selection, and the screen loads that project', async () => {
    const calls = stub(PROJECTS);
    show(<Analytics />);
    fireEvent.click(await screen.findByRole('button', { name: 'Ops' }));
    await waitFor(() => expect(localStorage.getItem('bd.projectId')).toBe('p2'));
    await waitFor(() => expect(calls.some((c) => /\/api\/projects\/p2\/boards$/.test(c))).toBe(true));
    expect(screen.queryByText('Pick a project')).toBeNull();
    expect(calls.some((c) => /\/api\/projects\/p1\//.test(c))).toBe(false);
  });

  it('the Agents tile does not report zero claimed tasks for no project', async () => {
    stub(PROJECTS);
    show(<Agents />);
    await screen.findByText('Pick a project');
    expect(screen.getByText('pick a project')).toBeInTheDocument();
  });
});

describe('with no project at all', () => {
  it.each(SCREENS.slice(0, 2))('%s says so, with the way to make one', async (_name, el) => {
    stub([]);
    show(el());
    expect(await screen.findByText('No projects yet')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Go to Projects' })).toHaveAttribute('href', '/projects');
    expect(screen.queryByText('Pick a project')).toBeNull();
  });
});

describe('POSITIVE CONTROL: with a project selected', () => {
  it.each(SCREENS)('%s shows no prompt', async (_name, el) => {
    localStorage.setItem('bd.projectId', 'p1');
    const calls = stub(PROJECTS);
    show(el());
    await waitFor(() => expect(calls.some((c) => /\/api\/projects\/p1\/boards$/.test(c))).toBe(true));
    expect(screen.queryByText('Pick a project')).toBeNull();
  });
});
