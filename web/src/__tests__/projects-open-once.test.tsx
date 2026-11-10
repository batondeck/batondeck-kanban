/**
 * Opening a project with no board creates its first board ONCE.
 *
 * A project's card lists the project's boards and, finding none, creates one. A double click ran
 * that twice: both clicks saw no board and both created "<name> board", so the project started life
 * with two identical boards.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { Projects } from '../screens/Projects.js';
import { ProjectProvider } from '../projectContext.js';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('a project card', () => {
  it('a double click on a project with no board creates ONE board', async () => {
    const created: string[] = [];
    let release: () => void = () => {};
    const listed = new Promise<void>((r) => { release = r; });
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const p = String(input).split('?')[0]!;
      const json = (b: unknown) => ({ ok: true, status: 200, json: async () => b }) as Response;
      if (p === '/api/projects') return json({ projects: [{ projectId: 'P-1', name: 'Apollo', role: 'admin', members: {} }] });
      if (p === '/api/projects/P-1/boards' && init?.method === 'POST') {
        created.push(p);
        return json({ board: { id: `B-${created.length}`, projectId: 'P-1', name: 'Apollo board', columns: [] } });
      }
      if (p === '/api/projects/P-1/boards') {
        await listed; // both clicks are in before the first list answers
        return json({ boards: [] });
      }
      return json({ sessions: [] });
    }));
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MemoryRouter initialEntries={['/projects']}>
          <ProjectProvider>
            <Routes>
              <Route path="/projects" element={<Projects />} />
              <Route path="/projects/:projectId/boards/:boardId" element={<p>board open</p>} />
            </Routes>
          </ProjectProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    const card = (await screen.findByText('Apollo')).closest('.proj-card')!;
    fireEvent.click(card);
    fireEvent.click(card);
    await act(async () => { release(); await new Promise((r) => setTimeout(r, 20)); });
    expect(await screen.findByText('board open')).toBeTruthy();
    expect(created, 'the second click created a second board').toHaveLength(1);
  });
});
