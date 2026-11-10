/**
 * Everything a ticket detail surface needs, in one hook.
 *
 * WHY: the drawer and the full page show the SAME record. If each ran its own queries and its own
 * derived state they would drift — different move targets, different lease maths, two caches. So the
 * data path is defined once here and both screens render the same panels (`./panels.tsx`) off it.
 */
import { useEffect, useMemo, useState } from 'react';
import { useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import * as agentsApi from '../../api/agents.js';
import * as boardsApi from '../../api/boards.js';
import * as contextApi from '../../api/context.js';
import * as feedApi from '../../api/feed.js';
import * as mediaApi from '../../api/media.js';
import * as projectsApi from '../../api/projects.js';
import * as streamApi from '../../api/stream.js';
import * as tasksApi from '../../api/tasks.js';
import { useAuth } from '../../auth.js';
import { boardTasksKey, readBoardTasks, useTaskAction } from '../../mutations.js';
import { uiSettings } from '../../settings.js';
import { buildAgentList, busyAgentKeys } from '../../agentStatus.js';
import { NOT_A_MEMBER, useDisplayNames, useMyProjectRole } from '../../displayNames.js';
import { closedWhy, mayCall, type GatedTool } from '../../toolFloors.js';
import { isCancelled } from '../../reports.js';
import type { ActivityEvent, AgentSession, Status, Task } from '../../types.js';
import { keepCachedDevices, replaceDevicePresence } from '../../presenceMerge.js';

// The fixed table (ADR-0005), the fallback until the board's own `transitions` (the same table, as
// get_board answers it) has loaded — so the buttons offer exactly the moves the server accepts.
const NEXT: Record<Status, Status[]> = {
  TODO: ['IN_PROGRESS'],
  IN_PROGRESS: ['REVIEW', 'BLOCKED', 'TODO'],
  BLOCKED: ['IN_PROGRESS', 'TODO'],
  REVIEW: ['DONE', 'IN_PROGRESS'],
  DONE: ['IN_PROGRESS'],
};

/** Fire a write, refresh the ticket's caches, surface the error instead of throwing. */
export type Run = <T>(p: Promise<T>, onSuccess?: (r: T) => void) => Promise<void>;

// Live ticking lease countdown ("4m 12s") + fraction remaining for the progress bar. Recomputes once
// a second off a now() tick; assumes the core's default 5-minute lease window (LEASE_DEFAULT_SECONDS)
// for the bar fill (purely cosmetic — same window the board card's lease bar uses).
function useLeaseCountdown(expiresAt: string | null | undefined): { label: string; frac: number; expired: boolean } {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!expiresAt) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [expiresAt]);
  if (!expiresAt) return { label: '', frac: 0, expired: true };
  const ms = new Date(expiresAt).getTime() - now;
  if (ms <= 0) return { label: 'expired', frac: 0, expired: true };
  const total = 5 * 60 * 1000;
  const s = Math.floor(ms / 1000);
  const label = s >= 60 ? `${Math.floor(s / 60)}m ${s % 60}s` : `${s}s`;
  return { label, frac: Math.max(0, Math.min(1, ms / total)), expired: false };
}

