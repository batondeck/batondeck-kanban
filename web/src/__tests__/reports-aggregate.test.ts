/**
 * The report aggregations — pure logic, so it is tested exhaustively (the repo's testing posture).
 *
 * Two cases here are anti-regressions for specific DISHONEST charts rather than for crashes:
 *
 *  - `creationTrend` must bucket on `createdAt`. Bucketing on `updatedAt` yields a chart that moves
 *    when someone comments on a closed ticket, inflating recent activity — plausible and wrong.
 *  - `denseOps` must zero-fill. Without it a five-day silence renders as one step and the line reads
 *    "steady" where the truth is "nothing ran".
 *
 * `now` is injected everywhere, so none of this needs fake timers.
 */
import { describe, it, expect } from 'vitest';
import {
  ageBuckets,
  assigneeRanking,
  censusCounts,
  countBy,
  creationTrend,
  dayKey,
  dayRange,
  denseOps,
  denseSeries,
  priorityCounts,
  statusCounts,
  subtaskProgress,
  topN,
} from '../reports.js';
import type { Status, Task } from '../types.js';

const NOW = Date.parse('2026-08-27T12:00:00.000Z');
const daysAgo = (n: number): string => new Date(NOW - n * 86_400_000).toISOString();

const mk = (over: Partial<Task> = {}): Task =>
  ({
    id: 'T-1', projectId: 'p1', boardId: 'b1', columnId: 'c1',
    title: 't', description: '', status: 'TODO' as Status, priority: 'normal',
    assignee: null, labels: [], requiredCapabilities: [], estimate: null, dueAt: null,
    customFields: {}, parentTaskId: null, order: 'a0', blockedBy: [], blocks: [],
    contextSummary: '', version: 1, claim: null, createdBy: 'u',
    createdAt: daysAgo(1), updatedAt: daysAgo(0),
    ...over,
  }) as Task;

describe('day bucketing', () => {
  it('buckets in UTC, so a chart does not change shape by geography', () => {
    // 23:30 UTC is "tomorrow" in Manila and "today" in London. The bucket must not care.
    expect(dayKey('2026-08-01T23:30:00Z')).toBe('2026-08-01');
    expect(dayKey('2026-08-01T00:30:00Z')).toBe('2026-08-01');
  });

  it('dayRange is dense and inclusive at both ends', () => {
    expect(dayRange('2026-08-01', '2026-08-05')).toEqual([
      '2026-08-01', '2026-08-02', '2026-08-03', '2026-08-04', '2026-08-05',
    ]);
    expect(dayRange('2026-08-01', '2026-08-01')).toEqual(['2026-08-01']);
  });

  it('denseSeries zero-fills the gaps', () => {
    const counts = new Map([['2026-08-01', 3], ['2026-08-03', 5]]);
    expect(denseSeries(counts, ['2026-08-01', '2026-08-02', '2026-08-03'])).toEqual([3, 0, 5]);
  });
});

describe('denseOps — the zero-fill is the honesty property', () => {
  it('fills days the ops summary omits', () => {
    const { days, values } = denseOps(
      [{ day: '2026-08-25', calls: 3 }, { day: '2026-08-27', calls: 5 }],
      3,
      NOW,
    );
    expect(days).toEqual(['2026-08-25', '2026-08-26', '2026-08-27']);
    expect(values).toEqual([3, 0, 5]);
    // The axis and the series are the same length, always — a chart cannot mis-align them.
    expect(values).toHaveLength(days.length);
  });

  it('POSITIVE CONTROL: the naive mapping really does differ', () => {
    // Without this, "denseOps zero-fills" could pass against an implementation that happened to be
    // handed dense input. The bug being prevented is concrete: `byDay.map(d => d.calls)` is length 2
    // for a 3-day window, so every point after the gap is drawn one step early.
    const byDay = [{ day: '2026-08-25', calls: 3 }, { day: '2026-08-27', calls: 5 }];
    expect(byDay.map((d) => d.calls)).toHaveLength(2);
    expect(denseOps(byDay, 3, NOW).values).toHaveLength(3);
  });

  it('a window with no operations at all is all zeros, not empty', () => {
    const { days, values } = denseOps([], 3, NOW);
    expect(days).toHaveLength(3);
    expect(values).toEqual([0, 0, 0]);
  });
});

