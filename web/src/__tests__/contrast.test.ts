/* WCAG 2.2 §1.4.3 "Contrast (Minimum)", Level AA, encoded as a test.
 *
 * https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum.html
 *   normal text  ≥ 4.5:1      large text (≥24px, or ≥18.66px bold) ≥ 3:1
 * Non-text (dots, bars, borders) is §1.4.11 "Non-text Contrast" at ≥ 3:1.
 *
 * WHY A TEST AND NOT A REVIEW. An in-browser audit found 19 failing text nodes on the board alone,
 * all traced to ONE token (`--text-muted` at 3.03–3.45:1). A colour that fails is invisible as a
 * defect — it looks like a design choice, and nothing errors. Palette edits are exactly the kind of
 * change made quickly and by eye, so the floor belongs in CI, not in someone's judgement.
 *
 * The ratio maths is the W3C definition (relative luminance, sRGB), not an approximation.
 */
import { describe, expect, it } from 'vitest';
import rawCss from '../index.css?raw';

type RGB = { r: number; g: number; b: number };

/* PALETTES, PLURAL (T-519). This guard read the `:root` block ONLY, which was correct when one
   palette shipped and would have been a hole the moment a second one did: a dark theme could have
   shipped with white-on-grey muted text and every assertion here would still have passed, green,
   against the light palette it was not describing.

   Blocks are brace-matched, not regex-scanned across the file — a whole-file search for `--ready`
   silently returns whichever theme happens to declare it last. The first draft of this guard did
   exactly that and "failed" on a token the browser measured at 6.06:1; the test was wrong, not the
   palette. A guard reading the wrong input is worse than no guard: it manufactures false findings
   and trains you to ignore it.

   A theme block INHERITS every token it does not override, so each palette is `:root` merged with
   its own declarations — which is also what the browser does. `tokens.test.ts` separately refuses a
   theme that moves `--bg` without moving the whole surface set, so the two guards together mean a
   palette is neither half-declared nor half-measured. */

/** Body of the block opened by the `{` at or after `from`, brace-matched. */
function blockBody(src: string, from: number): string {
  const open = src.indexOf('{', from);
  let depth = 0;
  for (let i = open; i < src.length; i++) {
    if (src[i] === '{') depth++;
    else if (src[i] === '}' && --depth === 0) return src.slice(open, i);
  }
  throw new Error(`unterminated block at ${from}`);
}

function bodyOf(selector: string): string {
  const at = rawCss.indexOf(selector);
  if (at === -1) throw new Error(`selector ${selector} not found in index.css`);
  return blockBody(rawCss, at);
}

/** `--token` → value, last declaration winning, as the cascade does. */
function declarations(body: string): Map<string, string> {
  const out = new Map<string, string>();
  for (const m of body.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) out.set(m[1], m[2].trim());
  return out;
}

const LIGHT_BODY = bodyOf(':root {');
const DARK_BODY = bodyOf('[data-theme="dark"] {');

/** Each palette as the browser resolves it: `:root`, overridden by the theme's own declarations. */
const PALETTES: Array<[string, Map<string, string>]> = [
  ['light', declarations(LIGHT_BODY)],
  ['dark', new Map([...declarations(LIGHT_BODY), ...declarations(DARK_BODY)])],
];

/** A palette's value for `--token`, required to be a 6-digit hex — the ratio maths needs a colour,
 *  and a token that has quietly become a `color-mix()` or a keyword is a hole in this guard rather
 *  than a skipped case, so it throws. */
