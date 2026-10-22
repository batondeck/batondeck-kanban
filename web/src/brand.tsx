/**
 * The BatonDeck brand in the SPA: the lockup, and the loader.
 *
 * THE ARTWORK IS THE KIT'S. `web/kit` vendors hdtp-web-kit's brand set into `public/brand/`, served
 * at `/brand/`, and `make kit-check` holds every file to the sha256 pinned in `web/kit/kit.lock`.
 * Nothing here draws a logo; it points at those files. The lockup comes in two cuts, ink-on-light
 * and cream-on-dark, and `styles/brand.css` shows the one that matches the theme.
 */
import { type CSSProperties } from 'react';

import './styles/brand.css';

/** The kit lockup's own aspect ratio (`batondeck-inline.svg`, 473x84), so a caller sizes by height. */
const LOCKUP_RATIO = 473 / 84;

/**
 * The full lockup: mark, then the word. `height` is the lockup's own height. The wrapper carries the
 * accessible name once; the two cuts inside it are decorative.
 */
export function BrandLockup({ height = 28, style }: { height?: number; style?: CSSProperties }) {
  const width = Math.round(height * LOCKUP_RATIO * 100) / 100;
  return (
    <span className="bd-lockup" role="img" aria-label="BatonDeck" style={style}>
      <img className="bd-logo bd-logo-light" src="/brand/batondeck-inline.svg" width={width} height={height} alt="" />
      <img className="bd-logo bd-logo-dark" src="/brand/batondeck-inline-dark.svg" width={width} height={height} alt="" />
    </span>
  );
}

/* ================================================================================================
   THE LOADER — fork/join.

   Two branches leave the top node and race to the join at the bottom; the ink branch lands first.
   That is the picture of what the board actually does, which is why it is the loader rather than a
   generic spinner.

   NOT EXPORTED. It is the primitive the three wrappers below compose, and both `BootLoader` and
   `WaitLoader` take `value` — so the determinate mode is fully reachable through the public API
   without a fourth exported symbol that nothing calls.

   `value` IS OPTIONAL AND THAT IS THE WHOLE DESIGN. Almost every wait in this SPA is a react-query
   `isPending` — a boolean, with no percentage to give. Passing one anyway would mean inventing a
   number, so with no `value` the component runs a CSS-only cycle instead (see styles/brand.css) and
   shows no figure. Give it a `value` and it becomes a real gauge.
   ============================================================================================= */

/** Clockwise ring wipe: maps a branch's progress across [a, z] onto 0-100 of a node's border. */
const sweep = (p: number, a: number, z: number) => Math.max(0, Math.min(1, (p - a) / (z - a))) * 100;

/** A ring at `v` percent. At full sweep the dash pattern is DROPPED: a dash of exactly one
 *  circumference meets its own start and leaves a visible seam. At zero it is hidden, because a
 *  zero-length round cap still paints a dot. */
function ring(v: number) {
  return {
    strokeOpacity: v <= 0 ? 0 : 1,
    strokeDasharray: v >= 99.9 ? 'none' : `${v} 100`,
  } as const;
}

