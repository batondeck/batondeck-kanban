/**
 * BUG A REPRO — the "+ assign" chip on a board card.
 *
 * The owner clicked it on the live board and nothing happened. This file drives the REAL `TaskCard`
 * and the REAL `AssigneePicker` (both exported from Board.tsx purely so a test can mount them) rather
 * than a hand-built stand-in: the whole question is whether the chip's own wiring reaches the
 * mutation, and a self-supplied button would answer a question nobody asked (rule 12, and the vacuous
 * guards this repo has already collected in `card-pending.test.tsx`).
 *
 * `TaskCard` is the right unit here, not `Board`. `DraggableCard` — the dnd-kit wrapper — is what sits
 * between the board and this card, and it is deliberately OUT of scope: it attaches drag listeners to
 * a parent node, so if it swallowed the click the failure would be in that composition, not here.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';
import { TaskCard } from '../screens/Board.js';
import type { AgentEntry } from '../agentStatus.js';
import type { Task } from '../types.js';
import boardSrc from '../screens/Board.tsx?raw';
import cssSrc from '../index.css?raw';

beforeEach(cleanup);

const task = (over: Partial<Task> = {}): Task =>
  ({
    id: 'T-1',
    projectId: 'P-1',
    boardId: 'B-1',
    columnId: 'c1',
    status: 'TODO',
    title: 'a task',
    description: '',
    priority: 'normal',
    labels: [],
    requiredCapabilities: [],
    blockedBy: [],
    blocks: [],
    artifacts: [],
    version: 3,
    assignee: null,
    claim: null,
    createdBy: 'u',
    createdAt: '2026-09-01T00:00:00.000Z',
    updatedAt: '2026-09-01T00:00:00.000Z',
    order: 'a',
    ...over,
  }) as unknown as Task;

const liveAgent = (name: string): AgentEntry => ({
  name,
  state: 'available',
  lastSeenAt: new Date().toISOString(),
  tool: null,
});

function mount(over: { agents?: AgentEntry[]; t?: Task } = {}): { onAssign: ReturnType<typeof vi.fn>; onOpen: ReturnType<typeof vi.fn> } {
  const onAssign = vi.fn();
  const onOpen = vi.fn();
  render(
    <TaskCard
      t={over.t ?? task()}
      col={{ status: 'TODO' }}
      selected={false}
      onOpen={onOpen}
      agents={over.agents ?? [liveAgent('creepy-gopher'), liveAgent('opus-bot')]}
      onAssign={onAssign}
      canAssign
    />,
  );
  return { onAssign, onOpen };
}

describe('the "+ assign" chip opens its menu and assigns', () => {
  it('clicking the ghost chip opens the agent menu', () => {
    mount();
    fireEvent.click(screen.getByTitle('Assign to an agent'));
    expect(screen.queryByRole('menu'), 'the click must open the assign menu').not.toBeNull();
  });

  it('picking an agent calls onAssign with the task id, version and name', () => {
    const { onAssign } = mount();
    fireEvent.click(screen.getByTitle('Assign to an agent'));
    const item = screen.getAllByRole('menuitem').find((n) => n.textContent?.includes('creepy-gopher'));
    expect(item, 'the live agent must be offered').toBeTruthy();
    fireEvent.click(item!);
    expect(onAssign).toHaveBeenCalledWith('T-1', 3, 'creepy-gopher');
  });

  it('picking an agent does NOT also open the drawer — the seam the portal moved', () => {
    // The menu now lives in `document.body`, not inside the card, so a menu item's NATIVE click no
    // longer bubbles through the card at all — but React's synthetic event still bubbles along the
    // component tree, through the wrapper `<span onClick={stop}>`. That is exactly the path the
    // portal changed, and the chip-click test above does not cover it: this asserts the item path.
    const { onAssign, onOpen } = mount();
    fireEvent.click(screen.getByTitle('Assign to an agent'));
    fireEvent.click(screen.getAllByRole('menuitem')[0]!);
    expect(onAssign).toHaveBeenCalled();
    expect(onOpen, 'assigning must not fling the task drawer open behind the menu').not.toHaveBeenCalled();
  });

  it('opening the menu does NOT also open the task drawer', () => {
    // The chip lives inside the card, and the card's own onClick opens the drawer. If the
    // stop-propagation wrapper regresses, assigning would fling the drawer open over the menu.
    const { onOpen } = mount();
    fireEvent.click(screen.getByTitle('Assign to an agent'));
    expect(onOpen).not.toHaveBeenCalled();
  });

  it('with NO live agents the menu still opens, with the free-type input', () => {
    // The "looks empty" branch: zero live agents must still give the operator a way through, not a
    // dead control.
    mount({ agents: [] });
    fireEvent.click(screen.getByTitle('Assign to an agent'));
    expect(screen.queryByRole('menu')).not.toBeNull();
    expect(screen.getByPlaceholderText('agent name / email')).toBeTruthy();
  });
});

/**
 * THE ACTUAL BUG, and why every test above passes while the owner saw nothing.
 *
 * jsdom has no layout engine, so those tests can only prove the STATE flips — `open` becomes true,
 * the menu mounts, picking an agent calls the mutation. All of that was already correct. What was
 * broken is WHERE the menu paints, which is a pure layout fact:
 *
 *   1. the ghost chip is only visible while the card is hovered (`ghostVisible={hover || selected}`),
 *      so clicking it GUARANTEES `.card:hover` is matching;
 *   2. `.card:hover` sets `transform: translateY(-1px)` (index.css, inside the
 *      `prefers-reduced-motion: no-preference` block — i.e. for essentially every user);
 *   3. a non-`none` transform makes that element a CONTAINING BLOCK for `position: fixed`
 *      descendants (CSS Position §4);
 *   4. `.assign-menu` is `position: fixed` and its `top`/`left` come from `getBoundingClientRect()`,
 *      which is measured in VIEWPORT coordinates.
 *
 * So the menu is offset by the card's own viewport position — for a card partway down a column that
 * is hundreds of pixels away, off-screen or behind a neighbouring column. It opened every time; it
 * was simply never where the pointer was looking.
 *
 * THE GUARD IS THE COUPLING, not "use a portal". A portal is only *required* while some ancestor of
 * the menu can carry a transform. Stating it that way means the test relaxes honestly if the lift is
 * ever removed, and fails if the portal is removed while the lift remains — which is the regression.
 */
