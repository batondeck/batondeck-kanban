/**
 * `useTaskCreate`'s optimistic placeholder, and the silent-failure surface behind `fire()`.
 *
 * The create hook used to write to the EXACT key `['tasks', projectId, boardId]`. The board list's key
 * now carries its page size, so an exact-key write would land on an entry no observer reads — the
 * placeholder card would simply never appear, and only after the server round-trip would the task show
 * up. It matches by PREFIX instead, the same way `patchTaskCaches` already did.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { renderHook, render, screen, act, cleanup, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { boardTasksKey, useTaskCreate } from '../mutations.js';
import { fire, ToastHost } from '../toast.js';
import type { Task } from '../types.js';

vi.mock('../api/boards.js', () => ({ createTask: vi.fn() }));
const boardsApi = await import('../api/boards.js');
const createTask = boardsApi.createTask as unknown as ReturnType<typeof vi.fn>;

afterEach(() => vi.restoreAllMocks());

function harness(): { qc: QueryClient; wrapper: (p: { children: ReactNode }) => ReactNode } {
  const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  // The board list as the board screen keys it TODAY: page size included.
  qc.setQueryData(boardTasksKey('P-1', 'B-1', 400), { tasks: [{ id: 'T-1' } as Task] });
  const wrapper = ({ children }: { children: ReactNode }): ReactNode => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  return { qc, wrapper };
}
const rows = (qc: QueryClient): Task[] =>
  (qc.getQueryData(boardTasksKey('P-1', 'B-1', 400)) as { tasks: Task[] }).tasks;

describe('useTaskCreate optimistic placeholder', () => {
  it('inserts into the board list the board is actually observing (a paged key)', async () => {
    createTask.mockImplementation(() => new Promise(() => {})); // never settles: hold the optimistic state
    const { qc, wrapper } = harness();
    const { result } = renderHook(() => useTaskCreate('P-1', 'B-1'), { wrapper });
    act(() => result.current.mutate({ title: 'brand new' }));
    await waitFor(() => expect(rows(qc)).toHaveLength(2));
    expect(rows(qc)[0]?.title).toBe('brand new');
  });

  // An infinite query under the board's prefix (the Analytics census's old key) must not make the
  // create throw before its request is sent.
  it('skips a non-page entry under the prefix instead of throwing on it', async () => {
    createTask.mockImplementation(() => new Promise(() => {}));
    const { qc, wrapper } = harness();
    qc.setQueryData(['tasks', 'P-1', 'B-1'], { pages: [{ tasks: [{ id: 'T-1' } as Task] }], pageParams: [''] });
    const { result } = renderHook(() => useTaskCreate('P-1', 'B-1'), { wrapper });
    act(() => result.current.mutate({ title: 'still sent' }));
    await waitFor(() => expect(rows(qc)).toHaveLength(2));
    expect(result.current.isError, String(result.current.error)).toBe(false);
    expect(createTask).toHaveBeenCalled();
  });

  it('rolls the placeholder back when the create fails', async () => {
    createTask.mockRejectedValue(new Error('boom'));
    const { qc, wrapper } = harness();
    const { result } = renderHook(() => useTaskCreate('P-1', 'B-1'), { wrapper });
    act(() => result.current.mutate({ title: 'doomed' }));
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(rows(qc)).toHaveLength(1);
    expect(rows(qc)[0]?.id).toBe('T-1');
  });
});

describe('fire() — a failed hand-rolled action must not be a silent no-op', () => {
  it('REFUSAL: a rejection is surfaced as a toast, not swallowed', async () => {
    render(<ToastHost />);
    // `void action()` produced NOTHING here — no toast, no state change, just an `Uncaught (in promise)`
    // nobody reads. LIMIT (rule 11): this asserts the toast, not the absence of an unhandled-rejection
    // event; jsdom does not surface those reliably.
    fire(Promise.reject(new Error('could not open project')));
    await waitFor(() => expect(screen.getByText('could not open project')).toBeTruthy());
    cleanup();
  });

  it('POSITIVE CONTROL: a successful action toasts nothing', async () => {
    const { container } = render(<ToastHost />);
    // A DELTA, not an absolute count: the toast store is module-global with a 5s TTL, so a toast from
    // the test above may still be on screen. What must hold is that a resolved promise adds nothing.
    const before = container.querySelectorAll('.toast').length;
    fire(Promise.resolve('fine'));

    // NO FIXED SLEEP. This is an ABSENCE assertion, and an absence is trivially true before anything
    // has happened — the `setTimeout(0)` version passed with the `fire()` line DELETED ENTIRELY
    // (mutation-proven, T-342), so it tested nothing. There is no positive signal for "a resolved
    // action toasts nothing", because producing nothing is the behaviour under test.
    //
    // So the DISCRIMINATION comes from firing a rejection through the same `fire()` afterwards and
    // watching a toast arrive. That proves the helper is wired, the container is the one being
    // measured, and the delta count works — which is exactly what the resolved case cannot prove
    // about itself. If `fire()` were a no-op, this control fails.
    fire(Promise.reject(new Error('the control toast')));
    await waitFor(() => expect(screen.getByText('the control toast')).toBeTruthy());

    // The rejection added exactly one; the resolution before it added none.
    expect(container.querySelectorAll('.toast')).toHaveLength(before + 1);
    cleanup();
  });
});
