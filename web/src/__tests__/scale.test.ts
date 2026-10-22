/* The type and spacing scales, enforced.
 *
 * WHY. An audit of the shipped CSS found 20 distinct font sizes (9, 9.5, 10, 10.5, 11, 11.5, 12,
 * 12.5, 13, 13.5, …) and 26 distinct spacing values (1, 3, 5, 6, 7, 9, 10, 11, 13, 15, 18, 22, 26,
 * 34, …). Steps below the just-noticeable difference read as inconsistency rather than hierarchy
 * (Bringhurst, *The Elements of Typographic Style* §3 "Harmony & Counterpoint"; Refactoring UI,
 * "define a limited type scale"), and off-grid spacing is why two panels that should align never
 * quite do (Material 3 layout/spacing; the 4/8pt grid convention).
 *
 * Scales are the classic thing that decays one reasonable-looking commit at a time — nobody adds
 * eleven sizes on purpose. A `font-size: 13px` looks perfectly sensible in isolation; it is only
 * wrong relative to a system nothing was checking. So the system is checked.
 */
import { describe, expect, it } from 'vitest';
import indexCss from '../index.css?raw';

const AREA_SHEETS = import.meta.glob('../styles/*.css', { query: '?raw', import: 'default', eager: true }) as Record<string, string>;

/* Still 7 steps, restated for "Deck" (T-519). Body moves 13.5 → 14: the old scale was tuned for an
   editorial surface and is the main reason the product read as cramped rather than dense. Every
   neighbouring pair is ≥1px apart at the small end and ≥2px above it, so no step sits below the
   just-noticeable difference — which is the property this file exists to hold, not the values. */
const TYPE_SCALE = [10, 11, 12, 14, 16, 20, 28];
const SPACE_SCALE = [0, 2, 4, 8, 12, 16, 24, 32];

/** Comments discuss old values ("was 8.5px") — strip them or the guard flags its own rationale. */
const strip = (s: string) => s.replace(/\/\*[\s\S]*?\*\//g, '');

const SHEETS: Array<[string, string]> = [
  ['index.css', strip(indexCss)],
  ...Object.entries(AREA_SHEETS).map(([k, v]) => [k, strip(v)] as [string, string]),
];

const FONT_SIZE = /(?<![-\w])font-size\s*:\s*([\d.]+)px/g;
const SPACING = /(?<![-\w])(?:padding|margin|gap|row-gap|column-gap)(?:-top|-right|-bottom|-left)?\s*:\s*([^;{}]+)/g;

describe('design scales are closed sets', () => {
  it('reads the stylesheets (vacuity guard)', () => {
    // vitest stubs CSS imports to '' without `css: true` in vite.config.ts; an empty read would
    // make every assertion below pass while checking nothing.
    // >= 1: index.css always. The per-area styles/*.css files went with the screens that owned
    // them, so zero of those is now the correct state — the byte-length check below is what
    // actually guards against a stubbed-empty read.
    expect(SHEETS.length).toBeGreaterThanOrEqual(1);
    expect(SHEETS.reduce((n, [, s]) => n + s.length, 0)).toBeGreaterThan(5000);
  });

  it('uses only the 7 type steps', () => {
    const off: string[] = [];
    for (const [file, css] of SHEETS) {
      for (const m of css.matchAll(FONT_SIZE)) {
        const v = parseFloat(m[1]);
        if (!TYPE_SCALE.includes(v)) off.push(`${file}: font-size ${v}px`);
      }
    }
    expect(off, `off-scale type (allowed: ${TYPE_SCALE.join(', ')}px):\n  ${off.join('\n  ')}`).toEqual([]);
  });

  it('uses only the spacing grid', () => {
    const off: string[] = [];
    for (const [file, css] of SHEETS) {
      for (const m of css.matchAll(SPACING)) {
        for (const raw of m[1].matchAll(/([\d.]+)px/g)) {
          const v = parseFloat(raw[1]);
          // >40px is a layout dimension expressed via padding (a rail inset, a hero band), not a
          // rhythm step — the grid governs rhythm, and forcing 112px to 32px would be nonsense.
          if (v <= 40 && !SPACE_SCALE.includes(v)) off.push(`${file}: ${v}px in "${m[0].split(':')[0].trim()}"`);
        }
      }
    }
    expect(off, `off-grid spacing (allowed: ${SPACE_SCALE.join(', ')}px):\n  ${off.join('\n  ')}`).toEqual([]);
  });

  /* THE SCALE ALSO HAS TO HOLD IN TSX, and until T-519 it did not.
   *
   * This file read stylesheets only, so `style={{ fontSize: 13 }}` was invisible to it — and that is
   * exactly where a scale rots, because inline style is what you reach for when you are not thinking
   * about the system. Measured at the time this arm was added: 13 distinct inline sizes against the
   * 7 the stylesheets were held to (10.5, 11.5, 13, 15, 18, 22, 26 all off-scale, 63 occurrences).
   *
   * The stylesheet arm above could not have caught any of them, which is the point of rule 17: the
   * check belongs at the layer that can observe the failure. Literals only — a computed
   * `fontSize: someVar` is a genuine one-off and is left alone.
   */
  it('uses only the 7 type steps in inline style too', () => {
    const TSX = import.meta.glob('../**/*.tsx', { query: '?raw', import: 'default', eager: true }) as Record<string, string>;
    // Comments quote old values ("was 13px") and JSX carries both comment forms.
    const clean = (t: string) => t.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/[^\n]*/g, '');
    const off: string[] = [];
    let seen = 0;
    for (const [file, raw] of Object.entries(TSX)) {
      for (const m of clean(raw).matchAll(/fontSize: (\d+(?:\.\d+)?)(?![\d.])/g)) {
        seen++;
        const v = parseFloat(m[1]);
        if (!TYPE_SCALE.includes(v)) off.push(`${file}: fontSize ${v}`);
      }
    }
    // Vacuity guard: the glob resolving to nothing would make this pass while checking nothing.
    expect(seen, 'no inline fontSize literals found at all — the glob is wrong').toBeGreaterThan(100);
    expect(off, `off-scale inline type (allowed: ${TYPE_SCALE.join(', ')}):\n  ${off.join('\n  ')}`).toEqual([]);
  });

  it('publishes the scales as tokens so new code can reach for a step, not a number', () => {
    for (const t of ['--fs-micro', '--fs-body', '--fs-title', '--sp-1', '--sp-4', '--sp-7']) {
      expect(indexCss, `${t} must be declared`).toContain(`${t}:`);
    }
  });
});
