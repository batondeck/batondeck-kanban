/**
 * Everything a ticket detail surface needs, in one hook.
 *
 * WHY: the drawer and the full page show the SAME record. If each ran its own queries and its own
 * derived state they would drift — different move targets, different lease maths, two caches. So the
 * data path is defined once here and both screens render the same panels (`./panels.tsx`) off it.
 */
import { useEffect, useMemo, useReducer, useState } from 'react';
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import * as agentsApi from '../../api/agents.js';
import * as boardsApi from '../../api/boards.js';
import * as contextApi from '../../api/context.js';
import * as feedApi from '../../api/feed.js';
import * as mediaApi from '../../api/media.js';
import * as projectsApi from '../../api/projects.js';
import * as streamApi from '../../api/stream.js';
import * as tasksApi from '../../api/tasks.js';
import { ApiError } from '../../api/http.js';
import { useAuth } from '../../auth.js';
import { boardTasksKey, useTaskAction } from '../../mutations.js';
import { uiSettings } from '../../settings.js';
import { buildAgentList, busyAgentKeys } from '../../agentStatus.js';
import { NOT_A_MEMBER, useDisplayNames, useMyProjectRole } from '../../displayNames.js';
import { closedWhy, mayCall, type GatedTool } from '../../toolFloors.js';
import { isCancelled } from '../../reports.js';
import type { ActivityEvent, AgentSession, Status, Task } from '../../types.js';
import { keepCachedDevices, replaceDevicePresence } from '../../presenceMerge.js';
import { forgetLease, onLeaseStorage, recallLease, rememberLease } from './leases.js';

// The fixed table (ADR-0005), the fallback until the board's own `transitions` (the same table, as
// get_board answers it) has loaded — so the buttons offer exactly the moves the server accepts.
const NEXT: Record<Status, Status[]> = {
  TODO: ['IN_PROGRESS'],
  IN_PROGRESS: ['REVIEW', 'BLOCKED', 'TODO'],
  BLOCKED: ['IN_PROGRESS', 'TODO'],
  REVIEW: ['DONE', 'IN_PROGRESS'],
  DONE: ['IN_PROGRESS'],
};

/**
 * The Edit draft: the title and description being edited, and the version and text they were taken
 * from. Save sends only the fields changed from `base`, and holds when someone else changed the title
 * or description since (see `editConflict`): the task keeps refreshing under an open editor, so the
 * version read at Save time is not the one the draft was based on.
 */
export interface EditDraft { title: string; description: string; version: number; base: { title: string; description: string } }

/** Fire a write, refresh the ticket's caches, surface the error instead of throwing. */
export type Run = <T>(p: Promise<T>, onSuccess?: (r: T) => void) => Promise<void>;

// Live ticking lease countdown ("4m 12s") + fraction remaining for the progress bar. Recomputes once
// a second off a now() tick. The claim on the wire carries its expiry, not its length, so the bar
// measures this browser's own claim from when it answered (`since`: a browser claim holds an hour)
// and any other against the server's default 5-minute lease (purely cosmetic — the window the board
// card's lease bar uses).
function useLeaseCountdown(expiresAt: string | null | undefined, since: number | null): { label: string; frac: number; expired: boolean } {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!expiresAt) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [expiresAt]);
  if (!expiresAt) return { label: '', frac: 0, expired: true };
  const ms = new Date(expiresAt).getTime() - now;
  if (ms <= 0) return { label: 'expired', frac: 0, expired: true };
  const end = new Date(expiresAt).getTime();
  const total = since !== null && end > since ? end - since : 5 * 60 * 1000;
  const s = Math.floor(ms / 1000);
  const label = s >= 60 ? `${Math.floor(s / 60)}m ${s % 60}s` : `${s}s`;
  return { label, frac: Math.max(0, Math.min(1, ms / total)), expired: false };
}

