/**
 * The optimistic patch reaches the entry the BOARD renders from — asserted as a property against a real
 * QueryClient, not as a regex over source text.
 *
 * WHY THIS FILE REPLACED TWO REGEX GUARDS. They matched source text for
 * `setQueryData<TaskPage>(['tasks', projectId, boardId]` and for the string `ctx.key`. Review restored
 * the exact bug spelled differently — a cast instead of the generic, `ctx.key` mentioned in a condition
 * while a different key was restored — and both stayed green. They tested a SPELLING. That was the fifth
 * vacuous guard in this lane, so the shape is the lesson: assert what the code DOES.
 *
 * WHY IT NOW TARGETS `patchTaskCaches`. Three bugs came from hand-built exact keys: `moveCard` and
 * `assignMut` each wrote a 3-segment key the board never reads, and the bespoke key-BUILDER written to
 * fix them still no-opped when `limit` changed mid-flight. Prefix iteration removes the whole class,
 * so the production path is now the prefix helper and this tests that.
 */
import { describe, it, expect } from 'vitest';
import { QueryClient } from '@tanstack/react-query';
import { boardTasksKey, mergeSnapshotPreservingLocalEdits, patchTaskCaches, restore } from '../mutations.js';
import type { Task } from '../types.js';

const P = 'P-1';
const B = 'B-1';

const task = (id: string, over: Partial<Task> = {}): Task =>
  ({ id, title: id, status: 'TODO', columnId: 'C-ready', order: 'a0', version: 1, assignee: null, ...over }) as Task;

type Page = { tasks: Task[] };
const pageAt = (qc: QueryClient, key: readonly unknown[]): Task[] => qc.getQueryData<Page>(key)!.tasks;

function seeded() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  qc.setQueryData(boardTasksKey(P, B, 200), { tasks: [task('T-1'), task('T-2')] });
  return qc;
}