function token(palette: Map<string, string>, name: string): string {
  const v = palette.get(name);
  if (v == null) throw new Error(`token ${name} is not declared`);
  if (!/^#[0-9a-fA-F]{6}$/.test(v)) throw new Error(`token ${name} = "${v}" is not a 6-digit hex`);
  return v;
}

function rgb(hex: string): RGB {
  const n = parseInt(hex.slice(1), 16);
  return { r: (n >> 16) & 255, g: (n >> 8) & 255, b: n & 255 };
}

/** WCAG relative luminance — https://www.w3.org/WAI/GL/wiki/Relative_luminance */
function luminance({ r, g, b }: RGB): number {
  const f = (v: number) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
}

export function contrast(a: string, b: string): number {
  const [L1, L2] = [luminance(rgb(a)), luminance(rgb(b))];
  return (Math.max(L1, L2) + 0.05) / (Math.min(L1, L2) + 0.05);
}

/* The three surfaces any of this ink can land on. `--band` is the darkest and therefore the binding
   constraint — a token checked only against white passes while failing on a record header. */
const SURFACES = ['--panel', '--bg', '--band'] as const;

describe.each(PALETTES)('WCAG 2.2 §1.4.3 — %s palette', (name, palette) => {
  /* Ink used for TEXT. `--text-muted` carries timestamps, ids, counts and nav labels — small,
     secondary, and the most likely thing to be dismissed as "just decoration". It is text. */
  const TEXT_TOKENS = ['--text', '--text-2', '--text-muted'];

  for (const t of TEXT_TOKENS) {
    it(`${t} is legible on panel, canvas and band`, () => {
      for (const s of SURFACES) {
        const r = contrast(token(palette, t), token(palette, s));
        expect(r, `[${name}] ${t} on ${s} = ${r.toFixed(2)}:1 (WCAG 2.2 §1.4.3 AA requires 4.5:1)`)
          .toBeGreaterThanOrEqual(4.5);
      }
    });
  }

  /* Status/role hues are rendered as BADGE TEXT (`.role`, `.tag`), not only as dots, so they are
     held to the text floor rather than §1.4.11's 3:1. `--backlog` (3.03:1) and `--progress`
     (3.63:1) both failed here before this guard existed. */
  const ROLE_TOKENS = ['--backlog', '--ready', '--progress', '--review', '--live', '--done', '--blocked'];

  for (const t of ROLE_TOKENS) {
    it(`${t} is legible as badge text`, () => {
      for (const s of ['--bg', '--band'] as const) {
        const r = contrast(token(palette, t), token(palette, s));
        expect(r, `[${name}] ${t} on ${s} = ${r.toFixed(2)}:1 — rendered as badge text, so §1.4.3 applies`)
          .toBeGreaterThanOrEqual(4.5);
      }
    });
  }

  /* The primary button is `--ink-fg` ON `--ink`, which no surface-vs-ink pairing above covers — the
     one text/background pair in the system where BOTH halves are palette tokens. `--ink-hover` is
     the same pair in the hover state and needs its own assertion: the fill moves, the label does
     not, so a hover colour chosen by eye can drop the label under the floor with the resting state
     still passing.

     DEVIATION: the floor here is 3.0, not the 4.5 every other text pair holds. Owner decision
     (2026-08-31): white labels on the bright Cloudflare-style orange (#ED6B3A, 3.10:1) — button-label
     AA traded for the brand look, the trade cloudflare.com itself ships. The authoritative record is
     design/brand.json `contrastFloors.buttonLabels` + `decisions`; restoring AA means moving those
     pairs back to the `text` group and raising this to 4.5 again. */
  it('button labels are legible on the primary fill, resting and hovered', () => {
    for (const fill of ['--ink', '--ink-hover'] as const) {
      const r = contrast(token(palette, '--ink-fg'), token(palette, fill));
      expect(r, `[${name}] --ink-fg on ${fill} = ${r.toFixed(2)}:1`).toBeGreaterThanOrEqual(3.0);
    }
  });

  /* THE HOLE THIS FILE SHIPPED WITH, closed.
   *
   * Until the ember rebrand this guard measured `--text*`, the seven status hues and
   * `--ink-fg`-on-`--ink` — and NOTHING against `--accent` or `--accent-tint`. Under indigo that was
   * harmless by luck: #4f46e5 cleared 4.5:1 on white by accident. brand-literal-ok: the retired
   * accent, named because that measurement IS the argument for the two arms below.
   * An accent picked to read well as a FILL is typically 2.5–3.5:1 as TEXT, so a warm
   * accent could have shipped at 3.38:1 on `.md a` links with the whole gate green.
   *
   * The two tokens carry DIFFERENT floors because they do different jobs, and conflating them is
   * how the hole would reopen:
   *   --accent      non-text only — focus rings, the selected-card border, the active tab underline,
   *                 the `.assign-check` glyph, and the base of every colour-mix wash. WCAG 2.2
   *                 §1.4.11, 3:1.
   *   --accent-tint text — `.md a`, `.role-badge.admin`. §1.4.3, 4.5:1.
   * If you find yourself wanting to paint word text in `--accent`, the answer is `--accent-tint`.
   */
  it('--accent clears the non-text floor on every surface it borders', () => {
    for (const s of SURFACES) {
      const r = contrast(token(palette, '--accent'), token(palette, s));
      expect(r, `[${name}] --accent on ${s} = ${r.toFixed(2)}:1 (WCAG 2.2 §1.4.11 needs 3:1)`)
        .toBeGreaterThanOrEqual(3);
    }
  });

  /* DEVIATION: 3.0, not 4.5 — the same owner waiver as the button labels (2026-08-31): the hot
     Cloudflare orange carries accent text product-wide. Record: brand.json contrastFloors.buttonLabels
     and decisions. The dark palette still measures over 4.5 on every surface; light is ~3.5. */
  it('--accent-tint clears the waived floor on every surface', () => {
    for (const s of SURFACES) {
      const r = contrast(token(palette, '--accent-tint'), token(palette, s));
      expect(r, `[${name}] --accent-tint on ${s} = ${r.toFixed(2)}:1 — link text under the owner waiver`)
        .toBeGreaterThanOrEqual(3.0);
    }
  });

  /* `--marker`/`--marker-fg` is a text-on-fill pair exactly like `--ink-fg`/`--ink`, and was equally
     unmeasured. It is the one loud colour in the product and the thing a user is looking straight
     at when they search. */
  it('a search hit is legible on its own highlight', () => {
    const r = contrast(token(palette, '--marker-fg'), token(palette, '--marker'));
    expect(r, `[${name}] --marker-fg on --marker = ${r.toFixed(2)}:1`).toBeGreaterThanOrEqual(4.5);
  });
});

describe('the guard is reading what it claims to read', () => {
  it('found two distinct palettes', () => {
    // Vacuity guard. If `bodyOf` silently matched the same block twice — or vitest stubbed the CSS
    // import — every assertion above would pass while measuring one palette twice.
    const [[, light], [, dark]] = PALETTES;
    expect(PALETTES.map(([n]) => n)).toEqual(['light', 'dark']);
    expect(token(light, '--bg')).not.toBe(token(dark, '--bg'));
    expect(light.size).toBeGreaterThan(20);
  });

  /* THE TWO DARK COPIES MUST AGREE. Dark is applied through two selectors —
   * `[data-theme="dark"]` and `:root:not([data-theme="light"])` inside a `prefers-color-scheme`
   * media query — because an explicit choice has to beat the OS in BOTH directions. CSS has no way
   * to share one declaration list between them, so the block is duplicated by intent, and a
   * duplicated block is a block that drifts. Editing one and not the other would leave the OS-dark
   * user on a different palette from the toggle-dark user, which nothing else here would notice:
   * the assertions above only read the `[data-theme]` copy.
   */
  it('the media-query dark copy is byte-identical to the attribute copy', () => {
    const media = declarations(bodyOf(':root:not([data-theme="light"]) {'));
    const attr = declarations(DARK_BODY);
    expect([...media.keys()].sort()).toEqual([...attr.keys()].sort());
    const drift = [...attr].filter(([k, v]) => media.get(k) !== v).map(([k, v]) => `${k}: ${v} vs ${media.get(k)}`);
    expect(drift, `the two dark blocks disagree:\n  ${drift.join('\n  ')}`).toEqual([]);
  });

  /* WCAG 2.2 §1.4.11 "Non-text Contrast" (≥3:1) for the ONE colour in the system that cannot be a
   * token: the project switcher's chevron lives inside a `data:` URI, which is opaque to the cascade,
   * so `var(--text-muted)` in there does not resolve — it fails to parse and the arrow vanishes. One
   * literal has to serve both palettes, which is exactly the kind of value that gets "tidied" to a
   * token's light-theme hex by someone who has not looked at it on the other one.
   */
  it('the switcher chevron clears 3:1 on BOTH panels (it cannot be a token)', () => {
    const m = rawCss.match(/stroke='%23([0-9a-fA-F]{6})'/);
    expect(m, 'the chevron data URI is gone — drop this guard or repoint it').not.toBeNull();
    const hex = `#${m![1]}`;
    for (const [name, palette] of PALETTES) {
      const r = contrast(hex, token(palette, '--panel'));
      expect(r, `chevron ${hex} on the ${name} --panel = ${r.toFixed(2)}:1 (§1.4.11 needs 3:1)`)
        .toBeGreaterThanOrEqual(3);
    }
  });

  it('computes the W3C ratio correctly (control — the maths, not the palette)', () => {
    // Anchors from the spec: black on white is 21:1; a colour against itself is 1:1.
    expect(contrast('#000000', '#ffffff')).toBeCloseTo(21, 1);
    expect(contrast('#4a4a4a', '#4a4a4a')).toBeCloseTo(1, 5);
    // A known-failing pair must actually be reported as failing.
    expect(contrast('#8e8a7e', '#f2f0ea')).toBeLessThan(4.5);
  });

  /* Positive control for the arm added by the ember rebrand (rule 5: a guard gets a refusal test AND
     a control, so you prove both that it blocks and that you did not over-restrict). The value here
     is the ACTUAL brand ember: it is correct as `--accent` at 3.38:1 non-text, and would be a real
     AA failure as `--accent-tint`. One number, two verdicts — which is the whole reason the two
     tokens are held to different floors. */
  it('would refuse the brand ember as link text, and accept it as a ring (positive control)', () => {
    const EMBER = '#E8613C';
    const light = new Map([...declarations(LIGHT_BODY)]);
    const onPanel = contrast(EMBER, token(light, '--panel'));
    expect(onPanel).toBeLessThan(4.5); //  as --accent-tint: fails §1.4.3
    expect(onPanel).toBeGreaterThanOrEqual(3); // as --accent: passes §1.4.11
  });

  it('catches a dark palette whose muted text is too dim (positive control)', () => {
    // Proves the new arm has teeth: a plausible dark theme — the exact mistake dark modes make,
    // grey-on-grey secondary text — must be reported, not waved through.
    const broken = new Map([...declarations(DARK_BODY), ['--text-muted', '#5a606c']]);
    expect(contrast(token(broken, '--text-muted'), token(broken, '--panel'))).toBeLessThan(4.5);
  });
});
