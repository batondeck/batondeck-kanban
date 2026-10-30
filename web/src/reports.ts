/**
 * REPORT AGGREGATION — pure functions over a task census. No React, no DOM, no clock.
 *
 * `now` is ALWAYS a parameter, never `Date.now()` inside: every function here is deterministic, so
 * the tests need no fake timers and a chart cannot change shape because a suite ran at midnight.
 *
 * ────────────────────────────────────────────────────────────────────────────────────────────────
 * WHAT WE DELIBERATELY DO NOT COMPUTE, AND WHY. Read this before adding a chart.
 *
 * Cycle time, lead time and a completion trend all need per-task STATUS HISTORY — when a ticket
 * entered IN_PROGRESS, when it reached DONE. That history does not exist anywhere the SPA can reach:
 *
 *   1. `move_task` / `complete_task` / `claim_task` are NOT in `SENSITIVE_ACTIONS`
 *      (`src/domain/audit.ts`), so the durable audit log never records a status change.
 *   2. The board feed is capped at 50 events and is unpaged (`src/resources/index.ts:276`); the
 *      gateway forwards no params. On a busy board that is minutes of history.
 *   3. BigQuery holds tool-call telemetry only, and `taskId` rides the ERROR path alone
 *      (`src/tools/common.ts:621`). There is no Firestore→BQ export of tasks.
 *
 * The only per-task timestamps are `createdAt` and `updatedAt`. `updatedAt` is the LAST EDIT, not the
 * completion — add a comment to a closed ticket and it jumps to today. A "completion trend" built on
 * it would look exactly like the reference chart and be wrong in the direction that flatters us:
 * recent completions inflated, with nothing on screen to reveal it.
 *
 * So: `creationTrend` (honest, `createdAt`) replaces the completion trend, and `ageBuckets` (honest,
 * "what is going stale") replaces the cycle-time scatter. If you are here to add cycle time, add
 * `move_task` to `SENSITIVE_ACTIONS` first and read the audit log — do not approximate it.
 * ────────────────────────────────────────────────────────────────────────────────────────────────
 */
import type { OpsSummary, Priority, Status, Task } from './types.js';

/** `YYYY-MM-DD`, always UTC. */
export type DayKey = string;

const DAY_MS = 86_400_000;

/**
 * UTC, not local. A local-time bucket makes a chart change shape by the reader's geography — the same
 * board would show a different "created per day" in Manila and in London, and a ticket created at
 * 23:30 UTC would land on either day depending on who is looking.
 */
export function dayKey(iso: string | number): DayKey {
  return new Date(iso).toISOString().slice(0, 10);
}

/** Every day from `from` to `to` inclusive — the dense axis a time series needs. */
export function dayRange(from: DayKey, to: DayKey): DayKey[] {
  const out: DayKey[] = [];
  for (let t = Date.parse(`${from}T00:00:00Z`); t <= Date.parse(`${to}T00:00:00Z`); t += DAY_MS) {
    out.push(dayKey(t));
  }
  return out;
}

export function countBy<T>(xs: readonly T[], key: (t: T) => string | null): Map<string, number> {
  const m = new Map<string, number>();
  for (const x of xs) {
    const k = key(x);
    if (k === null) continue;
    m.set(k, (m.get(k) ?? 0) + 1);
  }
  return m;
}

/** Counts onto a dense day axis, zero-filling the gaps. See `denseOps` for why that matters. */
export function denseSeries(counts: Map<DayKey, number>, days: readonly DayKey[]): number[] {
  return days.map((d) => counts.get(d) ?? 0);
}

