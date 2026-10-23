/**
 * The optimistic-move affordances: a spinner while the write is in flight, and a one-shot drop ripple.
 *
 * WHY THE SPINNER IS BEHAVIOUR AND NOT DECORATION. The move is optimistic — `useTaskAction`'s `onMutate`
 * redraws the card in its destination lane immediately, before the server has agreed. Without a pending
 * marker the UI asserts a success it does not have, and a rejected move (STALE version, WIP_EXCEEDED,
 * INVALID_TRANSITION) snaps the card back with no explanation the user can connect to anything. The
 * rollback itself already existed; what was missing was any sign that the outcome was still unknown.
 *
 * The ripple is genuinely decorative and is asserted only to be aria-hidden and non-blocking, because
 * decoration that reaches assistive tech or eats clicks stops being decoration.
 */
import { describe, it, expect } from 'vitest';
// Vite's `?raw` import rather than node:fs — the web tsconfig carries no node typings, and adding them
// to read one file would widen the type surface of the whole SPA for a test's convenience.
import boardSrc from '../screens/Board.tsx?raw';
import cssSrc from '../index.css?raw';

// Deliberately NOT rendering <Board/>: it drags in dnd-kit, framer, the palette and a live stream, none
// of which is under test. The card's contract is markup-level, and the two things that are genuinely
// wiring are asserted against the SOURCE below instead.

/**
 * CLASS NAMES ARE ASSERTED ACROSS FILES, NOT AGAINST SELF-SUPPLIED MARKUP.
 *
 * The tests that used to live here rendered their own `<div className="card-saving">` and asserted the
 * class was present — so renaming or deleting the class in Board.tsx and index.css left the veil and
 * spinner gone from the real UI with the suite green. Review caught it: all three class names could be
 * broken without a single failure. Same self-supplied-dependency shape as the earlier vacuous guards in
 * this lane, and the sixth instance overall.
 *
 * What IS checkable without a browser is the COUPLING: a class the component emits must exist in the
 * stylesheet. Rename one side only — the realistic regression — and this fails.
 *
 * WHAT IT DOES NOT CATCH, measured rather than assumed (rule 11). Renaming the class on the CSS side
 * still passes when the class is mentioned in a SECOND rule — `.card-ripple` also appears under
 * `prefers-reduced-motion`, so deleting its main rule leaves `inCss` true and the ripple silently
 * unstyled. Verified by mutation: TSX-side rename fails as intended, CSS-side rename does not.
 *
 * Not fixed by counting rules or parsing the stylesheet, because that starts approximating a CSS parser
 * to guard a class name — and the thing actually at risk here (does the veil LOOK right) is a browser
 * question this file can never answer. Stated as a known ceiling instead of dressed up as coverage.
 */
describe('the pending/ripple classes exist on BOTH sides', () => {
  for (const cls of ['card-saving', 'card-saving-spin', 'card-ripple']) {
    it(`.${cls} is emitted by Board.tsx AND styled in index.css`, () => {
      // BOUNDARY-MATCHED, NOT `includes`. The first version of this used `boardSrc.includes(cls)` and
      // passed when the class was renamed `card-saving` -> `card-savingX`, because the old name is a
      // SUBSTRING of the new one. Seventh vacuous guard in this lane, and the only one caused by
      // something as ordinary as a prefix match. `(?![\w-])` is what makes a rename observable.
      const boundary = (src: string, prefix: string) => new RegExp(`${prefix}${cls}(?![\\w-])`).test(src);
      const inTsx = boundary(boardSrc, 'className="');
      const inCss = boundary(cssSrc, '\\.');
      expect(inTsx || inCss, `${cls} appears in neither file — it is dead`).toBe(true);
      expect(inTsx, `${cls} is on one side only — a half-done rename`).toBe(inCss);
    });
  }

  it('the veil carries an accessible role, and the ripple is hidden from AT', () => {
    expect(boardSrc, 'a spinner with no role leaves screen readers with nothing').toContain('role="status"');
    expect(boardSrc).toContain('aria-busy');
    expect(boardSrc, 'decoration must not reach assistive tech').toMatch(/card-ripple[\s\S]{0,80}aria-hidden/);
  });
});

/**
 * Guards on the SOURCE, because the two failure modes that matter are wiring, not rendering — and this
 * lane has now been bitten three times by tests that supplied their own dependency and so could not see
 * what production wired (the `app.log` no-op, the `presenceFetch` shape, the unwired publisher).
 */