function Loader({
  value,
  size = 64,
  label = 'Loading',
}: {
  /** 0-100. Omit for an indeterminate loader — do not pass a made-up number. */
  value?: number;
  size?: number;
  label?: string;
}) {
  const determinate = typeof value === 'number' && Number.isFinite(value);
  const v = determinate ? Math.max(0, Math.min(100, value)) : 0;

  // The ink branch leads by ~18%, capped at 100 — the same asymmetry the indeterminate cycle shows,
  // so a load that starts unknown and becomes known does not change character halfway through.
  // The LABEL shows `value`, not the mean of the two branches: the number a caller passed in must
  // never disagree with the number on screen.
  const ember = v;
  const ink = Math.min(100, v * 1.18);
  const done = determinate && v >= 100;

  /* The run class is COMPUTED from `determinate`, never stripped out of a string.
     It was `cls.replace(' bd-run-e', '')`, and `'bd-run-e'` has no leading space — so the ember
     branch kept its animation class in determinate mode, the CSS keyframes outranked the
     `stroke-dasharray` presentation attribute, and that branch looped on 2.4s while ignoring `value`
     entirely. Half of "it accepts a percentage" was dead, and it read as fine because the OTHER
     branch (whose class DID have the leading space) tracked the value correctly.
     `brand-loader.test.tsx` is the regression: it asserts on both branches, separately. */
  const branch = (progress: number, base: string, run: string, d: string, colour?: string) => (
    <path
      className={determinate ? base || undefined : `${base} ${run}`.trim()}
      d={d}
      pathLength="100"
      strokeWidth="3.4"
      stroke={colour}
      strokeDasharray={determinate ? `${progress} 100` : undefined}
    />
  );

  return (
    <svg
      className="bd-loader"
      viewBox="0 0 64 64"
      width={size}
      height={size}
      role="img"
      aria-label={label}
      // A determinate loader IS a progressbar to assistive tech; an indeterminate one is not, and
      // announcing a bogus 0% would be worse than announcing nothing.
      {...(determinate
        ? { 'aria-valuenow': Math.round(v), 'aria-valuemin': 0, 'aria-valuemax': 100, role: 'progressbar' as const }
        : {})}
    >
      <g fill="none" strokeLinecap="round">
        <path
          className="bd-loader-track"
          d="M32 8 L8 32 L32 56 L56 32 Z"
          strokeWidth="2.75"
          strokeLinejoin="round"
        />
        {branch(ink, 'bd-i', 'bd-run-i', 'M32 8 L56 32 L32 56')}
        {branch(ember, '', 'bd-run-e', 'M32 8 L8 32 L32 56', 'var(--ink)')}
      </g>

      {/* start */}
      <circle cx="32" cy="8" r="4.6" fill="var(--ink)" />

      {/* the two waypoints: knocked out of the track, then their border drawn as its branch passes */}
      <circle className="bd-k" cx="8" cy="32" r="5.98" />
      <circle className="bd-loader-node" cx="8" cy="32" r="4.6" strokeWidth="2.75" fill={done ? 'var(--ink)' : undefined} />
      <circle
        cx="8" cy="32" r="4.6" fill="none" stroke="var(--ink)" strokeWidth="2.75"
        pathLength="100" strokeLinecap="round" transform="rotate(-45 8 32)"
        {...(determinate ? ring(sweep(ember, 44, 56)) : { strokeOpacity: 0 })}
      />
      <circle className="bd-k" cx="56" cy="32" r="5.98" />
      <circle className="bd-loader-node" cx="56" cy="32" r="4.6" strokeWidth="2.75" />
      <circle
        className="bd-i"
        cx="56" cy="32" r="4.6" fill="none" strokeWidth="2.75"
        pathLength="100" strokeLinecap="round" transform="rotate(-135 56 32)"
        {...(determinate ? ring(sweep(ink, 44, 56)) : { strokeOpacity: 0 })}
      />

      {/* the join: ink lands first and darkens the dot, then ember closes its border — that border
          reaching all the way round IS 100%. */}
      <circle className="bd-k" cx="32" cy="56" r="4.6" />
      <circle
        className="bd-if"
        cx="32" cy="56" r="4.6"
        fillOpacity={determinate && ink >= 100 ? 1 : 0.14}
      />
      <circle
        cx="32" cy="56" r="4.6" fill="none" stroke="var(--ink)" strokeWidth="2.75"
        pathLength="100" strokeLinecap="round" transform="rotate(-135 32 56)"
        {...(determinate ? ring(sweep(ember, 90, 100)) : { strokeOpacity: 0 })}
      />

      {determinate && (
        <text
          className="bd-loader-pct"
          x="33" y="35.5" textAnchor="middle"
          fontSize="9.5" fontWeight="600"
          fill={done ? 'var(--ink)' : undefined}
        >
          {Math.round(v)}%
        </text>
      )}
    </svg>
  );
}

/**
 * The whole-window wait, for when the app has nothing to render yet — booting, resolving a session,
 * resolving an organization. Replaces the bare "Loading…" line that used to sit in `.center muted`.
 */
export function BootLoader({ caption = 'Loading…', value }: { caption?: string; value?: number }) {
  return (
    <div className="bd-boot" role="status" aria-live="polite">
      <Loader size={72} value={value} label={caption} />
      <span className="bd-caption">{caption}</span>
    </div>
  );
}

/** The in-page wait: a screen that already has its chrome and is filling a panel. */
export function WaitLoader({ caption = 'Loading…', value }: { caption?: string; value?: number }) {
  return (
    <div className="bd-wait" role="status" aria-live="polite">
      <Loader size={48} value={value} label={caption} />
      <span className="bd-caption">{caption}</span>
    </div>
  );
}

/**
 * The inline one, for a row or a button that is mid-write. It takes the line's colour and carries no
 * caption — the surrounding text is the caption.
 *
 * DO NOT GO BELOW 16px. The mark's strokes are 2.75 units in a 64-unit viewBox, so at 12px they land
 * on ~0.5 device pixels and the whole thing renders as a few faint specks — rendered and looked at,
 * not reasoned about. 16 is where the diamond and its nodes still read; 12 and 14 do not.
 */
export function InlineLoader({ size = 16, label = 'Working' }: { size?: number; label?: string }) {
  return (
    <span className="bd-spin" style={{ width: size, height: size }}>
      <Loader size={size} label={label} />
    </span>
  );
}
