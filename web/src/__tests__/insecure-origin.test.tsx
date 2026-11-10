/**
 * The UI works on a plain-HTTP origin that is not localhost (a LAN address such as
 * http://devbox:8080), where the browser offers neither `crypto.randomUUID` nor
 * `navigator.clipboard`: both exist only in a secure context.
 *
 * "New record" threw `crypto.randomUUID is not a function` into the error boundary, On-call's
 * Generate did nothing, the token panel's and the connect dialog's Copy did nothing and said
 * nothing, and clicking a project or board id threw.
 */
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ChronicleNew } from '../screens/Chronicle.js';
import { OncallSetup } from '../screens/oncall/OncallSetup.js';
import { Settings } from '../screens/Settings.js';
import { Projects } from '../screens/Projects.js';
import { ConnectMcp } from '../connect-mcp.js';
import { ToastHost } from '../toast.js';
import { AuthProvider } from '../auth.js';
import { ProjectProvider } from '../projectContext.js';

vi.mock('../displayNames.js', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../displayNames.js')>()),
  useMyProjectRole: () => 'admin',
}));

const realUUID = crypto.randomUUID;
beforeEach(() => {
  // An insecure context: no randomUUID, no clipboard (jsdom has none to begin with).
  Object.defineProperty(crypto, 'randomUUID', { value: undefined, configurable: true, writable: true });
  Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true });
});
afterEach(() => {
  Object.defineProperty(crypto, 'randomUUID', { value: realUUID, configurable: true, writable: true });
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'content-type': 'application/json' } });
const client = () => new QueryClient({ defaultOptions: { queries: { retry: false } } });

describe('random ids without crypto.randomUUID', () => {
  it('/chronicle/new renders its editor', async () => {
    localStorage.setItem('bd.projectId', 'P-1');
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) =>
      String(input).endsWith('/api/projects') ? json({ projects: [{ projectId: 'P-1', name: 'P', role: 'admin' }] }) : json({ pages: [] })));
    render(
      <QueryClientProvider client={client()}>
        <MemoryRouter initialEntries={['/chronicle/new']}>
          <ProjectProvider><Routes><Route path="/chronicle/new" element={<ChronicleNew />} /></Routes></ProjectProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(await screen.findByRole('button', { name: /create record/i })).toBeTruthy();
  });

  it('On-call Generate fills in a 256-bit key', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) =>
      String(input).includes('/api/oncall/sources')
        ? json({ sources: [{ id: 'grafana', label: 'Grafana', authModes: ['api_key'], docsPage: 'x', authNote: '' }] })
        : json({ integrations: [], deliveries: [], boards: [] })));
    render(
      <QueryClientProvider client={client()}>
        <OncallSetup projectId="P-1" integrations={[]} onClose={() => {}} />
      </QueryClientProvider>,
    );
    fireEvent.click(await screen.findByRole('button', { name: /Grafana/i }));
    await screen.findByLabelText(/^Name$/i);
    fireEvent.click(screen.getByRole('button', { name: /^Generate$/ }));
    await waitFor(() => {
      const keys = [...document.querySelectorAll('input')].map((i) => i.value).filter((v) => /^[0-9a-f]{64}$/.test(v));
      expect(keys, 'Generate produced no key').toHaveLength(1);
    });
  });
});

describe('Copy without navigator.clipboard says what to do instead', () => {
  it('the minted CLI token stays on screen with a "copy it manually" message', async () => {
    const MINTED = ['bd', '0123456789abcdef', 'secret'].join('_');
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/api/session')) return json({ identity: 'U-1', csrfToken: 'c', isAdmin: false, permissions: [], uiSettings: {} });
      if (url.endsWith('/api/me/cli-tokens') && init?.method === 'POST') return json({ id: '0123456789abcdef', label: 'l', status: 'live', token: MINTED });
      return json({ projects: [], tokens: [], sessions: [] });
    }));
    render(
      <QueryClientProvider client={client()}>
        <AuthProvider><ProjectProvider><Settings /></ProjectProvider></AuthProvider>
      </QueryClientProvider>,
    );
    fireEvent.change(await screen.findByLabelText('Token label'), { target: { value: 'laptop' } });
    fireEvent.click(screen.getByRole('button', { name: 'Mint token' }));
    expect(await screen.findByDisplayValue(MINTED)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
    expect(await screen.findByText(/copy it manually/i)).toBeTruthy();
    expect(screen.getByDisplayValue(MINTED), 'the value must stay on screen to be copied by hand').toBeTruthy();
  });

  it('the connect dialog’s Copy says it could not copy', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ oauth: false })));
    render(
      <QueryClientProvider client={client()}>
        <MemoryRouter><ConnectMcp onClose={() => {}} /></MemoryRouter>
      </QueryClientProvider>,
    );
    fireEvent.click(screen.getAllByRole('button', { name: 'Copy' })[0]!);
    expect(await screen.findByRole('button', { name: /select and copy/i })).toBeTruthy();
  });

  it('clicking a project id toasts instead of throwing', async () => {
    const errors: unknown[] = [];
    const onError = (e: ErrorEvent) => { errors.push(e.error); e.preventDefault(); };
    window.addEventListener('error', onError);
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) =>
      String(input).split('?')[0] === '/api/projects'
        ? json({ projects: [{ projectId: 'P-123', name: 'Apollo', role: 'admin', members: {} }] })
        : json({ sessions: [] })));
    render(
      <QueryClientProvider client={client()}>
        <MemoryRouter><ProjectProvider><Projects /><ToastHost /></ProjectProvider></MemoryRouter>
      </QueryClientProvider>,
    );
    fireEvent.click(await screen.findByText('P-123'));
    expect(await screen.findByText(/Could not copy/i)).toBeTruthy();
    window.removeEventListener('error', onError);
    expect(errors).toEqual([]);
  });
});
