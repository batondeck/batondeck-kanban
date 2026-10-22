/* The loader's two modes, and the bug that shipped between them.
 *
 * WHY THIS FILE EXISTS. The loader was rendered and eyeballed in three places — the token harness,
 * the boot shell, and under `prefers-reduced-motion` — and every one of them was INDETERMINATE,
 * because that is the mode almost every call site uses. Determinate mode was never rendered at all,
 * and it was broken: the run class was removed with `cls.replace(' bd-run-e', '')`, `'bd-run-e'` has
 * no leading space, so the ember branch kept `.bd-run-e`, the CSS keyframes outranked the
 * `stroke-dasharray` attribute, and that branch looped on a 2.4s cycle while ignoring `value`.
 *
 * It looked correct in passing, which is the point: the OTHER branch's class DID have the leading
 * space, so half the loader tracked the value and half did not. Rule 17 — the cheapest check at the
 * layer that can observe it. jsdom is that layer: this is about which attributes React emits, and
 * nothing here needs a browser.
 */
import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { BootLoader, WaitLoader } from '../brand';

/** The two progress branches, in DOM order: ink first, then ember. */
function branches(container: HTMLElement): SVGPathElement[] {
  // The track is the 4-point diamond (`Z`-closed); the branches are the two open 3-point paths.
  return [...container.querySelectorAll('path')].filter((p) => !(p.getAttribute('d') ?? '').includes('Z'));
}

describe('the loader is determinate when — and only when — it is given a value', () => {
  it('drops BOTH run classes once a value arrives', () => {
    const { container } = render(<WaitLoader value={40} />);
    const [ink, ember] = branches(container);
    // Asserted on BOTH, separately, because the shipped bug affected exactly one of them.
    expect(ink.getAttribute('class') ?? '', 'the ink branch is still animating').not.toMatch(/bd-run-/);
    expect(ember.getAttribute('class') ?? '', 'the ember branch is still animating').not.toMatch(/bd-run-/);
  });

  it('keeps both run classes when there is no value', () => {
    // The positive control (rule 5). Without it, a component that simply never animates would pass
    // the refusal above — and an indeterminate loader that does not move is not a loader.
    const { container } = render(<WaitLoader />);
    const [ink, ember] = branches(container);
    expect(ink.getAttribute('class')).toContain('bd-run-i');
    expect(ember.getAttribute('class')).toContain('bd-run-e');
    expect(ink.getAttribute('stroke-dasharray'), 'CSS owns the dash in indeterminate mode').toBeNull();
  });

  it('both branches track the value, with ink leading', () => {
    const dash = (el: SVGPathElement) => Number((el.getAttribute('stroke-dasharray') ?? '').split(' ')[0]);
    for (const v of [0, 40, 100]) {
      const { container } = render(<WaitLoader value={v} />);
      const [ink, ember] = branches(container);
      expect(ember.getAttribute('stroke-dasharray'), `ember at ${v}`).toBe(`${v} 100`);
      // Ink leads by 18% and clamps — the fork/join has a winner, which is the whole picture.
      expect(dash(ink), `ink at ${v}`).toBeCloseTo(Math.min(100, v * 1.18), 5);
      expect(dash(ink)).toBeGreaterThanOrEqual(dash(ember));
    }
  });

  it('shows the value it was given, not a derived average', () => {
    // The two branches are at different percentages by design, so a mean would put a number on
    // screen that no caller passed in.
    const { container } = render(<BootLoader value={37} />);
    expect(container.querySelector('text')?.textContent).toBe('37%');
    expect(container.querySelector('svg')?.getAttribute('aria-valuenow')).toBe('37');
  });

  it('announces a progressbar only when it has progress to announce', () => {
    const withValue = render(<WaitLoader value={10} />).container.querySelector('svg');
    expect(withValue?.getAttribute('role')).toBe('progressbar');

    const without = render(<WaitLoader />).container.querySelector('svg');
    expect(without?.getAttribute('role'), 'an indeterminate loader is not a progressbar').toBe('img');
    expect(without?.getAttribute('aria-valuenow'), 'a bogus 0% is worse than no value').toBeNull();
    expect(without?.querySelector('text'), 'no figure when there is no figure to show').toBeNull();
  });

  it('clamps out-of-range input instead of drawing outside the track', () => {
    for (const [given, want] of [[-20, '0 100'], [140, '100 100']] as const) {
      const { container } = render(<WaitLoader value={given} />);
      expect(branches(container)[1].getAttribute('stroke-dasharray')).toBe(want);
    }
  });
});

describe('the brand marks announce themselves exactly once', () => {
  it('a decorative mark carries no accessible name', () => {
    // Two cuts stacked in one heading (the sign-in card) otherwise say "BatonDeck" twice.
    const { container } = render(<BootLoader />);
    expect(container.querySelectorAll('[aria-label="BatonDeck"]')).toHaveLength(0);
  });
});
