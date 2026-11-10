/**
 * The optimistic-mutation layer (Phase 1 — Speed). Every task action patches the react-query caches
 * immediately (the UI responds in one frame), snapshots for rollback, and reconciles with the server on
 * settle. Errors roll back here; the user-facing toast comes from the global MutationCache handler.
 */
import { useMutation, useQueryClient, type QueryClient } from '@tanstack/react-query';
import * as boardsApi from './api/boards.js';
import type { Task } from './types.js';

type TaskPage = { tasks: Task[]; nextCursor?: string };

/**
 * The cache key for a board's task list. It CARRIES the page size, and every observer builds it here.
 *
 * Board and TaskDrawer both watch this list, with page sizes that diverge the moment someone clicks
 * "Load more". React Query stores ONE queryFn per cache entry (the last observer to set options wins),
 * so while the key omitted the size the two observers shared an entry that had no notion of how many
 * rows it held: the next refetch — the 30s poll or an SSE nudge — could re-fetch the board at the
 * drawer's fixed 200 and silently drop the rows the user had just loaded. Prefix invalidation
 * (`['tasks', projectId]`, `['tasks', projectId, boardId]`) still matches, so nothing else moves.
 */
export const boardTasksKey = (projectId: string, boardId: string | undefined, limit: number): readonly unknown[] =>
  ['tasks', projectId, boardId, limit];
export type Snapshot = Array<[readonly unknown[], unknown]>;

/**
 * Merge a pushed board snapshot over the cached page, PRESERVING tasks the user has just edited.
 *
 * WHY THIS IS NEEDED NOW AND WAS NOT BEFORE. The optimistic patch and the SSE board snapshot write the
 * same cache entry — they did not previously, because the optimistic path was writing a key nobody read.
 * So a snapshot landing mid-flight (another agent's write, or a refresh that began before this move
 * committed) would overwrite the card back to its pre-move column while the "saving" veil is still up,
 * then jump forward again on the settle refetch. Not data loss, but a card that teleports twice under a
 * veil advertising "uncertain" tells the user the wrong thing.
 *
 * MERGE RATHER THAN SKIP: every other task in the snapshot still lands, so a colleague's change to a
 * different card is not delayed by your in-flight drag.
 *
 * `isLocal` is injected (production passes `isLocalEdit`) so this is testable without faking timers.
 */
export function mergeSnapshotPreservingLocalEdits(
  old: TaskPage | undefined,
  incoming: TaskPage,
  isLocal: (taskId: string) => boolean,
): TaskPage {
  if (!old?.tasks?.length || !incoming.tasks?.length) return incoming;
  const mine = new Map(old.tasks.filter((t) => isLocal(t.id)).map((t) => [t.id, t]));
  if (mine.size === 0) return incoming;
  return { ...incoming, tasks: incoming.tasks.map((t) => mine.get(t.id) ?? t) };
}

/**
 * Write a board frame's task page over a cached list that may hold MORE than that page.
 *
 * A frame carries the board's first page only (200 tasks, internal/api/stream.go boardTasksLimit),
 * with its cursor when the board holds more. Written over a list that "Load more" grew, it would drop
 * every row past its page and bring the button back. So the frame stands for the first page, and the
 * cached rows past it are kept, less any the frame now carries. A frame with no cursor holds the
 * whole board and replaces the list outright. The kept rows are as fresh as the last read of them:
 * the board refetches the list when it keeps any (Board.tsx).
 */
export function mergeFirstPage(old: TaskPage | undefined, frame: TaskPage): TaskPage {
  if (!frame.nextCursor || !old?.tasks || old.tasks.length <= frame.tasks.length) return frame;
  const inFrame = new Set(frame.tasks.map((t) => t.id));
  const rest = old.tasks.slice(frame.tasks.length).filter((t) => !inFrame.has(t.id));
  if (rest.length === 0) return frame;
  return { tasks: [...frame.tasks, ...rest], ...(old.nextCursor ? { nextCursor: old.nextCursor } : {}) };
}

/**
 * Apply an optimistic transform to a task everywhere it appears (board lists + the task detail).
 *
 * PREFIX-BASED, AND THAT IS THE WHOLE POINT. It iterates
 * `getQueriesData({queryKey: ['tasks', projectId]})` — every board list under the project, whatever its
 * `limit` — instead of computing one exact key.
 *
 * Three bugs came from hand-built exact keys, and all three are answered by not building one:
 *   1. `moveCard` wrote `['tasks', projectId, boardId]`, one segment short of what the board renders
 *      from, so the optimistic patch went to an entry nobody read and the rollback snapshot came back
 *      undefined. `invalidateQueries` matches by PREFIX and did hit the real entry, so the card moved on
 *      the settle refetch — a broken optimistic update looked merely slow.
 *   2. `assignMut` had the identical defect twenty lines below, and worse: `Analytics.tsx` DECLARES a
 *      query at exactly that key, so on a single-board project it patched another screen's cache.
 *   3. A bespoke key-BUILDING helper fixed both and still failed when `limit` changed mid-flight —
 *      "Load more" then drag, and the new entry does not exist yet, so the updater returns `old`,
 *      nothing is written and nothing is snapshotted.
 *
 * Prefix iteration cannot miss the rendered entry because it never has to guess which one that is.
 */
