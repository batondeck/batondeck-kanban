/**
 * UI GLYPHS — the same drawing as the nav, so a button icon and a nav icon are visibly one family.
 *
 * WHY THIS EXISTS. The ticket surfaces used EMOJI for controls: `🔒 Claim to work`, `🔗` for copy
 * link, `⤢` for expand, `✕` for close. Reported from staging: *"emojies not required, we need actual
 * icons which match rest of the site"*. Emoji are the wrong primitive for a control, and not as a
 * matter of taste:
 *
 *  - They are rendered by the PLATFORM font, so the same button is a flat glyph on one OS and a
 *    glossy full-colour sticker on another. Nothing in our stylesheet can reach them.
 *  - They ignore `currentColor`, so they do not follow a button's text colour, its disabled state,
 *    or either theme. A dark-mode button gets a light-mode emoji.
 *  - They carry their own text semantics — a screen reader announces "locked padlock" beside the
 *    word "Claim", which is noise, not information.
 *
 * These are stroked 24×24 paths on `currentColor`, matching `ICONS` in `App.tsx`. `aria-hidden` is
 * ALWAYS on: every caller pairs the glyph with a visible label or an `aria-label` on the button, and
 * a second name for the same control makes it announce twice.
 *
 * Keep the set small. An icon nothing renders is dead weight the bundler still ships, and knip does
 * not see through a lookup map.
 */
const PATHS = {
  /** Claim / lease — a closed padlock. Replaces 🔒. */
  lock: 'M7 11V8a5 5 0 0 1 10 0v3M5 11h14v10H5z',
  /** Copy link — two chain links. Replaces 🔗. */
  link: 'M10 13a5 5 0 0 0 7.5.5l2-2a5 5 0 0 0-7-7l-1 1M14 11a5 5 0 0 0-7.5-.5l-2 2a5 5 0 0 0 7 7l1-1',
  /** Open the full page — arrow out of a corner. Replaces ⤢. */
  expand: 'M15 3h6v6M21 3l-9 9M9 21H3v-6M3 21l9-9',
  /** Close. Replaces ✕. */
  close: 'M18 6 6 18M6 6l12 12',
  /** Parent / run-of — an arrow up to the record above. Replaces ↑. */
  up: 'M12 19V5M5 12l7-7 7 7',
  /** Estimate / duration — a clock. Replaces ⏱. */
  clock: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18M12 7v5l3 2',
  /** A configured value: model hint, custom field, required capability. Replaces ⚙. */
  sliders: 'M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1 14h6M9 8h6M17 16h6',
  /** Completion. Replaces ✓. */
  check: 'M20 6 9 17l-5-5',
  /** Starred / shortlist — the catalog favourite toggle and its filters. Replaces ★. */
  star: 'M12 3l2.9 5.9 6.5.9-4.7 4.6 1.1 6.4L12 17.8l-5.8 3 1.1-6.4L2.6 9.8l6.5-.9z',
  /** Subtask progress — a ticked box. Replaces ☑. */
  checkSquare: 'M9 11l3 3L20 6M20 12v7a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1h10',
  /** Sprint. Replaces ⚑. */
  flag: 'M5 21V4M5 4h11l-2 4 2 4H5',
  /** A finished/raced run. Replaces 🏁. */
  finish: 'M4 21V4h16v10H4M4 9h16M9 4v10M14 4v10',
  /** The winning run. Replaces 🏆. */
  trophy: 'M8 4h8v5a4 4 0 0 1-8 0zM8 6H5v1a3 3 0 0 0 3 3M16 6h3v1a3 3 0 0 1-3 3M10 17h4M9 21h6',
  /** The chronicle. Replaces 📖. */
  book: 'M4 19.5A2.5 2.5 0 0 1 6.5 17H20M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z',
  /** Notifications. Replaces 🔔. */
  bell: 'M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9M13.73 21a2 2 0 0 1-3.46 0',
  /** Settings / setup. Replaces ⚙. */
  settings: 'M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1 14h6M9 8h6M17 16h6',
  /** An agent, when no client logo is known. Replaces 🤖. */
  bot: 'M9 2v3M15 2v3M9 19v3M15 19v3M2 9h3M2 15h3M19 9h3M19 15h3M5 5h14v14H5z',
  /** Workspace admin. Replaces 👑. */
  crown: 'M3 18h18M3 18 2 7l5.5 4L12 4l4.5 7L22 7l-1 11',
  /** Disclosure. Replaces ▶. */
  chevronRight: 'M9 6l6 6-6 6',
  /** A menu trigger's caret. Replaces ▾. */
  chevronDown: 'M6 9l6 6 6-6',
  /**
   * THE PRODUCT MARK — one task gating two, the same figure as the docs-site logo, so the app and
   * the docs read as one product rather than two. Stroked like every other icon here, so it takes
   * the brand colour from `currentColor` at the call site.
   */
  batondeck: 'M12 7.2c0 3-5 2.6-5 6.6M12 7.2c0 3 5 2.6 5 6.6M12 4.4a1.4 1.4 0 1 0 0 2.8 1.4 1.4 0 0 0 0-2.8M7 13.8a1.4 1.4 0 1 0 0 2.8 1.4 1.4 0 0 0 0-2.8M17 13.8a1.4 1.4 0 1 0 0 2.8 1.4 1.4 0 0 0 0-2.8',
  /** Delta up / down on a tile. Replaces ▲ ▼. */
  arrowUp: 'M12 19V5M5 12l7-7 7 7',
  arrowDown: 'M12 5v14M19 12l-7 7-7-7',
  /** Attachment kinds. Replace 🖼 🎬 🎵 📄. */
  image: 'M3 5h18v14H3zM3 16l5-5 4 4 3-3 6 6',
  video: 'M15 10l6-4v12l-6-4zM3 6h12v12H3z',
  audio: 'M9 18V5l10-2v13M9 18a3 3 0 1 1-6 0 3 3 0 0 1 6 0m10-2a3 3 0 1 1-6 0 3 3 0 0 1 6 0',
  file: 'M14 3H6a1 1 0 0 0-1 1v16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8zM14 3v5h5',
} as const;

export type IconName = keyof typeof PATHS;

export function Icon({ name, size = 14 }: { name: IconName; size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      /* Baseline-aligns the glyph with adjacent label text; without it the icon rides high in a
         button whose line-height is set by the text beside it. */
      style={{ flex: '0 0 auto', display: 'block' }}
    >
      <path d={PATHS[name]} />
    </svg>
  );
}