describe('patchTaskCaches — the optimistic write reaches what the board renders', () => {
  it('THE BOARD ENTRY CHANGES: the card moves on drop, not on the settle refetch', () => {
    const qc = seeded();
    patchTaskCaches(qc, P, 'T-1', (t) => ({ ...t, columnId: 'C-review', status: 'REVIEW' }));
    const moved = pageAt(qc, boardTasksKey(P, B, 200)).find((t) => t.id === 'T-1')!;
    expect(moved.columnId, 'the optimistic patch must land where the board reads').toBe('C-review');
    expect(moved.status).toBe('REVIEW');
  });

  /**
   * THE "LOAD MORE" CASE, raised in review against the bespoke key-builder that preceded this.
   * `tasksQ` uses `keepPreviousData`, so right after Load more the board still renders the 200-entry
   * while the 400-entry does not exist yet. A builder targeting only `limit=400` wrote nothing and
   * snapshotted nothing — the fixed path failing again, with one actor and no concurrency.
   */
  it('LOAD MORE MID-FLIGHT: every limit variant is patched, so the rendered one cannot be missed', () => {
    const qc = seeded();
    qc.setQueryData(boardTasksKey(P, B, 400), { tasks: [task('T-1'), task('T-2')] });
    patchTaskCaches(qc, P, 'T-1', (t) => ({ ...t, columnId: 'C-review' }));
    expect(pageAt(qc, boardTasksKey(P, B, 200)).find((t) => t.id === 'T-1')!.columnId).toBe('C-review');
    expect(pageAt(qc, boardTasksKey(P, B, 400)).find((t) => t.id === 'T-1')!.columnId).toBe('C-review');
  });

  it('a flat page keyed 3-segment is patched CONSISTENTLY, not instead of the board', () => {
    const qc = seeded();
    qc.setQueryData(['tasks', P, B], { tasks: [task('T-1'), task('T-2')] });
    patchTaskCaches(qc, P, 'T-1', (t) => ({ ...t, columnId: 'C-review' }));
    expect(pageAt(qc, boardTasksKey(P, B, 200)).find((t) => t.id === 'T-1')!.columnId, 'the board must move').toBe('C-review');
    expect(pageAt(qc, ['tasks', P, B]).find((t) => t.id === 'T-1')!.columnId, 'and stay consistent with it').toBe('C-review');
  });

  /**
   * An INFINITE query under the prefix — the shape the Analytics census had when it was keyed
   * `['tasks', P, B]`. It outlived the screen by gcTime, and the flat-page map threw on it, so every
   * optimistic write in the project failed before its request was sent. A screen that keys a
   * non-page entry under the prefix again must cost it nothing more than not being patched.
   */
  it('a NON-PAGE entry under the prefix (an infinite query) is skipped, not thrown on', () => {
    const qc = seeded();
    const infinite = { pages: [{ tasks: [task('T-1')] }], pageParams: [''] };
    qc.setQueryData(['tasks', P, B], infinite);
    expect(() => patchTaskCaches(qc, P, 'T-1', (t) => ({ ...t, columnId: 'C-review' }))).not.toThrow();
    expect(pageAt(qc, boardTasksKey(P, B, 200)).find((t) => t.id === 'T-1')!.columnId, 'the board must still move').toBe('C-review');
    expect(qc.getQueryData(['tasks', P, B]), 'the foreign entry is left as it was').toEqual(infinite);
  });

  it('the SNAPSHOT round-trips: restore() undoes the patch across every entry', () => {
    const qc = seeded();
    qc.setQueryData(boardTasksKey(P, B, 400), { tasks: [task('T-1')] });
    const before200 = JSON.stringify(pageAt(qc, boardTasksKey(P, B, 200)));
    const before400 = JSON.stringify(pageAt(qc, boardTasksKey(P, B, 400)));
    const snapshot = patchTaskCaches(qc, P, 'T-1', (t) => ({ ...t, columnId: 'C-review' }));
    expect(snapshot.length, 'an empty snapshot means onError restores nothing').toBeGreaterThan(0);
    restore(qc, snapshot);
    expect(JSON.stringify(pageAt(qc, boardTasksKey(P, B, 200))), 'a rejected move must put the card back').toBe(before200);
    expect(JSON.stringify(pageAt(qc, boardTasksKey(P, B, 400)))).toBe(before400);
  });

  it('the open task detail is patched too, so the drawer agrees with the board', () => {
    const qc = seeded();
    qc.setQueryData(['task', P, 'T-1'], { task: task('T-1') });
    patchTaskCaches(qc, P, 'T-1', (t) => ({ ...t, columnId: 'C-review' }));
    expect(qc.getQueryData<{ task: Task }>(['task', P, 'T-1'])!.task.columnId).toBe('C-review');
  });

  it('only the named task is touched', () => {
    const qc = seeded();
    patchTaskCaches(qc, P, 'T-1', (t) => ({ ...t, columnId: 'C-review' }));
    expect(pageAt(qc, boardTasksKey(P, B, 200)).find((t) => t.id === 'T-2')!.columnId).toBe('C-ready');
  });

  it('another PROJECT is never touched', () => {
    const qc = seeded();
    qc.setQueryData(boardTasksKey('P-other', B, 200), { tasks: [task('T-1')] });
    patchTaskCaches(qc, P, 'T-1', (t) => ({ ...t, columnId: 'C-review' }));
    expect(pageAt(qc, boardTasksKey('P-other', B, 200))[0]!.columnId, 'cross-project bleed').toBe('C-ready');
  });
});

