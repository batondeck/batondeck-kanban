/**
 * FOUR STAGING REPORTS THAT NOTHING ELSE WATCHES, 2026-08-29.
 *
 * Each is a property no existing test could see: a glyph's character class, a sticky offset, a
 * regex over an actor string, and a CSS declaration. Nothing broke when they were wrong.
 */
import { describe, it, expect } from 'vitest';
import { eventActor } from '../components.js';
import { defaultBoardName } from '../api/boards.js';

const SOURCES = import.meta.glob('../**/*.{tsx,ts,css}', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

/** Comments quote the very strings these assertions hunt for, so they are stripped (see below). */
const strip = (s: string): string =>
  s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/.*$/gm, '$1');

const src = (rel: string): string => {
  const hit = Object.entries(SOURCES).find(([k]) => k.endsWith(`/${rel}`));
  if (!hit) throw new Error(`no source for ${rel}`);
  return strip(hit[1]);
};

/** The behavioural role-gate guards. Globbed separately — SOURCES is rooted at `src/` and does not
 *  reach this directory, so a lookup for a sibling test file throws rather than matching. */
const BEHAVIOURAL = import.meta.glob('./{oncall-role-gate,display-names}.test.tsx', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>;

/**
 * Blank out string and template literals before brace counting.
 *
 * ONE `{` inside a `queryKey` string was enough to unbalance `useQueryBlocks`, so the block ran to
 * EOF and inherited the NEXT query's `enabled:` — a review shipped a fully ungated call that way
 * with the suite green. Contents are replaced rather than removed so nothing else shifts meaning:
 * the block only gets tested for `oncallApi.listIntegrations(` and `enabled:`, neither of which lives in
 * a string. A brace inside a REGEX literal still defeats this; see the docblock.
 */
const stripStrings = (s: string): string =>
  s
    .replace(/'(?:[^'\\\n]|\\.)*'/g, "''")
    .replace(/"(?:[^"\\\n]|\\.)*"/g, '""')
    .replace(/`(?:[^`\\]|\\.)*`/g, '``');

/** The balanced `{…}` of every `useQuery({` call — bounded at its OWN closing brace, so a sibling's
 *  options cannot stand in for this query's gate. Feed it `stripStrings` output. */
function useQueryBlocks(s: string): string[] {
  const out: string[] = [];
  const needle = 'useQuery({';
  for (let i = s.indexOf(needle); i !== -1; i = s.indexOf(needle, i + 1)) {
    const start = i + needle.length - 1; // the '{' itself
    let depth = 0;
    for (let j = start; j < s.length; j += 1) {
      if (s[j] === '{') depth += 1;
      else if (s[j] === '}') {
        depth -= 1;
        if (depth === 0) {
          out.push(s.slice(start, j + 1));
          break;
        }
      }
    }
  }
  return out;
}

it('the source scan finds its population', () => {
  expect(Object.keys(SOURCES).length).toBeGreaterThan(15);
});

/* ---------------------------------------------------- emoji are not controls ---------------- */

describe('ticket controls use real icons, not emoji', () => {
  /**
   * Emoji render in the PLATFORM font, ignore `currentColor`, and carry their own text semantics —
   * so the same button is a flat glyph on one OS and a colour sticker on another, does not follow a
   * disabled or dark-theme colour, and announces "locked padlock" beside the word "Claim".
   */
  const EMOJI = /[\u{1F300}-\u{1FAFF}\u{2190}-\u{21FF}\u{2300}-\u{27BF}\u{2B00}-\u{2BFF}]/u;

  it.each([
    ['screens/task/TicketActions.tsx'],
    ['screens/TaskDrawer.tsx'],
  ])('%s has no emoji left in its markup', (file) => {
    const found = src(file)
      .split('\n')
      .filter((l) => EMOJI.test(l))
      .map((l) => l.trim().slice(0, 70));
    expect(found, `emoji still rendered:\n${found.join('\n')}`).toEqual([]);
  });

  it('the icon set draws stroked paths on currentColor, like the nav', () => {
    const icons = src('icons.tsx');
    expect(icons).toContain('stroke="currentColor"');
    expect(icons).toContain('viewBox="0 0 24 24"');
    // aria-hidden always: each caller pairs the glyph with a label or an aria-label.
    expect(icons).toContain('aria-hidden="true"');
  });
});

/* ------------------------------------------- the tabs clear the topbar ---------------------- */

describe('the section tabs do not park under the topbar', () => {
  /**
   * `.topbar` is `position: sticky; top: 0; z-index: 5` in the SAME scroll container. Tabs pinned to
   * `top: 0` therefore stick correctly and are covered — which on staging read as a half-clipped row
   * of labels bleeding through the header.
   */
  it('offsets by the measured topbar height, not zero', () => {
    const css = src('styles/task.css');
    expect(css).toMatch(/\.task-tabs\s*\{[^}]*top:\s*calc\(var\(--topbar-h/);
    expect(css, 'top: 0 puts the tabs under the topbar').not.toMatch(/\.task-tabs\s*\{[^}]*top:\s*0/);
  });

  it('something actually publishes --topbar-h', () => {
    // A `var()` with only its fallback is a constant wearing a variable's clothes.
    expect(src('screens/TaskPage.tsx')).toContain("setProperty('--topbar-h'");
  });
});

/* ------------------------------------------- ingest actors are readable --------------------- */

describe('account ids resolve to people, not uuids', () => {
  /**
   * `U-47f0d44f-7a04-4e44-a67f-a39601d12b79` filled the feed. The T-216 cutover made account ids
   * opaque on purpose — the core stores no email — so the NAME has to come from `memberDisplay`, a
   * sibling of `project` on `get_project`.
   */
  const U = 'U-47f0d44f-7a04-4e44-a67f-a39601d12b79';

  it('renders the local part of a member’s email', () => {
    expect(eventActor({ actor: U }, { [U]: 'alice@example.com' })).toBe('alice');
  });

  it('accepts a Map as readily as a record', () => {
    expect(eventActor({ actor: U }, new Map([[U, 'alice@example.com']]))).toBe('alice');
  });

  it('ELIDES rather than printing 38 characters when the map cannot name them', () => {
    // `memberDisplay` names PROJECT MEMBERS only — the core has a refusal test proving it cannot
    // name a non-member. A former member or a cross-project actor legitimately misses.
    const out = eventActor({ actor: U });
    expect(out).not.toBe(U);
    expect(out.length).toBeLessThan(U.length);
    expect(out.startsWith('U-')).toBe(true);
  });

  it('leaves a name that is already readable alone', () => {
    expect(eventActor({ actor: 'someone@example.com' })).toBe('someone@example.com');
    expect(eventActor({ actor: 'steady-donkey' })).toBe('steady-donkey');
  });

  it('every feed that renders an actor passes the names in', () => {
    // A WEAK COMPANION, kept only for the file-coverage half. An adversarial review killed the
    // original version of this: `.not.toMatch(/eventActor\(e\)/)` matches nothing today because every
    // call site already passes the map, and `.toContain('useDisplayNames')` is satisfied by the
    // import line — so the whole suite passed with the hook stubbed to `{}`. The real guard is
    // `display-names.test.tsx`, which asserts the map is BUILT.
    for (const f of ['screens/ActivityLog.tsx', 'screens/Analytics.tsx', 'screens/Board.tsx', 'screens/task/panels.tsx']) {
      expect(src(f), `${f} does not load display names`).toContain('useDisplayNames');
    }
  });
});

describe('on-call activity rows name the integration, not its id', () => {
  const EV = { actor: 'ingest:ing_ba3c6ec8c12cc970c3ae16e2f62ddbaf' };

  it('resolves the id to the operator’s own name', () => {
    expect(eventActor(EV, new Map([['ing_ba3c6ec8c12cc970c3ae16e2f62ddbaf', 'prod-grafana']])))
      .toBe('prod-grafana');
  });

  it('shortens rather than printing 36 characters of hex when unresolvable', () => {
    // The names load best-effort: a member without the on-call permission gets a 403.
    const out = eventActor(EV);
    expect(out).not.toBe(EV.actor);
    expect(out.length).toBeLessThan(EV.actor.length);
    expect(out).toContain('ingest:');
  });

  it('an agent still wins over both', () => {
    expect(eventActor({ actor: 'ingest:ing_abc123', agent: 'claude-oncall' })).toBe('claude-oncall');
  });

  it('a human actor is untouched', () => {
    expect(eventActor({ actor: 'someone@example.com' })).toBe('someone@example.com');
  });
});

/* ------------------------------------------- the closed select is ours ---------------------- */

describe('selects do not wear the platform control', () => {
  it('suppresses the native appearance and draws a caret', () => {
    const css = src('index.css');
    expect(css).toMatch(/select\s*\{[^}]*appearance:\s*none/);
    expect(css).toMatch(/select\s*\{[^}]*background-image:\s*url\("data:image\/svg\+xml/);
  });
});

/* ------------------------------------------- no emoji anywhere in the UI ------------------- */

describe('no screen renders an emoji as UI chrome', () => {
  /**
   * THE SWEEP GUARD. Owner decision 2026-08-29 after the ticket surfaces were fixed: do the whole
   * app, and stop it coming back.
   *
   * WHAT IS REFUSED: pictographs and dingbats — 🔒 📖 🏆 🏁 🔔 🖼 ⚙ ☑ ⚑ ✓ ✕ ● ▶ ▲. Every one of
   * these is drawn by the PLATFORM font, so it ignores `currentColor`, does not follow a disabled
   * state or either theme, and carries its own screen-reader name beside the label it decorates.
   *
   * WHAT IS ALLOWED, deliberately: arrows and typographic marks inside PROSE — `→` in
   * "median time claimed→done", `⌘↵` in a keyboard hint, `·` as a separator. Those are punctuation,
   * not controls, and a guard that failed on them would be refused on sight and then disabled.
   *
   * Comments are stripped first, because the docblocks explaining each replacement NAME the emoji
   * they replaced — an unstripped scan fails on its own explanation.
   */
  const BANNED = /[\u{1F300}-\u{1FAFF}\u{2600}-\u{27BF}\u{23F0}-\u{23FF}\u{2B00}-\u{2BFF}\u{25A0}-\u{25FF}\u{2610}-\u{2612}]/u;

  it('is scanning a real population', () => {
    const screens = Object.keys(SOURCES).filter((k) => k.includes('/screens/'));
    expect(screens.length, 'the glob found no screens — every assertion below would be vacuous').toBeGreaterThan(15);
  });

  /**
   * POSITIVE CONTROL (rule 5). The population check above proves the GLOB found files; it says
   * nothing about whether `BANNED` bites. A review narrowed the class to
   * `/[\u{10FFFE}-\u{10FFFF}]/u` — matching nothing any keyboard produces — and the sweep stayed
   * green while every emoji in the app would have been permitted.
   *
   * Each of these is a glyph the sweep exists to refuse, and the arrow is the boundary case: it must
   * be ALLOWED, or the guard fails on `claimed→done` in ordinary prose and gets disabled.
   */
  it('POSITIVE CONTROL: the pattern refuses real emoji and permits prose punctuation', () => {
    for (const bad of ['🔒', '📖', '🏆', '🔔', '⚙', '☑', '✕', '▶']) {
      expect(BANNED.test(bad), `the sweep would permit ${bad}`).toBe(true);
    }
    for (const ok of ['→', '·', '⌘', '↵', 'plain text']) {
      expect(BANNED.test(ok), `the sweep would fail on ${ok}, which is prose punctuation`).toBe(false);
    }
  });

  it('finds none', () => {
    const offenders: string[] = [];
    for (const [path, raw] of Object.entries(SOURCES)) {
      if (!/\.(tsx|ts)$/.test(path)) continue;
      if (path.includes('/__tests__/')) continue;
      strip(raw).split('\n').forEach((line, i) => {
        const m = BANNED.exec(line);
        if (m) offenders.push(`${path}:${i + 1} [${m[0]}] ${line.trim().slice(0, 60)}`);
      });
    }
    expect(offenders, `use <Icon> from icons.tsx instead:\n${offenders.join('\n')}`).toEqual([]);
  });
});

/* ------------------------------------------- the default board has a name ------------------ */

describe('an auto-created board is named after its project', () => {
  /**
   * Both auto-create call sites passed the literal string "Board", so every project's first board
   * was called the same thing and the switcher — which renders `board.name` alone — could not tell
   * two projects apart. Reported from staging: "both projects have `Board` named board, not sure
   * what it is".
   */
  it('derives the name from the project', () => {
    expect(defaultBoardName('TEST')).toBe('TEST board');
    expect(defaultBoardName('Cedar soak exerciser')).toBe('Cedar soak exerciser board');
  });

  it('falls back rather than inventing a name it does not have', () => {
    expect(defaultBoardName()).toBe('Board');
    expect(defaultBoardName('   ')).toBe('Board');
    expect(defaultBoardName(null)).toBe('Board');
  });

  it('no call site hardcodes the bare string any more', () => {
    for (const f of ['palette.tsx', 'screens/Projects.tsx']) {
      expect(src(f), `${f} still auto-creates a board called "Board"`)
        .not.toMatch(/createBoard\([^)]*,\s*'Board'\s*\)/);
    }
  });
});

/* ------------------------------------------- icons sit BESIDE their labels ----------------- */

describe('any container that can hold an icon is a flex row', () => {
  /**
   * `icons.tsx` draws glyphs `display: block`, so they align to the box rather than riding the text
   * baseline. Inside an INLINE container that means the glyph takes its own line and the control
   * grows to two rows.
   *
   * The On-call "Setup" button is the one that got reported. `.badge`, `.label`, `.pill`, `.chip`
   * and `.role-badge` all had it latent — the sprint flag, the lease padlock, the racing flag, the
   * model-hint and custom-field chips, the break-glass badge — and would have arrived one screenshot
   * at a time. This asserts the RULE rather than each call site.
   */
  const css = () => src('index.css');

  it('.btn is a flex row with a gap', () => {
    expect(css()).toMatch(/\.btn\s*\{[^}]*display:\s*inline-flex/);
    expect(css()).toMatch(/\.btn\s*\{[^}]*gap:/);
  });

  it('every chip class that can hold an icon is flex', () => {
    const rule = /\.badge,\s*\.label,\s*\.pill,\s*\.chip,\s*\.role-badge\s*\{([^}]*)\}/.exec(css());
    expect(rule, 'the shared chip rule is gone — icons will break to their own line').toBeTruthy();
    expect(rule![1]).toMatch(/display:\s*inline-flex/);
    expect(rule![1]).toMatch(/align-items:\s*center/);
  });

  it('an icon-only button is square, not a word-width pill', () => {
    const rule = /\.btn\.icon\s*\{([^}]*)\}/.exec(css());
    expect(rule, '.btn.icon is missing').toBeTruthy();
    expect(rule![1]).toMatch(/width:\s*\d+px/);
    expect(rule![1]).toMatch(/height:\s*\d+px/);
    expect(rule![1]).toMatch(/padding:\s*0/);
  });

  it('the quiet control keeps a focus ring', () => {
    // A borderless button that also drops its focus ring is unusable by keyboard.
    expect(css()).toMatch(/\.btn-quiet:focus-visible\s*\{[^}]*box-shadow:/);
  });

  it('icon-only buttons actually carry the class', () => {
    for (const f of ['screens/oncall/OncallSetup.tsx', 'screens/TaskDrawer.tsx', 'App.tsx', 'connect-mcp.tsx']) {
      expect(src(f), `${f} has an icon-only close button that is not square`)
        .not.toMatch(/className="btn secondary"[^>]*aria-label="Close[^"]*">\s*<Icon/);
    }
  });
});

/* ------------------------------------------- panels holding controls are padded ------------ */

describe('a panel holding controls carries its own gutter', () => {
  /**
   * `Panel` leaves padding off by default because its usual content — `KV`, `LineRow`, `table` — is
   * self-padding. The on-call drawer's Integrations panel holds BUTTONS AND PILLS, which are not, so
   * every integration row sat flush against the panel edge and "Show deleted" touched two of them.
   * Reported twice from staging before the cause was found.
   *
   * Lexical, because the defect is pure layout and jsdom computes none.
   */
  it('the Integrations panel passes pad', () => {
    const setup = src('screens/oncall/OncallSetup.tsx');
    const open = setup.indexOf('<Panel\n            title="Integrations"');
    expect(open, 'the Integrations panel was renamed or reindented — update this guard').toBeGreaterThan(-1);
    // Slice to the Panel's OWN closing bracket. A non-greedy `[\s\S]*?>` stops at the first `>`
    // inside the nested `right={<button …>}`, which is before `pad`.
    const props = setup.slice(open, setup.indexOf('\n          >', open));
    expect(props, 'Integrations holds buttons and pills; without `pad` they touch the edge')
      .toMatch(/^\s*pad\s*$/m);
  });
});

/* ------------------------------------- an unprivileged read must not PAGE ------------------ */

describe('useDisplayNames never provokes an escalation signal', () => {
  /**
   * `list_integrations` is `requireMaster` (`src/tools/integrations.ts:670`) — the `batondeck:oncall`
   * slug gates the SPA page, NOT the API. So a refusal here is not an inert best-effort miss: the
   * core answers `Requires master … (have: worker)`, `signals.ts` matches `/^Requires /`, and
   * `escalation-deny.yaml` pages CRITICAL on >3 in 5 minutes.
   *
   * This hook mounts on the four busiest screens, so four viewer/worker humans opening a board in one
   * window would have paged the project owner on entirely honest traffic (rule 19). The call must be
   * gated BEFORE it is made.
   */
  it('gates the call on role rather than absorbing a 403', () => {
    const src_ = src('displayNames.ts');
    expect(src_, 'the integrations query must be conditional on role').toMatch(/enabled:[^\n]*mayReadIntegrations/);
    expect(src_, 'the floor is master, the tier list_integrations enforces').toMatch(/ROLE_RANK\.master/);
  });

  /**
   * THIS TEST IS A COMPLETENESS SWEEP, AND DELIBERATELY NOTHING MORE.
   *
   * It used to assert the SHAPE of each call site's gate — that an `enabled:` clause existed, and
   * that it conjoined `mayReadIntegrations`. Two adversarial passes defeated that seven ways without
   * altering a single assertion, each time shipping a live ungated auto-refetching master-tier call
   * with the whole suite green: a second query in an already-listed file; `const … = true`; `let` +
   * reassign; a decoy `enabled:` in a nested object (`.exec` reads only the first); `(a && gate) ||
   * a`; and ONE `{` inside a string literal, which unbalanced the brace matcher so a block ran to
   * EOF and inherited the next query's gate.
   *
   * Each defeat was answered with a tighter regex, and the next pass beat the tighter regex. So the
   * behaviour is now guarded where it can be OBSERVED: `oncall-role-gate.test.tsx` renders each
   * screen with a sub-master role and watches `fetch`. All three defeats above fail there, because
   * none of them changes what `fetch` sees.
   *
   * THE SHAPE ASSERTIONS WERE DELETED WHEN THAT LANDED, AND THAT WAS WRONG. A sixth pass proved the
   * trade lost coverage: a second `useQuery` in `Oncall.tsx` gated `enabled: setupOpen &&
   * Boolean(activeProject)` — no role gate, behind the Setup gear — passes the behavioural test
   * 527/527, because that test never clicks the gear. The deleted assertion caught it by name and
   * offending text. A render test observes only what its fixtures REACH, and `OncallSetup.tsx` is
   * 821 lines; per-call enumeration and behavioural observation cover DIFFERENT things and neither
   * subsumes the other. Restored below, and both are kept.
   *
   * WHAT THE RESTORED ASSERTION IS FOR, so nobody over-trusts it again: it is an ENUMERATION AID,
   * not protection. It reads source as text and is therefore defeatable by anyone who wants to —
   * seven ways are listed above. Its job is to make an ungated call site VISIBLE to a reviewer, not
   * to stop a determined one. The protection is the behavioural test. Do not answer the next defeat
   * with a tighter regex here; extend the behavioural fixtures instead — EXCEPT for a control on
   * this guard's own population, which is the one thing no behavioural fixture can supply.
   *
   * KNOWN BLIND SPOTS — the list is the docblock's whole value, so it names every mechanism PROVEN
   * to get past this test, not a representative sample:
   *
   *  - ANY spelling other than the literal `oncallApi.listIntegrations(`: `?.(`, `['listIntegrations']`,
   *    `.call`, destructuring, or `const li = oncallApi.listIntegrations.bind(oncallApi)`. One `?.` defeats both
   *    the per-call gate AND the completeness sweep, which share this regex.
   *  - `SOURCES` globs only `.ts`/`.tsx`.
   *  - A brace inside a REGEX literal — `stripStrings` handles string and template literals only.
   *  - A `//` inside a string literal: `strip()` runs FIRST and eats the comment AND the closing
   *    quote, leaving a fragment `stripStrings` cannot blank (its class cannot cross a newline), so
   *    the braces unbalance. Needs a second ingredient to be exploitable, but it is real.
   *  - The link assertion below is satisfiable by a dead string carrying the specifier. Narrower
   *    than the comment hole it replaced — prose contains basenames naturally, a fake import has to
   *    be written on purpose — but not zero.
   *
   * All inherent to reading source as text. None is a reason to trust this further than it goes.
   */
  it('every listIntegrations caller is enumerated, gated, AND behaviourally covered', () => {
    const callers = ['displayNames.ts', 'screens/Oncall.tsx', 'screens/oncall/OncallSetup.tsx'];

    // Each enumerated caller must be exercised by the behavioural guard. Without this, a new caller
    // gets added to `callers` to silence the sweep below and never acquires a real test.
    //
    // MATCHED AS AN IMPORT SPECIFIER, over COMMENT-STRIPPED source. The first version matched a bare
    // basename over raw text, so it was satisfied by the docblock alone — deleting every test in
    // `oncall-role-gate.test.tsx` left it green — and `'Setup'` is a substring of `'OncallSetup'`,
    // so a brand-new `screens/oncall/Setup.tsx` could be added to `callers` with no test at all.
    const behavioural = Object.values(BEHAVIOURAL).map(strip).join('\n');
    expect(
      Object.keys(BEHAVIOURAL).length,
      'the behavioural guards are gone — this sweep is the only thing left and it proves nothing',
    ).toBeGreaterThanOrEqual(2);
    for (const f of callers) {
      const spec = `../${f.replace(/\.tsx?$/, '.js')}`;
      expect(
        behavioural,
        `no behavioural test imports ${spec} — a lexical sweep cannot prove a gate holds, ` +
          `only that a file was noticed`,
      ).toContain(spec);
    }

    // THE ENUMERATION AID (see the docblock — visibility, not protection). Every listIntegrations
    // call must sit in a useQuery block whose `enabled:` conjoins the role gate.
    for (const f of callers) {
      const s = stripStrings(src(f));
      const calls = (s.match(/oncallApi\.listIntegrations\(/g) ?? []).length;
      // A POPULATION CONTROL, not a skip (rule 18). `if (calls === 0) continue` was the inherited
      // shape and it is unreachable in the healthy state — all three callers carry the call in raw
      // source — so its ONLY reachable behaviour was swallowing a corrupted scan. And the scan is
      // accidentally corruptible: `strip()` deletes `/*…*/` ACROSS LINES, so one `/*` inside any
      // string in a listed caller deletes forward to the next `*/` and takes the call sites with it.
      // Measured: `queryKey: ['oncall-integrations /*v2', …]` drops Oncall.tsx from 2 matches to 0,
      // and the whole file is skipped in silence with an ungated query live.
      expect(
        calls,
        `${f}: the scan found no listIntegrations call — the source was corrupted before matching, ` +
          `so this file was about to be checked against nothing`,
      ).toBeGreaterThan(0);
      const blocks = useQueryBlocks(s).filter((b) => /oncallApi\.listIntegrations\(/.test(b));
      expect(
        blocks.length,
        `${f}: ${calls} listIntegrations call(s) but ${blocks.length} useQuery block(s) — ` +
          `one sits where an enabled: gate cannot reach it`,
      ).toBe(calls);
      for (const block of blocks) {
        // Anchored: a bare /enabled:/ also matches `disabled:`. All matches, not just the first —
        // a decoy `enabled:` in a nested object shadowed the real one when this used `.exec`.
        const clauses = [...block.matchAll(/(?:^|[\s{,(])enabled:\s*([^\n]*)/g)].map((m) => m[1]!);
        expect(clauses.length, `${f}: a listIntegrations query with no enabled: clause`).toBeGreaterThan(0);
        for (const c of clauses) {
          expect(
            c,
            `${f} gates listIntegrations with something other than a conjunction — ` +
              `'||' or a bare mention leaves it open to sub-master callers`,
          ).toMatch(/&&\s*mayReadIntegrations|mayReadIntegrations\s*&&/);
        }
      }
    }

    // And the enumeration itself must be complete: no OTHER file may call it.
    const all = Object.entries(SOURCES).filter(
      ([k, v]) => /\.tsx?$/.test(k) && !k.includes('__tests__') && /oncallApi\.listIntegrations\(/.test(v),
    );
    const unknown = all.map(([k]) => k).filter((k) => !callers.some((c) => k.endsWith(`/${c}`)));
    expect(unknown, `a new listIntegrations caller appeared — gate it and add it here`).toEqual([]);
  });

  it('the mirrored ROLE_RANK matches the core, so the floor cannot drift', () => {
    // The SPA cannot import from the core package, so the ranks are duplicated. Pin them.
    const web = /const ROLE_RANK: Record<string, number> = \{([^}]*)\}/.exec(src('displayNames.ts'));
    expect(web, 'the mirrored ROLE_RANK is gone').toBeTruthy();
    for (const [role, rank] of [['viewer', 0], ['worker', 1], ['master', 2], ['member', 3], ['admin', 4]] as const) {
      expect(web![1], `${role} must rank ${rank}`).toMatch(new RegExp(`${role}:\\s*${rank}`));
    }
  });
});

/* ------------------------------------------- both census screens page ---------------------- */

describe('every screen that counts a board drains its cursor', () => {
  /**
   * `list_tasks` caps at 200 and returns `nextCursor`. A screen that asks once and discards the
   * cursor reports a number that LOOKS total and is not.
   *
   * `/reports` was built with the drain; `/analytics` was left with the defect. That was survivable
   * while they were unrelated screens — then a `Tabs` bar put them one click apart, so two adjacent
   * pages reported different totals for the same board and the wrong one carried no caveat.
   *
   * Lexical because both screens need a full render harness to exercise; the property asserted is the
   * one that regressed — a `listTasks` read with no cursor threaded back in.
   */
  it.each([['screens/Reports.tsx'], ['screens/Analytics.tsx']])('%s uses a paging census', (file) => {
    const s = src(file);
    expect(s, `${file} must drain nextCursor`).toMatch(/getNextPageParam:[^\n]*nextCursor/);
    expect(s, `${file} must feed the cursor back into listTasks`).toMatch(/cursor:\s*pageParam/);
  });

  it('both say so when the drain stops at the cap', () => {
    for (const f of ['screens/Reports.tsx', 'screens/Analytics.tsx']) {
      expect(src(f), `${f} must disclose a truncated census`).toMatch(/Census stopped at/);
    }
  });
});
