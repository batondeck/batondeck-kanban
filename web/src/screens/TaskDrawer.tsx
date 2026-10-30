/**
 * The ticket drawer — quick triage without leaving the board.
 *
 * It owns the overlay, the header and the section nav; every panel below the header is the SHARED
 * component from `./task/panels.tsx`, rendered off the SHARED `useTaskDetail` hook — the same two
 * pieces `TaskPage.tsx` renders. Keeping one copy is the point: a drawer with its own private copy of
 * (say) the dependency panel drifts from the page within a sprint. The full record lives on the page;
 * the ⤢ button in the header goes there.
 */
import { useEffect, useRef } from 'react';
import { motion } from 'framer-motion';
import { pushToast } from '../toast.js';
import { Avatar, Marquee, PoisonBadge, PriorityDot, StatusPill, isPoisoned, personName, timeAgo } from '../components.js';
import { LIMITS } from '../types.js';
import { Cluster } from '../ui.js';
import { Icon } from '../icons.js';
import { Runs } from './Runs.js';
import { useTaskDetail, type LoadedTaskDetail } from './task/useTaskDetail.js';
import {
  ActivityPanel, ContextPanel, DeliveredPanel, DependenciesPanel, DescriptionPanel,
  DigestPanel, DiscussionPanel, FilesPanel, IncidentPanel, LinksPanel, MemoryPanel,
  META_CF,
  SubtasksPanel,
} from './task/panels.js';
import { TicketRail } from './task/TicketRail.js';
import { SECTIONS, useSectionNav } from './task/TicketBody.js';