describe('mergeSnapshotPreservingLocalEdits — a snapshot must not undo your own in-flight move', () => {
  const page = (t: Task[]) => ({ tasks: t });
  const none = () => false;

  it('THE RACE: a snapshot carrying the PRE-MOVE task does not drag the card back', () => {
    const optimistic = page([task('T-1', { columnId: 'C-review', status: 'REVIEW' }), task('T-2')]);
    const stale = page([task('T-1', { columnId: 'C-ready', status: 'TODO' }), task('T-2')]);
    const out = mergeSnapshotPreservingLocalEdits(optimistic, stale, (id) => id === 'T-1');
    expect(out.tasks.find((t) => t.id === 'T-1')!.columnId, 'your in-flight move must survive').toBe('C-review');
  });

  it('MERGE, NOT SKIP: everyone else’s changes still land while yours is in flight', () => {
    const optimistic = page([task('T-1', { columnId: 'C-review' }), task('T-2', { title: 'old' })]);
    const incoming = page([task('T-1', { columnId: 'C-ready' }), task('T-2', { title: 'renamed by a colleague' })]);
    const out = mergeSnapshotPreservingLocalEdits(optimistic, incoming, (id) => id === 'T-1');
    expect(out.tasks.find((t) => t.id === 'T-1')!.columnId).toBe('C-review');
    expect(out.tasks.find((t) => t.id === 'T-2')!.title, 'an unrelated card must not be held back').toBe('renamed by a colleague');
  });

  it('a NEWER server copy wins even inside the window: it already holds your write', () => {
    // An optimistic patch keeps the version it was made from, and the server bumps it when the write
    // lands. A snapshot at a later version therefore carries this tab's own edit, and keeping the
    // older local copy would send that older version with the next write (a drag after a drawer edit
    // answered STALE "expected 1, found 2", and the card snapped back).
    const local = page([task('T-1', { title: 'mine', version: 1 }), task('T-2')]);
    const newer = page([task('T-1', { title: 'mine, saved', version: 2 }), task('T-2')]);
    const out = mergeSnapshotPreservingLocalEdits(local, newer, (id) => id === 'T-1');
    expect(out.tasks.find((t) => t.id === 'T-1')!.version, 'the server copy, at its own version').toBe(2);
    // An in-flight move (same version as the snapshot) still keeps the local copy.
    const sameVersion = page([task('T-1', { columnId: 'C-ready', version: 1 })]);
    const moving = page([task('T-1', { columnId: 'C-review', version: 1 })]);
    expect(mergeSnapshotPreservingLocalEdits(moving, sameVersion, (id) => id === 'T-1').tasks[0]!.columnId).toBe('C-review');
  });

  it('with NOTHING in flight the snapshot wins outright — no staleness is introduced', () => {
    const cached = page([task('T-1', { columnId: 'C-ready' })]);
    const incoming = page([task('T-1', { columnId: 'C-done' })]);
    expect(mergeSnapshotPreservingLocalEdits(cached, incoming, none).tasks[0]!.columnId).toBe('C-done');
  });

  it('the window is what expires it: once the edit is no longer local, the server wins', () => {
    const optimistic = page([task('T-1', { columnId: 'C-review' })]);
    const truth = page([task('T-1', { columnId: 'C-ready' })]);
    // isLocalEdit has a 5s TTL; past it this returns false and the server's view must take over,
    // otherwise a rejected move would be preserved locally forever.
    expect(mergeSnapshotPreservingLocalEdits(optimistic, truth, none).tasks[0]!.columnId).toBe('C-ready');
  });

  it('a task absent from the snapshot is not resurrected by the merge', () => {
    const optimistic = page([task('T-1'), task('T-gone', { columnId: 'C-review' })]);
    const incoming = page([task('T-1')]);
    const out = mergeSnapshotPreservingLocalEdits(optimistic, incoming, () => true);
    expect(out.tasks.map((t) => t.id), 'a deleted task must stay deleted').toEqual(['T-1']);
  });

  it('an empty cache or empty snapshot passes the snapshot through unchanged', () => {
    const incoming = page([task('T-1')]);
    expect(mergeSnapshotPreservingLocalEdits(undefined, incoming, () => true)).toBe(incoming);
    expect(mergeSnapshotPreservingLocalEdits(page([]), incoming, () => true)).toBe(incoming);
  });
});
