import { useMemo, type CSSProperties, useEffect} from 'react';
import { useDisplayNames } from '../displayNames.js';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { Link } from 'react-router';
import * as agentsApi from '../api/agents.js';
import * as analyticsApi from '../api/analytics.js';
import * as boardsApi from '../api/boards.js';
import * as feedApi from '../api/feed.js';
import { WaitLoader } from '../brand.js';
import { Avatar, EmptyState, PageHeader, StatusPill, eventActor, eventVerb, timeAgo } from '../components.js';
import { useProject } from '../projectContext.js';
import { toolByAgent } from '../agentStatus.js';
import { useAuth } from '../auth.js';
import { SUPERADMIN, can } from '../permissions.js';
import { STATUSES, isAgent } from '../types.js';
import type { ActivityEvent, Status } from '../types.js';
import { Cluster, Stack, Tabs } from '../ui.js';

const STATUS_ORDER: Status[] = [...STATUSES];
const STATUS_COLOR: Record<Status, string> = {
  TODO: 'var(--ready)',
  IN_PROGRESS: 'var(--progress)',
  BLOCKED: 'var(--blocked)',
  REVIEW: 'var(--review)',
  DONE: 'var(--done)',
};

function Stat({ label, value, sub, color }: { label: string; value: string | number; sub?: string; color?: string }) {
  return (
    <div className="card" style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <span className="muted" style={{ fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.04em' }}>{label}</span>
      <span className="mono" style={{ fontSize: 28, fontWeight: 600, color: color ?? 'var(--text)', lineHeight: 1 }}>{value}</span>
      {sub && <span className="muted" style={{ fontSize: 12 }}>{sub}</span>}
    </div>
  );
}

function fmtDur(ms: number): string {
  const m = Math.round(ms / 60000);
  if (m < 1) return '<1m';
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h}h ${m % 60}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}

const ACTIVITY_ROW: CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 10,
  padding: '10px 12px',
  borderBottom: '1px solid var(--border)',
  color: 'inherit',
  textDecoration: 'none',
};

/** The server caps `list_tasks` at 200; asking for more is a VALIDATION error, not a bigger page. */
const CENSUS_PAGE = 200;
/** 10 x 200 = 2,000 tasks. Past that the page says it stopped rather than implying it counted all. */
const CENSUS_MAX_PAGES = 10;