describe('the assign menu escapes any transformed ancestor', () => {
  // Rules setting a non-`none` transform on a selector mentioning `.card`. `.drag-overlay .card` is
  // excluded: dnd-kit renders that clone OUTSIDE the column, and it never hosts this menu.
  const transformedCardRules = cssSrc
    .split('}')
    .map((chunk) => chunk.trim())
    .filter((chunk) => {
      const [selector = '', body = ''] = chunk.split('{');
      if (!/\.card(?![\w-])/.test(selector)) return false;
      if (selector.includes('drag-overlay')) return false;
      const m = /(?:^|[;{\s])transform:\s*([^;]+)/.exec(body);
      return !!m && m[1]!.trim() !== 'none';
    });

  it('DOCUMENTS THE HAZARD: at least one .card rule sets a transform', () => {
    // If this ever reaches zero the hazard is gone and the portal requirement below may be relaxed
    // deliberately — not silently. Today it is `.card:hover`, the one state a click cannot avoid.
    expect(
      transformedCardRules.length,
      'no .card transform found — re-read the portal requirement below before relaxing it',
    ).toBeGreaterThan(0);
  });

  it('so the menu MUST be portalled out of the card subtree', () => {
    expect(
      /createPortal\(/.test(boardSrc),
      'a `position: fixed` menu inside a transformed ancestor is positioned relative to that ancestor, ' +
        'not the viewport — it must be portalled to document.body',
    ).toBe(true);
  });

  it('and the portal must carry the assign menu specifically', () => {
    const portalArg = /createPortal\(\s*([\s\S]{0,400})/.exec(boardSrc)?.[1] ?? '';
    expect(portalArg, 'a portal exists but does not carry the assign menu').toContain('assign-menu');
  });
});
