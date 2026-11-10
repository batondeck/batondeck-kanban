/**
 * Settings → Your password: a signed-in user replaces the password they were handed (Settings →
 * Users shows the first one to the admin who added them), so whoever handed it out keeps no working
 * credential. The BFF checks the current password and the length (internal/api/password_test.go);
 * these pin what the panel sends and shows.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const json = (body: unknown, status = 200) => ({ ok: status < 400, status, json: async () => body }) as Response;
let posted: unknown[] = [];

async function mount(answer: () => Response) {
  posted = [];
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const p = String(input).split('?')[0]!;
    if (p === '/api/session') return json({ identity: 'U-1', csrfToken: 'c', isAdmin: false, permissions: [], subscriptionsEnabled: false, uiSettings: {} });
    if (p === '/api/me/password' && init?.method === 'POST') {
      posted.push(JSON.parse(String(init.body)));
      return answer();
    }
    return json({ projects: [], boards: [], sessions: [], tokens: [], events: [], tasks: [] });
  }));
  const { QueryClient, QueryClientProvider } = await import('@tanstack/react-query');
  const { AuthProvider } = await import('../auth.js');
  const { App } = await import('../App.js');
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/settings']}>
        <AuthProvider>
          <App />
        </AuthProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

const fill = async (current: string, next: string, repeat: string) => {
  fireEvent.change(await screen.findByLabelText('Current password'), { target: { value: current } });
  fireEvent.change(screen.getByLabelText('New password'), { target: { value: next } });
  fireEvent.change(screen.getByLabelText('Repeat the new password'), { target: { value: repeat } });
  fireEvent.click(screen.getByRole('button', { name: 'Change password' }));
};

describe('Settings: your password', () => {
  it('sends the current and the new password, then says how many other sessions were signed out and clears the fields', async () => {
    await mount(() => json({ otherSessionsEnded: 2 }));
    await fill('handed-out-pw', 'a-password-of-my-own', 'a-password-of-my-own');
    expect(await screen.findByText('Password changed. 2 other sessions were signed out.')).toHaveAttribute('role', 'status');
    expect(posted).toEqual([{ currentPassword: 'handed-out-pw', newPassword: 'a-password-of-my-own' }]);
    expect(screen.getByLabelText('Current password')).toHaveValue('');
    expect(screen.getByLabelText('New password')).toHaveValue('');
  });

  it('sends nothing when the two new passwords differ', async () => {
    await mount(() => json({ otherSessionsEnded: 0 }));
    await fill('handed-out-pw', 'a-password-of-my-own', 'a-password-of-my-owN');
    expect(await screen.findByText('The two new passwords differ.')).toBeInTheDocument();
    expect(posted).toEqual([]);
  });

  it('shows the server\'s refusal, and keeps the fields', async () => {
    await mount(() => json({ code: 'VALIDATION', message: 'The current password is wrong.' }, 400));
    await fill('wrong', 'a-password-of-my-own', 'a-password-of-my-own');
    expect(await screen.findByText('The current password is wrong.')).toBeInTheDocument();
    expect(screen.getByLabelText('New password')).toHaveValue('a-password-of-my-own');
    expect(screen.queryByText(/Password changed/)).toBeNull();
  });
});
