/**
 * T-257: the sprints rail, rendered with plain props (the component is pure — no queries, no router).
 * Pins the four behaviours the ticket names: done/total derived client-side from the tasks the board
 * already holds, the status pill, the PROPOSED row's "awaiting approval" link into the proposal task,
 * and click-sets-the-filter (with click-again-clears).
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { SprintsRail } from '../screens/Board.js';
import type { Sprint, Task } from '../types.js';

afterEach(cleanup);

const sprint = (over: Partial<Sprint>): Sprint => ({
  id: 'S-1',
  projectId: 'P-1',
  boardId: 'B-1',
  name: 'Sprint 1',
  goal: 'ship the widget',
  acceptance: 'both members DONE',
  status: 'ACTIVE',
  proposalTaskId: null,
  approvedBy: null,
  memberCount: 2,
  checkpoint: null,
  eventCursor: null,
  createdBy: 'a',
  createdAt: '2026-08-03T00:00:00Z',
  startedAt: null,
  closedAt: null,
  updatedBy: 'a',
  updatedAt: '2026-08-03T00:00:00Z',
  version: 1,
  ...over,
});
const task = (id: string, status: Task['status'], sprintIds?: string[]): Task =>
  ({ id, status, sprintIds, labels: [], blockedBy: [], blocks: [] }) as unknown as Task;

describe('SprintsRail', () => {
  it('derives done/total from the tasks the board already fetched — members only', () => {
    render(
      <SprintsRail
        sprints={[sprint({})]}
        tasks={[task('T-1', 'DONE', ['S-1']), task('T-2', 'READY', ['S-1']), task('T-3', 'DONE')]}
        filterSprint=""
        onFilter={() => {}}
        onOpenTask={() => {}}
      />,
    );
    expect(screen.getByText('Sprint 1')).toBeTruthy();
    expect(screen.getByText('ACTIVE')).toBeTruthy(); // the status pill
    expect(screen.getByText(/^\s*1\/2\s*$/)).toBeTruthy(); // T-3 is DONE but NOT a member — it must not count
    expect(screen.getByText('ship the widget')).toBeTruthy(); // goal, inside the expando
  });

  it('a PROPOSED row links the proposal task, and clicking it opens the drawer', () => {
    const onOpenTask = vi.fn();
    render(
      <SprintsRail
        sprints={[sprint({ id: 'S-2', name: 'Sprint 2', status: 'PROPOSED', proposalTaskId: 'T-9' })]}
        tasks={[]}
        filterSprint=""
        onFilter={() => {}}
        onOpenTask={onOpenTask}
      />,
    );
    fireEvent.click(screen.getByText('awaiting approval: T-9'));
    expect(onOpenTask).toHaveBeenCalledWith('T-9');
  });

  it('clicking a row sets the filter; clicking the active row clears it', () => {
    const onFilter = vi.fn();
    const { rerender } = render(
      <SprintsRail sprints={[sprint({})]} tasks={[]} filterSprint="" onFilter={onFilter} onOpenTask={() => {}} />,
    );
    fireEvent.click(screen.getByText('Sprint 1'));
    expect(onFilter).toHaveBeenLastCalledWith('S-1');
    rerender(
      <SprintsRail sprints={[sprint({})]} tasks={[]} filterSprint="S-1" onFilter={onFilter} onOpenTask={() => {}} />,
    );
    fireEvent.click(screen.getByText('Sprint 1'));
    expect(onFilter).toHaveBeenLastCalledWith('');
  });

  it('empty and error states say so instead of rendering a blank rail', () => {
    render(<SprintsRail sprints={[]} tasks={[]} filterSprint="" onFilter={() => {}} onOpenTask={() => {}} />);
    expect(screen.getByText('No sprints on this board.')).toBeTruthy();
    cleanup();
    render(<SprintsRail sprints={[]} tasks={[]} filterSprint="" onFilter={() => {}} onOpenTask={() => {}} error />);
    expect(screen.getByText('Couldn’t load sprints.')).toBeTruthy();
  });
});

/**
 * T-330 — the activation control. Approving was already possible (the proposal task's ordinary
 * REVIEW→DONE control in the drawer); STARTING the approved sprint had no button anywhere, so the
 * ceremony stopped halfway. These pin the gate, because a Start button that fires before approval
 * would just move the failure from the UI to an INVALID_TRANSITION the user sees after pressing it.
 */
