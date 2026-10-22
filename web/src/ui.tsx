/* ============================================================================================
   ui.tsx — the "Intent" component vocabulary.

   WHY THIS EXISTS. Before this file the screens carried ~600 inline `style={{…}}` objects
   (TaskDrawer alone had 179). That is not a tidiness complaint: it is why re-theming the app
   needed a `sed` pass over 43 hardcoded `borderRadius` literals, and why a token change could
   land as a HALF retheme — new canvas, old rounded corners. Anything expressed inline is
   invisible to the token layer.

   THE RULE: if a value comes from the design system — a colour, a radius, a border weight, a
   spacing step, a type role — it belongs to a component here or to a token in index.css.
   Inline `style` is for genuinely one-off geometry (a computed width, a chart bar percentage).

   These are deliberately thin. They own *which token* to use, never business logic; they compose
   with the domain components in components.tsx (StatusPill, Avatar, Marquee) rather than
   replacing them.
   ============================================================================================ */
import type { CSSProperties, ReactNode } from 'react';
import { Icon } from './icons.js';
import type { Status } from './types.js';

/* ---- layout ------------------------------------------------------------------------------
   Cluster/Stack exist to delete the single most-repeated inline object in the codebase:
   `{display:'flex', alignItems:'center', gap:8}`. `gap` is a step, not a free number. */
/** Gap is a STEP on the spacing scale, never a free number — 6/10/20 were dropped when the
 *  stylesheets were snapped to the 4-grid, and allowing them here would let the scale rot back in
 *  through inline style. Matches --sp-1..7 in index.css. */
type Gap = 0 | 2 | 4 | 8 | 12 | 16 | 24 | 32;

export function Cluster({
  gap = 8, align = 'center', wrap, justify, className, style, children,
}: {
  gap?: Gap; align?: CSSProperties['alignItems']; wrap?: boolean;
  justify?: CSSProperties['justifyContent']; className?: string;
  style?: CSSProperties; children: ReactNode;
}) {
  return (
    <div className={className} style={{
      display: 'flex', alignItems: align, gap, justifyContent: justify,
      flexWrap: wrap ? 'wrap' : undefined, ...style,
    }}>{children}</div>
  );
}

export function Stack({
  gap = 8, className, style, children,
}: { gap?: Gap; className?: string; style?: CSSProperties; children: ReactNode }) {
  return (
    <div className={className} style={{ display: 'flex', flexDirection: 'column', gap, ...style }}>
      {children}
    </div>
  );
}

/** Pushes whatever follows it to the far edge of a Cluster. Replaces `marginLeft:'auto'`. */
export function Spacer() {
  return <span style={{ flex: 1 }} />;
}

/* ---- the record --------------------------------------------------------------------------
   A Panel is the design's central object: a 1.5px rule around it, a banded header carrying a
   mono letter-spaced caps label. `right` is for counts and secondary actions. */
export function Panel({
  title, right, footnote, danger, pad, scroll, collapsible, defaultOpen = true, level = 3, className, children,
}: {
  title?: ReactNode; right?: ReactNode; footnote?: ReactNode; danger?: boolean;
  /** Pad the body. Needed whenever the children are prose or controls rather than
   *  self-padding rows (`KV`, `LineRow`, `table`) — without it they sit flush against
   *  the 1.5px rule, which reads as a rendering fault rather than a design choice. */
  pad?: boolean;
  /** Cap the body and scroll it when the row list is unbounded — a panel listing every DONE
   *  ticket must not grow to the height of the backlog. The header and `footnote` stay pinned
   *  OUTSIDE the scroll region: the footnote explains what the list means, so scrolling it out
   *  of view is exactly when the reader needs it. Pass the note as `footnote`, not as a child. */
  scroll?: boolean;
  /**
   * Render as a native `<details>` so the reader can fold the panel away.
   *
   * NATIVE `<details>`, NOT `{open && children}` — a closed details keeps its children MOUNTED, so a
   * half-typed field survives being folded and a query for a control inside still resolves.
   * Conditional rendering would drop both. Precedent: `Board.tsx` and `Chronicle.tsx`.
   */
  collapsible?: boolean;
  /** Only meaningful with `collapsible`. Open by default — "collapsible" is not "collapsed". */
  defaultOpen?: boolean;
  /** Heading level for `title` (WCAG 2.2 §1.3.1). 3 by default: pages carry an h1 and
   *  `<Section>` an h2, so a panel is the third rank. */
  level?: 2 | 3 | 4;
  className?: string; children?: ReactNode;
}) {
  const body = pad ? <div className="record-body">{children}</div> : children;
  const cls = ['record', danger ? 'danger' : '', className ?? ''].filter(Boolean).join(' ');
  const Box = collapsible ? 'details' : 'section';
  const Head = collapsible ? 'summary' : 'header';
  return (
    <Box className={cls} {...(collapsible ? { open: defaultOpen } : {})}>
      {title != null && (
        <Head className="record-head">
          {/* A heading element, not a <span>. WCAG 2.2 §1.3.1 (Level A): structure conveyed through
              presentation must be programmatically determinable — a panel title that LOOKS like a
              heading and isn't one leaves screen-reader users with no outline to navigate. The board
              alone renders ~20 of these; the document previously exposed only h1 and h2.
              `level` so a panel nested under a <Section> (h2) can be h3 and not invert the outline. */}
          {(() => {
            const H = `h${level}` as 'h2' | 'h3' | 'h4';
            return <H className="record-title">{title}</H>;
          })()}
          {right != null && <span className="record-head-right">{right}</span>}
        </Head>
      )}
      {scroll ? <div className="record-scroll">{body}</div> : body}
      {footnote != null && <div className="record-note">{footnote}</div>}
    </Box>
  );
}


