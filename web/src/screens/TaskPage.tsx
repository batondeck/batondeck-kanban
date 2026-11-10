/**
 * The full ticket page — the RECORD.
 *
 * The drawer over the board is triage: it is 520px wide and it disappears when you click away. This
 * is the addressable, linkable, printable version of the same ticket, with a reference rail beside
 * it (properties + the PRD panels). Both surfaces render the SAME panels off the
 * SAME hook (`./task/*`), so neither can drift from the other.
 */
import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from 'react-router';
import * as boardsApi from '../api/boards.js';
import { Icon } from '../icons.js';
import { copyLink } from '../clipboard.js';
import { PageHeader, personName, timeAgo } from '../components.js';
import { RoleBadge, SideWide, Tabs, roleOf } from '../ui.js';
import {
  ActivityPanel, ContextPanel, DeliveredPanel, DependenciesPanel, DescriptionPanel,
  DigestPanel, DiscussionPanel, FilesPanel, IncidentPanel, LinksPanel, MemoryPanel,
  SubtasksPanel,
} from './task/panels.js';
import { TicketRail } from './task/TicketRail.js';
import { SECTIONS, useSectionNav, type SectionId } from './task/TicketBody.js';
import { useTaskDetail, type LoadedTaskDetail } from './task/useTaskDetail.js';
import '../styles/task.css';



/**
 * Publishes the sticky topbar's measured height as `--topbar-h` so `.task-tabs` can park directly
 * beneath it (see `styles/task.css`).
 *
 * WHY MEASURED RATHER THAN A CONSTANT. `.topbar` holds an id, a title that wraps at narrow widths,
 * and a row of buttons — its height is content-dependent, and any px literal here is wrong the first
 * time a long ticket title takes a second line. The tabs then either overlap the header or float
 * below it with a gap of scrolled content showing through.
 *
 * Written to `.main` (the scroll container) rather than `:root` so the variable dies with the screen
 * instead of leaking a stale offset onto every other page.
 *
 * GUARDED FOR JSDOM: `ResizeObserver` is undefined there, and an unguarded `new ResizeObserver` throws
 * during render and takes the whole screen down to an ErrorBoundary. The CSS fallback covers that case.
 */
function useTopbarOffset(): void {
  useEffect(() => {
    const bar = document.querySelector<HTMLElement>('.main .topbar');
    const host = document.querySelector<HTMLElement>('.main');
    if (!bar || !host) return;
    const apply = (): void => host.style.setProperty('--topbar-h', `${Math.round(bar.getBoundingClientRect().height)}px`);
    apply();
    if (typeof ResizeObserver === 'undefined') return () => host.style.removeProperty('--topbar-h');
    const ro = new ResizeObserver(apply);
    ro.observe(bar);
    return () => {
      ro.disconnect();
      host.style.removeProperty('--topbar-h');
    };
  }, []);
}

/**
 * The ticket's ADDRESS — board and number — as the header's eyebrow line.
 *
 * Smaller than the title on purpose. The id is how you refer to a ticket to someone else, so it has
 * to be present and copyable; it is not what the ticket is ABOUT, so it must not outweigh the title.
 *
 * The board name comes from the SAME query key the other screens use (`['boards', projectId]`), so a
 * warm cache costs nothing and a cold one degrades to the word "Board" rather than blocking the
 * header on a fetch. Never `boardId` as the label — an opaque `B-7a3f01cf9dbf` is not a place.
 */
function TicketRef({ projectId, boardId, taskId }: { projectId: string; boardId: string; taskId: string }) {
  const boardsQ = useQuery({
    queryKey: ['boards', projectId],
    queryFn: () => boardsApi.listBoards(projectId),
    enabled: !!projectId,
    staleTime: 300_000,
  });
  const boardName = boardsQ.data?.boards?.find((b) => b.id === boardId)?.name;
  return (
    <span className="ticket-ref">
      <Link to={`/projects/${projectId}/boards/${boardId}`}>{boardName ?? 'Board'}</Link>
      <span aria-hidden="true"> / </span>
      <span className="mono">{taskId}</span>
      <button
        className="ticket-ref-copy"
        title="Copy link to this ticket"
        aria-label="Copy link to this ticket"
        onClick={() => copyLink(window.location.href, `Link to ${taskId} copied`)}
      >
        <Icon name="link" size={12} />
      </button>
    </span>
  );
}

