/**
 * TICKETACTIONS OFFERS ONLY WHAT THE CALLER'S ROLE CAN DO (P-209).
 *
 * `move_task` is tier master and `claim_task` tier write (`internal/domain/grants.go`); `authorize`
 * answers FORBIDDEN below either (`internal/service/authz.go`). The status select was enabled for a
 * worker, so a pick sent `/move`, the server refused it and the optimistic move rolled back with an
 * error toast; a viewer was offered "Claim to work". The server still decides: these assert only
 * that the closed door is shown closed, with its reason, and that no refused request is sent.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, render, screen, cleanup, waitFor, fireEvent, within } from '@testing-library/react';
import { TaskDrawer } from '../screens/TaskDrawer.js';
import { AuthProvider } from '../auth.js';
import { routeFetch, task } from './taskFixtures.js';
import type { Task } from '../types.js';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

type Calls = { mock: { calls: Array<[string, RequestInit?]> } };

const inProgress: Task = { ...task, status: 'IN_PROGRESS', columnId: 'c2', blockedBy: [] };
const todo: Task = { ...task, status: 'TODO', blockedBy: [] };

/** The drawer as alice, whose role in p1 is `role` (`null`: no member row, so no role is known). */
async function drawerAs(role: string | null, t: Task): Promise<Calls> {
  const members = role === null ? {} : { 'alice@example.com': role };
  const fetchMock = routeFetch({
    '/api/session$': { json: { identity: 'alice@example.com', csrfToken: null } },
    '/tasks/T-1$': { json: { task: t } },
    '/projects/p1$': { json: { project: { id: 'p1', name: 'P1', members, settings: { reviewEnabled: true, quotaEnabled: false, txnCap: null } } } },
    '/tasks/T-1/move$': { json: { task: { ...t, status: 'TODO' } } },
  }) as unknown as Calls;
  vi.stubGlobal('fetch', fetchMock);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <AuthProvider>
        <TaskDrawer projectId="p1" taskId="T-1" onClose={vi.fn()} />
      </AuthProvider>
    </QueryClientProvider>,
  );
  await screen.findByRole('dialog', { name: 'Task T-1' });
  // The role arrives with the session and the project, after the task: wait for both reads and the
  // board (which sets the move targets), so a disabled control is the role's answer, not the
  // not-yet-loaded default.
  const urls = () => fetchMock.mock.calls.map(([u]) => String(u));
  await waitFor(() => {
    expect(urls().some((u) => /\/projects\/p1$/.test(u))).toBe(true);
    expect(urls().some((u) => u.endsWith('/api/session'))).toBe(true);
    expect(urls().some((u) => /\/boards\/b1$/.test(u))).toBe(true);
  });
  await act(async () => { await new Promise((r) => setTimeout(r, 50)); });
  return fetchMock;
}

const moveSent = (f: Calls) => f.mock.calls.some(([u]) => String(u).endsWith('/tasks/T-1/move'));

describe('the status select', () => {
  it('a worker sees it disabled with the reason, and a pick sends no /move', async () => {
    const f = await drawerAs('worker', inProgress);
    const sel = screen.getByLabelText('Status');
    expect(sel).toBeDisabled();
    expect(sel).toHaveAttribute('title', 'Only a master can change the status');
    expect(screen.getByText('Only a master can change the status.')).toBeInTheDocument();
    fireEvent.change(sel, { target: { value: 'TODO' } });
    await act(async () => { await new Promise((r) => setTimeout(r, 50)); });
    expect(moveSent(f), 'a worker must not be sent to a master-tier refusal').toBe(false);
    // The claim hint no longer tells a worker to make a move their role cannot make.
    expect(screen.queryByText(/Move this task to/)).toBeNull();
    expect(screen.getByText(/A master can move this task to/)).toHaveTextContent('A master can move this task to To do; then it can be claimed.');
  });

  it('POSITIVE CONTROL: a master sees it enabled, and a pick sends /move', async () => {
    const f = await drawerAs('master', inProgress);
    const sel = screen.getByLabelText('Status');
    expect(sel).toBeEnabled();
    expect(screen.queryByText('Only a master can change the status.')).toBeNull();
    await within(sel).findByRole('option', { name: 'TODO' });
    fireEvent.change(sel, { target: { value: 'TODO' } });
    await waitFor(() => expect(moveSent(f)).toBe(true));
  });

  it('with no known role it stays closed but claims no reason yet (fail closed)', async () => {
    await drawerAs(null, inProgress);
    const sel = screen.getByLabelText('Status');
    expect(sel).toBeDisabled();
    expect(sel).toHaveAttribute('title', 'Checking your role');
    expect(screen.queryByText('Only a master can change the status.')).toBeNull();
  });
});

describe('Claim to work', () => {
  it('a viewer is not offered it, and is told why', async () => {
    await drawerAs('viewer', todo);
    expect(screen.queryByRole('button', { name: /Claim to work/ })).toBeNull();
    expect(screen.getByText('A viewer cannot claim tasks.')).toBeInTheDocument();
  });

  it('POSITIVE CONTROL: a worker is offered it', async () => {
    await drawerAs('worker', todo);
    expect(screen.getByRole('button', { name: /Claim to work/ })).toBeInTheDocument();
    expect(screen.queryByText('A viewer cannot claim tasks.')).toBeNull();
  });
});