/** Top `n` by value, with the remainder folded into one bucket rather than dropped. */
export function topN(
  counts: Map<string, number>,
  n: number,
): { rows: Array<{ key: string; value: number }>; otherValue: number; otherKeys: number } {
  const sorted = [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
  const rows = sorted.slice(0, n).map(([key, value]) => ({ key, value }));
  const rest = sorted.slice(n);
  return { rows, otherValue: rest.reduce((s, [, v]) => s + v, 0), otherKeys: rest.length };
}

const OPEN: ReadonlySet<Status> = new Set(['TODO', 'IN_PROGRESS', 'BLOCKED', 'REVIEW']);
const TERMINAL: ReadonlySet<Status> = new Set(['DONE']);

/** Cancelled (ADR-0001): DONE with the `cancelled` label. Conductor's cancelled state was never done, so it
 *  counts in the total and in no done figure (P-208). */
export function isCancelled(t: Pick<Task, 'status' | 'labels'>): boolean {
  return t.status === 'DONE' && (t.labels ?? []).includes('cancelled');
}

/**
 * Subtask progress per parent, as the board card shows it: every subtask counts in the total, and only
 * a delivered one in done, so a cancelled subtask (DONE with the cancelled label) is not done (P-208).
 */
export function subtaskProgress(tasks: ReadonlyArray<Pick<Task, 'parentTaskId' | 'status' | 'labels'>>): Map<string, { done: number; total: number }> {
  const m = new Map<string, { done: number; total: number }>();
  for (const t of tasks) {
    if (!t.parentTaskId) continue;
    const s = m.get(t.parentTaskId) ?? { done: 0, total: 0 };
    s.total += 1;
    if (t.status === 'DONE' && !isCancelled(t)) s.done += 1;
    m.set(t.parentTaskId, s);
  }
  return m;
}

export interface CensusCounts {
  total: number;
  open: number;
  done: number;
  blocked: number;
  inProgress: number;
  createdLast7: number;
  overdue: number;
  unassigned: number;
  activeLeases: number;
}

/**
 * The headline counts. `createdLast7` is deliberately CREATED, not completed — see the docblock
 * above; it is the honest counterpart to the reference's "completed in the last 7 days".
 */
export function censusCounts(tasks: readonly Task[], now: number): CensusCounts {
  const weekAgo = now - 7 * DAY_MS;
  let open = 0, done = 0, blocked = 0, inProgress = 0, createdLast7 = 0, overdue = 0, unassigned = 0, activeLeases = 0;
  for (const t of tasks) {
    if (OPEN.has(t.status)) open += 1;
    if (t.status === 'DONE' && !isCancelled(t)) done += 1;
    if (t.status === 'BLOCKED') blocked += 1;
    if (t.status === 'IN_PROGRESS') inProgress += 1;
    if (Date.parse(t.createdAt) >= weekAgo) createdLast7 += 1;
    // Overdue counts only tickets that can still be worked — a past due date on a DONE ticket is
    // history, not a problem.
    if (t.dueAt && Date.parse(t.dueAt) < now && !TERMINAL.has(t.status)) overdue += 1;
    if (!t.assignee && OPEN.has(t.status)) unassigned += 1;
    if (t.claim && Date.parse(t.claim.expiresAt) > now) activeLeases += 1;
  }
  return { total: tasks.length, open, done, blocked, inProgress, createdLast7, overdue, unassigned, activeLeases };
}

/**
 * Tickets created per day, stacked by their CURRENT status.
 *
 * The stack colour is where this differs from the reference, and the label must say so: it is the
 * status a ticket holds NOW, not the status it held when it was created. We cannot know the latter
 * (no status history), and a stack that silently implied otherwise would be the same dishonesty as a
 * completion trend on `updatedAt`.
 *
 * The window is derived from the data — earliest `createdAt` to today — never a fixed "last 30 days".
 * A three-day-old board must say three days.
 */
export function creationTrend(
  tasks: readonly Task[],
  now: number,
  maxDays = 60,
): { days: DayKey[]; series: Array<{ key: Status; values: number[] }> } {
  if (tasks.length === 0) return { days: [], series: [] };
  const today = dayKey(now);
  const earliest = tasks.reduce((min, t) => (t.createdAt < min ? t.createdAt : min), tasks[0]!.createdAt);
  // Bounded: a two-year-old board would otherwise render 700 bars two pixels wide.
  const floor = dayKey(Math.max(Date.parse(dayKey(earliest)), Date.parse(today) - (maxDays - 1) * DAY_MS));
  const days = dayRange(floor, today);
  const inWindow = tasks.filter((t) => dayKey(t.createdAt) >= floor);
  const statuses = [...new Set(inWindow.map((t) => t.status))].sort() as Status[];
  return {
    days,
    series: statuses.map((st) => ({
      key: st,
      values: denseSeries(countBy(inWindow.filter((t) => t.status === st), (t) => dayKey(t.createdAt)), days),
    })),
  };
}

/**
 * How long the OPEN work has been open — the honest replacement for a cycle-time chart.
 *
 * It answers the actionable half of the same question ("what is going stale?") using the one
 * timestamp we actually have. It measures age since CREATION, not time-in-status, and the label says
 * so: a ticket created three weeks ago and started yesterday still lands in 14d+.
 */
export function ageBuckets(tasks: readonly Task[], now: number): Array<{ label: string; value: number }> {
  const edges = [
    { label: '< 1d', max: DAY_MS },
    { label: '1–3d', max: 3 * DAY_MS },
    { label: '3–7d', max: 7 * DAY_MS },
    { label: '7–14d', max: 14 * DAY_MS },
    { label: '14d+', max: Infinity },
  ];
  const out = edges.map((e) => ({ label: e.label, value: 0 }));
  for (const t of tasks) {
    if (!OPEN.has(t.status)) continue;
    const age = now - Date.parse(t.createdAt);
    const i = edges.findIndex((e) => age < e.max);
    out[i === -1 ? out.length - 1 : i]!.value += 1;
  }
  return out;
}

/** Who is carrying the open work. Agent claims count — an agent holding a lease IS the assignee. */
export function assigneeRanking(tasks: readonly Task[], n: number): ReturnType<typeof topN> {
  return topN(
    countBy(tasks.filter((t) => OPEN.has(t.status)), (t) => t.assignee || t.claim?.agent || 'unassigned'),
    n,
  );
}

export function statusCounts(tasks: readonly Task[]): Map<string, number> {
  return countBy(tasks, (t) => t.status);
}

export function priorityCounts(tasks: readonly Task[]): Map<string, number> {
  return countBy(tasks, (t) => t.priority as Priority);
}

/**
 * Tool calls onto a dense day axis.
 *
 * *** `OpsSummary.byDay` OMITS ZERO DAYS. *** A band scale over the raw array therefore compresses a
 * five-day silence into one step, drawing a continuous line across days nothing happened — the chart
 * says "steady" where the truth is "nothing ran". Zero-filling is the honesty property, which is why
 * this is a pure function with its own test rather than a `.map` at the call site.
 */
export function denseOps(
  byDay: OpsSummary['byDay'],
  days: number,
  now: number,
): { days: DayKey[]; values: number[] } {
  const to = dayKey(now);
  const from = dayKey(Date.parse(to) - (Math.max(1, days) - 1) * DAY_MS);
  const axis = dayRange(from, to);
  const counts = new Map<DayKey, number>();
  for (const d of byDay) counts.set(dayKey(`${d.day}T00:00:00Z`), d.calls);
  return { days: axis, values: denseSeries(counts, axis) };
}