export function patchTaskCaches(qc: QueryClient, projectId: string, taskId: string, patch: (t: Task) => Task): Snapshot {
  const snapshot: Snapshot = [];
  // every board list under this project: ['tasks', projectId, *]
  for (const [key, data] of qc.getQueriesData<TaskPage>({ queryKey: ['tasks', projectId] })) {
    if (!data) continue;
    snapshot.push([key, data]);
    qc.setQueryData<TaskPage>(key, { ...data, tasks: data.tasks.map((t) => (t.id === taskId ? patch(t) : t)) });
  }
  // the open task detail: ['task', projectId, taskId]
  const detailKey = ['task', projectId, taskId] as const;
  const detail = qc.getQueryData<{ task: Task }>(detailKey);
  if (detail) {
    snapshot.push([detailKey, detail]);
    qc.setQueryData(detailKey, { task: patch(detail.task) });
  }
  return snapshot;
}

export function restore(qc: QueryClient, snapshot: Snapshot | undefined): void {
  for (const [key, data] of snapshot ?? []) qc.setQueryData(key, data);
}

function reconcile(qc: QueryClient, projectId: string, taskId?: string): void {
  void qc.invalidateQueries({ queryKey: ['tasks', projectId] });
  if (taskId) {
    // 'hist' is deliberately absent: its key carries the boardId third (['hist', p, boardId, taskId]),
    // so [k, projectId, taskId] could never match it. The ['hist', projectId] invalidate below is the
    // one that works.
    for (const k of ['task', 'ctx', 'subs', 'mem', 'att', 'followups', 'runs']) {
      void qc.invalidateQueries({ queryKey: [k, projectId, taskId] });
    }
  }
  void qc.invalidateQueries({ queryKey: ['hist', projectId] });
  void qc.invalidateQueries({ queryKey: ['board', projectId] });
}

/**
 * An optimistic task mutation: `patch` runs against the cached task instantly; `run` does the server
 * call; settle reconciles. Usage:
 *   const move = useTaskAction(projectId, (to: Status, t) => ({ ...t, status: to }),
 *                              (to, task) => tasksApi.moveTask(projectId, task.id, task.version, { toStatus: to }));
 *   move.mutate({ vars: 'DONE', task });
 */
export function useTaskAction<TVars>(
  projectId: string,
  patch: (vars: TVars, task: Task) => Task,
  run: (vars: TVars, task: Task) => Promise<unknown>,
) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ vars, task }: { vars: TVars; task: Task }) => run(vars, task),
    onMutate: async ({ vars, task }) => {
      // markLocalEdit is applied in req() (api/http.ts) — the choke point every task write goes through.
      await qc.cancelQueries({ queryKey: ['tasks', projectId] });
      await qc.cancelQueries({ queryKey: ['task', projectId, task.id] });
      return { snapshot: patchTaskCaches(qc, projectId, task.id, (t) => patch(vars, t)), taskId: task.id };
    },
    onError: (_e, _v, ctx) => restore(qc, ctx?.snapshot),
    onSettled: (_d, _e, _v, ctx) => reconcile(qc, projectId, ctx?.taskId),
  });
}

/** Optimistic create: a temp-ID card appears instantly in the target board list and is replaced by the
 * server's real task on settle (the invalidate swaps it). */
export function useTaskCreate(projectId: string, boardId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { title: string; priority?: string }) => boardsApi.createTask(projectId, boardId, body),
    onMutate: async (body) => {
      await qc.cancelQueries({ queryKey: ['tasks', projectId, boardId] });
      // Must match the Task interface exactly (no phantom/missing fields) — this short-lived placeholder is
      // replaced by the server's real task on settle. The `: Task` annotation (no cast) enforces the shape.
      const temp: Task = {
        id: `T-…`,
        projectId,
        boardId,
        columnId: '',
        status: 'TODO',
        title: body.title,
        description: '',
        priority: (body.priority ?? 'normal') as Task['priority'],
        assignee: null,
        labels: [],
        requiredCapabilities: [],
        estimate: null,
        dueAt: null,
        customFields: {},
        parentTaskId: null,
        order: 'a0',
        blockedBy: [],
        blocks: [],
        contextSummary: '',
        version: 0,
        claim: null,
        createdBy: 'me',
        createdAt: new Date().toISOString(),
        updatedAt: new Date().toISOString(),
      };
      // PREFIX match, not one exact key: the board list's key carries its page size
      // (['tasks', projectId, boardId, limit]), so an exact-key write would land on an entry no observer
      // reads and the optimistic card would never appear. Same shape as `patchTaskCaches` above — and it
      // stays correct whatever suffix the board's key grows next.
      const snapshot: Snapshot = [];
      for (const [key, data] of qc.getQueriesData<TaskPage>({ queryKey: ['tasks', projectId, boardId] })) {
        if (!data) continue;
        snapshot.push([key, data]);
        qc.setQueryData<TaskPage>(key, { ...data, tasks: [temp, ...data.tasks] });
      }
      return { snapshot };
    },
    onError: (_e, _v, ctx) => restore(qc, ctx?.snapshot),
    onSettled: () => reconcile(qc, projectId),
  });
}
