// The poison badge (plan §Fold 1, ADR-0001): a BLOCKED task carrying the `poisoned` system label shows
// it on its card and in the drawer, with its claimAttempts; a task BLOCKED by a dependency shows none.
// The drawer's requeue (BLOCKED → TODO) is enabled for a master and disabled for a worker.
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, render, screen, cleanup, waitFor, fireEvent } from '@testing-library/react';
import { TaskCard } from '../screens/Board.js';
import { TaskDrawer } from '../screens/TaskDrawer.js';
import { AuthProvider } from '../auth.js';
import { routeFetch, task } from './taskFixtures.js';
import type { Task } from '../types.js';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const poisoned: Task = { ...task, status: 'BLOCKED', labels: ['poisoned'], claimAttempts: 3, blockedBy: [], dueAt: null };
const depBlocked: Task = { ...task, status: 'BLOCKED', labels: [], blockedBy: ['T-9'], dueAt: null };

const card = (t: Task): HTMLElement =>
  render(<TaskCard t={t} col={{ status: 'BLOCKED' }} selected={false} onOpen={vi.fn()} agents={[]} onAssign={vi.fn()} canAssign />).container;

describe('the poison badge on the Blocked lane', () => {
  it('shows on a poisoned task, with its attempts', () => {
    const c = card(poisoned);
    const badge = c.querySelector('.badge.poisoned');
    expect(badge).not.toBeNull();
    expect(badge!.textContent).toBe('Poisoned · 3 attempts');
  });

  it('does not show on a dependency-blocked task', () => {
    const c = card(depBlocked);
    expect(c.querySelector('.badge.poisoned')).toBeNull();
    expect(c.querySelector('.badge.blocked')?.textContent).toBe('Blocked');
  });
});

function drawer(role: string, t: Task): ReturnType<typeof routeFetch> {
  const fetchMock = routeFetch({
    '/api/session$': { json: { identity: 'alice@example.com', csrfToken: null } },
    '/tasks/T-1$': { json: { task: t } },
    '/projects/p1$': { json: { project: { id: 'p1', name: 'P1', members: { 'alice@example.com': role }, settings: { reviewEnabled: true, quotaEnabled: false, txnCap: null } } } },
    '/tasks/T-1/move$': { json: { task: { ...t, status: 'TODO', labels: [] } } },
  });
  vi.stubGlobal('fetch', fetchMock);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <AuthProvider>
        <TaskDrawer projectId="p1" taskId="T-1" onClose={vi.fn()} />
      </AuthProvider>
    </QueryClientProvider>,
  );
  return fetchMock;
}

describe('the poison badge and requeue in the drawer', () => {
  it('a master sees the badge and requeues to To do', async () => {
    const fetchMock = drawer('master', poisoned);
    const dialog = await screen.findByRole('dialog', { name: 'Task T-1' });
    expect(dialog.querySelector('.badge.poisoned')?.textContent).toBe('Poisoned · 3 attempts');
    const requeue = screen.getByRole('button', { name: 'Requeue to To do' });
    await waitFor(() => expect(requeue).toBeEnabled());
    fireEvent.click(requeue);
    await waitFor(() => {
      const call = (fetchMock as unknown as { mock: { calls: Array<[string, RequestInit?]> } }).mock.calls.find(([u]) => String(u).endsWith('/tasks/T-1/move'));
      expect(call, 'no /move request was sent').toBeTruthy();
      expect(JSON.parse(String(call![1]?.body))).toEqual({ version: 3, toStatus: 'TODO' });
    });
  });

  it('a worker sees the requeue disabled', async () => {
    const fetchMock = drawer('worker', poisoned);
    await screen.findByRole('dialog', { name: 'Task T-1' });
    // The role arrives with the session and the project, after the task: wait for both reads to have
    // answered and rendered, so "disabled" is the worker's answer, not the not-yet-loaded default.
    const calls = (fetchMock as unknown as { mock: { calls: Array<[string]> } }).mock.calls;
    await waitFor(() => expect(calls.some(([u]) => /\/projects\/p1$/.test(String(u))) && calls.some(([u]) => String(u).endsWith('/api/session'))).toBe(true));
    await act(async () => { await new Promise((r) => setTimeout(r, 50)); });
    const requeue = screen.getByRole('button', { name: 'Requeue to To do' });
    expect(requeue).toBeDisabled();
    expect(requeue).toHaveAttribute('title', 'Only a master can requeue a poisoned task');
  });

  it('a dependency-blocked task shows neither the badge nor the requeue', async () => {
    drawer('master', depBlocked);
    const dialog = await screen.findByRole('dialog', { name: 'Task T-1' });
    expect(dialog.querySelector('.badge.poisoned')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Requeue to To do' })).toBeNull();
  });
});
