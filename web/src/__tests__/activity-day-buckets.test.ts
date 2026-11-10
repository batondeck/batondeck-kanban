/**
 * The activity feed files events under the VIEWER's day.
 *
 * It bucketed and labelled by UTC date while each row's time reads local, so at 17:30 in UTC-8
 * (01:30 UTC the next day) an action from 15:30 that afternoon sat under "Yesterday", and from
 * 16:00 local every new action opened a fresh "Today" above the rest of the same local day.
 */
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { dayLabel, groupByDay } from '../screens/ActivityLog.js';
import type { ActivityEvent } from '../types.js';

const ev = (id: string, ts: string) => ({ id, ts, type: 'task.moved', actor: 'U-1', agent: null, taskId: 'T-1', data: {} }) as unknown as ActivityEvent;

// Node reads TZ afresh when it changes. Reached through globalThis: the web tsconfig has no node typings.
const env = (globalThis as unknown as { process: { env: Record<string, string | undefined> } }).process.env;
const realTZ = env.TZ;
beforeEach(() => {
  env.TZ = 'America/Los_Angeles';
  vi.useFakeTimers();
  vi.setSystemTime(new Date('2026-11-10T01:30:00Z')); // 17:30 on Nov 9 in Los Angeles
});
afterEach(() => {
  vi.useRealTimers();
  if (realTZ === undefined) delete env.TZ; else env.TZ = realTZ;
});

describe('activity day buckets', () => {
  it('two actions on the same local afternoon share one "Today" group', () => {
    const groups = groupByDay([ev('a', '2026-11-10T00:30:00Z'), ev('b', '2026-11-09T23:30:00Z')]); // 16:30 and 15:30 local
    expect(groups.map((g) => [dayLabel(g.key), g.items.map((i) => i.id)])).toEqual([['Today', ['a', 'b']]]);
  });

  it('the evening before is "Yesterday", and an older day is its local date', () => {
    const groups = groupByDay([ev('y', '2026-11-09T05:00:00Z'), ev('o', '2026-11-07T20:00:00Z')]); // Nov 8 21:00, Nov 7 12:00 local
    expect(groups.map((g) => dayLabel(g.key))).toEqual(['Yesterday', new Date(2026, 10, 7).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })]);
  });
});
