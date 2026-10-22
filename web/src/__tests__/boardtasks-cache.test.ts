/**
 * The board task-list cache entry: who owns it, and what a refetch is allowed to do to it.
 *
 * Board and TaskDrawer both observe this list. Their page sizes diverge the moment someone clicks
 * "Load more" — and React Query keeps ONE queryFn per cache ENTRY, so while the key omitted the page
 * size the two observers shared an entry whose contents depended on which of them last set the options.
 * The next refetch (the 30s poll, or an SSE `updated` nudge) could then re-fetch the board at the
 * drawer's fixed 200 and silently drop the rows the user had just loaded.
 */
import { describe, it, expect, vi } from 'vitest';
import { QueryClient, QueryObserver } from '@tanstack/react-query';
import { boardTasksKey } from '../mutations.js';
import type { Task } from '../types.js';

const page = (n: number): { tasks: Task[] } => ({
  tasks: Array.from({ length: n }, (_, i) => ({ id: `T-${i}` }) as Task),
});
const settle = (): Promise<void> => new Promise((r) => setTimeout(r, 0));

/** Observe a board list the way a screen does, and return its live result. */
function observe(qc: QueryClient, limit: number): { rows: () => number; stop: () => void } {
  const obs = new QueryObserver<{ tasks: Task[] }>(qc, {
    queryKey: boardTasksKey('P-1', 'B-1', limit),
    queryFn: () => Promise.resolve(page(limit)),
    retry: false,
  });
  const stop = obs.subscribe(() => {});
  return { rows: () => obs.getCurrentResult().data?.tasks.length ?? 0, stop };
}

describe('board task list — Board and TaskDrawer must not fight over one cache entry', () => {
  it('REFUSAL: a drawer observing at the default page size cannot shrink a board that loaded more', async () => {
    const qc = new QueryClient();
    const board = observe(qc, 400); // the user clicked "Load more" once
    await settle();
    const drawer = observe(qc, 200); // ...then opened a card
    await settle();
    expect(board.rows()).toBe(400);
    expect(drawer.rows()).toBe(200);

    // The SSE nudge / poll that used to undo it: Board invalidates by PREFIX, which must still reach
    // both entries, and each must come back at ITS OWN size.
    await qc.invalidateQueries({ queryKey: ['tasks', 'P-1', 'B-1'] });
    await settle();
    expect(board.rows(), 'the board silently lost the rows "Load more" fetched').toBe(400);
    expect(drawer.rows()).toBe(200);
    board.stop();
    drawer.stop();
  });

  it('POSITIVE CONTROL: at the same page size they still SHARE one entry (one fetch, not two)', async () => {
    const qc = new QueryClient();
    const fn = vi.fn(() => Promise.resolve(page(200)));
    const mk = (): QueryObserver<{ tasks: Task[] }> =>
      new QueryObserver(qc, { queryKey: boardTasksKey('P-1', 'B-1', 200), queryFn: fn, retry: false });
    const a = mk();
    const b = mk();
    const stopA = a.subscribe(() => {});
    const stopB = b.subscribe(() => {});
    await settle();
    // Opening the drawer from an un-paged board must stay free — that reuse is why the key is shared
    // at all, and a fix that split it unconditionally would double every board fetch.
    expect(fn).toHaveBeenCalledTimes(1);
    expect(a.getCurrentResult().data?.tasks.length).toBe(200);
    expect(b.getCurrentResult().data?.tasks.length).toBe(200);
    stopA();
    stopB();
  });

  it('POSITIVE CONTROL: "Load more" does not blank the board while the bigger page loads', async () => {
    // Putting `limit` in the key means the click switches to a key with NO cached entry. Without
    // `keepPreviousData` (Board.tsx) `data` is undefined for a whole round-trip: every card disappears
    // and the "Load more" button, gated on `nextCursor`, vanishes mid-click. This is the regression the
    // key change would otherwise have introduced.
    const qc = new QueryClient();
    let release: (v: { tasks: Task[] }) => void = () => {};
    const obs = new QueryObserver<{ tasks: Task[] }>(qc, {
      queryKey: boardTasksKey('P-1', 'B-1', 200),
      queryFn: () => Promise.resolve(page(200)),
      placeholderData: (prev) => prev,
      retry: false,
    });
    const stop = obs.subscribe(() => {});
    await settle();
    expect(obs.getCurrentResult().data?.tasks.length).toBe(200);

    obs.setOptions({
      queryKey: boardTasksKey('P-1', 'B-1', 400),
      queryFn: () => new Promise<{ tasks: Task[] }>((r) => (release = r)),
      placeholderData: (prev) => prev,
      retry: false,
    });
    await settle();
    expect(obs.getCurrentResult().data?.tasks.length, 'the board blanked out mid-"Load more"').toBe(200);
    release(page(400));
    await settle();
    expect(obs.getCurrentResult().data?.tasks.length).toBe(400);
    stop();
  });

  it('POSITIVE CONTROL: the broad project-wide invalidate still reaches a paged board', async () => {
    const qc = new QueryClient();
    const board = observe(qc, 400);
    await settle();
    // `mutations.ts` → reconcile() and TaskDrawer → refresh() both invalidate this prefix. If the key
    // change had put the list out of their reach, every optimistic write would stop reconciling.
    const matched = qc.getQueriesData({ queryKey: ['tasks', 'P-1'] });
    expect(matched.length).toBe(1);
    board.stop();
  });
});
