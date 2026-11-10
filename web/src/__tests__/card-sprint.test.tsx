/**
 * A task carried from a CLOSED or ABANDONED sprint to a successor keeps the ended sprint in its
 * sprintIds for history, oldest first, so the sprint it is in now is the LAST id. The card's sprint
 * badge (and the board's name lookup behind it) named the first, so a carried ticket wore the
 * closed sprint's badge.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, cleanup } from '@testing-library/react';
import { TaskCard, currentSprintId } from '../screens/Board.js';
import type { Task } from '../types.js';

beforeEach(cleanup);

const task = (over: Partial<Task> = {}): Task =>
  ({
    id: 'T-1', projectId: 'P-1', boardId: 'B-1', columnId: 'c1', status: 'TODO',
    title: 'a task', description: '', priority: 'normal', labels: [], requiredCapabilities: [],
    blockedBy: [], blocks: [], artifacts: [], version: 3, assignee: null, claim: null,
    createdBy: 'u', createdAt: '2026-09-01T00:00:00.000Z', updatedAt: '2026-09-01T00:00:00.000Z',
    order: 'a', ...over,
  }) as unknown as Task;

const badge = (t: Task, sprintName?: string | null): string | null => {
  const { container } = render(
    <TaskCard t={t} col={{ status: 'TODO' }} sprintName={sprintName} selected={false} onOpen={vi.fn()} agents={[]} onAssign={vi.fn()} canAssign />,
  );
  return container.querySelector('.badge.sprint')?.textContent?.trim() ?? null;
};

describe('the sprint badge names the sprint the task is in now', () => {
  it('a carried task shows its successor sprint, not the ended one', () => {
    expect(badge(task({ sprintIds: ['S-closed', 'S-next'] }))).toBe('S-next');
  });

  it('currentSprintId is the last of sprintIds, undefined with none', () => {
    expect(currentSprintId(task({ sprintIds: ['S-closed', 'S-next'] }))).toBe('S-next');
    expect(currentSprintId(task({ sprintIds: ['S-1'] }))).toBe('S-1');
    expect(currentSprintId(task({ sprintIds: [] }))).toBeUndefined();
    expect(currentSprintId(task())).toBeUndefined();
  });

  it('POSITIVE CONTROL: a resolved name wins over the id, and no sprint shows no badge', () => {
    expect(badge(task({ sprintIds: ['S-closed', 'S-next'] }), 'Sprint 2')).toBe('Sprint 2');
    expect(badge(task())).toBeNull();
  });
});
