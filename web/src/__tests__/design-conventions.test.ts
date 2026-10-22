/**
 * DESIGN-SYSTEM CONVENTIONS THAT A TYPECHECK AND A RENDER TEST BOTH PASS THROUGH.
 *
 * `.label` in `index.css` is a **tag chip** — 10px JetBrains Mono on a filled, bordered, padded
 * surface, meant for a task label or a capability pill. It is NOT a form label. Putting it on a
 * `<label htmlFor>` renders the field caption as a tiny monospace pill: legible, obviously wrong,
 * and invisible to every check we run. `tsc` sees a valid string, the render tests query by
 * accessible name (which the class does not affect), and no CSS linter runs against TSX.
 *
 * It reached `main` once already — `Oncall.tsx` shipped that way in the on-call PR — and then the
 * whole setup drawer was written against the same wrong assumption, ~30 fields. That is the shape
 * this file exists to stop: not one typo, but a wrong convention copied.
 *
 * The in-repo precedent is `Chronicle.tsx`: `<Panel pad>` + `<Stack gap={12}>` + a bare
 * `<label htmlFor>` + `.input`. Follow that.
 *
 * LEXICAL, deliberately. The property is "which class is on which element", which no rendered
 * assertion can observe without pinning computed styles — and pinning computed styles against a
 * design system that is expected to evolve is a guard that cries wolf on every palette change.
 */
import { describe, it, expect } from 'vitest';

/**
 * `import.meta.glob` rather than `readdirSync` — the same mechanism `tokens.test.ts` uses. It
 * resolves against THIS MODULE, so the scan cannot depend on the process working directory (the
 * first version of this file scanned '/src/screens' and collapsed before a single case ran).
 */
const SOURCES = import.meta.glob('../screens/**/*.tsx', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;
const SHEET = import.meta.glob('../index.css', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

/** Comments are not code: a commented-out example must not satisfy or trip a lexical guard. */
const strip = (src: string): string =>
  src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');

const entries = Object.entries(SOURCES);

describe('the .label class is a tag chip, never a form label', () => {
  it('is non-empty and finds the screens — otherwise every assertion below is vacuous', () => {
    // Rule 18: a scan that cannot find its population returns a correct, meaningless pass. The
    // first draft of this file scanned the wrong directory entirely; without this case that would
    // have read as a pass rather than as an error.
    expect(entries.length).toBeGreaterThan(15);
    expect(entries.some(([f]) => f.endsWith('Oncall.tsx'))).toBe(true);
    expect(entries.some(([f]) => f.includes('oncall/IntegrationForm'))).toBe(true);
    expect(Object.values(SHEET)[0]?.length ?? 0).toBeGreaterThan(1000);
  });

  it('no <label> element carries className="label"', () => {
    const bad: string[] = [];
    for (const [f, raw] of entries) {
      // `<label ... className="label"` across newlines, up to the tag's closing bracket.
      for (const m of strip(raw).matchAll(/<label\b[^>]*>/g)) {
        if (/className=(["'])label\1/.test(m[0])) bad.push(`${f}: ${m[0].replace(/\s+/g, ' ').slice(0, 70)}`);
      }
    }
    expect(
      bad,
      'a <label> is using the tag-chip class — it will render as a 10px mono pill. Use a bare ' +
        '<label htmlFor="…">, as Chronicle.tsx does:\n  ' + bad.join('\n  '),
    ).toEqual([]);
  });

  it('.label is still the tag chip it claims to be — the assumption this file rests on', () => {
    // If a redesign ever turns `.label` INTO a form-label class, the guard above becomes wrong
    // rather than merely unnecessary. Pin the property that makes it true.
    const css = Object.values(SHEET)[0] ?? '';
    const rule = /^\.label \{([^}]*)\}/m.exec(css);
    expect(rule?.[1], '.label rule moved or was deleted — re-read this file before trusting it').toBeTruthy();
    expect(rule![1]).toMatch(/var\(--mono\)/);
    expect(rule![1]).toMatch(/border/);
  });

  /**
   * A `nowrap` TABLE MUST SIT IN AN `overflow-x` WRAPPER, or it is clipped rather than scrolled.
   *
   * Reported from the deployed page: the delivery log's action column (Replay / Download) was cut
   * off against the drawer edge. `.drawer` is a fixed 520px with `overflow-y` only (index.css:441),
   * so a wider table has nowhere to go. `index.css:603` states the rule for markdown tables —
   * "block+overflow keeps a wide table scrolling inside itself instead of the whole drawer body" —
   * and `.chr-table-wrap` is the same pattern; neither was applied here.
   *
   * IT SHIPPED TWICE: the drawer's log and `Oncall.tsx`'s seven-column integrations table, in two
   * different PRs. Nothing could catch it — jsdom has no layout, so a render test cannot observe
   * clipping, and `tsc` sees a valid style object. Lexical is the only layer that can.
   *
   * Scoped to `nowrap` tables deliberately: a table that WRAPS reflows and needs no wrapper, which
   * is why the Admin and Settings tables are untouched and must not be swept in.
   */
  it('every whiteSpace:nowrap table sits inside an overflow-x wrapper', () => {
    const bad: string[] = [];
    for (const [f, raw] of entries) {
      const src = strip(raw);
      const lines = src.split('\n');
      lines.forEach((line, i) => {
        if (!/<table[^>]*whiteSpace:\s*'nowrap'/.test(line)) return;
        // The wrapper is the enclosing element, so look at the few lines above the <table>.
        const above = lines.slice(Math.max(0, i - 4), i).join('\n');
        if (!/overflowX|chr-table-wrap/.test(above)) {
          bad.push(`${f}:${i + 1}`);
        }
      });
    }
    expect(
      bad,
      'a nowrap table has no overflow-x wrapper — wider than its container it will be CLIPPED, not ' +
        'scrolled, and the last column is what disappears:\n  ' + bad.join('\n  '),
    ).toEqual([]);
  });

  it('the on-call pages keep using .label on the SPANs that are real chips', () => {
    // POSITIVE CONTROL (rule 5): the rule above must not have been satisfied by deleting the class
    // everywhere. Task labels and capabilities are genuine chips and still use it.
    const chips = entries
      .filter(([f]) => /Board|TaskDrawer|Agents|ActivityLog/.test(f))
      .map(([, raw]) => strip(raw))
      .join('\n');
    expect(chips).toMatch(/<span[^>]*className="label"/);
  });
});
