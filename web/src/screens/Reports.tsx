/**
 * REPORTS — work-item reporting over a board's task census.
 *
 * Separate from `/analytics`, which is OPERATIONS telemetry (the tool_calls log, latency, error
 * rate). Different question, different reader, different failure mode: analytics can be entirely
 * unavailable while every chart here still renders, and vice versa. A two-tab strip links them.
 *
 * ── THE CENSUS, AND WHY IT PAGES ────────────────────────────────────────────────────────────────
 * `list_tasks` caps `limit` at 200 and returns `nextCursor`. `Analytics.tsx` asks for 200 and
 * DISCARDS the cursor, so every number on that page is silently truncated at 200 tasks — a headline
 * "N work items" cannot ship on that. This screen drains pages up to a stated bound, and when it
 * stops early it SAYS SO rather than rendering a smaller number as if it were the whole board.
 *
 * ── WHAT IS NOT HERE ────────────────────────────────────────────────────────────────────────────
 * No cycle time, no lead time, no completion trend. They need per-task status history, which does
 * not exist anywhere the SPA can reach — the full argument, with the three places it is absent from,
 * is the docblock at the top of `reports.ts`. `updatedAt` is the only proxy and it means "last
 * edit", so a completion trend built on it moves when someone comments on a closed ticket.
 */
import { useEffect, useMemo } from 'react';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import * as analyticsApi from '../api/analytics.js';
import * as boardsApi from '../api/boards.js';
import { PageHeader } from '../components.js';
import { AreaLine, ChartFrame, Donut, RankedBars, StackedBars, describeQueryError, type Slice } from '../charts.js';
import { useProject } from '../projectContext.js';
import { Cols, Note, Tabs, Tile, Tiles } from '../ui.js';
import {
  ageBuckets,
  assigneeRanking,
  censusCounts,
  creationTrend,
  denseOps,
  priorityCounts,
  statusCounts,
} from '../reports.js';
import type { Priority, Status, Task } from '../types.js';
import { STATUSES } from '../types.js';

/** `list_tasks` max (src/tools/tasks.ts) — asking for more is a VALIDATION error, not a bigger page. */
const PAGE = 200;
/** 2,000 tasks. A bound, not a silence: hitting it renders a footnote naming the number. */
const MAX_PAGES = 10;

const STATUS_COLOR: Record<Status, string> = {
  TODO: 'var(--ready)',
  IN_PROGRESS: 'var(--progress)',
  BLOCKED: 'var(--blocked)',
  REVIEW: 'var(--review)',
  DONE: 'var(--done)',
};
const PRIORITY_COLOR: Record<Priority, string> = {
  low: 'var(--text-muted)',
  normal: 'var(--ready)',
  high: 'var(--progress)',
  urgent: 'var(--blocked)',
};
const STATUS_ORDER: Status[] = [...STATUSES];
const PRIORITY_ORDER: Priority[] = ['urgent', 'high', 'normal', 'low'];

const pretty = (s: string): string => s.charAt(0) + s.slice(1).toLowerCase().replace('_', ' ');