describe('SprintsRail: the sprint lifecycle (T-330)', () => {
  const proposed = (over: Partial<Sprint> = {}) =>
    sprint({ id: 'S-9', status: 'PROPOSED', proposalTaskId: 'T-99', ...over });

  it('REFUSAL: Start is disabled while the proposal task is not DONE, and says why', () => {
    render(
      <SprintsRail
        sprints={[proposed()]}
        tasks={[task('T-99', 'REVIEW')]}
        filterSprint=""
        onFilter={() => {}}
        onOpenTask={() => {}}
        onLifecycle={() => {}}
      />,
    );
    const start = screen.getByRole('button', { name: /start sprint/i });
    expect(start).toBeDisabled();
    expect(start.getAttribute('title')).toMatch(/approve T-99 first/i);
  });

  it('POSITIVE CONTROL: once the proposal task is DONE, Start is enabled and fires ACTIVE', () => {
    const onLifecycle = vi.fn();
    render(
      <SprintsRail
        sprints={[proposed()]}
        tasks={[task('T-99', 'DONE')]}
        filterSprint=""
        onFilter={() => {}}
        onOpenTask={() => {}}
        onLifecycle={onLifecycle}
      />,
    );
    const start = screen.getByRole('button', { name: /start sprint/i });
    expect(start).not.toBeDisabled();
    fireEvent.click(start);
    expect(onLifecycle).toHaveBeenCalledTimes(1);
    expect(onLifecycle.mock.calls[0]?.[0]?.id).toBe('S-9');
    expect(onLifecycle.mock.calls[0]?.[1]).toBe('ACTIVE');
  });

  it('REFUSAL: a sprint with no proposal task bound cannot be started', () => {
    render(
      <SprintsRail
        sprints={[proposed({ proposalTaskId: null })]}
        tasks={[]}
        filterSprint=""
        onFilter={() => {}}
        onOpenTask={() => {}}
        onLifecycle={() => {}}
      />,
    );
    const start = screen.getByRole('button', { name: /start sprint/i });
    expect(start).toBeDisabled();
    expect(start.getAttribute('title')).toMatch(/no proposal task bound/i);
  });

  it('an ACTIVE sprint offers Close, not Start', () => {
    const onLifecycle = vi.fn();
    render(
      <SprintsRail
        sprints={[sprint({ id: 'S-9', status: 'ACTIVE' })]}
        tasks={[task('T-1', 'DONE', ['S-9'])]}
        filterSprint=""
        onFilter={() => {}}
        onOpenTask={() => {}}
        onLifecycle={onLifecycle}
      />,
    );
    expect(screen.queryByRole('button', { name: /start sprint/i })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: /^close$/i }));
    expect(onLifecycle.mock.calls[0]?.[1]).toBe('CLOSED');
  });

  it('closing with non-terminal members is offered, and the title says the server will warn', () => {
    render(
      <SprintsRail
        sprints={[sprint({ id: 'S-9', status: 'ACTIVE' })]}
        tasks={[task('T-1', 'DONE', ['S-9']), task('T-2', 'READY', ['S-9'])]}
        filterSprint=""
        onFilter={() => {}}
        onOpenTask={() => {}}
        onLifecycle={() => {}}
      />,
    );
    // Not disabled: the core returns a warning here, it does not refuse. The UI must not invent a
    // stricter rule than the server's.
    const close = screen.getByRole('button', { name: /^close$/i });
    expect(close).not.toBeDisabled();
    expect(close.getAttribute('title')).toMatch(/1 loaded member\(s\) are not terminal/i);
  });

  it('POSITIVE CONTROL: the busy sprint disables its own controls, others are unaffected', () => {
    render(
      <SprintsRail
        sprints={[proposed(), sprint({ id: 'S-8', status: 'ACTIVE' })]}
        tasks={[task('T-99', 'DONE')]}
        filterSprint=""
        onFilter={() => {}}
        onOpenTask={() => {}}
        onLifecycle={() => {}}
        busySprintId="S-9"
      />,
    );
    expect(screen.getByRole('button', { name: /starting…/i })).toBeDisabled();
    expect(screen.getByRole('button', { name: /^close$/i })).not.toBeDisabled();
  });

  it('REFUSAL: with no handler wired, no lifecycle control renders at all', () => {
    // The render test and any read-only embedding must not grow buttons that post nothing.
    render(
      <SprintsRail sprints={[proposed()]} tasks={[task('T-99', 'DONE')]} filterSprint="" onFilter={() => {}} onOpenTask={() => {}} />,
    );
    expect(screen.queryByRole('button', { name: /start sprint/i })).toBeNull();
  });

  it('a CLOSED sprint offers no lifecycle control', () => {
    render(
      <SprintsRail
        sprints={[sprint({ id: 'S-9', status: 'CLOSED' })]}
        tasks={[]}
        filterSprint=""
        onFilter={() => {}}
        onOpenTask={() => {}}
        onLifecycle={() => {}}
      />,
    );
    expect(screen.queryByRole('button', { name: /start sprint|^close$|abandon/i })).toBeNull();
  });
});