export function Analytics() {
  const { permissions, ui } = useAuth();
  const { projectId, projects, loading: projectsLoading } = useProject();
  const projectName = projects.find((p) => p.projectId === projectId)?.name;
  const displayNames = useDisplayNames(projectId);

  const boardsQ = useQuery({
    queryKey: ['boards', projectId],
    queryFn: () => boardsApi.listBoards(projectId),
    enabled: !!projectId,
  });
  const boardId = boardsQ.data?.boards[0]?.id;
  const boardName = boardsQ.data?.boards[0]?.name;

  const boardQ = useQuery({
    queryKey: ['board', projectId, boardId],
    queryFn: () => boardsApi.getBoard(projectId, boardId!),
    enabled: !!projectId && !!boardId,
  });
  /**
   * THE CENSUS PAGES ITS CURSOR, like `/reports` does.
   *
   * This asked for `ui.taskPageLimit` tasks and DISCARDED `nextCursor`, so every figure on the page
   * was silently truncated at one page — a board with more tasks than that reported numbers that
   * looked total and were not.
   *
   * `/reports` was built with the drain and this screen was left with the defect, which was tolerable
   * only while the two were far apart. Then this PR added a `Tabs` bar putting `/reports` one click
   * away, so two adjacent screens reported different totals for the same board and the WRONG one
   * carried no caveat. Fixing the screen is the smaller change of the two available.
   *
   * `MAX_PAGES` bounds the drain; `truncated` below says so rather than letting a capped number read
   * as complete.
   */
  const censusQ = useInfiniteQuery({
    queryKey: ['tasks', projectId, boardId],
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      boardsApi.listTasks(projectId, boardId!, {
        limit: String(CENSUS_PAGE),
        ...((pageParam as string) ? { cursor: pageParam as string } : {}),
      }),
    getNextPageParam: (last) => last.nextCursor ?? null,
    enabled: !!projectId && !!boardId,
    // No retry: a hard failure otherwise burns three round trips before settling on the zeros the
    // notice below has to explain. `/reports` does the same for the same census.
    retry: false,
  });
  useEffect(() => {
    if (
      censusQ.hasNextPage &&
      !censusQ.isFetchingNextPage &&
      (censusQ.data?.pages.length ?? 0) < CENSUS_MAX_PAGES
    ) {
      void censusQ.fetchNextPage();
    }
  }, [censusQ.hasNextPage, censusQ.isFetchingNextPage, censusQ.data?.pages.length, censusQ]);

  /** Shaped like the old single-page result so every downstream reader is unchanged. */
  const tasksQ = {
    data: censusQ.data ? { tasks: censusQ.data.pages.flatMap((p) => p.tasks) } : undefined,
    isPending: censusQ.isPending,
    isLoading: censusQ.isPending,
    isError: censusQ.isError,
  };
  /** The census failed, so every task-derived figure below is 0 for want of data rather than
   *  because the board is empty. Said out loud — see the notice. `/reports` renders an em dash in
   *  its tiles for the same reason; this screen's cards are `Stat`, so the notice carries it. */
  const censusFailed = censusQ.isError;
  /** True when the drain stopped at the cap with pages still available. */
  const censusTruncated =
    (censusQ.data?.pages.length ?? 0) >= CENSUS_MAX_PAGES && !!censusQ.hasNextPage;
  const feedQ = useQuery({
    queryKey: ['feed', projectId, boardId],
    queryFn: () => feedApi.feed(projectId, boardId!),
    enabled: !!projectId && !!boardId,
  });
  // Historical operations (last 7d) from the BigQuery-backed analytics service — what the live tools
  // can't show (throughput trend, per-tool usage, error rate over time). Scoped to this project.
  const opsQ = useQuery({
    queryKey: ['analytics-ops', projectId],
    queryFn: () => analyticsApi.analyticsOperations(projectId, 7),
    enabled: !!projectId,
    retry: false,
  });
  const ops = opsQ.data;

  // MCP session telemetry → client logos on activity rows. Identity-scoped for members; admins get the
  // workspace-wide view (all:true, admin-enforced at the core) so every agent's client resolves.
  // A WIDENING, not a gate — and it reads the permission, not the `isAdmin` alias, for the reason
  // stated next to `SUPERADMIN` in `permissions.ts`. Key and fn move together or the cache lies.
  const allSessions = can(permissions, SUPERADMIN);
  const sessionsQ = useQuery({
    queryKey: ['agentSessions', allSessions],
    queryFn: () => agentsApi.agentSessions(ui.sessionsLimit, allSessions),
    staleTime: 60000,
  });
  const toolMap = useMemo(() => toolByAgent(sessionsQ.data?.sessions ?? []), [sessionsQ.data]);

  const tasks = useMemo(() => tasksQ.data?.tasks ?? [], [tasksQ.data]);
  const board = boardQ.data?.board;
  const events = useMemo(() => feedQ.data?.events ?? [], [feedQ.data]);

  const stats = useMemo(() => {
    const byStatus = new Map<Status, number>();
    let activeLeases = 0;
    let agentDone = 0;
    let humanDone = 0;
    for (const t of tasks) {
      byStatus.set(t.status, (byStatus.get(t.status) ?? 0) + 1);
      if (t.claim && new Date(t.claim.expiresAt).getTime() > Date.now()) activeLeases += 1;
      if (t.status === 'DONE') {
        // Prefer explicit agent evidence (agent-held claim, agent-created) over name heuristics —
        // assignee/createdBy alone misses tasks agents create on behalf of humans.
        if (t.claim?.agent || t.createdByAgent || isAgent(t.assignee ?? '')) agentDone += 1;
        else humanDone += 1;
      }
    }
    const total = tasks.length;
    const inProgress = byStatus.get('IN_PROGRESS') ?? 0;
    const blocked = byStatus.get('BLOCKED') ?? 0;
    const done = byStatus.get('DONE') ?? 0;
    return { byStatus, activeLeases, agentDone, humanDone, total, inProgress, blocked, done };
  }, [tasks]);

  // WIP per column: count tasks placed in each column by matching status (falling back to columnId).
  const wipPerColumn = useMemo(() => {
    if (!board) return [] as Array<{ id: string; name: string; status: Status; count: number; wipLimit: number | null }>;
    const counts = new Map<string, number>();
    for (const t of tasks) {
      const own = board.columns.find((c) => c.id === t.columnId);
      let cid = t.columnId;
      if (!own || own.status !== t.status) {
        const byStatus = board.columns.find((c) => c.status === t.status);
        if (byStatus) cid = byStatus.id;
      }
      counts.set(cid, (counts.get(cid) ?? 0) + 1);
    }
    return board.columns.map((c) => ({ id: c.id, name: c.name, status: c.status, count: counts.get(c.id) ?? 0, wipLimit: c.wipLimit }));
  }, [board, tasks]);

  // Columns map 1:1 onto statuses (unique status per column, same counts, no status left over) →
  // Status Distribution and WIP per Column would show identical data; render one combined card.
  const combined = useMemo(() => {
    if (wipPerColumn.length === 0) return false;
    const statuses = new Set(wipPerColumn.map((c) => c.status));
    if (statuses.size !== wipPerColumn.length) return false;
    if (!wipPerColumn.every((c) => c.count === (stats.byStatus.get(c.status) ?? 0))) return false;
    return STATUS_ORDER.every((s) => statuses.has(s) || (stats.byStatus.get(s) ?? 0) === 0);
  }, [wipPerColumn, stats]);

  // Median claimed→done from the loaded feed window (only tasks with both events in the window).
  const leadTime = useMemo(() => {
    const byTask = new Map<string, { claims: number[]; done: number }>();
    for (const e of events) {
      if (!e.taskId || (e.type !== 'task.claimed' && e.type !== 'task.completed')) continue;
      const entry = byTask.get(e.taskId) ?? { claims: [], done: 0 };
      const ts = new Date(e.ts).getTime();
      if (e.type === 'task.claimed') entry.claims.push(ts);
      else entry.done = Math.max(entry.done, ts);
      byTask.set(e.taskId, entry);
    }
    const samples: number[] = [];
    for (const { claims, done } of byTask.values()) {
      if (!done) continue;
      const before = claims.filter((c) => c <= done);
      if (before.length > 0) samples.push(done - Math.max(...before));
    }
    samples.sort((a, b) => a - b);
    const n = samples.length;
    const median = n === 0 ? null : n % 2 === 1 ? samples[(n - 1) / 2] : (samples[n / 2 - 1] + samples[n / 2]) / 2;
    return { median, n };
  }, [events]);

  const maxStatus = Math.max(1, ...STATUS_ORDER.map((s) => stats.byStatus.get(s) ?? 0));
  const maxWip = Math.max(1, ...wipPerColumn.map((c) => c.count));

  const throughputPct = stats.total > 0 ? Math.round((stats.done / stats.total) * 100) : 0;

  // ---- loading / empty states ----
  if (projectsLoading) {
    return (
      <>
        <PageHeader title="Analytics" />
        <div className="page"><WaitLoader /></div>
      </>
    );
  }
  if (!projectId) {
    return (
      <>
        <PageHeader title="Analytics" />
        <div className="page">
          <div className="callout">No projects yet. Create a project and board to see workspace telemetry.</div>
        </div>
      </>
    );
  }
  if (!boardsQ.isLoading && !boardId) {
    return (
      <>
        <PageHeader title="Analytics" />
        <div className="page">
          <div className="callout">Project <strong>{projectName}</strong> has no boards yet. Create one to see analytics.</div>
        </div>
      </>
    );
  }

  return (
    <>
      <PageHeader
        title="Workspace Telemetry"
        subtitle="High-level overview of human-agent collaboration and system throughput."
        actions={boardName ? <span className="chip">{boardName}</span> : undefined}
      />
      {/* Two views of one surface: this page answers "how is the SYSTEM running", /reports answers
          "how is the WORK going". They fail independently, so they are separate screens. */}
      <Tabs
        items={[
          { id: 'reports', label: 'Reports', href: '/reports' },
          { id: 'analytics', label: 'Operations', href: '/analytics' },
        ]}
        active="analytics"
      />

      <div className="page" style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
        {/* A capped census must SAY it is capped. A number that looks total and is not is worse than
            no number — and `/reports` is one tab away making exactly this claim about the same board. */}
        {censusTruncated && (
          <div className="callout" role="status">
            Census stopped at {CENSUS_PAGE * CENSUS_MAX_PAGES} tasks; this board has more. Every
            task-derived figure below counts only those.
          </div>
        )}
        {/* A FAILED census is not an empty board, and the cards below cannot tell you apart — they
            render 0 either way, with "clear" and "0% throughput" beside them. This is the difference
            between the two, and it was previously plumbed (`isError` was assigned) but never read. */}
        {censusFailed && (
          <div className="callout" role="alert">
            The task census could not be loaded, so every task-derived figure below reads 0 for want
            of data — not because the board is empty. Operations telemetry is unaffected.
          </div>
        )}
        {(tasksQ.isLoading || boardQ.isLoading) && <WaitLoader caption="Loading telemetry…" />}

        {/* KPI stat cards */}
        <div className="grid">
          <Stat label="Total Tasks" value={stats.total} sub={`across ${wipPerColumn.length} columns`} />
          <Stat label="In Progress" value={stats.inProgress} color="var(--progress)" sub={`${stats.activeLeases} active lease${stats.activeLeases === 1 ? '' : 's'}`} />
          <Stat label="Blocked" value={stats.blocked} color="var(--blocked)" sub={stats.blocked > 0 ? 'needs attention' : 'clear'} />
          <Stat label="Done" value={stats.done} color="var(--done)" sub={`${throughputPct}% throughput`} />
          <Stat
            label="Completion Split"
            value={`${stats.agentDone} / ${stats.humanDone}`}
            sub="agents / humans"
            color="var(--ready)"
          />
        </div>

        {/* Board load (combined when columns ≡ statuses) + lead time */}
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(320px, 1fr))', gap: 12 }}>
          {combined ? (
            <div className="card" style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
              <Cluster justify='space-between'>
                <h2 style={{ fontSize: 14, margin: 0 }}>Board Load — status × column</h2>
                <span className="muted mono" style={{ fontSize: 11 }}>{stats.total} tasks</span>
              </Cluster>
              <Stack gap={8}>
                {wipPerColumn.map((c) => {
                  const over = c.wipLimit != null && c.count > c.wipLimit;
                  const denom = c.wipLimit != null ? Math.max(c.wipLimit, c.count, 1) : maxWip;
                  const pct = Math.round((c.count / denom) * 100);
                  return (
                    <div key={c.id} style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                      <div style={{ width: 96, flex: '0 0 96px' }}><StatusPill status={c.status} /></div>
                      <div style={{ flex: 1, height: 8, background: 'var(--panel-2)', borderRadius: 'var(--r-full)', overflow: 'hidden' }}>
                        <div style={{ width: `${pct}%`, height: '100%', background: over ? 'var(--blocked)' : STATUS_COLOR[c.status], borderRadius: 'var(--r-full)' }} />
                      </div>
                      <span className={`wip${over ? ' over' : ''}`}>{c.count}{c.wipLimit != null ? ` / ${c.wipLimit}` : ''}</span>
                    </div>
                  );
                })}
              </Stack>
            </div>
          ) : (
            <>
              {/* Status distribution */}
              <div className="card" style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                <Cluster justify='space-between'>
                  <h2 style={{ fontSize: 14, margin: 0 }}>Status Distribution</h2>
                  <span className="muted mono" style={{ fontSize: 11 }}>{stats.total} tasks</span>
                </Cluster>
                <Stack gap={8}>
                  {STATUS_ORDER.map((s) => {
                    const n = stats.byStatus.get(s) ?? 0;
                    const pct = Math.round((n / maxStatus) * 100);
                    return (
                      <div key={s} style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                        <div style={{ width: 96, flex: '0 0 96px' }}><StatusPill status={s} /></div>
                        <div style={{ flex: 1, height: 8, background: 'var(--panel-2)', borderRadius: 'var(--r-full)', overflow: 'hidden' }}>
                          <div style={{ width: `${pct}%`, height: '100%', background: STATUS_COLOR[s], borderRadius: 'var(--r-full)' }} />
                        </div>
                        <span className="mono" style={{ width: 28, textAlign: 'right', fontSize: 12, color: 'var(--text-2)' }}>{n}</span>
                      </div>
                    );
                  })}
                </Stack>
              </div>

              {/* WIP per column */}
              <div className="card" style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                <Cluster justify='space-between'>
                  <h2 style={{ fontSize: 14, margin: 0 }}>WIP per Column</h2>
                  <span className="muted mono" style={{ fontSize: 11 }}>{boardName ?? 'board'}</span>
                </Cluster>
                <Stack gap={8}>
                  {wipPerColumn.map((c) => {
                    const over = c.wipLimit != null && c.count > c.wipLimit;
                    const denom = c.wipLimit != null ? Math.max(c.wipLimit, c.count, 1) : maxWip;
                    const pct = Math.round((c.count / denom) * 100);
                    return (
                      <div key={c.id} style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                        <div style={{ width: 96, flex: '0 0 96px', fontSize: 12, color: 'var(--text-2)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{c.name}</div>
                        <div style={{ flex: 1, height: 8, background: 'var(--panel-2)', borderRadius: 'var(--r-full)', overflow: 'hidden' }}>
                          <div style={{ width: `${pct}%`, height: '100%', background: over ? 'var(--blocked)' : STATUS_COLOR[c.status], borderRadius: 'var(--r-full)' }} />
                        </div>
                        <span className={`wip${over ? ' over' : ''}`}>{c.count}{c.wipLimit != null ? ` / ${c.wipLimit}` : ''}</span>
                      </div>
                    );
                  })}
                  {wipPerColumn.length === 0 && <span className="muted" style={{ fontSize: 12 }}>No columns.</span>}
                </Stack>
              </div>
            </>
          )}

          {/* Lead time from the loaded feed window */}
          <div className="card" style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Cluster justify='space-between'>
              <h2 style={{ fontSize: 14, margin: 0 }}>Lead Time</h2>
              <span className="muted mono" style={{ fontSize: 11 }}>recent feed window</span>
            </Cluster>
            {leadTime.median != null ? (
              <Stack gap={8}>
                <span className="mono" style={{ fontSize: 28, fontWeight: 600, lineHeight: 1 }}>{fmtDur(leadTime.median)}</span>
                <span className="muted" style={{ fontSize: 12 }}>median time claimed→done · n={leadTime.n}</span>
              </Stack>
            ) : (
              <EmptyState title="No lead-time samples" hint="No completed claims in the recent window." />
            )}
          </div>
        </div>

        {/* Operations (BigQuery-backed, last 7d) — the historical/throughput view the live tools can't give */}
        <div className="card" style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
          <Cluster justify='space-between'>
            <h2 style={{ fontSize: 14, margin: 0 }}>Operations · last 7 days</h2>
            <span className="muted mono" style={{ fontSize: 11 }}>via BigQuery</span>
          </Cluster>
          {opsQ.isLoading && <span className="muted" style={{ fontSize: 12 }}>Loading…</span>}
          {opsQ.isError && <span className="muted" style={{ fontSize: 12 }}>Analytics service unavailable.</span>}
          {ops && ops.total === 0 && <span className="muted" style={{ fontSize: 12 }}>No operations recorded in this window yet.</span>}
          {ops && ops.total > 0 && (
            <>
              <div style={{ display: 'flex', gap: 28, flexWrap: 'wrap' }}>
                {([['Operations', String(ops.total)], ['Error rate', `${Math.round(ops.errorRate * 1000) / 10}%`], ['Avg latency', `${ops.avgLatencyMs}ms`], ['Agents', String(ops.byAgent.length)]] as Array<[string, string]>).map(([l, v]) => (
                  <div key={l}>
                    <div className="muted" style={{ fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.04em' }}>{l}</div>
                    <div className="mono" style={{ fontSize: 20, fontWeight: 600, color: l === 'Error rate' && ops.errorRate > 0.05 ? 'var(--blocked)' : 'var(--text)' }}>{v}</div>
                  </div>
                ))}
              </div>
              <div>
                <div className="muted" style={{ fontSize: 11, marginBottom: 4 }}>Throughput / day</div>
                {(() => {
                  const max = Math.max(...ops.byDay.map((x) => x.calls), 1);
                  const sparse = ops.byDay.length > 14; // label every 2nd bar when dense
                  return (
                    <Cluster gap={2}>
                      {ops.byDay.map((d, i) => (
                        <div key={d.day} title={`${d.day}: ${d.calls}`} style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 2, minWidth: 0 }}>
                          <div style={{ height: 48, display: 'flex', alignItems: 'flex-end' }}>
                            <div style={{ width: '100%', height: `${(d.calls / max) * 100}%`, minHeight: 2, background: 'var(--accent)', borderRadius: 'var(--r-1)' }} />
                          </div>
                          <span className="mono muted" style={{ fontSize: 10, textAlign: 'center' }}>
                            {sparse && i % 2 === 1 ? ' ' : d.day.slice(8, 10)}
                          </span>
                        </div>
                      ))}
                    </Cluster>
                  );
                })()}
              </div>
              <Stack gap={4}>
                {ops.byTool.slice(0, 8).map((t) => {
                  const max = ops.byTool[0]?.calls || 1;
                  return (
                    <div key={t.tool} style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12 }}>
                      <span className="mono" style={{ width: 160, color: 'var(--text-2)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{t.tool}</span>
                      <div style={{ flex: 1, background: 'var(--surface-low)', borderRadius: 'var(--r-1)', overflow: 'hidden', height: 14 }}>
                        <div style={{ width: `${(t.calls / max) * 100}%`, height: '100%', background: 'color-mix(in srgb, var(--accent) 60%, transparent)' }} />
                      </div>
                      <span className="mono" style={{ width: 44, textAlign: 'right', color: 'var(--text-2)' }}>{t.calls}</span>
                      <span className="mono" style={{ width: 58, textAlign: 'right', color: 'var(--text-muted)' }}>{t.avgMs}ms</span>
                    </div>
                  );
                })}
              </Stack>
            </>
          )}
        </div>

        {/* Recent activity strip */}
        <div className="card" style={{ display: 'flex', flexDirection: 'column', gap: 0, padding: 0, overflow: 'hidden' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: 12, borderBottom: '1px solid var(--border)' }}>
            <h2 style={{ fontSize: 14, margin: 0 }}>Recent Activity</h2>
            <span className="muted mono" style={{ fontSize: 11, textTransform: 'uppercase', letterSpacing: '0.06em' }}>Activity log</span>
          </div>
          <Stack>
            {events.slice(0, ui.recentActivityLimit).map((e: ActivityEvent) => {
              // Attribute agent-performed events to the agent (with its client's logo), not the human it acts for.
              const name = eventActor(e, displayNames);
              const inner = (
                <>
                  <Avatar id={name} active={!!e.agent || isAgent(name)} tool={toolMap.get(name) ?? null} />
                  <span style={{ fontSize: 14 }}>{name.split('@')[0]}</span>
                  <span className="muted" style={{ fontSize: 14 }}>{eventVerb(e.type)}</span>
                  {e.taskId && <span className="id mono" style={{ fontSize: 12 }}>{e.taskId}</span>}
                  <div style={{ flex: 1 }} />
                  <span className="muted mono" style={{ fontSize: 11 }}>{timeAgo(e.ts)}</span>
                </>
              );
              // Rows with a task deep-link into the board drawer (same board the feed is scoped to).
              return e.taskId && boardId ? (
                <Link key={e.id} to={`/projects/${projectId}/boards/${boardId}?task=${e.taskId}`} style={ACTIVITY_ROW}>
                  {inner}
                </Link>
              ) : (
                <div key={e.id} style={ACTIVITY_ROW}>{inner}</div>
              );
            })}
            {events.length === 0 && !feedQ.isLoading && (
              <div className="muted" style={{ fontSize: 12, padding: 12 }}>No activity yet.</div>
            )}
          </Stack>
        </div>
      </div>
    </>
  );
}