export function TaskDrawer({ projectId, taskId, onClose, onOpenTask }: {
  projectId: string;
  taskId: string;
  onClose: () => void;
  /** Navigate the drawer to another task (dep chips, subtasks, parent breadcrumb). Optional. */
  onOpenTask?: (taskId: string) => void;
}) {
  const d = useTaskDetail(projectId, taskId);
  const { task, taskQ, err, editing, draft, setDraft, startEdit, setEditing, taskById, canRequeue, move } = d;

  // Section-nav anchors (Details / Discussion / Context / Files / Activity).
  const { refs, scrollTo } = useSectionNav();

  // a11y: focus the dialog on open and close it on Escape (focus returns to the page underneath).
  // While typing in a field, Escape only blurs that field — it must NOT nuke the drawer (and the
  // comment/follow-up draft with it); a second Escape then closes. onClose is read through a ref so
  // Board's inline closure (new identity every render — 10s presence poll, SSE invalidations) doesn't
  // re-run this effect and yank focus out of whatever field the user is typing in.
  const dialogRef = useRef<HTMLDivElement>(null);
  const onCloseRef = useRef(onClose);
  useEffect(() => { onCloseRef.current = onClose; }, [onClose]);
  // Focus once the dialog actually exists: on a cold open the first render is the skeleton (no
  // dialogRef), so a mount-only effect would strand focus in the background page behind aria-modal.
  const loaded = !!task;
  useEffect(() => {
    if (loaded) dialogRef.current?.focus();
  }, [loaded, taskId]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent): void => {
      if (e.key !== 'Escape') return;
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT' || t.isContentEditable)) {
        t.blur();
        return;
      }
      onCloseRef.current();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [taskId]);

  // Focus the error dialog too — it renders via the early return below, where the happy-path focus
  // effect's dialogRef doesn't exist, and an unfocused aria-modal dialog strands keyboard/SR users.
  const errDialogRef = useRef<HTMLDivElement>(null);
  const taskLoadFailed = taskQ.isError && !taskQ.data;
  useEffect(() => {
    if (taskLoadFailed) errDialogRef.current?.focus();
  }, [taskLoadFailed]);

  if (!task) {
    // A bad deep link (?task=T-999) or a load failure must not strand the user on an infinite skeleton.
    if (taskQ.isError) {
      return (
        <div className="drawer-overlay" onClick={onClose}>
          <div className="drawer" role="dialog" aria-modal="true" aria-label="Task not found" tabIndex={-1} ref={errDialogRef} onClick={(e) => e.stopPropagation()} style={{ padding: 18, display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Cluster justify='flex-end'>
              <button className="btn secondary icon" onClick={onClose} aria-label="Close"><Icon name="close" /></button>
            </Cluster>
            <h2 style={{ margin: 0 }}>Couldn’t open {taskId}</h2>
            <p className="muted" style={{ margin: 0, fontSize: 14, lineHeight: 1.5 }}>
              {taskQ.error instanceof Error ? taskQ.error.message : 'This task may have been deleted, or the link points at a different project.'}
            </p>
            <Cluster gap={8}>
              <button className="btn secondary" onClick={() => void taskQ.refetch()}>Retry</button>
              <button className="btn" onClick={onClose}>Back to board</button>
            </Cluster>
          </div>
        </div>
      );
    }
    return (
      <div className="drawer-overlay" onClick={onClose}>
        <div className="drawer" onClick={(e) => e.stopPropagation()} style={{ padding: 18 }}>
          <div className="skeleton" style={{ width: 90, height: 12, marginBottom: 14 }} />
          <div className="skeleton" style={{ width: '75%', height: 22, marginBottom: 12 }} />
          <div className="skeleton" style={{ width: 180, height: 22, marginBottom: 22 }} />
          <div className="skeleton" style={{ width: '100%', height: 64, marginBottom: 12 }} />
          <div className="skeleton" style={{ width: '88%', height: 64 }} />
        </div>
      </div>
    );
  }
  // The ticket has loaded; the panels take the narrowed shape (see LoadedTaskDetail).
  const dl = d as LoadedTaskDetail;
  // Plain anchor, not a react-router <Link>: the drawer is rendered from the board overlay and from
  // tests that mount it outside a Router, and a full-page ticket link must work in both.

  return (
    <div className="drawer-overlay" onClick={onClose}>
      <motion.div
        className="drawer" role="dialog" aria-modal="true" aria-label={`Task ${task.id}`} tabIndex={-1} ref={dialogRef}
        onClick={(e) => e.stopPropagation()} style={{ padding: 0, display: 'flex', flexDirection: 'column' }}
        initial={{ x: 64, opacity: 0 }} animate={{ x: 0, opacity: 1 }}
        transition={{ type: 'spring', stiffness: 480, damping: 42 }}
      >
        {/* Header */}
        <header style={{ padding: 16, borderBottom: '1px solid var(--border)', display: 'flex', flexDirection: 'column', gap: 8 }}>
          {/* Parent breadcrumb — this is a subtask (or run child); jump up to its parent. */}
          {(task.parentTaskId || task.runOf) && onOpenTask && (
            <button
              type="button"
              onClick={() => onOpenTask((task.parentTaskId ?? task.runOf)!)}
              style={{ alignSelf: 'flex-start', display: 'inline-flex', alignItems: 'center', gap: 6, padding: 0, background: 'none', border: 'none', cursor: 'pointer', fontSize: 12, color: 'var(--text-muted)' }}
            >
              <Icon name="up" />
              <span>{task.runOf ? 'Run of' : 'Sub-task of'}</span>
              <span className="mono" style={{ color: 'var(--accent-tint)' }}>{task.parentTaskId ?? task.runOf}</span>
              {(() => {
                const parent = taskById.get((task.parentTaskId ?? task.runOf)!);
                return parent ? <Marquee style={{ color: 'var(--text-2)', maxWidth: 240 }}>— {parent.title}</Marquee> : null;
              })()}
            </button>
          )}
          <Cluster justify='space-between'>
            <span className="mono" style={{ fontSize: 12, color: 'var(--text-2)' }}>{task.id} · v{task.version}</span>
            <Cluster gap={4}>
              {/* Escape hatch to the full record. The drawer is for quick triage; the page carries
                  the panels that do not fit a 520px rail. */}
              <a className="btn secondary icon"
                 href={`/projects/${projectId}/boards/${task.boardId}/tasks/${task.id}`}
                 title="Open the full ticket page" aria-label="Open full page"><Icon name="expand" /></a>
              <button
                className="btn secondary icon"
                title="Copy link"
                aria-label="Copy link"
                onClick={() => {
                  const url = new URL(window.location.href);
                  url.searchParams.set('task', task.id);
                  void navigator.clipboard.writeText(url.toString()).then(() => pushToast(`Link to ${task.id} copied`, 'info'));
                }}
              >
                <Icon name="link" />
              </button>
              <button className="btn secondary" onClick={() => (editing ? setEditing(false) : startEdit())}>{editing ? 'Cancel' : 'Edit'}</button>
              <button className="btn secondary icon" onClick={onClose} aria-label="Close"><Icon name="close" /></button>
            </Cluster>
          </Cluster>

          {editing ? (
            <input className="input" maxLength={LIMITS.title} style={{ width: '100%', fontSize: 20, fontWeight: 600 }} value={draft.title} onChange={(e) => setDraft({ ...draft, title: e.target.value })} />
          ) : (
            <h1 style={{ margin: 0, fontSize: 20, fontWeight: 600, letterSpacing: '-0.02em', lineHeight: 1.2 }}>{task.title}</h1>
          )}

          <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 2, flexWrap: 'wrap' }}>
            {/* The header shows the status; CHANGING it is `TicketActions`, below, which both surfaces
                share. Two controls with the accessible name "Status" in one dialog would also make
                `getByLabelText('Status')` ambiguous — a real a11y defect, not just a test problem. */}
            <StatusPill status={task.status} />
            {isPoisoned(task) && (
              <>
                <PoisonBadge task={task} />
                {/* Un-poison is master-only (plan §Fold 1): a worker sees the control, disabled, so the
                    closed door reads as a rule rather than a missing feature. */}
                <button
                  type="button"
                  className="btn secondary"
                  disabled={!canRequeue || move.isPending}
                  title={canRequeue ? 'Move back to To do: clears the attempts and the poisoned label' : 'Only a master can requeue a poisoned task'}
                  onClick={() => move.mutate({ vars: 'TODO', task })}
                >
                  Requeue to To do
                </button>
              </>
            )}
            {task.racing && (
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, padding: '2px 8px', borderRadius: 'var(--r-1)', color: 'var(--progress)', background: 'color-mix(in srgb, var(--progress) 15%, transparent)', border: '1px solid color-mix(in srgb, var(--progress) 25%, transparent)' }}>
                <span className="dot" style={{ width: 6, height: 6, background: 'var(--progress)' }} />
                <span style={{ fontSize: 12, fontWeight: 500 }}>Racing</span>
              </span>
            )}
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, padding: '2px 8px', borderRadius: 'var(--r-1)', border: '1px solid var(--border)' }}>
              <PriorityDot priority={task.priority} />
              <span style={{ fontSize: 12, color: 'var(--text-2)', textTransform: 'capitalize' }}>{task.priority}</span>
            </span>
            {(() => {
              // Due chip — red when overdue on a task that's still live. Overdue compares UTC calendar
              // days (the Fields date input stores midnight UTC), so a task due TODAY isn't already red,
              // and the displayed date matches the Fields input rather than shifting by timezone.
              if (!task.dueAt) return null;
              const due = new Date(task.dueAt);
              if (Number.isNaN(due.getTime())) return null; // core doesn't validate dueAt format
              const dueDay = due.toISOString().slice(0, 10);
              const today = new Date().toISOString().slice(0, 10);
              const overdue = dueDay < today && task.status !== 'DONE';
              const c = overdue ? 'var(--blocked)' : 'var(--text-2)';
              return (
                <span title={due.toLocaleString()} style={{ display: 'inline-flex', alignItems: 'center', gap: 5, padding: '2px 8px', borderRadius: 'var(--r-1)', border: `1px solid ${overdue ? 'var(--blocked)' : 'var(--border)'}`, fontSize: 12, color: c }}>
                  <Icon name="clock" size={12} />
                  {overdue ? 'Overdue · ' : 'Due '}
                  {due.toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' })}
                </span>
              );
            })()}
            {task.estimate != null && (
              <span className="mono" title="estimate" style={{ padding: '2px 8px', borderRadius: 'var(--r-1)', border: '1px solid var(--border)', fontSize: 12, color: 'var(--text-2)' }}>est {task.estimate}</span>
            )}
            {task.modelHint && (
              <span className="mono" title={task.modelHint.rationale || 'model hint'} style={{ padding: '2px 8px', borderRadius: 'var(--r-1)', border: '1px solid var(--border)', fontSize: 12, color: 'var(--text-2)' }}>
                <Icon name="sliders" size={12} /> {task.modelHint.model}{task.modelHint.effort ? ` · ${task.modelHint.effort}` : ''}
              </span>
            )}
            {META_CF.filter((k) => task.customFields[k] !== undefined).map((k) => (
              <span key={`cf-${k}`} className="mono" title={`${k} (custom field)`} style={{ padding: '2px 8px', borderRadius: 'var(--r-1)', border: '1px solid var(--border)', fontSize: 12, color: 'var(--text-2)' }}>
                <Icon name="sliders" size={12} /> {k} {String(task.customFields[k])}
              </span>
            ))}
            {task.assignee && (
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, padding: '2px 8px', borderRadius: 'var(--r-1)', border: '1px solid var(--border)', marginLeft: 'auto' }}>
                <Avatar id={task.assignee} active={!!task.claim} />
                <span style={{ fontSize: 12, color: 'var(--text-2)' }}>{task.assignee}</span>
              </span>
            )}
          </div>

          {/* Labels + provenance — everything you'd otherwise scroll to Properties for. */}
          {(task.labels.length > 0 || task.requiredCapabilities.length > 0) && (
            <Cluster gap={4} wrap>
              {task.labels.map((l) => <span className="label" key={l}>{l}</span>)}
              {task.requiredCapabilities.map((c) => (
                <span className="label" key={`cap-${c}`} title="required capability" style={{ color: 'var(--accent-tint)', borderColor: 'color-mix(in srgb, var(--accent) 40%, transparent)' }}><Icon name="sliders" size={12} /> {c}</span>
              ))}
            </Cluster>
          )}
          <div className="mono" style={{ fontSize: 11, color: 'var(--text-muted)', display: 'flex', alignItems: 'center', gap: 5, flexWrap: 'wrap' }}>
            {task.createdByAgent ? (
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5 }}>
                Created by <Avatar id={task.createdByAgent} /> {task.createdByAgent}
              </span>
            ) : (
              // Bare emails read noisy — show the local part; the tooltip keeps the full name. An account
              // id is named, never printed raw (P-314).
              <span title={personName(task.createdBy, d.names, { full: true })}>Created by {personName(task.createdBy, d.names)}</span>
            )}
            <span>· updated {timeAgo(task.updatedAt)}</span>
          </div>
        </header>

        {/* Sticky section nav — the drawer is one long scroll; jump straight to a group. Lives
            outside the scroll body so it never scrolls away. */}
        <nav aria-label="Sections" style={{ display: 'flex', gap: 6, flexWrap: 'wrap', padding: '8px 16px', borderBottom: '1px solid var(--border)' }}>
          {SECTIONS.map(({ id, label }) => (
            <button
              key={id}
              type="button"
              className="chip"
              style={{ cursor: 'pointer', fontFamily: 'inherit' }}
              onClick={() => scrollTo(id)}
            >
              {label}
            </button>
          ))}
        </nav>

        {/* Scrollable body — panels only, all shared with the full page. */}
        <div style={{ flex: 1, overflowY: 'auto', padding: 16 }}>
          {err && <p className="error" style={{ fontSize: 12, margin: '0 0 12px' }}>{err}</p>}

          {/* INSIDE the scroll body, not pinned above it beside the nav: an expanded Details fold on
              a short viewport would otherwise push the content out of reach with no way to scroll to
              it. Same component the page mounts in its 420px rail. */}
          <TicketRail d={dl} compactDetails />

          <div ref={refs.details}>
            <DescriptionPanel d={dl} />
            <IncidentPanel d={dl} />
            <DigestPanel d={dl} />
          </div>

          <div ref={refs.context}>
            <ContextPanel d={dl} />
            <LinksPanel d={dl} />
            <MemoryPanel d={dl} />
          </div>

          <div ref={refs.work}>
            <DependenciesPanel d={dl} onOpenTask={onOpenTask} />
            <SubtasksPanel d={dl} onOpenTask={onOpenTask} />
            {/* Runs — race section (renders only when this task is/was raced). Drawer-only: it
                always renders a section and fires its own `list_runs` read, so promoting it to the
                page is scope this commit does not take. */}
            <Runs projectId={projectId} task={task} />
          </div>

          <div ref={refs.files}>
            <DeliveredPanel d={dl} />
            <FilesPanel d={dl} />
          </div>

          <div ref={refs.discussion}><DiscussionPanel d={dl} /></div>

          <div ref={refs.activity}><ActivityPanel d={dl} /></div>
        </div>
      </motion.div>
    </div>
  );
}