describe('the wiring that makes the spinner correct', () => {
  /**
   * The two key-guards that lived here were REGEXES over source text, and review restored the exact bug
   * spelled differently — a cast instead of the generic, `ctx.key` mentioned in a condition while a
   * different key was restored — with both staying green. They tested a spelling, not a behaviour.
   *
   * They are gone. `optimistic-key.test.ts` asserts the property against a real QueryClient (the board's
   * entry actually changes; Analytics' entry does not; the snapshot round-trips) and catches both
   * spellings. Keeping a guard that has been proven not to bite is worse than having none — it reads as
   * cover.
   */
  /**
   * THE VEIL STUCK ON STAGING, and the reason is WHERE the clear lived, not which callback it was.
   *
   * It was `mutate(vars, { onSettled })` — a PER-CALL callback. React Query does not fire those if the
   * observer unmounts before the mutation finishes, and a card DOES unmount when it changes column
   * (AnimatePresence moves it between SortableContexts). Observed: drag Backlog → Review, the server
   * refuses the illegal transition, the card correctly rolls back, and the veil never clears — the
   * exact "spinner forever" case choosing settled-over-success was meant to prevent.
   *
   * The clear now lives in the MUTATION-level `onSettled`, which always fires and receives `variables`.
   */
  it('the veil is cleared by the MUTATION-level onSettled, not a per-call callback', () => {
    const src = boardSrc.replace(/\/\/[^\n]*/g, '').replace(/\/\*[\s\S]*?\*\//g, '');
    const call = /moveCard\.mutate\([\s\S]*?\);/.exec(src)?.[0] ?? '';
    expect(call, 'no moveCard.mutate call found').not.toBe('');
    expect(call, 'per-call callbacks do not fire when the card unmounts mid-move')
      .not.toMatch(/onSettled|onSuccess|onError/);

    const mutation = /const moveCard = useMutation\(\{[\s\S]*?\n {2}\}\);/.exec(src)?.[0] ?? '';
    expect(mutation, 'no moveCard useMutation found').not.toBe('');
    const settled = /onSettled: \([^)]*\) => \{[\s\S]*?\n {4}\},/.exec(mutation)?.[0] ?? '';
    expect(settled, 'the mutation must define onSettled').not.toBe('');
    expect(settled, 'onSettled must clear the veil for the settled task').toContain('setMovingIds');
  });

  it('a NET clears the veil whenever no move is pending — a stuck veil needs a page reload to escape', () => {
    const src = boardSrc.replace(/\/\/[^\n]*/g, '').replace(/\/\*[\s\S]*?\*\//g, '');
    expect(src, 'without this, any future gap in the clear path strands a card greyed out forever')
      .toMatch(/if \(moveCard\.isPending\) return;[\s\S]{0,120}setMovingIds/);
  });

  it('the board no longer refetches ITS OWN three queries off the nudge — that is the snapshot`s job', () => {
    const src = boardSrc;
    // Scope to the streamApi.stream(...) CALL, not to a slice of the file.
    //
    // The first version of this sliced at `src.indexOf('setStreamLive')`, which finds the useState
    // declaration near the TOP of the file — so it asserted against a region that never contained the
    // handler and passed with the refetches restored. Fourth vacuous guard in this lane; mutation
    // testing caught every one. Anchoring on the actual call is what makes it bite.
    const handler = /streamApi\.stream\([\s\S]*?\n {6}\),/.exec(src)?.[0] ?? '';
    expect(handler, 'no streamApi.stream(...) call found — the anchor moved').not.toBe('');
    const code = handler.replace(/\/\/[^\n]*/g, '');
    expect(code, 'invalidating here fires exactly the three refetches the snapshot exists to remove')
      .not.toMatch(/invalidateQueries\(\{\s*queryKey:\s*\['(feed|board|tasks)', projectId, boardId\]/);
    expect(code, 'the snapshot must be written to the cache').toContain("qc.setQueryData(['feed', projectId, boardId]");
  });

  /**
   * THE REBOUND. Reported from prod: on a successful drop the card visibly flew back to its ORIGINAL
   * column while simultaneously appearing in the target.
   *
   * dnd-kit's default drop animation tweens the overlay back to the active node's last measured rect.
   * Correct for a no-op drop; wrong here, because the move is OPTIMISTIC — the card has already
   * re-rendered in its destination by the time the overlay animates, so the remembered rect points at
   * where it used to be. `dropAnimation={null}` hands the transition to the card's own enter animation.
   *
   * Asserted on the source because the alternative is measuring a 250ms tween in a browser, which this
   * suite cannot reach — and the regression (someone removes the prop) is exactly what a source check
   * catches.
   */
  it('the DragOverlay disables dnd-kit’s drop animation, or the card rebounds to its old column', () => {
    const src = boardSrc.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/[^\n]*/g, '');
    const overlay = /<DragOverlay[^>]*>/.exec(src)?.[0] ?? '';
    expect(overlay, 'no DragOverlay found — the anchor moved').not.toBe('');
    expect(overlay, 'without dropAnimation={null} the overlay tweens to a stale rect').toMatch(/dropAnimation=\{null\}/);
  });
});