/** A mono, letter-spaced caps heading between panels. */
export function Section({ children }: { children: ReactNode }) {
  return <h2 className="record-section">{children}</h2>;
}

/* ---- rows --------------------------------------------------------------------------------- */

/** Label-left, value-right. The properties-list row. */
export function KV({ k, children }: { k: ReactNode; children: ReactNode }) {
  return (
    <div className="kv"><span className="kv-k">{k}</span><span className="kv-v">{children}</span></div>
  );
}

/** Full-width row with the value pushed to the right edge. The metric row. */
export function LineRow({ label, value, onClick }: { label: ReactNode; value?: ReactNode; onClick?: () => void }) {
  return (
    <div className="line-row" onClick={onClick} role={onClick ? 'button' : undefined}>
      <span>{label}</span>
      {value != null && <b className="line-row-v">{value}</b>}
    </div>
  );
}

/* ---- vocabulary ---------------------------------------------------------------------------
   PRD P-1 requires facts and opinions to be visually distinct classes, everywhere. Making that
   a prop rather than a colour is what stops the two drifting into looking alike. */
export type TagKind = 'fact' | 'opinion' | 'guard' | 'shadow' | 'plain';
export function Tag({ kind = 'plain', children }: { kind?: TagKind; children: ReactNode }) {
  return <span className={`tag tag-${kind}`}>{children}</span>;
}

export type PillTone = 'ok' | 'no' | 'warn' | 'neutral';
export function Pill({ tone = 'neutral', children }: { tone?: PillTone; children: ReactNode }) {
  return <span className={`pill pill-${tone}`}>{children}</span>;
}

/** Lifecycle role (PRD P-3) — the analytical vocabulary, distinct from a column name AND from
 *  the membership `Role` in types.ts. It lives HERE, not in types.ts: that file mirrors the core's
 *  wire shape and the core emits no role, so putting it there fails architecture.test.ts. Derived
 *  from `status` by `roleOf()`; move it once the core actually writes a column role. */
export type Role =
  | 'intake' | 'ready' | 'doing' | 'handover' | 'parked' | 'verifying' | 'accepted' | 'rejected';
const ROLE_OF: Record<Status, Role> = {
  BACKLOG: 'intake', READY: 'ready', IN_PROGRESS: 'doing', REVIEW: 'handover',
  BLOCKED: 'parked', DONE: 'accepted', CANCELLED: 'rejected',
};
/** Derive the role from a status. Keeps callers off column identity (P-3). */
export function roleOf(status: Status): Role {
  return ROLE_OF[status];
}
export function RoleBadge({ role }: { role: Role }) {
  return <span className={`role role-${role}`}>{role}</span>;
}

/* ---- figures ------------------------------------------------------------------------------ */
export function Tile({ label, value, delta, note }: {
  label: ReactNode; value: ReactNode; delta?: { dir: 'up' | 'down'; text: string }; note?: ReactNode;
}) {
  return (
    <div className="tile">
      <div className="tile-k">{label}</div>
      <div className="tile-v">{value}</div>
      {delta && <div className={`tile-d ${delta.dir}`}><Icon name={delta.dir === 'up' ? 'arrowDown' : 'arrowUp'} size={10} /> {delta.text}</div>}
      {note != null && <div className="tile-note">{note}</div>}
    </div>
  );
}
export function Tiles({ children }: { children: ReactNode }) {
  return <div className="tiles">{children}</div>;
}

/* ---- action -------------------------------------------------------------------------------
   A thin wrapper over the existing `.btn` classes so variants are a prop, not a string of
   class names assembled at each call site. */
export function Button({
  variant = 'secondary', size, className, ...rest
}: { variant?: 'primary' | 'secondary' | 'danger'; size?: 'sm' } &
  React.ButtonHTMLAttributes<HTMLButtonElement>) {
  const cls = ['btn',
    variant === 'secondary' ? 'secondary' : '',
    variant === 'danger' ? 'danger' : '',
    size === 'sm' ? 'sm' : '',
    className ?? ''].filter(Boolean).join(' ');
  return <button className={cls} {...rest} />;
}

