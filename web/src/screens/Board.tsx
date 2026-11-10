/**
 * The Kanban board — Linear-grade interaction layer (Phases 1–3):
 *  - keyboard selection (j/k/arrows move, Enter opens, c creates, s/p open palette sub-pages)
 *  - URL-addressable drawer (?task=T-12 — deep links + working back button)
 *  - dnd-kit drag (pointer only) with drop highlight + lifted overlay. No KeyboardSensor: Enter or Space
 *    on a focused card opens it, and the keyboard moves a card through `s` (Set status)
 *  - framer-motion card enter/leave; skeleton loading; optimistic create/move
 *  - one motion vocabulary (--ease/--t-1..3 in index.css) — extend it, don't invent alongside it
 */
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { NOT_A_MEMBER, useDisplayNames, useMyProjectRole } from '../displayNames.js';
import { closedWhy, mayCall } from '../toolFloors.js';
import { Icon } from '../icons.js';
import { useNavigate, useParams, useSearchParams } from 'react-router';
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AnimatePresence, motion } from 'framer-motion';
import { DndContext, DragOverlay, PointerSensor, closestCorners, useDroppable, useSensor, useSensors } from '@dnd-kit/core';
import { SortableContext, arrayMove, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { generateKeyBetween } from 'fractional-indexing';
import * as agentsApi from '../api/agents.js';
import * as boardsApi from '../api/boards.js';
import * as feedApi from '../api/feed.js';
import * as mediaApi from '../api/media.js';
import * as sprintsApi from '../api/sprints.js';
import * as streamApi from '../api/stream.js';
import * as tasksApi from '../api/tasks.js';
import { ApiError, isLocalEdit } from '../api/http.js';
import { Avatar, Marquee, PageHeader, PoisonBadge, PriorityDot, StatusPill, eventActor, eventVerb, isPoisoned, leaseLeft, timeAgo } from '../components.js';
import { useShortcuts } from '../shortcuts.js';
import { publishPalette, clearPalette, openPalette } from '../palette.js';
import { TEMP_TASK_ID, boardTasksKey, isTempTaskId, mergeFirstPage, mergeSnapshotPreservingLocalEdits, patchTaskCaches, restore, useTaskAction, useTaskCreate } from '../mutations.js';
import { TaskDrawer } from './TaskDrawer.js';
import { type AgentEntry, STATE_META, agentKey, buildAgentList, busyAgentKeys, isLiveEntry } from '../agentStatus.js';
import { uiSettings } from '../settings.js';
import { StatusDot } from '../AgentSelect.js';
import type { ActivityEvent, AgentSession, Board as BoardT, Sprint, SprintStatus, Status, Task } from '../types.js';
import { STATUSES } from '../types.js';
import { keepCachedDevices, replaceDevicePresence } from '../presenceMerge.js';
import { isCancelled, subtaskProgress } from '../reports.js';

const SPRINT_STATUS_COLOR: Record<SprintStatus, string> = {
  PROPOSED: 'var(--review)',
  ACTIVE: 'var(--progress)',
  CLOSED: 'var(--done)',
  ABANDONED: 'var(--text-muted)',
};

// Connected-agents presence cluster for the board header — "who's online in this workspace right now".
function AgentPresence({ agents }: { agents: AgentEntry[] }) {
  // Only "who's around now" — not the all-time session history. An agent that hasn't made a request
  // in the presence window drops off, so a genuinely active agent stands out instead of being buried.
  const live = agents.filter((a) => isLiveEntry(a, Date.now()));
  const online = live.filter((a) => a.state !== 'offline').length;
  const pill = (
    <span
      style={{
        display: 'inline-flex', alignItems: 'center', gap: 5, fontSize: 11, whiteSpace: 'nowrap',
        padding: '2px 9px', borderRadius: 999, border: '1px solid var(--border)',
        color: online > 0 ? 'var(--done)' : 'var(--text-muted)',
      }}
    >
      <span aria-hidden="true" style={{ width: 7, height: 7, borderRadius: '50%', background: online > 0 ? 'var(--done)' : 'var(--text-muted)' }} />
      {online > 0 ? `${online} agent${online === 1 ? '' : 's'} online` : 'no agents online'}
    </span>
  );
  // NO PER-AGENT CHIPS. They were a horizontally-unbounded list in a fixed-height bar: five names, each
  // with a dot and a tool logo, pushing the board name and the action buttons around as agents came and
  // went. The count is the thing being scanned; WHO is a detail, and a detail belongs in the tooltip
  // rather than in the layout. The full roster (not a truncated five) is on the pill's `title`, and the
  // Agents screen is where it is genuinely browsable.
  return (
    <div className="presence" title={live.map((a) => `${a.name} — ${STATE_META[a.state].label}`).join('\n')}>
      {pill}
    </div>
  );
}

const PRIORITIES = ['low', 'normal', 'high', 'urgent'] as const;

// Lifecycle tools advance a task's *status* in place (only an explicit move changes columnId), so the
// board places a card by the column whose status matches the task — falling back to its columnId for
// columnless statuses (BLOCKED), which are surfaced via a StatusPill.
function columnIdOf(board: BoardT | undefined, t: Task): string {
  if (!board) return t.columnId;
  const own = board.columns.find((c) => c.id === t.columnId);
  if (own && own.status === t.status) return t.columnId;
  const byStatus = board.columns.find((c) => c.status === t.status);
  return byStatus ? byStatus.id : t.columnId;
}

// Inline assign-to-agent control on a card. Lists agents the workspace has seen (sessions + feed),
// accepts a free-typed name, and writes task.assignee — which is exactly what the assignee-aware
// wait_for_task / next_task {assignee} picks up. Menu is fixed-positioned so the column's
// own overflow scroll can't clip it; clicks are stopped so they neither open the drawer nor start a drag.
export function AssigneePicker({ task, agents, onAssign, tabbable = true, canAssign, closedWhy }: {
  task: Task;
  agents: AgentEntry[];
  onAssign: (taskId: string, version: number, assignee: string | null) => void;
  /**
   * May the caller set the assignee? It is `update_task`, tier master (P-440). Below it the chip stays
   * (it is the card's WHO, and the assignee is still worth reading) but is disabled, its title says why,
   * and `closedWhy.id` points at the one reason the board shows for every card, rather than one per card.
   */
  canAssign: boolean;
  closedWhy?: { id: string; text: string } | null;
  /**
   * Is this chip in the tab order? On a board card the answer is "only when the card is SELECTED".
   *
   * The chip used to be `visibility: hidden` until hover, and that was doing two jobs — hiding it, and
   * keeping ~200 buttons off the tab sequence, since `visibility: hidden` removes an element from it.
   * Making the chip always visible (so cards stop changing shape under the pointer) silently undid the
   * second job and put a tab stop between every pair of adjacent cards. `tabIndex={-1}` restores it
   * WITHOUT re-hiding the control, and routes keyboard access through the board's existing j/k
   * selection model — the same affordance `.card.selected .marquee` already uses rather than inventing
   * a second one.
   */
  tabbable?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [coords, setCoords] = useState<{ top: number; left: number } | null>(null);
  const [typed, setTyped] = useState('');
  const btnRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent): void => {
      const n = e.target as Node;
      if (btnRef.current?.contains(n) || menuRef.current?.contains(n)) return;
      setOpen(false);
    };
    const onKey = (e: KeyboardEvent): void => { if (e.key === 'Escape') setOpen(false); };
    document.addEventListener('mousedown', onDoc);
    document.addEventListener('keydown', onKey);
    return () => { document.removeEventListener('mousedown', onDoc); document.removeEventListener('keydown', onKey); };
  }, [open]);

  const toggle = (): void => {
    if (open) { setOpen(false); return; }
    const r = btnRef.current?.getBoundingClientRect();
    if (r) {
      const w = 230;
      setCoords({ top: r.bottom + 6, left: Math.max(8, Math.min(window.innerWidth - w - 8, r.right - w)) });
    }
    setOpen(true);
  };
  const pick = (name: string | null): void => { onAssign(task.id, task.version, name); setOpen(false); setTyped(''); };
  const stop = (e: React.SyntheticEvent): void => e.stopPropagation();
  const menuAgents = agents.filter((a) => isLiveEntry(a, Date.now()));
  // Closed: no menu, and the reason in the title (and, once the role is known, the board's line).
  const closed = !canAssign;
  const closedTitle = closedWhy?.text ?? 'Checking your role';

  return (
    <span onClick={stop} onPointerDown={stop}>
      {task.assignee ? (
        <button
          ref={btnRef}
          type="button"
          className="assign-chip"
          onClick={toggle}
          disabled={closed}
          title={closed ? `Assigned to ${task.assignee}. ${closedTitle}` : `Assigned to ${task.assignee} — click to reassign`}
          aria-describedby={closed && closedWhy ? closedWhy.id : undefined}
        >
          <StatusDot state={agents.find((a) => a.name === task.assignee)?.state ?? 'offline'} size={7} />
          <Avatar id={task.assignee} tool={agents.find((a) => a.name === task.assignee)?.tool ?? null} />
          {/* No `label` — the enclosing button's title already names it; a second name would be read twice. */}
          <Marquee className="assign-name">{task.assignee.split('@')[0]}</Marquee>
        </button>
      ) : (
        <button
          ref={btnRef}
          type="button"
          className="assign-chip ghost"
          onClick={toggle}
          disabled={closed}
          title={closed ? closedTitle : 'Assign to an agent'}
          aria-describedby={closed && closedWhy ? closedWhy.id : undefined}
          tabIndex={tabbable ? undefined : -1}
        >
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
          assign
        </button>
      )}
      {/* PORTALLED TO document.body, AND THAT IS THE BUG FIX — not tidiness.
          `.assign-menu` is `position: fixed` with `top`/`left` taken from `getBoundingClientRect()`,
          i.e. VIEWPORT coordinates. A `position: fixed` element is positioned against the nearest
          ancestor with a transform, not the viewport (CSS Position §4) — and `.card:hover` sets
          `transform: translateY(-1px)`. The ghost chip is only visible on hover, so clicking it
          GUARANTEES that transform is live: the menu opened every time and painted offset by the
          card's own position on screen, off-screen for most cards. That is the "+ assign does
          nothing" the owner hit. Rendering into `document.body` puts the menu outside every card,
          so no ancestor transform — this one, the `card-in` keyframe, or the drag overlay's
          `rotate(2deg)` — can capture it again. */}
      {open && !closed && coords && createPortal(
        <div ref={menuRef} className="assign-menu" role="menu" style={{ top: coords.top, left: coords.left }} onClick={stop} onPointerDown={stop}>
          <div className="assign-head">Assign to agent</div>
          <div className="assign-list">
            {menuAgents.length === 0 && <div className="assign-empty">No agents online — type a name to assign.</div>}
            {menuAgents.map((a) => (
              <button key={agentKey(a.identityId, a.name)} type="button" className="assign-item" role="menuitem" onClick={() => pick(a.name)} title={STATE_META[a.state].label}>
                <StatusDot state={a.state} size={7} />
                <Avatar id={a.name} tool={a.tool} />
                <Marquee className="assign-name">{a.name.split('@')[0]}</Marquee>
                {task.assignee === a.name && <span className="assign-check"><Icon name="check" size={12} /></span>}
              </button>
            ))}
          </div>
          <div className="assign-type">
            <input
              className="input"
              placeholder="agent name / email"
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter' && typed.trim()) pick(typed.trim()); }}
            />
            <button type="button" className="btn" disabled={!typed.trim()} onClick={() => pick(typed.trim())}>Set</button>
          </div>
          {task.assignee && <button type="button" className="assign-item unassign" onClick={() => pick(null)}>Unassign</button>}
        </div>,
        document.body,
      )}
    </span>
  );
}