export function Reports() {
  const { projectId, projects, loading: projectsLoading } = useProject();
  const projectName = projects.find((p) => p.projectId === projectId)?.name;

  const boardsQ = useQuery({
    queryKey: ['boards', projectId],
    queryFn: () => boardsApi.listBoards(projectId),
    enabled: !!projectId,
  });
  const board = boardsQ.data?.boards[0];
  const boardId = board?.id;

  // THE CENSUS. Drains `nextCursor` up to MAX_PAGES so the figures cover the board rather than its
  // first page — the defect this screen exists to not repeat.
  const censusQ = useInfiniteQuery({
    queryKey: ['task-census', projectId, boardId],
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      boardsApi.listTasks(projectId, boardId!, {
        limit: String(PAGE),
        ...((pageParam as string) ? { cursor: pageParam as string } : {}),
      }),
    getNextPageParam: (last) => last.nextCursor ?? null,
    enabled: !!projectId && !!boardId,
    retry: false,
  });

  useEffect(() => {
    if (censusQ.hasNextPage && !censusQ.isFetchingNextPage && (censusQ.data?.pages.length ?? 0) < MAX_PAGES) {
      void censusQ.fetchNextPage();
    }
  }, [censusQ.hasNextPage, censusQ.isFetchingNextPage, censusQ.data?.pages.length, censusQ]);

  const opsQ = useQuery({
    queryKey: ['analytics-ops', projectId],
    queryFn: () => analyticsApi.analyticsOperations(projectId, 30),
    enabled: !!projectId,
    retry: false,
  });

  const tasks: Task[] = useMemo(
    () => censusQ.data?.pages.flatMap((p) => p.tasks) ?? [],
    [censusQ.data],
  );
  // ONE `now` for the whole render, threaded into every aggregation — two calls a millisecond apart
  // could put a boundary-aged ticket in two different buckets on the same screen.
  const now = useMemo(() => Date.now(), [tasks]);

  const truncated = censusQ.hasNextPage && (censusQ.data?.pages.length ?? 0) >= MAX_PAGES;
  const counts = useMemo(() => censusCounts(tasks, now), [tasks, now]);
  const trend = useMemo(() => creationTrend(tasks, now), [tasks, now]);
  const ages = useMemo(() => ageBuckets(tasks, now), [tasks, now]);
  const byAssignee = useMemo(() => assigneeRanking(tasks, 8), [tasks]);
  const ops = opsQ.data;
  const opsSeries = useMemo(
    () => (ops ? denseOps(ops.byDay, ops.days, now) : { days: [], values: [] }),
    [ops, now],
  );

  const censusState = censusQ.isLoading
    ? ('loading' as const)
    : censusQ.isError
      ? ('error' as const)
      : tasks.length === 0
        ? ('empty' as const)
        : ('ready' as const);
  const censusError = censusQ.isError ? describeQueryError(censusQ.error, 'the board') : undefined;
  /** An em dash when the census failed. `censusCounts([])` is 0 for every field, and a confident 0
   *  reads as "this board is empty" — the one thing we know it does not mean. */
  const tile = (n: number): string => (censusState === 'error' ? '—' : String(n));
  const opsState = opsQ.isLoading
    ? ('loading' as const)
    : opsQ.isError
      ? ('error' as const)
      : !ops || ops.total === 0
        ? ('empty' as const)
        : ('ready' as const);
  const opsError = opsQ.isError ? describeQueryError(opsQ.error, 'operations telemetry') : undefined;

  const censusWindow = truncated ? `capped at ${tasks.length}` : 'on this board';
  const opsWindow = ops ? `last ${ops.days} days` : 'operations';

  const slices = (m: Map<string, number>, order: string[], color: Record<string, string>): Slice[] =>
    order
      .filter((k) => (m.get(k) ?? 0) > 0)
      .map((k) => ({ key: k, label: pretty(k), value: m.get(k) ?? 0, color: color[k]! }));

  if (projectsLoading) {
    return (
      <div className="page">
        <PageHeader title="Reports" subtitle="work-item reporting" />
        <p className="muted">Loading…</p>
      </div>
    );
  }
  if (!projectId) {
    return (
      <div className="page">
        <PageHeader title="Reports" subtitle="work-item reporting" />
        <div className="callout">No projects yet — create one to see reports.</div>
      </div>
    );
  }
  if (!boardsQ.isLoading && !boardId) {
    return (
      <div className="page">
        <PageHeader title="Reports" subtitle="work-item reporting" />
        <div className="callout">
          Project <strong>{projectName}</strong> has no boards yet.
        </div>
      </div>
    );
  }

  return (
    <div className="page">
      <PageHeader
        title="Reports"
        subtitle={board ? `${projectName} · ${board.name}` : projectName}
      />
      <Tabs
        items={[
          { id: 'reports', label: 'Reports', href: '/reports' },
          { id: 'analytics', label: 'Operations', href: '/analytics' },
        ]}
        active="reports"
      />

      {/* The census bound, stated. A number that silently covered only part of the board would be
          worse than no number — the reader cannot tell the difference from the chart. */}
      {truncated && (
        <Note>
          Census stopped at {tasks.length} tasks; this board has more. Every figure on this page
          counts only those.
        </Note>
      )}

      {/* The tile row carries the census size, so the number on screen always says what it counted.
          ON ERROR IT SHOWS AN EM DASH, NOT A ZERO. `censusCounts` over an empty array returns 0 for
          every field, so a 403 rendered "Tasks 0 / Open 0 / Overdue 0" — a confident, and confidently
          wrong, "this board is empty". The charts below already said "you do not have access"; the
          tiles contradicted them in bigger type. */}
      <Tiles>
        <Tile label="Tasks" value={tile(counts.total)} note={censusWindow} />
        <Tile label="Open" value={tile(counts.open)} note={`${counts.inProgress} in progress`} />
        <Tile label="Created" value={tile(counts.createdLast7)} note="last 7 days" />
        <Tile label="Overdue" value={tile(counts.overdue)} note={`${counts.unassigned} unassigned`} />
      </Tiles>

      <Cols n={2} min={320}>
        <ChartFrame
          title="By status"
          window="snapshot · now"
          state={censusState}
          {...(censusError ? { errorText: censusError } : {})}
          emptyText="No tasks on this board yet."
        >
          <Donut
            slices={slices(statusCounts(tasks), STATUS_ORDER, STATUS_COLOR)}
            total={counts.total}
            totalLabel="tasks"
          />
        </ChartFrame>

        <ChartFrame
          title="By priority"
          window="snapshot · now"
          state={censusState}
          {...(censusError ? { errorText: censusError } : {})}
          emptyText="No tasks on this board yet."
        >
          <Donut
            slices={slices(priorityCounts(tasks), PRIORITY_ORDER, PRIORITY_COLOR)}
            total={counts.total}
            totalLabel="tasks"
          />
        </ChartFrame>
      </Cols>

      <ChartFrame
        title="Created per day"
        window={trend.days.length ? `${trend.days.length} days` : 'no data'}
        state={censusState}
        {...(censusError ? { errorText: censusError } : {})}
        emptyText="Nothing created yet."
        footnote="Colour is each ticket's CURRENT status, not the status it had when it was created — the board keeps no status history."
      >
        <StackedBars
          days={trend.days}
          series={trend.series.map((s) => ({
            key: s.key,
            label: pretty(s.key),
            color: STATUS_COLOR[s.key],
            values: s.values,
          }))}
          title="Tickets created per day, stacked by current status"
          desc={trend.series.map((s) => `${pretty(s.key)}: ${s.values.reduce((a, b) => a + b, 0)}`).join(', ')}
        />
      </ChartFrame>

      <Cols n={2} min={320}>
        <ChartFrame
          title="Age of open work"
          window="since creation · now"
          state={censusState}
          {...(censusError ? { errorText: censusError } : {})}
          emptyText="No open work."
          footnote="Age since CREATION, not time in status — a ticket created three weeks ago and started yesterday still counts as 14d+."
        >
          <RankedBars rows={ages.map((b) => ({ key: b.label, label: b.label, value: b.value }))} />
        </ChartFrame>

        <ChartFrame
          title="Open work by holder"
          window="snapshot · now"
          state={censusState}
          {...(censusError ? { errorText: censusError } : {})}
          emptyText="No open work."
          {...(byAssignee.otherKeys > 0
            ? { footnote: `Plus ${byAssignee.otherValue} across ${byAssignee.otherKeys} more holders.` }
            : {})}
        >
          <RankedBars rows={byAssignee.rows.map((r) => ({ key: r.key, label: r.key, value: r.value }))} />
        </ChartFrame>
      </Cols>

      <ChartFrame
        title="Tool calls per day"
        window={opsWindow}
        state={opsState}
        {...(opsError ? { errorText: opsError } : {})}
        emptyText="No operations recorded in this window yet."
      >
        <AreaLine
          days={opsSeries.days}
          values={opsSeries.values}
          title="Tool calls per day"
          desc={`${ops?.total ?? 0} calls over ${ops?.days ?? 0} days`}
        />
      </ChartFrame>

      <ChartFrame
        title="Busiest tools"
        window={opsWindow}
        state={opsState}
        {...(opsError ? { errorText: opsError } : {})}
        emptyText="No operations recorded in this window yet."
      >
        <RankedBars
          rows={(ops?.byTool ?? []).slice(0, 8).map((t) => ({
            key: t.tool,
            label: <span className="mono">{t.tool}</span>,
            value: t.calls,
            note: <span className="mono">{Math.round(t.avgMs)}ms</span>,
          }))}
        />
      </ChartFrame>
    </div>
  );
}