/* ============================================================================================
   BOARD + DETAIL VOCABULARY
   Added BEFORE the screens that consume them, deliberately: these are the pieces the board
   grid, the ticket detail page and the PRD panels all share. Building them first is what stops
   four screens each inventing their own tab bar.
   ============================================================================================ */

/** The small square that marks a lifecycle role on a column or panel head. */
export function Dot({ role }: { role: Role }) {
  return <span className={`dot ${role}`} />;
}

/* ---- navigation within a screen ------------------------------------------------------------
   `Tabs` takes items rather than children so that every tab bar in the app has the same
   markup and the same active rule. `href` renders an anchor (real navigation); `onSelect`
   renders a button (in-page section switch). */
export function Tabs<T extends string>({ items, active, onSelect }: {
  items: ReadonlyArray<{ id: T; label: ReactNode; href?: string }>;
  active: T;
  onSelect?: (id: T) => void;
}) {
  return (
    <nav className="tabs">
      {items.map((t) =>
        t.href != null ? (
          <a key={t.id} className={t.id === active ? 'on' : ''} href={t.href}>{t.label}</a>
        ) : (
          <button key={t.id} type="button" className={t.id === active ? 'on' : ''}
            aria-current={t.id === active ? 'page' : undefined}
            onClick={() => onSelect?.(t.id)}>{t.label}</button>
        ),
      )}
    </nav>
  );
}

/** A toggleable filter chip. `on` is driven by the caller — the chip owns no state. */
export function Chip({ on, onClick, title, children }: {
  on?: boolean; onClick?: () => void; title?: string; children: ReactNode;
}) {
  return (
    <button type="button" className={`chip${on ? ' on' : ''}`} aria-pressed={on} title={title} onClick={onClick}>
      {children}
    </button>
  );
}
export function Chips({ children }: { children: ReactNode }) {
  return <div className="chips">{children}</div>;
}

/* ---- page layouts --------------------------------------------------------------------------- */

/** Detail layout: main column + a fixed rail (mock `.side-wide`, 1fr + 420px).
 *  Collapses to a single column under 1100px — the rail is reference material, not chrome. */
export function SideWide({ rail, children }: { rail: ReactNode; children: ReactNode }) {
  return (
    <div className="side-wide">
      <div className="side-main">{children}</div>
      <aside className="side-rail">{rail}</aside>
    </div>
  );
}

/** Equal-width column grid (mock `.grid2` / `.grid3`, and the board's `repeat(N, 1fr)`).
 *  `min` sets the point at which columns stop shrinking and the grid wraps instead. */
export function Cols({ n, min, gap = 16, align, children }: {
  n: number; min?: number; gap?: Gap; align?: 'start'; children: ReactNode;
}) {
  return (
    <div className="cols" style={{
      gridTemplateColumns: min != null
        ? `repeat(${n}, minmax(${min}px, 1fr))`
        : `repeat(${n}, 1fr)`,
      gap, alignItems: align,
    }}>{children}</div>
  );
}

/* ---- small pieces ---------------------------------------------------------------------------- */

/** The recessed explanatory paragraph under a panel's rows — where the *why* goes. */
export function Note({ children }: { children: ReactNode }) {
  return <div className="record-note">{children}</div>;
}

/** Empty state inside a panel or column. */
export function Empty({ children }: { children: ReactNode }) {
  return <div className="empty">{children}</div>;
}

/** Horizontal progress/proportion bar. `tone` carries meaning (a lease running out is not
 *  the same event as soak progressing), so it is a prop rather than an inline colour. */
export type BarTone = 'doing' | 'crit' | 'verifying' | 'accepted' | 'neutral';
export function Bar({ pct, tone = 'neutral', label }: { pct: number; tone?: BarTone; label?: ReactNode }) {
  const w = Math.max(0, Math.min(100, pct));
  return (
    <div className={`bar-h tone-${tone}`}>
      {label != null && <span className="bar-h-label">{label}</span>}
      <span className="bar"><i style={{ width: `${w}%` }} /></span>
    </div>
  );
}

/** Provenance chip — WHERE a ticket came from (human chat, schedule, threshold, agent).
 *  PRD P-1: provenance is a fact about the record and is never styled like a judgement. */
export function Prov({ children }: { children: ReactNode }) {
  return <span className="prov">⌁ {children}</span>;
}

/** Role → the status colour token that represents it.
 *
 * ONE definition. This existed as three byte-identical copies (Trajectory, Timeline, Dependencies)
 * because three screens each needed it and none could edit a shared file. Three copies of a
 * role→colour map is a drift bug waiting to happen: add a role, update two of them, and the third
 * screen paints it as `undefined` — an invisible mark with no error. Use `roleColor(role)`.
 */
export const ROLE_TOKEN: Record<Role, string> = {
  intake: 'backlog', ready: 'ready', doing: 'progress', handover: 'review',
  parked: 'backlog', verifying: 'live', accepted: 'done', rejected: 'blocked',
};
/** `var(--…)` for a role, ready to drop into a style value. */
export function roleColor(role: Role): string {
  return `var(--${ROLE_TOKEN[role]})`;
}