describe('creationTrend', () => {
  it('buckets on createdAt, NEVER on updatedAt', () => {
    // The trap: a ticket created 5 days ago and edited today. On `updatedAt` it lands on today and
    // the chart claims work that did not happen then.
    const t = mk({ createdAt: daysAgo(5), updatedAt: daysAgo(0) });
    const { days, series } = creationTrend([t], NOW);
    const idx = days.indexOf(dayKey(daysAgo(5)));
    expect(idx).toBeGreaterThanOrEqual(0);
    expect(series[0]!.values[idx]).toBe(1);
    expect(series[0]!.values[days.length - 1]).toBe(0); // NOT today
  });

  it('derives its window from the data — a three-day-old board shows three days', () => {
    const { days } = creationTrend([mk({ createdAt: daysAgo(2) })], NOW);
    expect(days).toHaveLength(3);
    expect(days.at(-1)).toBe(dayKey(NOW));
  });

  it('stacks by status, and every series spans the whole axis', () => {
    const { days, series } = creationTrend(
      [mk({ status: 'DONE', createdAt: daysAgo(1) }), mk({ status: 'TODO', createdAt: daysAgo(1) })],
      NOW,
    );
    expect(series.map((s) => s.key).sort()).toEqual(['DONE', 'TODO']);
    for (const s of series) expect(s.values).toHaveLength(days.length);
  });

  it('is empty for an empty board rather than throwing', () => {
    expect(creationTrend([], NOW)).toEqual({ days: [], series: [] });
  });

  it('caps the axis so an old board does not render hundreds of bars', () => {
    const { days } = creationTrend([mk({ createdAt: daysAgo(400) }), mk({ createdAt: daysAgo(0) })], NOW, 60);
    expect(days).toHaveLength(60);
  });
});

describe('ageBuckets', () => {
  it('partitions the OPEN work exactly once', () => {
    const tasks = [
      mk({ createdAt: daysAgo(0) }),
      mk({ createdAt: daysAgo(2) }),
      mk({ createdAt: daysAgo(5) }),
      mk({ createdAt: daysAgo(10) }),
      mk({ createdAt: daysAgo(30) }),
      mk({ status: 'DONE', createdAt: daysAgo(30) }), // terminal — not open work
    ];
    const buckets = ageBuckets(tasks, NOW);
    expect(buckets.map((b) => b.value)).toEqual([1, 1, 1, 1, 1]);
    expect(buckets.reduce((s, b) => s + b.value, 0)).toBe(5);
  });

  it('puts a boundary-aged ticket in exactly one bucket', () => {
    // Exactly 7 days old: `< 7d` is false, so it belongs to 7–14d and nowhere else.
    const buckets = ageBuckets([mk({ createdAt: new Date(NOW - 7 * 86_400_000).toISOString() })], NOW);
    expect(buckets.filter((b) => b.value > 0).map((b) => b.label)).toEqual(['7–14d']);
  });
});