export function useTaskDetail(projectId: string, taskId: string) {
  const qc = useQueryClient();
  const { identity } = useAuth();
  // The lease id this browser's claim was given (see ./leases.ts): kept outside the component, so
  // closing the drawer, j/k or a reload does not strand the claim's holder. Read on every render, so
  // the page following a link to another ticket reads that ticket's entry, not this one's.
  const [, leaseChanged] = useReducer((n: number) => n + 1, 0);
  const leaseEntry = identity ? recallLease(identity, projectId, taskId) : null;
  // Another tab of this browser claimed or released: read the entry again.
  useEffect(() => onLeaseStorage(leaseChanged), []);
  const setLease = (leaseId: string | null): void => {
    if (!identity) return;
    if (leaseId) rememberLease(identity, projectId, taskId, leaseId);
    else forgetLease(identity, projectId, taskId);
    leaseChanged();
  };
  const [err, setErr] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<EditDraft>({ title: '', description: '', version: 0, base: { title: '', description: '' } });

  // The board's stream refreshes the ticket on every change on its board (the subscription below),
  // keeping task.version fresh for version-checked actions (Pick, Cancel race, follow-ups, moves). This
  // 30s poll is just a backstop for a dropped stream.
  const taskQ = useQuery({ queryKey: ['task', projectId, taskId], queryFn: () => tasksApi.getTask(projectId, taskId), refetchInterval: 30000 });
  const projectQ = useQuery({ queryKey: ['project', projectId], queryFn: () => projectsApi.getProject(projectId) });
  // Account id → display name, for the header's "created by" (P-314). The same cache entry the
  // Activity panel reads, which both ticket screens mount, so this adds no request.
  const names = useDisplayNames(projectId);
  // Known agent names (for the Assignee picker) — assigning a task to one of these routes it to that
  // agent's `wait_for_task { assignee }` loop. Best-effort; the field still accepts any string.
  // Same tunable as Board's fetch — these share the ['agentsessions'] cache key, so the queryFns must
  // agree or the entry flaps between differently-sized lists, and keep the device rows the same way.
  const agentsQ = useQuery({
    queryKey: ['agentsessions'],
    queryFn: async () => {
      const fresh = await agentsApi.agentSessions(uiSettings().sessionsLimit);
      return { sessions: keepCachedDevices(fresh.sessions, qc.getQueryData<{ sessions: AgentSession[] }>(['agentsessions'])?.sessions) };
    },
    staleTime: 60_000,
  });
  const task = taskQ.data?.task;
  // The shared socket, scoped to the ticket's board once the ticket has loaded (joining it opens no
  // second one). A board frame names no task, so each refreshes this ticket's own reads: on the full
  // page no Board is mounted to do it, and comments are in no board nudge. In the drawer the Board's
  // nudge refetches most of these first; `cancelRefetch: false` joins that fetch instead of restarting it.
  // The kept device rows are retired only by a presence frame, so this hook writes the frames itself:
  // on the full task page no Board is mounted to do it, and a session that left would otherwise stay
  // listed through every refetch (P-197). In the drawer the Board writes the same rows the same way.
  // A frame is the device's whole live list.
  const streamBoardId = task?.boardId;
  useEffect(() => {
    const onPresence = (sessions: AgentSession[]): void => {
      qc.setQueryData<{ sessions: AgentSession[] }>(['agentsessions'], (old) => ({
        sessions: replaceDevicePresence(old?.sessions, sessions),
      }));
    };
    if (!streamBoardId) return streamApi.userStream({ onPresence });
    const onUpdate = (): void => {
      for (const k of ['task', 'ctx', 'subs', 'mem', 'att', 'comments', 'followups', 'runs']) {
        void qc.invalidateQueries({ queryKey: [k, projectId, taskId] }, { cancelRefetch: false });
      }
      void qc.invalidateQueries({ queryKey: ['hist', projectId, streamBoardId, taskId] }, { cancelRefetch: false });
    };
    return streamApi.stream(projectId, streamBoardId, onUpdate, undefined, onPresence);
  }, [qc, projectId, streamBoardId, taskId]);
  const [ctxQ, subQ, memQ, attQ] = useQueries({
    queries: [
      { queryKey: ['ctx', projectId, taskId], queryFn: () => contextApi.taskContext(projectId, taskId) },
      { queryKey: ['subs', projectId, taskId], queryFn: () => contextApi.listSubtasks(projectId, taskId) },
      { queryKey: ['mem', projectId, taskId], queryFn: () => contextApi.readMemory(projectId, taskId) },
      { queryKey: ['att', projectId, taskId], queryFn: () => mediaApi.listAttachments(projectId, taskId) },
    ],
  });
  // The board's newest events: the agents seen on this board (the Assignee list, @mentions).
  const histQ = useQuery({
    queryKey: ['hist', projectId, task?.boardId, taskId],
    queryFn: () => feedApi.feed(projectId, task!.boardId),
    enabled: !!task,
  });
  // The ticket's own events, for its Activity: the board's newest page holds the board's newest
  // events whatever task they are on, so filtering it by task read an older ticket as "No activity".
  // Under the same ['hist', projectId] prefix every refresh and the board's nudge invalidate.
  const ownHistQ = useQuery({
    queryKey: ['hist', projectId, task?.boardId, taskId, 'own'],
    queryFn: () => feedApi.taskFeed(projectId, task!.boardId, taskId),
    enabled: !!task,
  });
  // Board (for column-aware "Move to" labels) + board tasks (to derive which agents are "busy" = hold a
  // live claim). Both reuse the board screen's cache keys, so opening the drawer from the board is free.
  const boardQ = useQuery({ queryKey: ['board', projectId, task?.boardId], queryFn: () => boardsApi.getBoard(projectId, task!.boardId), enabled: !!task });
  // The page size is IN the key (see `boardTasksKey`). At the default limit this is byte-identical to
  // Board's key, so opening the drawer from the board is still free; once the board has loaded more,
  // the two get their own entries instead of fighting over one.
  const boardTasksQ = useQuery({ queryKey: boardTasksKey(projectId, task?.boardId, uiSettings().taskPageLimit), queryFn: () => boardsApi.listTasks(projectId, task!.boardId, { limit: String(uiSettings().taskPageLimit) }), enabled: !!task });
  const busyKeys = useMemo(() => busyAgentKeys(boardTasksQ.data?.tasks ?? []), [boardTasksQ.data]);
  const agentList = useMemo(
    () => buildAgentList(
      agentsQ.data?.sessions ?? [],
      busyKeys,
      [
        ...((histQ.data?.events ?? []).map((e) => e.agent).filter(Boolean) as string[]),
        ...((boardTasksQ.data?.tasks ?? []).map((t) => t.assignee).filter(Boolean) as string[]),
      ],
      Date.now(),
    ),
    [agentsQ.data, busyKeys, histQ.data, boardTasksQ.data],
  );
  // @mention candidates for the comment composer: project member identities + known agent names. Only
  // member identities actually resolve to a notification server-side; agent names are offered for convenience.
  const mentionCandidates = useMemo(() => {
    const set = new Set<string>(Object.keys(projectQ.data?.project.members ?? {}));
    for (const a of agentList) if (a.name) set.add(a.name);
    return [...set];
  }, [projectQ.data, agentList]);

  const refresh = (): void => {
    // No 'hist' here: its key is ['hist', projectId, boardId, taskId], so ['hist', projectId, taskId]
    // matched nothing. The line below, which invalidates the whole ['hist', projectId] prefix, is what
    // has actually been refreshing it.
    for (const k of ['task', 'ctx', 'subs', 'mem', 'att', 'comments', 'followups', 'runs']) void qc.invalidateQueries({ queryKey: [k, projectId, taskId] });
    void qc.invalidateQueries({ queryKey: ['tasks', projectId] });
    void qc.invalidateQueries({ queryKey: ['hist', projectId] });
  };
  // onSuccess runs ONLY when the promise resolved — so on failure we surface the error and DON'T run the
  // caller's success-path state change (e.g. setLease(null) after a failed release, which would desync the
  // UI from the server). The rejection is consumed here (no unhandled rejection at the void call sites).
  const run: Run = (p, onSuccess) => {
    setErr(null);
    // Every detail write (labels, fields, summary, subtasks, memory, deps, claim/release/complete/
    // block/handoff) is marked as a local edit inside req() (api/http.ts), so the board's "changed under you"
    // ring keeps meaning "someone ELSE touched this" rather than firing on your own typing.
    return p.then((r) => { refresh(); onSuccess?.(r); }).catch((e) => {
      // Only a lease outcome answers LEASE_EXPIRED: the remembered lease is spent, so stop offering it.
      if (e instanceof ApiError && e.code === 'LEASE_EXPIRED') setLease(null);
      // STALE: the task moved on. Read it now, so an open editor shows what changed under it.
      if (e instanceof ApiError && e.code === 'STALE') void qc.invalidateQueries({ queryKey: ['task', projectId, taskId] });
      setErr(String(e instanceof Error ? e.message : e));
    });
  };

  // Shared upload path for the attachments dropzone (click-to-browse and drag-drop both call this).
  const uploadFile = (file: File): void => {
    const mimeType = file.type || 'application/octet-stream';
    const kind = mimeType.startsWith('image/') ? 'image' : mimeType.startsWith('audio/') ? 'audio' : mimeType.startsWith('video/') ? 'video' : 'file';
    void run(
      mediaApi
        .createAttachment(projectId, taskId, { fileName: file.name, mimeType, kind, bytes: file.size })
        .then((r) => fetch(r.uploadUrl, { method: 'PUT', headers: { 'content-type': mimeType }, body: file }))
        // Surface a failed object-store PUT instead of leaving the attachment "pending" forever with no feedback.
        .then((res) => { if (!res.ok) throw new Error(`Upload failed (${res.status})`); }),
    );
  };

  // Optimistic: the status changes in one frame everywhere (board card + this screen), rolls back on error.
  const move = useTaskAction<Status>(
    projectId,
    (to, t) => ({ ...t, status: to }),
    (to, t) => tasksApi.moveTask(projectId, t.id, t.version, { toStatus: to }),
  );

  // The remembered lease is offered only while the live claim is this identity's with no agent named
  // (a claim that names an agent is that agent's, even under this identity's token: P-320). A claim
  // with no lease here is someone else's, or this identity's from another browser or session.
  const claimIsMine = !!task?.claim && !!identity && task.claim.owner === identity && !task.claim.agent;
  const myLease = claimIsMine && leaseEntry ? leaseEntry.leaseId : null;
  const countdown = useLeaseCountdown(task?.claim?.expiresAt, myLease ? leaseEntry!.at : null);
  const heldByOther = !!task?.claim && !myLease;
  // Drop a remembered lease once a read of the task made after the claim shows the claim gone or not
  // this browser's (released, completed or reaped elsewhere), so storage does not collect them.
  const staleLease = !!leaseEntry && !!task && !claimIsMine && taskQ.dataUpdatedAt > leaseEntry.at;
  useEffect(() => {
    if (staleLease && identity) { forgetLease(identity, projectId, taskId); leaseChanged(); }
  }, [staleLease, identity, projectId, taskId]);

  const history = (ownHistQ.data?.events ?? []).filter((e: ActivityEvent) => e.taskId === taskId);
  /** The ticket's events loaded so far; `truncated` when the read stopped before its first event. */
  const historyState = { loading: ownHistQ.isLoading || !task, failed: ownHistQ.isError, truncated: !!ownHistQ.data?.truncated };
  const subs = subQ.data?.tasks ?? [];
  // A cancelled subtask is DONE but not delivered: it counts in the total and not in done (P-208).
  const subsDone = subs.filter((s) => s.status === 'DONE' && !isCancelled(s)).length;
  const subsPct = subs.length ? Math.round((subsDone / subs.length) * 100) : 0;

  // Dependency chips, split into blocked-by / blocks relative to this task.
  const edges = ctxQ.data?.deps.edges ?? [];
  const blockedByEdges = edges.filter((e) => e.type === 'blocks' && e.toTaskId === taskId);
  const blocksEdges = edges.filter((e) => e.type === 'blocks' && e.fromTaskId === taskId);
  const relatesEdges = edges.filter((e) => e.type !== 'blocks');

  // Resolve linked task ids (deps, parent) to their live card from the board cache — best-effort: tasks
  // on other boards or beyond the list limit fall back to a bare id chip.
  const taskById = new Map<string, Task>((boardTasksQ.data?.tasks ?? []).map((t) => [t.id, t]));

  // Board-aware move targets — shared by the header status select and the Actions move buttons.
  // The board's transition table (the fixed one, read-only), else the default; only offer statuses
  // that have a column on THIS board once it loads (labels use the column's name).
  const cols = boardQ.data?.board.columns ?? [];
  const colFor = (s: string): { name: string } | undefined => cols.find((c) => c.status === s);
  /** The statuses `s` may move to: the board's table once it has loaded, else the fixed one. */
  const nextOf = (s: Status): Status[] => boardQ.data?.board.transitions?.[s] ?? NEXT[s];
  const transitions = task ? nextOf(task.status) : [];
  const allMoves = transitions.map((to) => ({ to, col: colFor(to) }));
  const moveTargets = cols.length ? allMoves.filter((x) => x.col) : allMoves;

  // What the caller's role lets them do here (P-209, P-440). The server decides; these only keep a
  // caller from being offered a refusal. Every one fails closed while the role is loading. The floors
  // are the tools' tiers, from the one table in toolFloors.ts (which points at grants.go):
  //  - `move_task` (master): the status, the requeue of a poisoned task (BLOCKED → TODO, plan §Fold 1),
  //    which is the same call, and a subtask's checkbox.
  //  - `update_task` (master): Edit (title, description) and every field in Details.
  //  - `claim_task`, `add_comment`, `add_context_item` (note, link), `write_memory`, `attach_file`,
  //    `set_summary`, `ack_follow_up` (write: a worker or above).
  //  - `add_dependency`/`remove_dependency`, `add_subtask`, `add_follow_up`, and the race tools (master).
  // The role is the caller's in this project from the roster `ProjectProvider` holds (`useMyProjectRole`),
  // the source every screen gates on. Where the roster has no row for it (a surface rendered with no
  // provider) the project's own member list, already read here for names, says the same thing.
  // A roster that has loaded without a row (`NOT_A_MEMBER`, P-443) defers to the member map too, so a
  // roster read before the caller was added does not close their controls; it stands once the project
  // read has answered without them.
  const rosterRole = useMyProjectRole(projectId);
  const memberRole = identity ? projectQ.data?.project.members[identity] : undefined;
  const myRole = rosterRole !== undefined && rosterRole !== NOT_A_MEMBER
    ? rosterRole
    : memberRole ?? (rosterRole === NOT_A_MEMBER && !projectQ.isLoading ? NOT_A_MEMBER : undefined);
  const canMove = mayCall(myRole, 'move_task');
  const canRequeue = canMove;
  const canClaim = mayCall(myRole, 'claim_task');
  const canComment = mayCall(myRole, 'add_comment');
  const canEdit = mayCall(myRole, 'update_task');
  const canSummarise = mayCall(myRole, 'set_summary');
  const canLink = mayCall(myRole, 'add_dependency') && mayCall(myRole, 'remove_dependency');
  const canAddSubtask = mayCall(myRole, 'add_subtask');
  const canAnnotate = mayCall(myRole, 'add_context_item');
  const canWriteMemory = mayCall(myRole, 'write_memory');
  const canAttach = mayCall(myRole, 'attach_file');
  const canFollowUp = mayCall(myRole, 'add_follow_up');
  const canAck = mayCall(myRole, 'ack_follow_up');
  const canRace = mayCall(myRole, 'start_runs');
  /** The reason a control calling `tool` is closed to this caller, or `undefined` (open, or loading). */
  const why = (tool: GatedTool, what: string): string | undefined => closedWhy(myRole, tool, what);

  const startEdit = (): void => {
    if (!task || !canEdit) return;
    setEditing(true);
    setDraft({ title: task.title, description: task.description, version: task.version, base: { title: task.title, description: task.description } });
  };
  // Someone else changed the title or the description since the draft was taken. Save holds until the
  // editor chooses: `keepMyEdit` or Cancel. A version bump that changed neither (a label, a claim)
  // is no conflict: Save then sends only the edited fields on the version last read, and the server's
  // check refuses it (STALE) if the task has moved on again.
  const editConflict = editing && !!task && (task.title !== draft.base.title || task.description !== draft.base.description);
  /** Keep my edit over the newer version: a field I did not touch takes the newer text. */
  const keepMyEdit = (): void => {
    if (!task) return;
    setDraft((dr) => ({
      title: dr.title === dr.base.title ? task.title : dr.title,
      description: dr.description === dr.base.description ? task.description : dr.description,
      version: task.version,
      base: { title: task.title, description: task.description },
    }));
  };
  const saveEdit = (): void => {
    if (!task || editConflict) return;
    const patch: { title?: string; description?: string } = {};
    if (draft.title !== draft.base.title) patch.title = draft.title;
    if (draft.description !== draft.base.description) patch.description = draft.description;
    if (patch.title === undefined && patch.description === undefined) { setEditing(false); return; }
    void run(tasksApi.updateTask(projectId, taskId, task.version, patch), () => setEditing(false));
  };

  return {
    projectId, taskId, identity, task, taskQ, ctx: ctxQ.data, memQ, attQ,
    err, setErr, run, uploadFile, move, countdown, myLease, claimIsMine, heldByOther, setLease,
    editing, setEditing, draft, setDraft, startEdit, editConflict, keepMyEdit, saveEdit,
    agentList, mentionCandidates, history, historyState,
    subs, subsDone, subsPct, blockedByEdges, blocksEdges, relatesEdges, taskById,
    colFor, moveTargets, nextOf, agentsQ, histQ, myRole, canRequeue, canMove, canClaim, canComment, names,
    canEdit, canSummarise, canLink, canAddSubtask, canAnnotate, canWriteMemory, canAttach, canFollowUp, canAck, canRace, why,
  };
}

export type TaskDetail = ReturnType<typeof useTaskDetail>;
/** The same object once the ticket has loaded. Panels take this, so none of them re-checks `task`.
 *  ponytail: one cast per screen (`d as LoadedTaskDetail`) after the `if (!d.task)` early return —
 *  narrowing a property does not narrow the object, and a discriminated union here would be ceremony. */
export type LoadedTaskDetail = TaskDetail & { task: Task };