export function TaskCard({ t, col, sub, sprintName, selected, changed, pending, ripple, onOpen, agents, onAssign, canAssign, assignClosedWhy }: {
  t: Task;
  col: { status: Status };
  sub?: { done: number; total: number };
  /** Name of the task's sprint (first of sprintIds); null/undefined → fall back to the raw id. */
  sprintName?: string | null;
  selected: boolean;
  /** Something (an agent, another human) changed this task under you — flash the ack ring. */
  changed?: boolean;
  /** A move for this card is IN FLIGHT. The card is already drawn in its DESTINATION lane (the move is
   *  optimistic), so without this the UI asserts success before the server has agreed — and on failure
   *  the card snaps back with no explanation. The blurred veil + spinner is what makes the optimism
   *  honest: the card is where it will be, but not yet confirmed to be. */
  pending?: boolean;
  /** Play the drop ripple once, on the card that was just dropped. */
  ripple?: boolean;
  onOpen: () => void;
  agents: AgentEntry[];
  onAssign: (taskId: string, version: number, assignee: string | null) => void;
  /** Passed to the assign chip (P-440). */
  canAssign: boolean;
  assignClosedWhy?: { id: string; text: string } | null;
}) {
  const lease = t.claim ? leaseLeft(t.claim.expiresAt) : null;
  // The hover-reveal state that used to gate the "+ assign" chip is GONE, along with its two mouse
  // handlers: the chip is always rendered now, so there is nothing left to reveal. Keeping a
  // per-card `useState` that only fed a `visibility` toggle meant every card in a 200-card board
  // re-rendered on pointer move for a purely cosmetic effect.
  return (
    <div
      className={`card${selected ? ' selected' : ''}${changed ? ' card-changed' : ''}${pending ? ' card-pending' : ''}`}
      onClick={onOpen}
      style={{ cursor: 'pointer' }}
      data-task-id={t.id}
      aria-busy={pending || undefined}
    >
      {/* Drop ripple — purely decorative, so it is aria-hidden and pointer-events:none in CSS. Keyed
          off a one-shot flag the board clears when the animation ends. */}
      {ripple && <span className="card-ripple" aria-hidden="true" />}
      {/* IN-FLIGHT MOVE. The card is already in its destination lane (the move is optimistic), so this
          is what stops the UI from asserting success the server has not given yet. `aria-busy` above
          carries the same fact to assistive tech, which a purely visual spinner would not. */}
      {pending && (
        <span className="card-saving" role="status" aria-label="Saving move">
          <svg className="card-saving-spin" width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true">
            <circle cx="12" cy="12" r="9" stroke="currentColor" strokeOpacity="0.25" strokeWidth="3" />
            <path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
          </svg>
        </span>
      )}
      <div className="row" style={{ display: 'flex', justifyContent: 'space-between' }}>
        <span className="id">{isTempTaskId(t.id) ? TEMP_TASK_ID : t.id}</span>
        <PriorityDot priority={t.priority} />
      </div>
      <div className="title">{t.title}</div>
      {/* Labels get their own full-width line and are never truncated per-chip — hover/focus (or the
          keyboard selection) marquees the row so every label can actually be read. T-51. */}
      {t.labels.length > 0 && (
        <Marquee className="card-labels" label={`Labels: ${t.labels.join(', ')}`} more>
          {t.labels.map((l) => (
            <span className="label" key={l}>{l}</span>
          ))}
        </Marquee>
      )}
      {/* THE META ROW HAS A FIXED SHAPE: badges on the left, the WHO control on the right, always.
          It used to be one flex row of independently-conditional children with a `flex:1` spacer, so
          every card was a different shape — a plain ticket rendered an EMPTY 24px row, and the assign
          chip appeared and vanished with the pointer, making two identical cards look unrelated. The
          two zones below always render; only their CONTENTS collapse. `.card-badges` may legitimately
          be empty (a plain ticket has nothing to say), and `.card-who` never is — there is always
          either a claim, an assignee, or the control that sets one. */}
      <div className="card-meta">
        <div className="card-badges">
          {t.status !== col.status && <StatusPill status={t.status} />}
          {isPoisoned(t) ? <PoisonBadge task={t} /> : (t.status === 'BLOCKED' || t.blockedBy.length > 0) && <span className="badge blocked">Blocked</span>}
          {t.racing && <span className="badge racing" title="Racing: runs of this task are open until one is picked"><Icon name="finish" size={11} /> Racing</span>}
          {(t.sprintIds?.length ?? 0) > 0 && (
            <span className="badge sprint" title={`Sprint: ${sprintName ?? t.sprintIds![0]!}`}>
              <Icon name="flag" size={11} /> {sprintName ?? t.sprintIds![0]!}
            </span>
          )}
          {t.modelHint?.effort && <span className="badge effort" title={t.modelHint.model}>{t.modelHint.effort}</span>}
          {sub && <span className="badge lease"><Icon name="checkSquare" size={11} /> {sub.done}/{sub.total}</span>}
          {lease && <span className="badge lease"><Icon name="lock" size={11} /> {lease}</span>}
        </div>
        <div className="card-who">
          {t.claim ? (
            <Avatar id={t.claim.agent || t.claim.owner} active={!!lease} />
          ) : (
            <>
              {/* Unclaimed + unassigned: at least show who authored the ticket (usually the planning agent). */}
              {!t.assignee && t.createdByAgent && (
                <span className="card-author" title={`created by ${t.createdByAgent}`}>
                  <Avatar id={t.createdByAgent} />
                </span>
              )}
              {/* ALWAYS VISIBLE. This was `ghostVisible={hover || selected}` with `visibility: hidden`
                  otherwise, which reserved the space and blanked the control — so the card changed
                  shape under the pointer and two identical tickets read differently. The chip is
                  styled quiet (dashed, muted) instead, which keeps resting cards calm without making
                  the only way to assign work invisible until you happen to hover it. */}
              <AssigneePicker task={t} agents={agents} onAssign={onAssign} tabbable={selected} canAssign={canAssign} closedWhy={assignClosedWhy} />
            </>
          )}
        </div>
      </div>
      {lease && t.claim && (
        <div
          className="lease-bar"
          style={{ width: `${Math.max(4, Math.min(100, ((new Date(t.claim.expiresAt).getTime() - Date.now()) / 300000) * 100))}%` }}
        />
      )}
    </div>
  );
}

function DraggableCard({ canMove, ...props }: Parameters<typeof TaskCard>[0] & { canMove: boolean }) {
  // Sortable so cards reorder *within* a lane (not just move between lanes); dnd-kit supplies the
  // transform/transition for the drag + neighbour shuffle. A move is `move_task`, tier master: below it
  // the card does not drag at all (P-440), rather than snapping back after the server refuses it.
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: props.t.id, disabled: !canMove });
  // Leave is framer's (React removes the node the instant a task moves column, so CSS can't see it);
  // ENTER is the plain `.card` CSS keyframe, which also gives the column-transition for free — the
  // card unmounts here and mounts in the destination column, where it plays in. Exit animates opacity
  // only: dnd-kit owns `transform` on this node and two owners of one property is a jank bug.
  // dnd-kit makes this wrapper the card's tab stop (role=button, tabIndex 0) with no key handler of its
  // own, and the board's `Enter` shortcut leaves a focused control its own keys (shortcuts.tsx). So
  // Enter or Space here opens THIS card, not whichever one j/k last selected.
  const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>): void => {
    listeners?.onKeyDown?.(e);
    if (e.target !== e.currentTarget || e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      props.onOpen();
    }
  };
  return (
    <motion.div
      ref={setNodeRef}
      {...attributes}
      {...listeners}
      onKeyDown={onKeyDown}
      data-card-for={props.t.id}
      style={{ transform: CSS.Transform.toString(transform), transition, opacity: isDragging ? 0.35 : 1 }}
      // A create's placeholder leaves at once: it is replaced by the server's card, which enters as it
      // goes, and a fade would show the task twice for its length.
      exit={isTempTaskId(props.t.id) ? undefined : { opacity: 0 }}
      transition={{ duration: 0.16, ease: [0.22, 1, 0.36, 1] }}
    >
      <TaskCard {...props} />
    </motion.div>
  );
}

function Column({ col, count, over, children }: {
  col: { id: string; name: string; wipLimit?: number | null };
  count: number;
  over: boolean;
  children: React.ReactNode;
}) {
  const { setNodeRef, isOver } = useDroppable({ id: col.id });
  return (
    <div className={`column${isOver ? ' col-drop' : ''}`} ref={setNodeRef}>
      <header>
        <span>{col.name}</span>
        <span className={`wip${over ? ' over' : ''}`}>{count}{col.wipLimit != null ? ` / ${col.wipLimit}` : ''}</span>
      </header>
      <div className="cards">{children}</div>
    </div>
  );
}