describe('censusCounts', () => {
  it('counts what the tiles claim', () => {
    const c = censusCounts(
      [
        mk({ status: 'TODO', createdAt: daysAgo(1) }),
        mk({ status: 'IN_PROGRESS', createdAt: daysAgo(2) }),
        mk({ status: 'BLOCKED', createdAt: daysAgo(20) }),
        mk({ status: 'DONE', createdAt: daysAgo(20) }),
      ],
      NOW,
    );
    expect(c).toMatchObject({ total: 4, open: 3, done: 1, blocked: 1, inProgress: 1, createdLast7: 2 });
  });

  it('a cancelled task (DONE with the cancelled label) is in the total and not done (P-208)', () => {
    const c = censusCounts([mk({ status: 'DONE' }), mk({ status: 'DONE', labels: ['cancelled'] })], NOW);
    expect(c).toMatchObject({ total: 2, done: 1, open: 0 });
  });

  it('does not count a past due date on a finished ticket as overdue', () => {
    const overdue = { dueAt: daysAgo(1) };
    expect(censusCounts([mk({ ...overdue, status: 'TODO' })], NOW).overdue).toBe(1);
    expect(censusCounts([mk({ ...overdue, status: 'DONE' })], NOW).overdue).toBe(0);
    expect(censusCounts([mk({ ...overdue, status: 'DONE', labels: ['cancelled'] })], NOW).overdue).toBe(0);
  });

  it('counts only LIVE leases', () => {
    const live = { claim: { owner: 'a', leaseId: 'l1', expiresAt: new Date(NOW + 60_000).toISOString() } };
    const dead = { claim: { owner: 'a', leaseId: 'l2', expiresAt: new Date(NOW - 60_000).toISOString() } };
    expect(censusCounts([mk(live as Partial<Task>), mk(dead as Partial<Task>)], NOW).activeLeases).toBe(1);
  });
});

describe('topN and the rankings', () => {
  it('folds the remainder into "other" instead of dropping it', () => {
    const counts = new Map([['a', 10], ['b', 5], ['c', 3], ['d', 1]]);
    const { rows, otherValue, otherKeys } = topN(counts, 2);
    expect(rows).toEqual([{ key: 'a', value: 10 }, { key: 'b', value: 5 }]);
    expect(otherValue).toBe(4);
    expect(otherKeys).toBe(2);
    // Nothing is lost: the parts sum to the whole.
    expect(rows.reduce((s, r) => s + r.value, 0) + otherValue).toBe(19);
  });

  it('ranks the open work by holder, counting an agent lease as ownership', () => {
    const { rows } = assigneeRanking(
      [
        mk({ assignee: 'ana' }),
        mk({ assignee: 'ana' }),
        mk({ assignee: null, claim: { owner: 'x', agent: 'bot-1', leaseId: 'l', expiresAt: daysAgo(-1) } } as Partial<Task>),
        mk({ assignee: null }),
        mk({ assignee: 'ana', status: 'DONE' }), // terminal — not open work
      ],
      8,
    );
    expect(rows).toEqual([
      { key: 'ana', value: 2 },
      { key: 'bot-1', value: 1 },
      { key: 'unassigned', value: 1 },
    ]);
  });

  it('countBy skips nulls, and the status/priority rollups agree with the census', () => {
    expect([...countBy([1, 2, 3], (n) => (n === 2 ? null : 'k')).entries()]).toEqual([['k', 2]]);
    const tasks = [mk({ status: 'TODO', priority: 'high' }), mk({ status: 'TODO', priority: 'low' })];
    expect(statusCounts(tasks).get('TODO')).toBe(2);
    expect(priorityCounts(tasks).get('high')).toBe(1);
  });
});

describe('subtaskProgress (P-208)', () => {
  it('counts a cancelled subtask in the total and not in done', () => {
    const m = subtaskProgress([
      mk({ id: 'T-2', parentTaskId: 'T-1', status: 'DONE' }),
      mk({ id: 'T-3', parentTaskId: 'T-1', status: 'DONE', labels: ['cancelled'] }),
      mk({ id: 'T-4', parentTaskId: 'T-1', status: 'IN_PROGRESS' }),
      mk({ id: 'T-5', parentTaskId: null, status: 'DONE' }),
    ]);
    expect(m.get('T-1')).toEqual({ done: 1, total: 3 });
    expect(m.size).toBe(1);
  });
});
