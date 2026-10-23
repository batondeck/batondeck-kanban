/**
 * BUG B — the card's meta row rendered a different shape per card.
 *
 * Four real symptoms, one cause: the row was a single flex line of independently-conditional
 * children plus a `flex:1` spacer, so the card's silhouette was a function of its data AND of the
 * pointer. A plain ticket drew an empty ~24px row; the assign chip was `visibility: hidden` until
 * hover, which reserved its width and blanked it, so two identical tickets looked like different
 * components; and the badge family was inconsistent by construction — `.blocked` and `.lease` took
 * colour+border from CSS while racing / sprint / effort took theirs from INLINE styles in the TSX,
 * which is why `Blocked` read as a red outlined pill beside `high` as bare grey text.
 *
 * WHAT IS ASSERTED HERE AND WHAT IS NOT. The always-rendered control and the badge classes are real
 * DOM facts, so they are driven through the REAL `TaskCard`. Whether the result LOOKS level is a
 * browser question no jsdom test can answer, and it is listed as a ceiling rather than dressed up as
 * coverage — the same honesty `card-pending.test.tsx` already applies to the pending veil.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, cleanup } from '@testing-library/react';
import { TaskCard } from '../screens/Board.js';
import boardSrc from '../screens/Board.tsx?raw';
import cssSrc from '../index.css?raw';
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

const mount = (t: Task, selected = false): HTMLElement => {
  const { container } = render(
    <TaskCard t={t} col={{ status: 'TODO' }} selected={selected} onOpen={vi.fn()} agents={[]} onAssign={vi.fn()} />,
  );
  return container;
};

describe('the meta row has ONE shape, whatever the ticket carries', () => {
  it('a plain ticket still renders both zones — the row is never empty', () => {
    // Symptom 1: every child was conditional but the row was not, so a plain card drew a blank strip.
    const c = mount(task());
    expect(c.querySelector('.card-meta'), 'the meta row must exist').toBeTruthy();
    expect(c.querySelector('.card-badges'), 'the badge zone is always present, even when empty').toBeTruthy();
    expect(c.querySelector('.card-who'), 'the who zone is never empty — there is always a control').toBeTruthy();
  });

  it('the assign control is rendered WITHOUT hovering, and is not visibility-hidden', () => {
    // Symptom 2. This is the behavioural core of the bug: the chip used to exist but be blanked, so
    // the card changed shape under the pointer. Deleting the always-render reddens this.
    const chip = mount(task()).querySelector<HTMLElement>('.assign-chip.ghost');
    expect(chip, 'the assign chip must render on a resting card').toBeTruthy();
    expect(chip!.style.visibility, 'the chip must not be blanked while holding its space').not.toBe('hidden');
  });

  it('the always-visible chip is NOT a tab stop until its card is selected', () => {
    // The regression this exists to stop is one I introduced and had to be told about. The chip was
    // `visibility: hidden` until hover, which was doing TWO jobs — hiding it, and keeping ~200 buttons
    // out of the tab sequence, because `visibility: hidden` removes an element from it. Making the
    // chip permanently visible fixed the shape-shifting and silently put a tab stop between every
    // pair of adjacent cards. `tabIndex={-1}` keeps it visible AND out of the sequence; the board's
    // j/k selection is what makes it reachable, matching `.card.selected .marquee`.
    const resting = mount(task()).querySelector<HTMLElement>('.assign-chip.ghost');
    expect(resting!.tabIndex, 'a resting card must not add a tab stop').toBe(-1);
    // POSITIVE CONTROL: selecting the card puts it back in the sequence, so it is reachable, not lost.
    const sel = mount(task(), true).querySelector<HTMLElement>('.assign-chip.ghost');
    expect(sel!.tabIndex, 'a selected card must expose the chip to the keyboard').not.toBe(-1);
  });

  it('a claimed ticket shows the claimant instead — the zone swaps, it does not vanish', () => {
    const c = mount(task({ claim: { agent: 'creepy-gopher', owner: 'u', leaseId: 'l', expiresAt: new Date(Date.now() + 300000).toISOString() } } as Partial<Task>));
    expect(c.querySelector('.card-who'), 'the who zone survives a claim').toBeTruthy();
    expect(c.querySelector('.assign-chip.ghost'), 'a claimed card offers no assign chip').toBeNull();
  });
});

describe('every badge is one CSS family, never an inline colour', () => {
  it('badge variants carry a class, and Board.tsx sets no inline colour/border on them', () => {
    // Symptom 3. The realistic regression is someone adding a badge back with `style={{ color }}`,
    // which reintroduces exactly the "unrelated widgets" look. Scan the badge spans specifically
    // rather than the whole file, so unrelated inline styles elsewhere stay allowed.
    const badgeTags = [...boardSrc.matchAll(/<span className="badge[^"]*"[^>]*>/g)].map((m) => m[0]);
    expect(badgeTags.length, 'no badge markup found — the scan is vacuous').toBeGreaterThan(3);
    const inlineStyled = badgeTags.filter((tag) => /style=\{\{[^}]*(color|border)/.test(tag));
    expect(inlineStyled, 'a badge is styled inline again — move the hue into an index.css variant').toEqual([]);
  });

  it('each badge variant the TSX emits is actually styled in index.css', () => {
    // The half-done-rename guard: a class on one side only is dead styling or an unstyled badge.
    const variants = new Set(
      [...boardSrc.matchAll(/className="badge ([a-z-]+)"/g)].map((m) => m[1]!),
    );
    expect(variants.size, 'no badge variants found — the scan is vacuous').toBeGreaterThan(2);
    for (const v of variants) {
      expect(
        new RegExp(`\\.badge\\.${v}(?![\\w-])`).test(cssSrc),
        `.badge.${v} is emitted by Board.tsx but styled nowhere`,
      ).toBe(true);
    }
  });

  it('the base badge owns the shape, so a variant only has to change the hue', () => {
    const base = /\.badge \{([\s\S]*?)\}/.exec(cssSrc)?.[1] ?? '';
    expect(base, 'the badge base must own its border, or variants diverge in shape again').toContain('border:');
    expect(base, 'the badge base must own its padding').toContain('padding:');
  });
});

describe('the layout classes exist on both sides', () => {
  for (const cls of ['card-meta', 'card-badges', 'card-who', 'card-author']) {
    it(`.${cls} is emitted by Board.tsx AND styled in index.css`, () => {
      const inTsx = new RegExp(`className="${cls}(?![\\w-])`).test(boardSrc);
      const inCss = new RegExp(`\\.${cls}(?![\\w-])`).test(cssSrc);
      expect(inTsx || inCss, `${cls} appears in neither file — it is dead`).toBe(true);
      expect(inTsx, `${cls} is on one side only — a half-done rename`).toBe(inCss);
    });
  }

  it('the spacer div is gone from the CARD — alignment comes from the layout, not a stretched node', () => {
    // Symptom 5: `<span style={{flex:1}}/>` threw the chip across an empty gutter on a badge-less
    // card. `margin-left: auto` on `.card-who` does the same job and holds when the row is empty.
    //
    // SCOPED TO `TaskCard`'s BODY, not the file. Board.tsx legitimately uses `flex: 1` spacers in
    // four other places (sprint rows, headers); asserting against the whole source would fail on
    // code this ticket never touched, which is a guard that cries wolf rather than one that holds.
    const body = /export function TaskCard[\s\S]*?\nfunction DraggableCard/.exec(boardSrc)?.[0] ?? '';
    expect(body, 'TaskCard body not found — the slice is vacuous').toContain('card-meta');
    expect(body, 'the flex:1 spacer is back inside the card').not.toContain('style={{ flex: 1 }}');
    expect(/\.card-who \{[^}]*margin-left: auto/.test(cssSrc), '.card-who must right-align itself').toBe(true);
  });
});