function BoardSkeleton() {
  return (
    <div className="columns" style={{ flex: 1, minWidth: 0 }}>
      {[0, 1, 2, 3].map((i) => (
        <div className="column" key={i}>
          <header><span className="skeleton" style={{ width: 90, height: 14 }} /></header>
          <div className="cards">
            {[0, 1, 2].map((j) => (
              // staggered so the board assembles instead of appearing all at once
              <div className="card" key={j} style={{ animationDelay: `${(i * 3 + j) * 40}ms` }}>
                <div className="skeleton" style={{ width: 48, height: 10, marginBottom: 8 }} />
                <div className="skeleton" style={{ width: '85%', height: 13, marginBottom: 8 }} />
                <div className="skeleton" style={{ width: '55%', height: 10 }} />
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

/**
 * The sprints rail (T-257, spec §7) — an Activity-rail clone listing the board's sprints: name,
 * status pill, done/total, a goal/acceptance/checkpoint expando (native <details>, no state), a
 * PROPOSED row's "awaiting approval: T-n" link into the proposal task, and click-to-filter.
 *
 * done/total is derived CLIENT-SIDE from the tasks the board already fetched — zero extra core
 * reads. ponytail: KNOWN CEILING — the task list is paged, so members beyond the loaded page are
 * not counted; `memberCount` on the doc is the write-side truth if a board ever outgrows its page.
 *
 * Exported for the render test: pure props in, DOM out — no queries, no router.
 */
export function SprintsRail({ sprints, tasks, filterSprint, onFilter, onOpenTask, onLifecycle, lifecycleClosedWhy, busySprintId, loading, error }: {
  sprints: Sprint[];
  tasks: Task[];
  /** The currently filtered sprint id ('' = none) — the active row is marked and clicking it clears. */
  filterSprint: string;
  onFilter: (sprintId: string) => void;
  onOpenTask: (taskId: string) => void;
  /**
   * The lifecycle transition (T-330). Omitted in the render test, which is why every control below is
   * guarded on it rather than assuming a handler.
   */
  onLifecycle?: (sprint: Sprint, status: SprintStatus) => void;
  /**
   * Why the lifecycle controls are absent: `update_sprint` is tier master (P-440). The board omits
   * `onLifecycle` below master, which hides Start, Close and Abandon on every row; this one line says why.
   */
  lifecycleClosedWhy?: string | null;
  /** The sprint currently mid-transition — its controls disable so a double-click cannot double-post. */
  busySprintId?: string;
  loading?: boolean;
  error?: boolean;
}) {
  return (
    <div className="column" style={{ width: 280, flex: '0 0 280px' }}>
      <header><span>Sprints</span></header>
      <div className="cards">
        {loading && [0, 1].map((i) => <div key={i} className="skeleton" style={{ height: 34 }} />)}
        {error && <div className="error" style={{ fontSize: 12 }}>Couldn’t load sprints.</div>}
        {lifecycleClosedWhy && sprints.some((s) => s.status === 'PROPOSED' || s.status === 'ACTIVE') && (
          <div className="muted" style={{ fontSize: 12 }}>{lifecycleClosedWhy}</div>
        )}
        {sprints.map((s) => {
          const members = tasks.filter((t) => t.sprintIds?.includes(s.id));
          // A cancelled member is DONE but not delivered, so it is not done here, as get_sprint counts it (P-208).
          const done = members.filter((t) => t.status === 'DONE' && !isCancelled(t)).length;
          // Close's warning is the server's: it lists members whose status is not DONE, and a cancelled
          // member is DONE, so this counts by status alone, not by `done` (P-208).
          const notDone = members.filter((t) => t.status !== 'DONE').length;
          // Approval IS the proposal task reaching DONE (spec §5.4) and not cancelled (P-208) — there is no
          // separate flag, so the rail derives it exactly as the core's activation gate does. `find` over the
          // loaded board is enough because the proposal task lives on the sprint's own board by construction.
          const proposal = s.proposalTaskId ? tasks.find((t) => t.id === s.proposalTaskId) : undefined;
          const approvedAlready = !!proposal && proposal.status === 'DONE' && !isCancelled(proposal);
          const busy = busySprintId === s.id;
          const active = filterSprint === s.id;
          const c = SPRINT_STATUS_COLOR[s.status];
          return (
            <div key={s.id} className="card" style={active ? { borderColor: 'var(--accent)' } : undefined} data-sprint-id={s.id}>
              <div
                role="button"
                tabIndex={0}
                aria-pressed={active}
                title={active ? 'Clear the sprint filter' : `Show only ${s.name}`}
                style={{ display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer', minWidth: 0 }}
                onClick={() => onFilter(active ? '' : s.id)}
                onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') onFilter(active ? '' : s.id); }}
              >
                <Marquee className="title" style={{ flex: 1 }}>{s.name}</Marquee>
                <span className="pill" style={{ color: c, background: `color-mix(in srgb, ${c} 18%, transparent)` }}>{s.status}</span>
                <span className="muted" style={{ fontSize: 11, whiteSpace: 'nowrap' }} title={`${done} of ${members.length} loaded members delivered (a cancelled one counts in the total only)`}>
                  <Icon name="checkSquare" size={11} /> {done}/{members.length}
                </span>
              </div>
              {s.status === 'PROPOSED' && s.proposalTaskId && (
                <button
                  type="button"
                  className="btn secondary"
                  style={{ fontSize: 11, padding: '1px 7px', marginTop: 6 }}
                  onClick={() => onOpenTask(s.proposalTaskId!)}
                >
                  {approvedAlready ? 'approved: ' : 'awaiting approval: '}{s.proposalTaskId}
                </button>
              )}
              {/*
                THE ACTIVATION CONTROL (T-330). Approving the proposal task was already possible — it is
                the ordinary REVIEW→DONE control in the drawer — but nothing anywhere could then START
                the sprint, so a human could approve an objective and be left with no way to begin it.
                The BFF route already forwarded an arbitrary patch; only this button was missing.

                It is DISABLED until the proposal task is DONE, and says why. The core refuses the same
                case with INVALID_TRANSITION, so this is not the enforcement — it is the difference
                between a button that explains itself and one that fails after you press it.
              */}
              {s.status === 'PROPOSED' && onLifecycle && (
                <div style={{ display: 'flex', gap: 6, marginTop: 6 }}>
                  <button
                    type="button"
                    className="btn"
                    style={{ fontSize: 11, padding: '1px 9px' }}
                    disabled={!approvedAlready || busy}
                    title={
                      !s.proposalTaskId
                        ? 'This sprint has no proposal task bound, so there is nothing to approve yet.'
                        : approvedAlready
                          ? 'Start the sprint — its members become claimable'
                          : proposal && isCancelled(proposal)
                            ? `${s.proposalTaskId} was cancelled, so it approves nothing; bind a delivered proposal task.`
                            : `Approve ${s.proposalTaskId} first (move it to Done); activation is gated on that.`
                    }
                    onClick={() => onLifecycle(s, 'ACTIVE')}
                  >
                    {busy ? 'Starting…' : 'Start sprint'}
                  </button>
                  <button
                    type="button"
                    className="btn secondary"
                    style={{ fontSize: 11, padding: '1px 9px' }}
                    disabled={busy}
                    title="Abandon this proposal — members are released, nothing is deleted"
                    onClick={() => onLifecycle(s, 'ABANDONED')}
                  >
                    Abandon
                  </button>
                </div>
              )}
              {s.status === 'ACTIVE' && onLifecycle && (
                <div style={{ display: 'flex', gap: 6, marginTop: 6 }}>
                  <button
                    type="button"
                    className="btn"
                    style={{ fontSize: 11, padding: '1px 9px' }}
                    disabled={busy}
                    title={
                      notDone === 0
                        ? 'Close the sprint — every loaded member is DONE'
                        : `Close the sprint. ${notDone} loaded member(s) are not terminal — the server returns a warning, it does not refuse.`
                    }
                    onClick={() => onLifecycle(s, 'CLOSED')}
                  >
                    {busy ? 'Closing…' : 'Close'}
                  </button>
                  <button
                    type="button"
                    className="btn secondary"
                    style={{ fontSize: 11, padding: '1px 9px' }}
                    disabled={busy}
                    title="Abandon this sprint — members are released, nothing is deleted"
                    onClick={() => onLifecycle(s, 'ABANDONED')}
                  >
                    Abandon
                  </button>
                </div>
              )}
              <details style={{ marginTop: 6 }}>
                <summary className="muted" style={{ fontSize: 11, cursor: 'pointer' }}>goal & acceptance</summary>
                <div className="muted" style={{ fontSize: 12, whiteSpace: 'pre-wrap', marginTop: 4 }}>{s.goal}</div>
                <div className="muted" style={{ fontSize: 11, marginTop: 6 }}>Done when:</div>
                <div className="muted" style={{ fontSize: 12, whiteSpace: 'pre-wrap' }}>{s.acceptance}</div>
                {s.checkpoint && (
                  <>
                    <div className="muted" style={{ fontSize: 11, marginTop: 6 }}>Checkpoint:</div>
                    <div className="mono" style={{ fontSize: 11, whiteSpace: 'pre-wrap' }}>{s.checkpoint}</div>
                  </>
                )}
              </details>
            </div>
          );
        })}
        {!loading && !error && sprints.length === 0 && <div className="muted" style={{ fontSize: 12 }}>No sprints on this board.</div>}
      </div>
    </div>
  );
}

/**
 * Which cards changed *under* you (T-51). The board is live — agents move and edit tasks while you
 * are reading it — and before this the card just silently swapped its contents. Watch `version` per
 * task id and flash an ack ring for ~1.4s.
 *
 * Two things it must NOT do: flash the whole board on first load (a version first seen is not a
 * change — seed it silently), and flash your own edits back at you (your optimistic mutation bumps
 * the version too — `isLocalEdit` suppresses those for a few seconds).
 */
function useChangedTasks(tasks: Task[]): Set<string> {
  const seen = useRef(new Map<string, number>());
  const [flashing, setFlashing] = useState<Set<string>>(() => new Set());
  useEffect(() => {
    const fresh: string[] = [];
    for (const t of tasks) {
      const prev = seen.current.get(t.id);
      seen.current.set(t.id, t.version);
      if (prev !== undefined && prev !== t.version && !isLocalEdit(t.id)) fresh.push(t.id);
    }
    if (fresh.length === 0) return;
    setFlashing((s) => new Set([...s, ...fresh]));
    // Deliberately NOT cleaned up on re-run: the next task update must not cancel this batch's
    // fade-out. React ignores state updates on an unmounted component, so the worst case is a
    // no-op timer 1.4s after you navigate away.
    setTimeout(() => {
      setFlashing((s) => {
        const next = new Set(s);
        for (const id of fresh) next.delete(id);
        return next;
      });
    }, 1400);
  }, [tasks]);
  return flashing;
}

export function Board() {
  const { projectId, boardId } = useParams() as { projectId: string; boardId: string };
  const qc = useQueryClient();
  const nav = useNavigate();
  // URL-addressable drawer: ?task=T-12 — deep links work, back button closes.
  const [params, setParams] = useSearchParams();
  const openTask = params.get('task');
  const setOpenTask = (id: string | null): void => {
    setParams((p) => {
      const next = new URLSearchParams(p);
      if (id) next.set('task', id);
      else next.delete('task');
      return next;
    });
  };

  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [title, setTitle] = useState('');
  const [creating, setCreating] = useState(false);
  // Activity pane is hidden by default; only shown if the user has explicitly opened it before.
  const [railOpen, setRailOpen] = useState(() => localStorage.getItem('bd.boardRail') === 'open');
  const toggleRail = (): void => {
    const next = !railOpen;
    localStorage.setItem('bd.boardRail', next ? 'open' : 'closed');
    setRailOpen(next);
  };
  const [filters, setFilters] = useState({ status: '', assignee: '', label: '', sprint: '', unblocked: false, q: '' });
  // Sprints rail — same show-only-if-opened-before persistence as the Activity rail above.
  const [sprintRailOpen, setSprintRailOpen] = useState(() => localStorage.getItem('bd.sprintRail') === 'open');
  const toggleSprintRail = (): void => {
    const next = !sprintRailOpen;
    localStorage.setItem('bd.sprintRail', next ? 'open' : 'closed');
    setSprintRailOpen(next);
  };
  const newTaskRef = useRef<HTMLInputElement>(null);
  const newTaskBtnRef = useRef<HTMLButtonElement>(null);
  // `create_task` is tier master (`internal/domain/grants.go`), so below master "+ New task" and its
  // `c` shortcut are closed and say why, the way the task's Status and Claim do (P-432). The role is
  // the caller's in this project, from the roster; while it loads the button is closed with no reason
  // shown yet. Hiding is not enforcing: the server still refuses.
  const myRole = useMyProjectRole(projectId);
  const canCreate = mayCall(myRole, 'create_task');
  const createClosedWhy = closedWhy(myRole, 'create_task', 'create tasks') ?? null;
  // The rest of the board's writes, from the same table (P-440): a new board is `create_board` (write),
  // the card's assign chip and the palette's priority are `update_task`, a drag and the palette's status
  // are `move_task`, the sprint lifecycle is `update_sprint` (all master). Each is closed below its floor.
  const canNewBoard = mayCall(myRole, 'create_board');
  const newBoardWhy = closedWhy(myRole, 'create_board', 'create boards') ?? null;
  const canAssign = mayCall(myRole, 'update_task');
  const canMoveCards = mayCall(myRole, 'move_task');
  const cardsWhy = closedWhy(myRole, 'update_task', 'assign, move or reprioritise tasks') ?? null;
  const assignClosedWhy = cardsWhy ? { id: 'cards-closed', text: cardsWhy } : null;
  const statusWhy = closedWhy(myRole, 'move_task', 'change the status') ?? null;
  const priorityWhy = closedWhy(myRole, 'update_task', 'change the priority') ?? null;
  const canRunSprints = mayCall(myRole, 'update_sprint');
  const sprintsWhy = closedWhy(myRole, 'update_sprint', 'start, close or abandon sprints') ?? null;
  // Close the create row and return focus to the trigger (keyboard/SR users aren't dropped to <body>).
  const closeCreate = (): void => { setCreating(false); setTimeout(() => newTaskBtnRef.current?.focus(), 0); };

  // Board switcher + creation: a project has many boards — let the user jump between them and add one
  // right here, without going back to the Projects page.
  const displayNames = useDisplayNames(projectId);
  const projectBoardsQ = useQuery({ queryKey: ['boards', projectId], queryFn: () => boardsApi.listBoards(projectId), enabled: !!projectId });
  const projectBoards = projectBoardsQ.data?.boards ?? [];
  const [newBoardOpen, setNewBoardOpen] = useState(false);
  const [newBoardName, setNewBoardName] = useState('');
  const newBoardRef = useRef<HTMLInputElement>(null);
  /**
   * The sprint lifecycle write (T-330). Approve was already a UI action — the proposal task's ordinary
   * REVIEW→DONE control — but ACTIVATE was not, anywhere, so the ceremony stopped halfway: you could
   * approve an objective and then had no way to start it. `update_sprint` was reachable over MCP and
   * through the BFF the whole time; nothing in the SPA ever sent it.
   *
   * WARNINGS ARE SURFACED, NOT SWALLOWED. The core answers the soft cases with `warnings` rather than
   * an error — closing while members are non-terminal, and activating your OWN proposal under the
   * 'warn' rung. That second one is the whole point of the self-approval gate: silently discarding it
   * would leave a record saying a human approved when the same actor did both halves.
   */
  const sprintLifecycle = useMutation({
    mutationFn: ({ sprint, status }: { sprint: Sprint; status: SprintStatus }) =>
      sprintsApi.updateSprint(projectId, sprint.id, sprint.version, { status }),
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: ['sprints', projectId, boardId] });
      // Members become claimable on activation and are released on close/abandon, so the board itself
      // is stale too — not just the rail. PREFIX key on purpose: `boardTasksKey` closes over `limit`,
      // which is not in scope here and would only ever match one page anyway.
      void qc.invalidateQueries({ queryKey: ['tasks', projectId, boardId] });
      setSprintMsg((r.warnings ?? []).join(' · ') || null);
    },
    onError: (e: unknown) => setSprintMsg(e instanceof Error ? e.message : 'Could not update the sprint'),
  });

  const createBoard = useMutation({
    mutationFn: (n: string) => boardsApi.createBoard(projectId, n),
    onSuccess: (r) => {
      setNewBoardName('');
      setNewBoardOpen(false);
      void qc.invalidateQueries({ queryKey: ['boards', projectId] });
      nav(`/projects/${projectId}/boards/${r.board.id}`); // jump straight into the new board
    },
  });

  // Live updates arrive via the SSE stream below, and since T-191 that stream is fed by the
  // `board-events` Pub/Sub carrier fanned out in-process by the gateway — NOT by `wait_for_updates`
  // long-polls, which this comment described until the long-poll was deleted. Each 'updated' nudge
  // triggers a refetch of these queries; React Query swaps data in the background (no reload) and the
  // framer-motion card glide animates moves.
  //
  // *** WHILE THE STREAM IS LIVE THERE IS NO POLL AT ALL. *** It used to run at 30s even in the healthy
  // case, as a backstop against a stalled-but-open connection. That backstop is redundant: `streamApi.stream`
  // already reports health through `setStreamLive`, driven by the server's named `ping` frames and a
  // stall watchdog that reconnects after three missed (T-47) — so a silently-dead stream flips
  // `streamLive` to false and the fallback below takes over. Paying a 30s refetch on every open tab to
  // insure against a condition the watchdog already detects is the cost this whole lane exists to remove.
  //
  // The fallback is NOT removed, only the healthy-case poll: while the stream is DOWN this tightens to
  // 10s so a board still refreshes on its own. Polling is the fallback; the stream is the transport.
  // Note also that `refetchInterval` only fires while the tab is focused, so it never covered a
  // backgrounded board anyway — the SSE nudge always did that.
  // IN-FLIGHT MOVES. The move is optimistic — the card is redrawn in its destination lane immediately —
  // so without this the UI asserts a success the server has not given, and a rejected move snaps back
  // with no explanation. A Set because two cards can be in flight at once.
  const [movingIds, setMovingIds] = useState<ReadonlySet<string>>(new Set());
  // The card that was just dropped, for a one-shot ripple. Cleared on a timer rather than animationend
  // so a card that unmounts mid-animation (it moved column) cannot leave the flag stuck on.
  const [rippleId, setRippleId] = useState<string | null>(null);
  useEffect(() => {
    if (!rippleId) return;
    const t = setTimeout(() => setRippleId(null), 550);
    return () => clearTimeout(t);
  }, [rippleId]);


  const [streamLive, setStreamLive] = useState(true);
  const pollMs = streamLive ? (false as const) : 10000;
  const boardQ = useQuery({ queryKey: ['board', projectId, boardId], queryFn: () => boardsApi.getBoard(projectId, boardId), refetchInterval: pollMs });
  const [limit, setLimit] = useState(() => uiSettings().taskPageLimit);
  // The stream's frame handler reads the key through this, so a "Load more" does not tear the shared
  // socket down and up again to hand the handler a new `limit`.
  const limitRef = useRef(limit);
  limitRef.current = limit;
  // `limit` IS part of the key — see `boardTasksKey`. It used to be held out of it and forced through a
  // manual invalidate effect, which left one cache entry shared with TaskDrawer's differently-sized
  // observer.
  // `limit` is how many rows the board shows, NOT the `limit` of one request: `list_tasks` refuses more
  // than 200, so `listTasksUpTo` reads the rows page by page with the cursor. "Load more" asked for
  // `limit=400` once, the server answered 400 VALIDATION, and every later refetch at that size failed.
  // "Load more" reads the bigger list BEFORE switching to its key (`loadMore` below), so the board never
  // renders a key with no data; `keepPreviousData` stays as the net for an entry collected meanwhile.
  const tasksQ = useQuery({ queryKey: boardTasksKey(projectId, boardId, limit), queryFn: () => boardsApi.listTasksUpTo(projectId, boardId, limit), refetchInterval: pollMs, placeholderData: keepPreviousData });
  const [moreErr, setMoreErr] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const loadMore = async (): Promise<void> => {
    const next = limit + boardsApi.LIST_TASKS_MAX;
    setLoadingMore(true);
    try {
      await qc.fetchQuery({ queryKey: boardTasksKey(projectId, boardId, next), queryFn: () => boardsApi.listTasksUpTo(projectId, boardId, next), retry: false });
      setMoreErr(null);
      setLimit(next);
    } catch (e) {
      setMoreErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoadingMore(false);
    }
  };
  const feedQ = useQuery({ queryKey: ['feed', projectId, boardId], queryFn: () => feedApi.feed(projectId, boardId), refetchInterval: pollMs });
  const searchQ = useQuery({
    queryKey: ['search', projectId, filters.q],
    queryFn: () => mediaApi.search(projectId, filters.q),
    enabled: filters.q.trim().length > 1,
  });
  // Sprints on this board (T-257). Gated so a board with no sprint members and a closed rail pays
  // ZERO queries; once any card carries a sprintId the one bounded list also names the chips and
  // fills the filter. Live: task-side changes ride the board snapshot; sprint-DOC changes arrive as
  // plain `updated` frames (sprint events carry boardId), which invalidate this query below — no new
  // SSE frame.
  const sprintsQ = useQuery({
    queryKey: ['sprints', projectId, boardId],
    queryFn: () => sprintsApi.listSprints(projectId, boardId),
    enabled: sprintRailOpen || (tasksQ.data?.tasks ?? []).some((t) => (t.sprintIds?.length ?? 0) > 0),
    refetchInterval: pollMs,
  });
  const sprints = useMemo(() => sprintsQ.data?.sprints ?? [], [sprintsQ.data]);
  const sprintNameOf = useCallback(
    (t: Task): string | null => sprints.find((s) => s.id === t.sprintIds?.[0])?.name ?? null,
    [sprints],
  );
  // Connected agents in this workspace (available/busy/offline/connecting).
  //
  // Presence still is NOT carried by the event log — `heartbeat_task` deliberately appends NO event (it
  // writes claim.expiresAt only; heartbeat is liveness, not progress), and `TouchBuffer` exists to avoid
  // a write per agent call. So there is nothing for the board-events carrier to fan out, and that has
  // not changed.
  //
  // THE STREAM DOES NOT CARRY THIS LIST. Its presence frame is the device directory's entries for the
  // viewer (CLI processes on this machine, internal/api/stream.go), sent on open and on a directory
  // change. Every other session, an MCP client that pulls each turn (Cursor, a claude.ai connector), is
  // only in `/api/agents/sessions`, and is online while its `lastSeenAt` is within
  // `presenceOfflineAfterMs`. So the list is re-read every half of that window while the board is open
  // (and its tab is in front): fetched once, an agent active seconds ago read offline 75 s after load,
  // and one that connected later never appeared.
  //
  // Each answer keeps the device rows a presence frame already put in the cache (P-197): the list never
  // carries them, and a frame comes on open and then only on a directory change, so an answer landing
  // after the open frame, or any refetch later, would show a live session as offline until the next change.
  const agentsQ = useQuery({
    queryKey: ['agentsessions'],
    queryFn: async () => {
      const fresh = await agentsApi.agentSessions(uiSettings().sessionsLimit);
      return { sessions: keepCachedDevices(fresh.sessions, qc.getQueryData<{ sessions: AgentSession[] }>(['agentsessions'])?.sessions) };
    },
    refetchInterval: Math.max(1000, Math.floor(uiSettings().presenceOfflineAfterMs / 2)),
  });

  // `resync`, or a rebuilt socket: no board frame comes until the next change, so the board, its tasks
  // and its feed are refetched. Only then: the nudge below must not (card-pending.test.tsx).
  const refetchBoard = useCallback(() => {
    void qc.invalidateQueries({ queryKey: ['board', projectId, boardId] });
    void qc.invalidateQueries({ queryKey: ['tasks', projectId, boardId] });
    void qc.invalidateQueries({ queryKey: ['feed', projectId, boardId] });
  }, [qc, projectId, boardId]);

  useEffect(
    () =>
      streamApi.stream(
        projectId,
        boardId,
        () => {
          // THIS BOARD's three queries are NOT invalidated here any more — the `board` snapshot below
          // writes them straight to the cache, so invalidating would fire exactly the three refetches
          // this change exists to remove (one transition used to cost 3 HTTP calls PER OPEN TAB).
          // Everything else still keys off the nudge: drawer state, context, follow-ups and runs are
          // not in the snapshot, and a stale drawer is worse than a refetch.
          for (const k of ['task', 'ctx', 'subs', 'mem', 'att', 'hist', 'followups', 'runs', 'sprints']) void qc.invalidateQueries({ queryKey: [k, projectId] });
        },
        setStreamLive,
        // Presence frames land straight in the cache — no refetch. A frame is this device's whole live
        // list for the viewer, so its rows are replaced (presenceMerge.ts).
        (sessions) =>
          qc.setQueryData<{ sessions: AgentSession[] }>(['agentsessions'], (old) => ({
            sessions: replaceDevicePresence(old?.sessions, sessions),
          })),
        // The composed board snapshot: board + tasks + feed in ONE frame, written directly to the
        // cache. This is what replaces the three refetches above.
        (d) => {
          const snap = d as { board?: unknown; tasks?: { tasks?: Task[] }; feed?: unknown };
          if (snap.board) qc.setQueryData(['board', projectId, boardId], snap.board);
          if (snap.tasks) {
            // *** PRESERVE THE USER'S OWN IN-FLIGHT EDITS. ***
            // The optimistic patch and this snapshot now write the SAME cache entry — they did not
            // before, because the optimistic path was writing a key nobody read. So a snapshot landing
            // mid-flight (an unrelated agent's write, or a refresh that began before this move
            // committed) would overwrite the card back to its pre-move column while the veil is still
            // up, then jump forward again on the settle refetch. Not data loss, but a card that
            // teleports twice under a veil advertising "uncertain" says the wrong thing.
            //
            // `isLocalEdit` is the mechanism that already exists for exactly this (5s window, stamped
            // in `req() (api/http.ts)` — the choke point every task write goes through, so it covers moves AND
            // assigns). Reused rather than adding a second notion of "recently touched by me".
            //
            // MERGE, not skip: every OTHER task in the snapshot still lands, so a colleague's change to
            // a different card is not delayed by your in-flight drag.
            //
            // THE FRAME IS THE FIRST PAGE ONLY (200 tasks). Over a list "Load more" grew, it stands for
            // that page and the rows past it are kept (`mergeFirstPage`); those rows are then re-read,
            // since the frame says nothing about them. A read already in flight began before this
            // change, so it is cancelled and started again (the default `cancelRefetch`): left to
            // finish, it would put the older rows back. Otherwise a frame costs no read.
            const incoming = snap.tasks as { tasks: Task[]; nextCursor?: string };
            const key = boardTasksKey(projectId, boardId, limitRef.current);
            const held = qc.getQueryData<{ tasks: Task[] }>(key)?.tasks.length ?? 0;
            const inFlight = qc.isFetching({ queryKey: key, exact: true }) > 0;
            qc.setQueryData<{ tasks: Task[] }>(key, (old) =>
              mergeSnapshotPreservingLocalEdits(old, mergeFirstPage(old, incoming), isLocalEdit),
            );
            if (inFlight || (incoming.nextCursor && held > incoming.tasks.length)) void qc.invalidateQueries({ queryKey: key, exact: true });
          }
          if (snap.feed) qc.setQueryData(['feed', projectId, boardId], snap.feed);
        },
        refetchBoard,
      ),
    [projectId, boardId, qc, refetchBoard],
  );

  const [newPriority, setNewPriority] = useState('normal');
  const [newLabels, setNewLabels] = useState('');
  const create = useTaskCreate(projectId, boardId);
  const submitCreate = (): void => {
    if (!title) return;
    create.mutate({ title, priority: newPriority, labels: newLabels.split(',').map((s) => s.trim()).filter(Boolean) } as { title: string; priority?: string });
    setTitle('');
    setNewLabels('');
  };

  const board = boardQ.data?.board;
  const allTasks = useMemo(() => tasksQ.data?.tasks ?? [], [tasksQ.data]);
  // NOT LOADED IS NOT EMPTY. A list that failed (or is still on its way) has no rows to show, and
  // drawing it as "0 tasks" with "No tasks" in every column tells the reader the board is empty.
  const tasksLoaded = !!tasksQ.data;
  // The board's own total, from the counts `get_board` answers, for when the list holds one page of it.
  const boardTotal = Object.values(boardQ.data?.taskCounts ?? {}).reduce((n, c) => n + c, 0);
  const tasksSubtitle = !tasksLoaded
    ? tasksQ.isError ? 'tasks not loaded' : 'loading tasks…'
    : tasksQ.data?.nextCursor && boardTotal > allTasks.length
      ? `${allTasks.length} of ${boardTotal} tasks`
      : `${allTasks.length} ${allTasks.length === 1 ? 'task' : 'tasks'}`;
  const changedTasks = useChangedTasks(allTasks);

  // Live agent presence: an agent holding a non-expired claim is "busy"; everything else is derived from
  // session recency. Recomputed as the (polled) sessions/tasks change, so the dots stay current.
  const busyKeys = useMemo(() => busyAgentKeys(allTasks), [allTasks]);
  // `dataUpdatedAt` moves on every answer, even one equal to the last (which keeps the same `data`), so
  // each poll re-derives the states against a fresh `Date.now()` and a silent agent goes offline.
  const agentList = useMemo(
    () => buildAgentList(agentsQ.data?.sessions ?? [], busyKeys, allTasks.map((t) => t.assignee).filter(Boolean) as string[], Date.now()),
    [agentsQ.data, agentsQ.dataUpdatedAt, busyKeys, allTasks],
  );

  const [moveErr, setMoveErr] = useState<string | null>(null);
  // Sprint lifecycle feedback (T-330): the server's `warnings` AND its refusals land here. Same
  // surface for both deliberately — a self-approval warning is not an error, but a human who
  // pressed Start must see it, and a warning shown nowhere is a warning that did not happen.
  const [sprintMsg, setSprintMsg] = useState<string | null>(null);
  const moveCard = useMutation({
    mutationFn: (v: { taskId: string; version: number; toColumnId: string; order: string }) =>
      tasksApi.moveTask(projectId, v.taskId, v.version, { toColumnId: v.toColumnId, order: v.order }),
    onMutate: async (v) => {
      // *** THE KEY MUST BE `boardTasksKey`, WHICH INCLUDES `limit`. ***
      // This read and wrote `['tasks', projectId, boardId]` — one segment short of the key the board
      // actually renders from (`boardTasksKey` appends `limit`). `setQueryData` and `getQueryData` match
      // EXACTLY, so the optimistic patch landed in a cache entry nobody reads and the snapshot came back
      // undefined: the card did not move until the server answered, and the rollback restored nothing.
      //
      // It hid because `invalidateQueries` matches by PREFIX and so did hit the real entry — the card
      // moved on the refetch, which looks like a slow optimistic update rather than a broken one.
      // Your own drag must not flash the "changed under you" ring at you — req() (api/http.ts) marks it.
      await qc.cancelQueries({ queryKey: ['tasks', projectId] });
      const targetStatus = board?.columns.find((c) => c.id === v.toColumnId)?.status;
      // PREFIX-BASED. Building an exact key here is what produced this bug, the identical one in
      // `assignMut`, and — after a bespoke key-builder "fixed" both — a third where a `limit` change
      // mid-flight left the patch writing an entry that did not exist yet. `patchTaskCaches` iterates
      // every `['tasks', projectId, *]` entry, so it cannot miss the one the board is rendering.
      return { snapshot: patchTaskCaches(qc, projectId, v.taskId, (t) => ({
        ...t,
        columnId: v.toColumnId,
        status: targetStatus ?? t.status,
        order: v.order,
      })) };
    },
    onError: (e, _v, ctx) => {
      restore(qc, ctx?.snapshot);
      setMoveErr(String(e instanceof Error ? e.message : e));
    },
    onSuccess: () => setMoveErr(null),
    // THE VEIL CLEARS HERE — a MUTATION-level callback, not a per-call one.
    //
    // It was passed to `mutate(vars, { onSettled })`, and those per-call callbacks are React Query's
    // fragile path: they are documented not to fire if the observer unmounts before the mutation
    // finishes, and a card DOES unmount when it changes column (AnimatePresence swaps it between
    // SortableContexts). Observed on staging: drag Backlog → Review, the server refuses the illegal
    // transition, the card correctly rolls back — and the veil stays up forever. Exactly the
    // "spinner forever" case the settled-not-success choice was meant to prevent, defeated by WHERE
    // the callback lived rather than by which one it was.
    //
    // Mutation-level callbacks always fire, and `variables` carries the taskId.
    onSettled: (_d, _e, v) => {
      setMovingIds((prev) => {
        if (!prev.has(v.taskId)) return prev;
        const next = new Set(prev);
        next.delete(v.taskId);
        return next;
      });
      void qc.invalidateQueries({ queryKey: ['tasks', projectId, boardId] });
      void qc.invalidateQueries({ queryKey: ['board', projectId, boardId] });
    },
  });

  /**
   * THE NET: no move is in flight, so no card may be veiled.
   *
   * `onSettled` above is the intended clear and is enough on its own. This exists because the failure
   * mode is unacceptable rather than merely untidy — a stuck veil greys a card out permanently and the
   * only escape is a page reload, and the FIRST version of this feature shipped exactly that (the clear
   * sat in a per-call callback that never fired when the card unmounted mid-move).
   *
   * Derived from the mutation's own state rather than a second bookkeeping path, so it cannot itself
   * fall out of sync: while `isPending` is false there is nothing to be pending ABOUT.
   */
  useEffect(() => {
    if (moveCard.isPending) return;
    setMovingIds((prev) => (prev.size === 0 ? prev : new Set()));
  }, [moveCard.isPending]);

  // Assign a task to an agent straight from the board (sets task.assignee — what the assignee-aware
  // wait_for_task / next_task {assignee} picks up). Optimistic; the next poll reconciles version.
  const assignMut = useMutation({
    mutationFn: (v: { taskId: string; version: number; assignee: string | null }) =>
      tasksApi.updateTask(projectId, v.taskId, v.version, { assignee: v.assignee }),
    onMutate: async (v) => {
      // THE SAME DEFECT `moveCard` HAD, still here twenty lines below the fix for it — found in review,
      // not by me. And worse than moveCard's, because `['tasks', projectId, boardId]` is not merely an
      // entry nobody reads: `Analytics.tsx` DECLARES a query at exactly that key. Board's `boardId`
      // comes from `useParams()`, Analytics' from `boards[0].id` — identical on a single-board project
      // — so this patched ANOTHER SCREEN'S cache and, on error, restored a pre-assign snapshot into it,
      // while the board saw neither.
      await qc.cancelQueries({ queryKey: ['tasks', projectId] });
      return { snapshot: patchTaskCaches(qc, projectId, v.taskId, (t) => ({ ...t, assignee: v.assignee })) };
    },
    onError: (e, _v, ctx) => {
      restore(qc, ctx?.snapshot);
      setMoveErr(String(e instanceof Error ? e.message : e));
    },
    onSettled: () => { void qc.invalidateQueries({ queryKey: ['tasks', projectId, boardId] }); },
  });
  const onAssign = (taskId: string, version: number, assignee: string | null): void =>
    assignMut.mutate({ taskId, version, assignee });

  // optimistic status/priority (palette + s/p keys act on the selection)
  const setStatus = useTaskAction<Status>(
    projectId,
    (to, t) => ({ ...t, status: to }),
    (to, task) => tasksApi.moveTask(projectId, task.id, task.version, { toStatus: to }),
  );
  const setPriority = useTaskAction<Task['priority']>(
    projectId,
    (to, t) => ({ ...t, priority: to }),
    (to, task) => tasksApi.updateTask(projectId, task.id, task.version, { priority: to }),
  );

  // A cancelled subtask is DONE but not delivered: in the total, not in done (P-208).
  const childStats = useMemo(() => subtaskProgress(allTasks), [allTasks]);

  // The faceted filter (status/assignee/label/unblocked), shared by the board view AND the project-wide
  // search results (T-37) so "keyword + status:review + assignee:alice" narrows both consistently.
  const facetMatch = useCallback(
    (t: Task): boolean => {
      if (filters.status && t.status !== filters.status) return false;
      if (filters.assignee && t.assignee !== filters.assignee) return false;
      if (filters.label && !t.labels.includes(filters.label)) return false;
      if (filters.sprint && !(t.sprintIds ?? []).includes(filters.sprint)) return false;
      if (filters.unblocked && t.blockedBy.length > 0) return false;
      return true;
    },
    [filters],
  );
  const tasks = useMemo(() => allTasks.filter((t) => {
    if (!facetMatch(t)) return false;
    if (filters.q) {
      const q = filters.q.toLowerCase();
      if (!`${t.id} ${t.title} ${t.labels.join(' ')}`.toLowerCase().includes(q)) return false;
    }
    return true;
  }), [allTasks, filters, facetMatch]);

  // Project-wide search hits (T-37): the raw results, the facet-narrowed view, and a status histogram
  // for the drill-down chips. Counts are over the RAW hits so a chip shows how many a facet would keep.
  const searchRaw = useMemo(() => searchQ.data?.results ?? [], [searchQ.data]);
  const searchResults = useMemo(() => searchRaw.filter((r) => facetMatch(r.task)), [searchRaw, facetMatch]);
  const searchByStatus = useMemo(() => {
    const m = new Map<string, number>();
    for (const r of searchRaw) m.set(r.task.status, (m.get(r.task.status) ?? 0) + 1);
    return [...m.entries()].sort((a, b) => a[0].localeCompare(b[0]));
  }, [searchRaw]);

  // visible order, column-major — the path j/k walks
  const visibleOrder = useMemo(() => {
    const ids: string[] = [];
    for (const col of board?.columns ?? []) {
      for (const t of tasks) if (columnIdOf(board, t) === col.id) ids.push(t.id);
    }
    return ids;
  }, [board, tasks]);
  const selectedTask = allTasks.find((t) => t.id === selectedId) ?? null;

  const moveSelection = (delta: number): void => {
    if (visibleOrder.length === 0) return;
    const idx = selectedId ? visibleOrder.indexOf(selectedId) : -1;
    const next = visibleOrder[Math.min(visibleOrder.length - 1, Math.max(0, idx + delta))] ?? visibleOrder[0]!;
    setSelectedId(next);
    document.querySelector(`[data-task-id="${next}"]`)?.scrollIntoView({ block: 'nearest' });
    // Focus left on a card (a mouse drag ends with it on the dragged card's wrapper) follows the
    // selection: a focused card takes Enter for itself, so it would open that card, not this one.
    const focusedCard = document.activeElement?.closest('[data-card-for]');
    if (focusedCard && focusedCard.getAttribute('data-card-for') !== next) {
      document.querySelector<HTMLElement>(`[data-card-for="${next}"]`)?.focus({ preventScroll: true });
    }
  };

  // board-scope keys (inert while the drawer is open for j/k — the drawer registers its own)
  useShortcuts('board', [
    { keys: 'j', label: 'Next task', run: () => (openTask ? false : moveSelection(1)) },
    { keys: 'k', label: 'Previous task', run: () => (openTask ? false : moveSelection(-1)) },
    { keys: 'ArrowDown', label: '', run: () => (openTask ? false : moveSelection(1)) },
    { keys: 'ArrowUp', label: '', run: () => (openTask ? false : moveSelection(-1)) },
    { keys: 'Enter', label: 'Open selected task', run: () => (selectedId && !openTask ? setOpenTask(selectedId) : false) },
    ...(canCreate ? [{ keys: 'c', label: 'New task', run: () => { setCreating(true); setTimeout(() => newTaskRef.current?.focus(), 0); } }] : []),
    { keys: 's', label: 'Set status (selection)', run: () => (selectedId ? openPalette('status') : false) },
    { keys: 'p', label: 'Set priority (selection)', run: () => (selectedId ? openPalette('priority') : false) },
    { keys: 'Escape', label: '', run: () => (openTask ? false : selectedId ? setSelectedId(null) : false) },
  ], [selectedId, openTask, visibleOrder, canCreate]);

  // drawer-scope j/k = previous/next task while the drawer is open (the board owns the order)
  useShortcuts('drawer', [
    { keys: 'j', label: 'Next task', run: () => (openTask ? walkOpen(1) : false) },
    { keys: 'k', label: 'Previous task', run: () => (openTask ? walkOpen(-1) : false) },
  ], [openTask, visibleOrder]);
  const walkOpen = (delta: number): void => {
    if (!openTask || visibleOrder.length === 0) return;
    const idx = visibleOrder.indexOf(openTask);
    const next = visibleOrder[Math.min(visibleOrder.length - 1, Math.max(0, idx + delta))];
    if (next && next !== openTask) {
      setOpenTask(next);
      setSelectedId(next);
    }
  };

  // publish palette context: actions on the selection (or open task) + task search + open-task
  useEffect(() => {
    const target = (openTask ? allTasks.find((t) => t.id === openTask) : null) ?? selectedTask;
    const actions = target
      ? [
          { id: 'open', label: `Open ${target.id}`, kbd: '↵', run: () => setOpenTask(target.id) },
          // Below master both are closed (P-440): listed, so the reader learns they exist, but with no
          // sub-page to open and the reason beside them. `s` and `p` then open the palette on that reason.
          canMoveCards
            ? {
                id: 'status',
                label: 'Set status',
                kbd: 's',
                children: STATUSES.map((st) => ({ label: st.replace('_', ' '), run: () => setStatus.mutate({ vars: st, task: target }) })),
              }
            : { id: 'status', label: 'Set status', kbd: 's', closedWhy: statusWhy ?? 'Checking your role' },
          canAssign
            ? {
                id: 'priority',
                label: 'Set priority',
                kbd: 'p',
                children: PRIORITIES.map((pr) => ({ label: pr, run: () => setPriority.mutate({ vars: pr, task: target }) })),
              }
            : { id: 'priority', label: 'Set priority', kbd: 'p', closedWhy: priorityWhy ?? 'Checking your role' },
        ]
      : [];
    publishPalette({ projectId, boardId, actions, openTask: (id) => setOpenTask(id) });
    return () => clearPalette();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectId, boardId, selectedId, openTask, allTasks, canMoveCards, canAssign, statusWhy, priorityWhy]);

  // Is the board showing a NARROWED view? Drives the column badge (see the Column render): with a
  // filter on, a badge counting every task contradicts the column under it.
  const filtering =
    filters.q.trim().length > 0 || filters.status !== 'any' || filters.assignee !== 'any' || filters.label !== 'any' || filters.sprint !== '' || filters.unblocked;

  const counts: Record<string, number> = {};
  for (const t of allTasks) {
    const cid = columnIdOf(board, t);
    counts[cid] = (counts[cid] ?? 0) + 1;
  }
  const assignees = [...new Set(allTasks.map((t) => t.assignee).filter(Boolean) as string[])];
  const labels = [...new Set(allTasks.flatMap((t) => t.labels))];

  const [dragId, setDragId] = useState<string | null>(null);
  const dragTask = dragId ? allTasks.find((t) => t.id === dragId) : null;
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }));

  // WHERE THE COLUMNS START, measured (P-446). Their height fills the viewport below that point, so the
  // horizontal scroll track sits on the viewport floor. A fixed `calc(100vh - 140px)` assumed a one-row
  // header; the header now wraps its actions onto a second row when they do not fit, and the reasons
  // and messages above the columns vary too. Re-measured after every render, on window resize, and
  // when the header changes height without either (a web font arriving late, P-448); the state only
  // changes when the offset does, so this settles in one pass.
  const columnsRef = useRef<HTMLDivElement>(null);
  const [columnsTop, setColumnsTop] = useState<number | null>(null);
  const measureColumns = useCallback((): void => {
    const el = columnsRef.current;
    if (!el) return;
    const scroller = el.closest('.main');
    const top = Math.round(el.getBoundingClientRect().top + (scroller?.scrollTop ?? 0));
    setColumnsTop((prev) => (prev === top ? prev : top));
  }, []);
  useLayoutEffect(() => {
    measureColumns();
  });
  useEffect(() => {
    window.addEventListener('resize', measureColumns);
    // Everything this screen renders into the app's `.main` (its header first, then the filter bar,
    // the reasons and messages above the columns, and the body), so a height change in any of them
    // re-measures. A change below the columns' top leaves the offset as it is, so nothing loops.
    const observer = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(() => measureColumns()) : null;
    for (const child of Array.from(document.querySelector('.main')?.children ?? [])) observer?.observe(child);
    return () => {
      window.removeEventListener('resize', measureColumns);
      observer?.disconnect();
    };
  }, [measureColumns]);

  return (
    <>
      {/* Board header — identity + the two primary affordances (create, activity) */}
      <PageHeader
        title={board?.name ?? 'Board'}
        subtitle={tasksSubtitle}
        actions={
          <>
            {projectBoards.length > 1 && (
              <select
                className="proj-switch"
                aria-label="Switch board"
                value={boardId}
                onChange={(e) => nav(`/projects/${projectId}/boards/${e.target.value}`)}
                title="Switch board"
              >
                {projectBoards.map((b) => <option key={b.id} value={b.id}>{b.name}</option>)}
              </select>
            )}
            <AgentPresence agents={agentList} />
            {/* Each closed button and its reason are one group, so they wrap onto the next row together. */}
            <span className="topbar-action">
            <button
              className="btn secondary"
              disabled={!canNewBoard}
              onClick={() => { setNewBoardOpen((v) => !v); setTimeout(() => newBoardRef.current?.focus(), 0); }}
              aria-expanded={canNewBoard && newBoardOpen}
              title={canNewBoard ? 'Create a new board in this project' : newBoardWhy ?? 'Checking your role'}
              aria-describedby={newBoardWhy ? 'new-board-closed' : undefined}
            >
              + New board
            </button>
            {newBoardWhy && <span id="new-board-closed" className="muted" style={{ fontSize: 12 }}>{newBoardWhy}</span>}
            </span>
            <span className="topbar-action">
            <button
              ref={newTaskBtnRef}
              className="btn"
              disabled={!canCreate}
              title={createClosedWhy ?? (canCreate ? undefined : 'Checking your role')}
              aria-describedby={createClosedWhy ? 'new-task-closed' : undefined}
              onClick={() => { setCreating((v) => !v); setTimeout(() => newTaskRef.current?.focus(), 0); }}
              aria-expanded={canCreate && creating}
            >
              + New task
            </button>
            {createClosedWhy && <span id="new-task-closed" className="muted" style={{ fontSize: 12 }}>{createClosedWhy}</span>}
            </span>
            <button className="btn secondary" onClick={toggleSprintRail} title="Toggle the sprints rail">{sprintRailOpen ? 'Hide sprints' : 'Sprints'}</button>
            <button className="btn secondary" onClick={toggleRail} title="Toggle the activity feed">{railOpen ? 'Hide activity' : 'Activity'}</button>
          </>
        }
      />

      {/* New-board row — a project has many boards; create one here and jump into it. */}
      {newBoardOpen && canNewBoard && (
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', padding: '10px 12px', borderBottom: '1px solid var(--border)', background: 'var(--surface-low)' }}>
          <input
            ref={newBoardRef}
            className="input"
            style={{ flex: '1 1 260px' }}
            placeholder="New board name…"
            aria-label="New board name"
            value={newBoardName}
            onChange={(e) => setNewBoardName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && newBoardName && !createBoard.isPending) createBoard.mutate(newBoardName);
              if (e.key === 'Escape') setNewBoardOpen(false);
            }}
          />
          <button className="btn" disabled={!newBoardName || createBoard.isPending} onClick={() => createBoard.mutate(newBoardName)}>Create board</button>
          <button className="btn secondary" onClick={() => setNewBoardOpen(false)}>Cancel</button>
        </div>
      )}

      {/* Create row — only shown when adding, so it never clutters the bar (opens on the "c" shortcut too) */}
      {creating && canCreate && (
        <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', padding: '10px 12px', borderBottom: '1px solid var(--border)', background: 'var(--surface-low)' }}>
          <input
            ref={newTaskRef}
            className="input"
            style={{ flex: '1 1 260px' }}
            placeholder="Task title…"
            aria-label="Task title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !create.isPending) submitCreate();
              if (e.key === 'Escape') closeCreate();
            }}
          />
          <select className="input" value={newPriority} onChange={(e) => setNewPriority(e.target.value)} aria-label="priority">
            {PRIORITIES.map((p) => <option key={p} value={p}>{p}</option>)}
          </select>
          <input className="input" style={{ width: 170 }} placeholder="labels (comma-separated)" aria-label="Labels (comma-separated)" value={newLabels} onChange={(e) => setNewLabels(e.target.value)} />
          <button className="btn" disabled={!title || create.isPending} onClick={submitCreate}>Add task</button>
          <button className="btn secondary" onClick={closeCreate}>Done</button>
        </div>
      )}

      {/* Filter bar — all the "narrow the view" controls grouped together and clearly labelled */}
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', padding: '8px 12px', borderBottom: '1px solid var(--border)' }}>
        <span className="muted" style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 12 }}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M3 5h18l-7 8v6l-4 2v-8z" /></svg>
          Filter
        </span>
        <select className="input" value={filters.status} onChange={(e) => setFilters({ ...filters, status: e.target.value })} aria-label="Filter by status">
          <option value="">Status: any</option>
          {STATUSES.map((s) => <option key={s} value={s}>{s.replace('_', ' ')}</option>)}
        </select>
        <select className="input" value={filters.assignee} onChange={(e) => setFilters({ ...filters, assignee: e.target.value })} aria-label="Filter by assignee">
          <option value="">Assignee: any</option>
          {assignees.map((a) => <option key={a} value={a}>{a}</option>)}
        </select>
        <select className="input" value={filters.label} onChange={(e) => setFilters({ ...filters, label: e.target.value })} aria-label="Filter by label">
          <option value="">Label: any</option>
          {labels.map((l) => <option key={l} value={l}>{l}</option>)}
        </select>
        {sprints.length > 0 && (
          <select className="input" value={filters.sprint} onChange={(e) => setFilters({ ...filters, sprint: e.target.value })} aria-label="Filter by sprint">
            <option value="">Sprint: any</option>
            {sprints.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
          </select>
        )}
        <label className="muted" style={{ fontSize: 12, display: 'flex', alignItems: 'center', gap: 5, cursor: 'pointer' }}>
          <input type="checkbox" checked={filters.unblocked} onChange={(e) => setFilters({ ...filters, unblocked: e.target.checked })} />
          Unblocked only
        </label>
        <div style={{ flex: 1 }} />
        <div style={{ position: 'relative', display: 'inline-flex', alignItems: 'center' }}>
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" style={{ position: 'absolute', left: 9, pointerEvents: 'none' }}><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></svg>
          <input className="input" style={{ paddingLeft: 30, width: 200 }} placeholder="Search the project…" aria-label="Search tasks" value={filters.q} onChange={(e) => setFilters({ ...filters, q: e.target.value })} />
        </div>
        {(filters.status || filters.assignee || filters.label || filters.sprint || filters.unblocked || filters.q) && (
          <button className="btn secondary" onClick={() => setFilters({ status: '', assignee: '', label: '', sprint: '', unblocked: false, q: '' })}>Clear</button>
        )}
        {tasksQ.data?.nextCursor && (
          <button className="btn secondary" disabled={loadingMore} onClick={() => void loadMore()}>{loadingMore ? 'Loading…' : 'Load more'}</button>
        )}
      </div>

      {/* The task list's read failed: said, with a way to try again, rather than drawn as an empty
          board. With rows from an earlier read still on screen, they stay and the line says so. */}
      {tasksQ.isError && !tasksQ.isFetching && (
        <p className="error" role="alert" style={{ fontSize: 12, margin: '6px 12px 0' }}>
          {tasksLoaded ? 'Couldn’t refresh the tasks; showing the last ones read' : 'Couldn’t load the tasks'}
          {tasksQ.error instanceof Error && tasksQ.error.message ? `: ${tasksQ.error.message}` : ''}.{' '}
          <button type="button" className="btn secondary" style={{ fontSize: 11, padding: '1px 7px' }} onClick={() => void tasksQ.refetch()}>Retry</button>
        </p>
      )}
      {moreErr && (
        <p className="error" role="alert" style={{ fontSize: 12, margin: '6px 12px 0' }}>
          Couldn’t load more tasks: {moreErr}.{' '}
          <button type="button" className="btn secondary" style={{ fontSize: 11, padding: '1px 7px' }} onClick={() => setMoreErr(null)}>Dismiss</button>
        </p>
      )}

      {/* A refusal because the caller is not a member says so, as the closed controls above do (P-445).
          The server answers FORBIDDEN "Not a member of project …"; the roster may say it first. */}
      {boardQ.isError && (
        <p className="error" style={{ fontSize: 12, margin: '0 12px' }}>
          {myRole === NOT_A_MEMBER || (boardQ.error instanceof ApiError && boardQ.error.status === 403 && /^Not a member\b/.test(boardQ.error.message))
            ? 'You are not a member of this project, so its board cannot be shown. A project admin can add you.'
            : 'Couldn’t load this board.'}
        </p>
      )}
      {moveErr && <p className="error" style={{ fontSize: 12, margin: '0 12px' }}>{moveErr}</p>}
      {/* One reason for every card's closed assign chip and drag (P-440), referenced by each chip. */}
      {cardsWhy && <p id="cards-closed" className="muted" style={{ fontSize: 12, margin: '6px 12px 0' }}>{cardsWhy}</p>}
      {sprintMsg && (
        <p className="error" role="status" style={{ fontSize: 12, margin: '0 12px' }} onClick={() => setSprintMsg(null)}>{sprintMsg}</p>
      )}
      {filters.q.trim().length > 1 && searchRaw.length > 0 && (
        <div className="section" style={{ margin: '0 12px' }}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 4 }}>
            <span className="muted" style={{ fontSize: 12 }}>
              Search results (whole project): {searchResults.length}
              {searchResults.length !== searchRaw.length ? ` of ${searchRaw.length}` : ''}
            </span>
            {/* status-facet drill-down: each chip narrows to that status (toggle off to clear) */}
            {searchByStatus.map(([st, n]) => (
              <button
                key={st}
                className="btn secondary"
                style={{ fontSize: 11, padding: '1px 7px', ...(filters.status === st ? { borderColor: 'var(--accent)', color: 'var(--accent-tint)' } : {}) }}
                onClick={() => setFilters({ ...filters, status: filters.status === st ? '' : st })}
                aria-pressed={filters.status === st}
              >
                {st.replace('_', ' ')} {n}
              </button>
            ))}
          </div>
          {searchResults.map((r) => {
            const onThisBoard = r.task.boardId === boardId;
            const colName = onThisBoard ? board?.columns.find((c) => c.id === columnIdOf(board, r.task))?.name : undefined;
            return (
              <div key={r.task.id} className="row" style={{ display: 'flex', gap: 8, alignItems: 'center', fontSize: 14, cursor: 'pointer', padding: '4px 0' }}
                onClick={() => setOpenTask(r.task.id)}>
                <span className="id mono">{r.task.id}</span>
                <span>{r.task.title}</span>
                <StatusPill status={r.task.status} />
                {colName ? (
                  <span className="muted" style={{ fontSize: 11 }}>in {colName}</span>
                ) : !onThisBoard ? (
                  <span className="muted" style={{ fontSize: 11 }}>other board</span>
                ) : null}
                <span style={{ flex: 1 }} />
                {r.task.assignee && <span className="muted" style={{ fontSize: 11 }}>@{r.task.assignee}</span>}
                <span className="muted" style={{ fontSize: 11 }}>score {r.score.toFixed(2)}</span>
              </div>
            );
          })}
          {searchResults.length === 0 && (
            <span className="muted" style={{ fontSize: 12 }}>No results match the active filters.</span>
          )}
        </div>
      )}

      <div className="page" style={{ display: 'flex', gap: 12 }}>
        {boardQ.isLoading && !board ? (
          <BoardSkeleton />
        ) : (
          <DndContext
            sensors={sensors}
            collisionDetection={closestCorners}
            onDragStart={(e) => setDragId(String(e.active.id))}
            onDragEnd={(e) => {
              setDragId(null);
              if (!canMoveCards) return;
              const activeId = String(e.active.id);
              const overId = e.over?.id ? String(e.over.id) : null;
              if (!board || !overId || overId === activeId) return;
              const active = allTasks.find((x) => x.id === activeId);
              if (!active) return;
              const overTask = allTasks.find((x) => x.id === overId);
              const targetColId = overTask ? columnIdOf(board, overTask) : overId;
              if (!board.columns.some((c) => c.id === targetColId)) return;
              // Compute the drop slot against the FULL ordered column (so hidden/filtered cards still
              // count), then derive a fractional-index `order` strictly between the new neighbours.
              const byOrder = (a: Task, b: Task): number => (a.order < b.order ? -1 : a.order > b.order ? 1 : 0);
              const orderOf = (id: string): string | null => allTasks.find((t) => t.id === id)?.order ?? null;
              const colFull = allTasks.filter((t) => columnIdOf(board, t) === targetColId).sort(byOrder);
              let prev: string | null = null;
              let next: string | null = null;
              if (columnIdOf(board, active) === targetColId) {
                const ids = colFull.map((t) => t.id);
                const from = ids.indexOf(activeId);
                const to = overTask ? ids.indexOf(overId) : ids.length - 1;
                if (from === -1 || to === -1 || from === to) return;
                const reordered = arrayMove(ids, from, to);
                const pos = reordered.indexOf(activeId);
                prev = pos > 0 ? orderOf(reordered[pos - 1]!) : null;
                next = pos < reordered.length - 1 ? orderOf(reordered[pos + 1]!) : null;
              } else {
                const others = colFull.filter((t) => t.id !== activeId);
                const at = overTask ? Math.max(0, others.findIndex((t) => t.id === overId)) : others.length;
                prev = at > 0 ? others[at - 1]!.order : null;
                next = at < others.length ? others[at]!.order : null;
              }
              let order: string;
              try {
                order = generateKeyBetween(prev, next);
              } catch {
                return;
              }
              // Ripple immediately (the drop happened), spinner until the server agrees.
              setRippleId(active.id);
              setMovingIds((prev) => new Set(prev).add(active.id));
              // No per-call callbacks: the veil is cleared by the mutation's own `onSettled`, which
              // fires even when this card unmounts on its way to another column.
              moveCard.mutate({ taskId: active.id, version: active.version, toColumnId: targetColId, order });
            }}
            onDragCancel={() => setDragId(null)}
          >
            {/* minWidth:0 lets this flex item shrink and scroll internally (columns are flex-basis
                300px, shrink 0 in CSS) — the rail never crushes columns, the board pans instead. */}
            <div
              ref={columnsRef}
              className="columns"
              style={{ flex: 1, minWidth: 0, ...(columnsTop !== null ? ({ '--columns-top': `${columnsTop}px` } as React.CSSProperties) : {}) }}
            >
              {board?.columns.map((col) => {
                const colTasks = tasks
                  .filter((t) => columnIdOf(board, t) === col.id)
                  .sort((a, b) => (a.order < b.order ? -1 : a.order > b.order ? 1 : 0));
                // THE BADGE COUNTS WHAT IS VISIBLE; THE WIP LIMIT COUNTS WHAT EXISTS.
                // These were the same number, taken from the unfiltered `allTasks`, so with a filter on
                // the header said "1" above a column reading "No tasks — press c". Reported from a
                // search for "wake": Blocked and In Progress both showed 1 and both were empty.
                // The WIP check must NOT follow the filter — a limit is a property of the board, not of
                // your current view, and hiding an overflow because you filtered it out would turn a
                // guard-rail into a decoration.
                const trueCount = counts[col.id] ?? 0;
                const count = filtering ? colTasks.length : trueCount;
                const over = col.wipLimit != null && trueCount > col.wipLimit;
                return (
                  <Column key={col.id} col={col} count={count} over={over}>
                    <SortableContext items={colTasks.map((t) => t.id)} strategy={verticalListSortingStrategy}>
                      <AnimatePresence initial={false}>
                        {colTasks.map((t) => (
                          <DraggableCard
                            key={t.id}
                            t={t}
                            col={col}
                            sub={childStats.get(t.id)}
                            sprintName={sprintNameOf(t)}
                            selected={t.id === selectedId}
                            changed={changedTasks.has(t.id)}
                            pending={movingIds.has(t.id)}
                            ripple={rippleId === t.id}
                            onOpen={() => {
                              setSelectedId(t.id);
                              setOpenTask(t.id);
                            }}
                            agents={agentList}
                            onAssign={onAssign}
                            canAssign={canAssign}
                            assignClosedWhy={assignClosedWhy}
                            canMove={canMoveCards}
                          />
                        ))}
                      </AnimatePresence>
                      {colTasks.length === 0 && tasksLoaded && (
                        <div className="muted" style={{ fontSize: 12, padding: '8px 4px' }}>
                          {/* A created task always lands in the board's first TODO column (create_task,
                              ADR-0005; todoColumn in internal/service/dependencies.go), so only it offers c. */}
                          {canCreate && col.id === board.columns.find((c) => c.status === 'TODO')?.id ? (
                            <>No tasks — press <span className="kbd">c</span></>
                          ) : (
                            'No tasks'
                          )}
                        </div>
                      )}
                    </SortableContext>
                  </Column>
                );
              })}
            </div>
            {/*
              * NO DROP ANIMATION — the card would otherwise visibly REBOUND to its old column.
              *
              * dnd-kit's default drop animation tweens the overlay back to the ACTIVE node's last
              * measured rect. That is right when a drop is a no-op, and wrong here: the move is
              * OPTIMISTIC, so by the time the overlay animates the card has already been re-rendered in
              * its destination column and the remembered rect points at where it used to be. The result
              * is the dropped card flying home while a copy of it appears in the target — reported as
              * "card rebound animation which takes dropped card back to original column but card is
              * added in target column anyway".
              *
              * `null` hands the transition to the card's own enter animation in the destination column
              * (`.card` keyframe, --t-3 "travel"), which is the motion the board already speaks.
              */}
            <DragOverlay className="drag-overlay" dropAnimation={null}>
              {dragTask && <TaskCard t={dragTask} col={{ status: dragTask.status }} sprintName={sprintNameOf(dragTask)} selected={false} onOpen={() => {}} agents={agentList} onAssign={onAssign} canAssign={canAssign} assignClosedWhy={assignClosedWhy} />}
            </DragOverlay>
          </DndContext>
        )}

        {sprintRailOpen && (
          <SprintsRail
            sprints={sprints}
            tasks={allTasks}
            filterSprint={filters.sprint}
            onFilter={(sprintId) => setFilters({ ...filters, sprint: sprintId })}
            onOpenTask={(id) => { setOpenTask(id); setSelectedId(id); }}
            onLifecycle={canRunSprints ? (s, status) => sprintLifecycle.mutate({ sprint: s, status }) : undefined}
            lifecycleClosedWhy={sprintsWhy}
            busySprintId={sprintLifecycle.isPending ? sprintLifecycle.variables?.sprint.id : undefined}
            loading={sprintsQ.isLoading}
            error={sprintsQ.isError}
          />
        )}

        {railOpen && (
          <div className="column" style={{ width: 280, flex: '0 0 280px' }}>
            <header><span>Activity</span></header>
            <div className="cards">
              {feedQ.isLoading && [0, 1, 2].map((i) => <div key={i} className="skeleton" style={{ height: 34 }} />)}
              {/* WHO GETS A LINE OF ITS OWN. The row was one nowrap line (avatar, name, verb, task id,
                  time) in which only the name could shrink, so beside a long verb ("updated an
                  attachment on") it was cut to a few letters and claude-code read as claude-codex.
                  The name now shares its line with the time only; the action goes underneath. */}
              {(feedQ.data?.events ?? []).map((e: ActivityEvent) => {
                const actor = eventActor(e, displayNames);
                return (
                  <div key={e.id} className="row" style={{ display: 'flex', gap: 7, alignItems: 'flex-start', fontSize: 12, minWidth: 0 }}>
                    <Avatar id={actor} />
                    <div style={{ flex: 1, minWidth: 0 }}>
                      <div style={{ display: 'flex', gap: 7, alignItems: 'center', minWidth: 0 }}>
                        <Marquee label={actor} style={{ flex: 1 }}>{actor.split('@')[0]}</Marquee>
                        <span className="muted" style={{ fontSize: 11, whiteSpace: 'nowrap' }} title={new Date(e.ts).toLocaleString()}>{timeAgo(e.ts)}</span>
                      </div>
                      <div className="muted" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {eventVerb(e.type)}
                        {e.taskId && <> <span className="mono" style={{ color: 'var(--text)' }}>{e.taskId}</span></>}
                      </div>
                    </div>
                  </div>
                );
              })}
              {feedQ.data && feedQ.data.events.length === 0 && <div className="muted" style={{ fontSize: 12 }}>No activity yet.</div>}
            </div>
          </div>
        )}
      </div>
      {openTask && (
        <TaskDrawer
          // key remounts the drawer per task: in-drawer navigation (dep chips, subtasks, breadcrumb,
          // j/k walk) must NOT carry edit drafts, composer text, or a stale leaseId from the previous
          // task — a leaked draft would pass the version check and silently overwrite the new task.
          key={openTask}
          projectId={projectId}
          taskId={openTask}
          onClose={() => setOpenTask(null)}
          onOpenTask={(id) => { setOpenTask(id); setSelectedId(id); }}
        />
      )}
    </>
  );
}