export function useTaskDetail(projectId: string, taskId: string) {
  const qc = useQueryClient();
  const { identity } = useAuth();
  const [lease, setLease] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<{ title: string; description: string }>({ title: '', description: '' });

  // The board's SSE stream invalidates ['task', projectId, …] on every board change (the ui-backend keeps
  // a stateful MCP session even under STATELESS_MCP=true, so resource subscriptions fire), keeping
  // task.version fresh for version-checked actions (Pick, Cancel race, follow-ups, moves). This 30s poll
  // is just a backstop for a dropped stream.
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
  // The kept device rows are retired only by a presence frame, so this hook writes the frames itself:
  // on the full task page no Board is mounted to do it, and a session that left would otherwise stay
  // listed through every refetch (P-197). In the drawer the Board writes the same rows the same way.
  // Joining the shared socket opens no second one; a frame is the device's whole live list.
  useEffect(
    () =>
      streamApi.userStream({
        onPresence: (sessions) =>
          qc.setQueryData<{ sessions: AgentSession[] }>(['agentsessions'], (old) => ({
            sessions: replaceDevicePresence(old?.sessions, sessions),
          })),
      }),
    [qc],
  );
  const task = taskQ.data?.task;
  const [ctxQ, subQ, memQ, attQ] = useQueries({
    queries: [
      { queryKey: ['ctx', projectId, taskId], queryFn: () => contextApi.taskContext(projectId, taskId) },
      { queryKey: ['subs', projectId, taskId], queryFn: () => contextApi.listSubtasks(projectId, taskId) },
      { queryKey: ['mem', projectId, taskId], queryFn: () => contextApi.readMemory(projectId, taskId) },
      { queryKey: ['att', projectId, taskId], queryFn: () => mediaApi.listAttachments(projectId, taskId) },
    ],
  });
  const histQ = useQuery({
    queryKey: ['hist', projectId, task?.boardId, taskId],
    queryFn: () => feedApi.feed(projectId, task!.boardId),
    enabled: !!task,
  });
  // Board (for column-aware "Move to" labels) + board tasks (to derive which agents are "busy" = hold a
  // live claim). Both reuse the board screen's cache keys, so opening the drawer from the board is free.
  const boardQ = useQuery({ queryKey: ['board', projectId, task?.boardId], queryFn: () => boardsApi.getBoard(projectId, task!.boardId), enabled: !!task });
  // The page size is IN the key (see `boardTasksKey`). At the default limit this is byte-identical to
  // Board's key, so opening the drawer from the board is still free; once the board has loaded more,
  // the two get their own entries instead of fighting over one. Both read through `readBoardTasks`: the
  // shared entry runs the read of whichever observer rendered last, and a raw read could put rows older
  // than the board's last frame back on screen.
  const boardTasksQ = useQuery({ queryKey: boardTasksKey(projectId, task?.boardId, uiSettings().taskPageLimit), queryFn: () => readBoardTasks(qc, projectId, task!.boardId, uiSettings().taskPageLimit), enabled: !!task });
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
    return p.then((r) => { refresh(); onSuccess?.(r); }).catch((e) => { setErr(String(e instanceof Error ? e.message : e)); });
  };

  // Shared upload path for the attachments dropzone (click-to-browse and drag-drop both call this).
  const uploadFile = (file: File): void => {
    const mimeType = file.type || 'application/octet-stream';
    const kind = mimeType.startsWith('image/') ? 'image' : mimeType.startsWith('audio/') ? 'audio' : mimeType.startsWith('video/') ? 'video' : 'file';
    void run(
      mediaApi
        .createAttachment(projectId, taskId, { fileName: file.name, mimeType, kind, bytes: file.size })
        .then((r) => fetch(mediaApi.onOwnOrigin(r.uploadUrl), { method: 'PUT', headers: { 'content-type': mimeType }, body: file }))
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

  const countdown = useLeaseCountdown(task?.claim?.expiresAt);

  // The leaseId survives a reload via task.claim: if the signed-in human owns the live claim, they can
  // act on it even though the in-session `lease` state was reset. (lease covers the just-claimed case.)
  const myLease = lease ?? (task?.claim && task.claim.owner === identity ? task.claim.leaseId : null);
  const heldByOther = !!task?.claim && task.claim.owner !== identity && !lease;

  const history = (histQ.data?.events ?? []).filter((e: ActivityEvent) => e.taskId === taskId);
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
  const transitions = (task && (boardQ.data?.board.transitions?.[task.status] ?? NEXT[task.status])) ?? [];
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
    setDraft({ title: task.title, description: task.description });
  };

  return {
    projectId, taskId, identity, task, taskQ, ctx: ctxQ.data, ctxQ, subQ, memQ, attQ,
    err, setErr, run, uploadFile, move, countdown, myLease, heldByOther, setLease,
    editing, setEditing, draft, setDraft, startEdit,
    agentList, mentionCandidates, history,
    subs, subsDone, subsPct, blockedByEdges, blocksEdges, relatesEdges, taskById,
    colFor, moveTargets, agentsQ, histQ, myRole, canRequeue, canMove, canClaim, canComment, names,
    canEdit, canSummarise, canLink, canAddSubtask, canAnnotate, canWriteMemory, canAttach, canFollowUp, canAck, canRace, why,
  };
}

export type TaskDetail = ReturnType<typeof useTaskDetail>;
/** The same object once the ticket has loaded. Panels take this, so none of them re-checks `task`.
 *  ponytail: one cast per screen (`d as LoadedTaskDetail`) after the `if (!d.task)` early return —
 *  narrowing a property does not narrow the object, and a discriminated union here would be ceremony. */
export type LoadedTaskDetail = TaskDetail & { task: Task };
