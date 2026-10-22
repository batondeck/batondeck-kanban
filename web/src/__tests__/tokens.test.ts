/* Every token the DEFAULT theme references must actually resolve.
 *
 * The failure this guards against is completely silent. `transition: background var(--t-1) var(--ease)`
 * with an undefined token is an INVALID declaration, and the browser drops invalid declarations
 * without a console error — the transition simply never runs and nothing on screen looks broken.
 *
 * This shipped: the motion vocabulary (`--ease`, `--t-1`…`--t-3`) was defined inside
 * `[data-theme="neopop"]` rather than `:root`, so every transition and animation in the app was dead
 * on the default theme and alive only if you switched to a backup theme.
 *
 * The rule: a rule OUTSIDE a theme block may only reference a token that `:root` defines, unless it
 * supplies a fallback (`var(--x, 8px)`). Theme blocks may override tokens; they may not be the only
 * place a token is born.
 */
import { describe, expect, it } from 'vitest';
// Vite's `?raw` rather than node:fs — it needs no @types/node, and it resolves relative to THIS
// file, so the guard cannot be silently reading the wrong stylesheet depending on the cwd.
import rawCss from '../index.css?raw';

/* Every OTHER stylesheet in the app, globbed rather than listed.
   `styles/*.css` are per-area sheets imported by individual components. They are exactly as able to
   reference a dead token as index.css is, and a guard that reads only index.css would not notice —
   which is how the bug this file exists to catch would walk straight back in through a new file.
   Globbing (not an explicit list) means a sheet added tomorrow is covered without anyone remembering. */
const OTHER_SHEETS = import.meta.glob('../styles/*.css', { query: '?raw', import: 'default', eager: true }) as Record<string, string>;