export function TaskPage() {
  useTopbarOffset();
  const { projectId = '', boardId = '', taskId = '' } = useParams();
  const navigate = useNavigate();
  const d = useTaskDetail(projectId, taskId);
  const { task, taskQ, err, editing, setEditing, startEdit, canEdit, why } = d;
  // Edit is `update_task`, tier master (P-440): below it the button is closed and says why.
  const editWhy = why('update_task', 'edit tasks');
  const [tab, setTab] = useState<SectionId>('details');
  const { refs, scrollTo } = useSectionNav();
  const boardHref = `/projects/${projectId}/boards/${boardId}`;

  if (!task) {
    return (
      <>
        <PageHeader title={taskId || 'Ticket'} subtitle="ticket detail" />
        <div className="page">
          {taskQ.isError ? (
            <p className="error">
              Couldn’t open {taskId}: {taskQ.error instanceof Error ? taskQ.error.message : 'not found'}.{' '}
              <Link to={boardHref}>Back to the board</Link>
            </p>
          ) : (
            <p className="muted">Loading…</p>
          )}
        </div>
      </>
    );
  }
  // The ticket has loaded; the panels take the narrowed shape (see LoadedTaskDetail).
  const dl = d as LoadedTaskDetail;
  // Dependency chips and subtasks navigate to that ticket's own page rather than opening a drawer.
  const openTask = (id: string): void => { void navigate(`/projects/${projectId}/boards/${boardId}/tasks/${id}`); };

  return (
    <>
      {/* THE TITLE IS THE TITLE. This passed `title={task.id}` and `subtitle={task.title}`, so the
          16px h1 read "T-17" while the sentence describing the ticket sat under it at 12px muted —
          the identifier shouted and the content whispered. The id still matters (it is how you refer
          to the ticket), so it stays as the eyebrow line, smaller, with the copy control the drawer
          already offers. */}
      <PageHeader
        title={<span style={{ fontSize: 20, lineHeight: 1.25 }}>{task.title}</span>}
        subtitle={<TicketRef projectId={projectId} boardId={boardId} taskId={task.id} />}
        actions={
          <>
            {/* THE PAGE TOLD YOU TO CLICK AN EDIT BUTTON IT DID NOT HAVE. `DescriptionPanel`'s empty
                copy reads "click Edit (or double-click)" — the drawer has one in its header, the page
                never did, so the instruction pointed at nothing. Same `startEdit`/`editing` state the
                double-click path already uses. */}
            <button
              className="btn secondary"
              disabled={!editing && !canEdit}
              title={!editing && !canEdit ? editWhy ?? 'Checking your role' : undefined}
              aria-describedby={!editing && editWhy ? 'task-edit-closed' : undefined}
              onClick={editing ? () => setEditing(false) : startEdit}
            >
              {editing ? 'Cancel' : 'Edit'}
            </button>
            {!editing && editWhy && <span id="task-edit-closed" className="muted" style={{ fontSize: 12 }}>{editWhy}</span>}
            <Link className="btn secondary" to={boardHref}>Back to board</Link>
          </>
        }
      />
      <div className="page">
        {/*
          THE ID AND TITLE WERE PRINTED TWICE. `PageHeader` above already renders `task.id` as the
          title and `task.title` as the subtitle; this block repeated both — a `Board › T-3` crumb
          and an `<h1>` of the same title, one on top of the other. Reported from staging as
          "duplicate information". The crumb's only unique job was linking back to the board, and
          `PageHeader`'s actions already carry a "Back to board" button, so nothing is lost.

          What survives is the metadata line, which appears nowhere else.
        */}
        <div className="task-sub task-head">
          <RoleBadge role={roleOf(task.status)} />
          <span>v{task.version}</span>
          <span>· created by {task.createdByAgent ?? personName(task.createdBy, d.names)}</span>
          <span>· updated {timeAgo(task.updatedAt)}</span>
        </div>

        <div className="task-tabs">
          <Tabs
            items={SECTIONS}
            active={tab}
            onSelect={(id) => { setTab(id); scrollTo(id); }}
          />
        </div>

        {err && <p className="error task-err">{err}</p>}

        <SideWide
          rail={
            <>
              {/* Status, ownership and every property — the same rail the drawer mounts at the top
                  of its scroll body, so both surfaces put the controls in one place.

                  The mock's rail also carries Gates / Effector / Outcome / Cost / Judge /
                  Guardrails. Those are PRD entities the core does not store, so they are NOT
                  rendered here — a fabricated cost or outcome reads as a measurement. They come
                  back when the core writes them, not before. */}
              <TicketRail d={dl} />
            </>
          }
        >
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
            <DependenciesPanel d={dl} onOpenTask={openTask} />
            <SubtasksPanel d={dl} onOpenTask={openTask} />
          </div>

          <div ref={refs.files}>
            <DeliveredPanel d={dl} />
            <FilesPanel d={dl} />
          </div>

          <div ref={refs.discussion}><DiscussionPanel d={dl} /></div>

          <div ref={refs.activity}>
            <ActivityPanel d={dl} />
          </div>
        </SideWide>
      </div>
    </>
  );
}
