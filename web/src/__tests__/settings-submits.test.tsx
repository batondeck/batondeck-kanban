/**
 * Settings writes go out once, and only with a value the server reads the way the reader meant.
 *
 *  - Enter in the token label minted a token per press while the first was in flight (the button
 *    checked `isPending`, the Enter handler did not), each a live credential whose secret was never
 *    shown; a double click on "+ Add Column" added the column twice.
 *  - A WIP limit that is not a whole number ("five", "1O") was sent as `Number(...)`, NaN, which
 *    JSON writes as null, and null is "no limit": the column's limit was silently cleared.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Settings } from '../screens/Settings.js';
import { AuthProvider } from '../auth.js';
import { ProjectProvider } from '../projectContext.js';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
});

const json = (b: unknown) => ({ ok: true, status: 200, json: async () => b }) as Response;
const board = {
  id: 'b1', projectId: 'p1', name: 'Main board',
  columns: [{ id: 'c2', name: 'Review', order: 2, status: 'REVIEW', wipLimit: 3 }],
};

let writes: Array<{ method: string; url: string; body: unknown }> = [];
function mount(opts: { hangWrites?: boolean } = {}): void {
  writes = [];
  localStorage.setItem('bd.projectId', 'p1');
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    if (method !== 'GET') {
      writes.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined });
      if (opts.hangWrites) return new Promise<Response>(() => {});
      return json({ board, column: {}, token: 'bd_x_y', id: 'x', label: 'l', status: 'live' });
    }
    if (url.endsWith('/api/session')) return json({ identity: 'U-1', csrfToken: 'c', isAdmin: true, permissions: ['batondeck:superadmin'], subscriptionsEnabled: false, uiSettings: {} });
    if (/\/api\/projects$/.test(url)) return json({ projects: [{ projectId: 'p1', name: 'Roadmap', role: 'admin' }] });
    if (/\/api\/projects\/p1$/.test(url)) return json({ project: { id: 'p1', name: 'Roadmap', members: { 'U-1': 'admin' }, settings: { reviewEnabled: true } }, memberDisplay: {} });
    if (/\/api\/projects\/p1\/boards$/.test(url)) return json({ boards: [board] });
    return json({ tokens: [], sessions: [], users: [] });
  }));
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <AuthProvider>
        <ProjectProvider>
          <Settings />
        </ProjectProvider>
      </AuthProvider>
    </QueryClientProvider>,
  );
}

/** A second press a human makes: a separate event, after the first one's render has settled. */
const later = () => act(() => new Promise<void>((r) => setTimeout(r, 20)));

describe('one press, one write', () => {
  it('Enter twice in the token label mints ONE token', async () => {
    mount({ hangWrites: true });
    const label = await screen.findByLabelText('Token label');
    fireEvent.change(label, { target: { value: 'laptop' } });
    fireEvent.keyDown(label, { key: 'Enter' });
    await later();
    fireEvent.keyDown(label, { key: 'Enter' });
    await later();
    expect(writes.filter((w) => w.url.endsWith('/api/me/cli-tokens'))).toHaveLength(1);
  });

  it('a double click on + Add Column adds ONE column', async () => {
    mount({ hangWrites: true });
    fireEvent.change(await screen.findByLabelText('New column name'), { target: { value: 'QA' } });
    const add = screen.getByRole('button', { name: '+ Add Column' });
    await waitFor(() => expect(add).not.toBeDisabled());
    fireEvent.click(add);
    await later();
    fireEvent.click(add);
    await later();
    expect(writes.filter((w) => w.url.endsWith('/boards/b1/columns'))).toHaveLength(1);
  });
});

describe('a WIP limit is a whole number of 1 or more, or blank', () => {
  const saveWip = async (value: string) => {
    const input = await screen.findByLabelText('WIP limit of column Review');
    await waitFor(() => expect(input).not.toBeDisabled());
    fireEvent.change(input, { target: { value } });
    fireEvent.click(screen.getByRole('button', { name: 'Save column Review' }));
    await later();
  };
  const patches = () => writes.filter((w) => w.method === 'PATCH');

  it.each(['five', '1O', '0', '2.5', '-1'])('%j is refused with a message, and the limit is not cleared', async (v) => {
    mount();
    await saveWip(v);
    expect(patches(), `"${v}" was sent`).toEqual([]);
    expect(screen.getByText(/WIP limit .*whole number/i)).toBeTruthy();
  });

  it('POSITIVE CONTROL: "5" is sent as 5, and blank as no limit', async () => {
    mount();
    await saveWip(' 5 ');
    await waitFor(() => expect(patches().at(-1)?.body).toMatchObject({ wipLimit: 5 }));
    await saveWip('');
    await waitFor(() => expect(patches().at(-1)?.body).toMatchObject({ wipLimit: null }));
  });

  it('a new column with a bad WIP limit is refused too', async () => {
    mount();
    fireEvent.change(await screen.findByLabelText('New column name'), { target: { value: 'QA' } });
    fireEvent.change(screen.getByLabelText('New column WIP limit'), { target: { value: 'ten' } });
    const add = screen.getByRole('button', { name: '+ Add Column' });
    await waitFor(() => expect(add).not.toBeDisabled());
    fireEvent.click(add);
    await later();
    expect(writes.filter((w) => w.url.endsWith('/boards/b1/columns'))).toEqual([]);
    expect(screen.getByText(/WIP limit .*whole number/i)).toBeTruthy();
  });
});