const strip = (s: string) => s.replace(/\/\*[\s\S]*?\*\//g, ''); // comments mention tokens prosaically; they are not code
const CSS = strip(rawCss);

/** Extent of the block opened by the `{` at or after `from`, via brace matching. */
function blockEnd(src: string, from: number): number {
  const open = src.indexOf('{', from);
  if (open === -1) return src.length;
  let depth = 0;
  for (let i = open; i < src.length; i++) {
    if (src[i] === '{') depth++;
    else if (src[i] === '}' && --depth === 0) return i + 1;
  }
  return src.length;
}

/** The stylesheet with every `[data-theme="…"] { … }` block removed — i.e. what the default theme sees. */
function withoutThemeBlocks(src: string): string {
  let out = src;
  for (;;) {
    const at = out.search(/\[data-theme=/);
    if (at === -1) return out;
    out = out.slice(0, at) + out.slice(blockEnd(out, at));
  }
}

/** Each `[data-theme="name"] { … }` block as [name, body]. */
function themeBlocks(src: string): Array<[string, string]> {
  const out: Array<[string, string]> = [];
  for (const m of src.matchAll(/\[data-theme="([\w-]+)"\][^{]*\{/g)) {
    const open = m.index + m[0].length - 1;
    out.push([m[1], src.slice(open, blockEnd(src, open))]);
  }
  return out;
}

/** Tokens defined by the `:root` block. */
function rootTokens(src: string): Set<string> {
  const at = src.indexOf(':root');
  const body = src.slice(at, blockEnd(src, at));
  return new Set([...body.matchAll(/(--[\w-]+)\s*:/g)].map((m) => m[1]));
}

/** Tokens referenced WITHOUT a fallback: `var(--x)` counts, `var(--x, 8px)` does not. */
function referencedWithoutFallback(src: string): Map<string, number> {
  const out = new Map<string, number>();
  for (const m of src.matchAll(/var\(\s*(--[\w-]+)\s*\)/g)) {
    out.set(m[1], (out.get(m[1]) ?? 0) + 1);
  }
  return out;
}

/* Tokens written onto individual elements at runtime rather than declared in CSS. Each entry must
   name the code that sets it, so this list cannot quietly become a dumping ground for real bugs. */
const SET_BY_JS = new Set<string>([
  '--marquee-shift', // components.tsx — measured per chip, removed when the label fits
  '--marquee-ms', //    components.tsx — duration scaled to the overflow distance
]);

describe('CSS custom properties resolve on the default theme', () => {
  it('actually read the stylesheet', () => {
    // Not ceremony. Vitest stubs CSS imports to '' unless `css: true` is set in vite.config.ts,
    // and with an empty string every other test here passes VACUOUSLY — nothing referenced, so
    // nothing missing. This assertion is what makes the rest of the file mean anything.
    expect(CSS.length).toBeGreaterThan(2000);
    expect(CSS).toContain(':root');
    expect(referencedWithoutFallback(CSS).size).toBeGreaterThan(10);
  });

  it('defines every token the non-themed rules reference', () => {
    const base = withoutThemeBlocks(CSS);
    const defined = rootTokens(CSS);
    const missing = [...referencedWithoutFallback(base).keys()]
      .filter((t) => !defined.has(t) && !SET_BY_JS.has(t))
      .sort();

    expect(missing, `referenced with no :root definition and no fallback → the declaration is dropped silently:\n  ${missing.join('\n  ')}`).toEqual([]);
  });

  it('keeps the motion vocabulary on :root, not in a theme block', () => {
    // Named explicitly because this is the exact regression that shipped, and because a palette
    // changes what the UI looks like, never how long it takes.
    const defined = rootTokens(CSS);
    for (const t of ['--ease', '--ease-inout', '--t-1', '--t-2', '--t-3']) {
      expect(defined, `${t} must be defined on :root`).toContain(t);
    }
  });

  it('covers every per-area stylesheet, not just index.css', () => {
    // There are currently NO styles/*.css — each went with the screen that imported it. The glob
    // stays so a sheet added tomorrow is covered without anyone remembering to wire it up; with
    // zero matches the loop below is a no-op, which is correct rather than a silent gap (index.css
    // itself is covered by the sibling test above).
    const defined = rootTokens(CSS); // tokens live on :root in index.css; area sheets only consume them
    const offenders: string[] = [];
    for (const [file, raw] of Object.entries(OTHER_SHEETS)) {
      for (const t of referencedWithoutFallback(withoutThemeBlocks(strip(raw))).keys()) {
        if (!defined.has(t) && !SET_BY_JS.has(t)) offenders.push(`${file} → ${t}`);
      }
    }
    expect(offenders, `area stylesheets referencing a token :root never defines:\n  ${offenders.join('\n  ')}`).toEqual([]);
  });

  /* A theme that moves the canvas must move the WHOLE surface set.
   *
   * This shipped too, and no existing check saw it: `[data-theme="neopop"]` redefined 36 tokens but
   * neither `--band` nor `--rule`, so a black canvas inherited :root's warm paper band (#f2f0ea)
   * under white `--text` — the record title rendered white-on-white — and drew its 1.5px record
   * edges in near-black on near-black. Every token resolved, so tokens are not the unit that
   * matters here; a PALETTE is. Definition-completeness cannot catch a half-overridden palette,
   * which is why this asserts override-completeness instead. Found by looking at a render. */
  const SURFACE_TOKENS = [
    '--bg', '--surface-lowest', '--surface-low', '--panel', '--panel-2', '--surface-highest',
    '--band', '--border', '--outline-variant', '--rule', '--text', '--text-2', '--text-muted',
  ];

  it('every theme that moves --bg redefines the whole surface set', () => {
    // T-398 deleted every `[data-theme=…]` block, so this guard has nothing real to match and says so
    // rather than asserting an empty array green (review finding 7). It pins the no-theme fact, flips
    // to the real check if one returns, and the synthetic control below proves the matcher still works.
    const blocks = themeBlocks(CSS);
    if (blocks.length === 0) {
      expect(CSS).not.toMatch(/\[data-theme=/);
      return;
    }
    const gaps: string[] = [];
    for (const [name, body] of blocks) {
      const defined = new Set([...body.matchAll(/(--[\w-]+)\s*:/g)].map((m) => m[1]));
      if (!defined.has('--bg')) continue; // not a palette theme; it may tweak whatever it likes
      for (const t of SURFACE_TOKENS) {
        if (!defined.has(t)) gaps.push(`[data-theme="${name}"] → ${t}`);
      }
    }
    expect(gaps, `a theme moved --bg but left these inheriting :root's palette:\n  ${gaps.join('\n  ')}`).toEqual([]);
  });

  it('detects a token that only a theme block defines (positive control for the guard)', () => {
    // Proves the guard has teeth: reproduce the shipped bug in a miniature stylesheet and
    // confirm the same analysis reports it, so a passing suite means the check works.
    const broken = `:root { --bg: #fff; }
      .btn { transition: background var(--t-1) var(--ease); }
      [data-theme="neopop"] { --t-1: 120ms; --ease: linear; }`;
    const missing = [...referencedWithoutFallback(withoutThemeBlocks(broken)).keys()]
      .filter((t) => !rootTokens(broken).has(t));
    expect(missing.sort()).toEqual(['--ease', '--t-1']);
  });

  /* The narrow-viewport rule hides EVERY `.nav-group-label`, including the avatar menu's "Organization"
   * heading, which left the org name unlabelled on a phone (review finding 13). jsdom evaluates no media
   * queries, so assert on the stylesheet text that the opt-back-in sits INSIDE the same block.
   */
  it('keeps the avatar menu’s group heading visible below 760px', () => {
    // There is more than one `max-width: 760px` block, so this finds the one that actually hides the
    // label rather than the first one — taking `indexOf` alone passed on a stylesheet where the
    // opt-back-in had been deleted, which is the mutation this test exists to catch.
    const blocks: string[] = [];
    for (let at = CSS.indexOf('@media'); at !== -1; at = CSS.indexOf('@media', at + 6)) {
      blocks.push(CSS.slice(at, blockEnd(CSS, at)));
    }
    const hiding = blocks.filter((b) => /(^|[,{}\s])\.nav-group-label\s*\{[^}]*display:\s*none/.test(b));
    expect(hiding.length, 'no media block hides .nav-group-label any more — drop this guard').toBe(1);
    expect(hiding[0], 'the sidebar hide is back to catching the avatar menu').toMatch(
      /\.avatar-menu\s+\.nav-group-label\s*\{\s*display:\s*block/,
    );
  });
});
