/**
 * T-430 — the Chronicle byline: collaborator avatar chips, names-only tooltips, short version label.
 *
 * The owner's rule for this surface: profile chips for everyone who collaborated on a version, their
 * display NAMES on the tooltip, and NEVER a raw email or account id anywhere in the byline DOM. The
 * version label drops the `CV-` prefix (`v ae1e8df`), with the full id kept on the title attribute.
 *
 * What the response shape can represent bounds "collaborators" at two per version: `agent` (display
 * name, when the write came through the agent surface) and `author` (the caller identity — a human's
 * account id when a person made or co-made it, a service identity when the agent acted alone).
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { render, cleanup, waitFor } from '@testing-library/react';
import { ChroniclePageView } from '../screens/Chronicle.js';
import { ProjectProvider } from '../projectContext.js';

const PAGE = {
  id: 'CP-1',
  projectId: 'P-1',
  kind: 'adr' as const,
  slug: 'adr/0001-x',
  title: 'A decision',
  headVersionId: 'CV-ae1e8dfeed45',
  sourcePath: null,
  createdAt: '2026-07-31T00:00:00Z',
  updatedAt: '2026-07-31T00:00:00Z',
};

const head = (over: Partial<{ source: 'sweep' | 'human'; author: string; agent: string | null }>) => ({
  id: 'CV-ae1e8dfeed45',
  pageId: 'CP-1',
  parentVersionId: null,
  source: 'human' as const,
  author: 'U-42',
  agent: null,
  ts: '2026-07-31T00:00:00Z',
  blocks: [{ id: 'b1', state: 'authored' as const, text: 'body', citations: [], origin: null }],
  ...over,
});

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200 });

function stub(h: ReturnType<typeof head>, memberDisplay?: Record<string, string>) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/api/session')) return json({ identity: 'a@b.c', csrfToken: 't', isAdmin: false });
      if (url.includes('/chronicle/CP-1'))
        return json({ page: PAGE, head: h, drifted: [], conflicts: null });
      if (url.endsWith('/api/projects'))
        return json({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] });
      // getProject — where the byline's display names come from (T-319).
      if (url.endsWith('/api/projects/P-1'))
        return json({ project: { projectId: 'P-1', name: 'P' }, memberDisplay });
      return json({});
    }),
  );
}

function mount() {
  localStorage.setItem('bd.projectId', 'P-1');
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/chronicle/CP-1']}>
        <ProjectProvider>
          <Routes>
            <Route path="/chronicle/:pageId" element={<ChroniclePageView />} />
          </Routes>
        </ProjectProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return qc;
}

/** The byline lives in the Panel footnote. */
const byline = () => document.querySelector('.record-note') as HTMLElement;

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe('T-430 — collaborator chips in the byline', () => {
  it('an agent-authored version shows the agent chip, name on the tooltip — and no service email', async () => {
    stub(head({ source: 'sweep', agent: 'claude-sweeper', author: 'sweeper@svc.iam.gserviceaccount.com' }));
    mount();
    await waitFor(() => expect(byline()).toBeTruthy());

    const chip = byline().querySelector('.chr-byline [aria-label]') as HTMLElement;
    // Prefix-based, exactly like everywhere else: `claude-` classifies as an agent with the tool logo.
    expect(chip.getAttribute('aria-label')).toBe('agent claude-sweeper');
    expect(chip.getAttribute('title')).toBe('claude-sweeper');
    // The agent acted alone — its service identity is not a second collaborator.
    expect(byline().querySelectorAll('.chr-byline .avatar')).toHaveLength(1);
    expect(byline().outerHTML).not.toContain('@');
  });

  it('a human-collaborated version shows BOTH chips, the person by display name', async () => {
    stub(head({ source: 'human', agent: 'claude-sweeper', author: 'U-42' }), { 'U-42': 'Priya Sharma' });
    mount();
    await waitFor(() => expect(byline()).toBeTruthy());

    // Name resolution arrives with the (separate) project read — wait for it, then pin both chips.
    await waitFor(() =>
      expect([...byline().querySelectorAll('.chr-byline .avatar')].map((c) => c.getAttribute('aria-label'))).toEqual([
        'agent claude-sweeper',
        'human Priya Sharma',
      ]),
    );
  });

  it('NEVER a raw email or U- account id anywhere in the byline DOM', async () => {
    stub(head({ source: 'human', agent: null, author: 'U-42' }), { 'U-42': 'priya@example.com' });
    mount();
    await waitFor(() => expect(byline()).toBeTruthy());

    // Resolution yielded only an email — the byline shows its name part, never the raw address.
    await waitFor(() =>
      expect([...byline().querySelectorAll('.chr-byline .avatar')].map((c) => c.getAttribute('title'))).toEqual(['priya']),
    );
    expect(byline().outerHTML).not.toContain('U-42');
    expect(byline().outerHTML).not.toContain('@');
  });

  it('the version label is the short form — full id only on the title attribute', async () => {
    stub(head({}), { 'U-42': 'Priya Sharma' });
    mount();
    await waitFor(() => expect(byline()).toBeTruthy());

    expect(byline().textContent).toContain('v ae1e8df');
    expect(byline().textContent).not.toContain('CV-ae1e8dfeed45');
    expect(byline().querySelector('[title="CV-ae1e8dfeed45"]')).toBeTruthy();
    // The relative time survives the redesign.
    expect(byline().textContent).toMatch(/ago/);
  });
});
