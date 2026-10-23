/**
 * CHART PRIMITIVES — inline SVG, drawn from d3's pure geometry helpers.
 *
 * REACT OWNS THE DOM; d3 OWNS THE MATH. Every d3 symbol imported here (`arc`, `pie`, `line`, `area`,
 * `stack`, `scaleBand`, `scaleLinear`) is a pure function returning a path string or a number —
 * nothing selects, appends or transitions. That is why the sub-packages are declared rather than the
 * full `d3` bundle: importing `d3` puts `d3-selection` one autocomplete away, and a `d3.select()`
 * into a React-rendered subtree is how a codebase ends up with two renderers fighting over the same
 * nodes. It also makes these testable — a pure function's output is a string jsdom can assert on.
 *
 * EVERY FILL IS A THEME TOKEN. Both palettes are driven by CSS custom properties on `data-theme`, so
 * a hardcoded colour here would be correct in exactly one of them. No chart introduces a token; they
 * reuse the status and priority ramps that already ship and are already contrast-measured.
 *
 * EVERY CHART STATES ITS WINDOW. `ChartFrame` takes `window` as a REQUIRED prop, so a chart that
 * cannot say what period it covers is a type error rather than a judgement call. Our data is a
 * bounded census, not the full history — a chart that implies otherwise is the failure mode this
 * whole module was designed around (see `reports.ts`).
 */
import { useId, type ReactNode } from 'react';
import { arc, pie, line, area, stack, stackOrderNone, stackOffsetNone, curveLinear } from 'd3-shape';
import { scaleBand, scaleLinear } from 'd3-scale';
import { Empty, Note, Panel } from './ui.js';

/* ---- frame ----------------------------------------------------------------------------------- */

export type ChartState = 'loading' | 'error' | 'empty' | 'ready';

export function ChartFrame({
  title,
  window: win,
  state,
  errorText,
  emptyText,
  footnote,
  children,
}: {
  title: string;
  /** REQUIRED. What period this covers, in the reader's words — "Snapshot · now", "last 14 days". */
  window: string;
  state: ChartState;
  errorText?: string;
  emptyText?: string;
  footnote?: ReactNode;
  children: ReactNode;
}) {
  return (
    <Panel
      title={title}
      // The window rides the panel head, so it is visible in EVERY state — including loading, where
      // the period is a property of the question and is already known.
      right={<span className="mono" style={{ fontSize: 11 }}>{win}</span>}
      pad
      {...(footnote ? { footnote } : {})}
    >
      {state === 'loading' && <div className="skeleton" style={{ height: 120 }} />}
      {state === 'error' && <Note>{errorText ?? 'Could not load this chart.'}</Note>}
      {state === 'empty' && <Empty>{emptyText ?? 'Nothing to show yet.'}</Empty>}
      {state === 'ready' && children}
    </Panel>
  );
}

/* ---- donut ----------------------------------------------------------------------------------- */

export interface Slice {
  key: string;
  label: string;
  value: number;
  /** A `var(--…)` token, never a literal colour. */
  color: string;
}

export function Donut({
  slices,
  total,
  totalLabel,
}: {
  slices: readonly Slice[];
  total: number;
  totalLabel: string;
}) {
  const size = 168;
  const thickness = 30;
  const titleId = useId();
  const descId = useId();
  const live = slices.filter((s) => s.value > 0);
  const r = size / 2;
  const layout = pie<Slice>()
    .sort(null) // caller's order is meaningful (status ramp); do not re-sort by value
    .value((s) => s.value)(live as Slice[]);
  const shape = arc<(typeof layout)[number]>().innerRadius(r - thickness).outerRadius(r).padAngle(0.012);
  const pct = (v: number): string => (total > 0 ? `${Math.round((v / total) * 100)}%` : '0%');

  return (
    <div style={{ display: 'flex', gap: 16, alignItems: 'center', flexWrap: 'wrap' }}>
      <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} role="img" aria-labelledby={`${titleId} ${descId}`}>
        <title id={titleId}>{`${totalLabel}: ${total}`}</title>
        <desc id={descId}>{live.map((s) => `${s.label} ${s.value} (${pct(s.value)})`).join(', ') || 'no data'}</desc>
        <g transform={`translate(${r},${r})`}>
          {layout.map((d) => (
            <path key={d.data.key} d={shape(d) ?? undefined} fill={d.data.color} />
          ))}
          <text textAnchor="middle" y={-2} style={{ fontSize: 20, fontWeight: 600, fill: 'var(--text)' }}>
            {total}
          </text>
          <text textAnchor="middle" y={18} style={{ fontSize: 11, fill: 'var(--text-muted)' }}>
            {totalLabel}
          </text>
        </g>
      </svg>
      {/* A LIST, not a stack of divs — the legend carries every value, so a screen reader gets the
          same numbers a sighted reader does without parsing the SVG. */}
      <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0 }}>
        {live.map((s) => (
          <li key={s.key} style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12 }}>
            <span style={{ width: 9, height: 9, borderRadius: 2, background: s.color, flex: '0 0 auto' }} />
            <span style={{ color: 'var(--text-2)' }}>{s.label}</span>
            <span className="mono" style={{ marginLeft: 'auto', color: 'var(--text-muted)' }}>
              {s.value} · {pct(s.value)}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/* ---- stacked bars ---------------------------------------------------------------------------- */

export function StackedBars({
  days,
  series,
  height = 160,
  title,
  desc,
  table = true,
}: {
  days: readonly string[];
  series: readonly { key: string; label: string; color: string; values: readonly number[] }[];
  height?: number;
  title: string;
  desc: string;
  /** A `<details>` data table beside the chart. On by default where the numbers are the point. */
  table?: boolean;
}) {
  const titleId = useId();
  const descId = useId();
  const W = 640;
  const padB = 18;
  const rows = days.map((_day, i) => {
    const row: Record<string, number> = { i };
    for (const s of series) row[s.key] = s.values[i] ?? 0;
    return row;
  });
  const stacked = stack<Record<string, number>>()
    .keys(series.map((s) => s.key))
    .order(stackOrderNone)
    .offset(stackOffsetNone)(rows);
  const maxY = Math.max(1, ...stacked.flatMap((layer) => layer.map((seg) => seg[1])));
  // A BAND SCALE GIVES ONE ITEM THE WHOLE WIDTH. Measured on staging: a board created today rendered
  // "created per day" as a single 640px-wide slab that reads as a broken block rather than a bar.
  // Capping the band keeps a sparse chart looking like a chart; the scale still spans the full range
  // so many days fill the width as before.
  const MAX_BAND = 64;
  const bandW = Math.min(MAX_BAND, (W / Math.max(1, days.length)) * 0.8);
  const x = scaleBand<number>()
    .domain(days.map((_, i) => i))
    .range([0, Math.min(W, (bandW / 0.8) * days.length)])
    .padding(0.2);
  const y = scaleLinear().domain([0, maxY]).range([height - padB, 0]);
  const every = Math.max(1, Math.ceil(days.length / 12));

  return (
    <>
      <div style={{ overflowX: 'auto' }}>
        <svg
          viewBox={`0 0 ${W} ${height}`}
          style={{ width: '100%', minWidth: 320, height }}
          role="img"
          aria-labelledby={`${titleId} ${descId}`}
        >
          <title id={titleId}>{title}</title>
          <desc id={descId}>{desc}</desc>
          {stacked.map((layer, li) => (
            <g key={series[li]!.key} fill={series[li]!.color}>
              {layer.map((seg, i) =>
                seg[1] - seg[0] > 0 ? (
                  <rect
                    key={i}
                    x={x(i) ?? 0}
                    y={y(seg[1])}
                    width={x.bandwidth()}
                    height={Math.max(1, y(seg[0]) - y(seg[1]))}
                  >
                    <title>{`${days[i]} · ${series[li]!.label}: ${seg[1] - seg[0]}`}</title>
                  </rect>
                ) : null,
              )}
            </g>
          ))}
          {days.map((d, i) =>
            i % every === 0 ? (
              <text
                key={d}
                x={(x(i) ?? 0) + x.bandwidth() / 2}
                y={height - 4}
                textAnchor="middle"
                style={{ fontSize: 10, fill: 'var(--text-muted)' }}
              >
                {d.slice(5)}
              </text>
            ) : null,
          )}
        </svg>
      </div>
      {table && (
        <details style={{ marginTop: 8 }}>
          <summary style={{ cursor: 'pointer', fontSize: 12, color: 'var(--text-muted)' }}>Data table</summary>
          <div style={{ overflowX: 'auto' }}>
            <table style={{ whiteSpace: 'nowrap' }}>
              <thead>
                <tr>
                  <th>Day</th>
                  {series.map((s) => (
                    <th key={s.key}>{s.label}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {days.map((d, i) => (
                  <tr key={d}>
                    <td>{d}</td>
                    {series.map((s) => (
                      <td key={s.key}>{s.values[i] ?? 0}</td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </details>
      )}
    </>
  );
}

/* ---- area + line ----------------------------------------------------------------------------- */

export function AreaLine({
  days,
  values,
  color = 'var(--accent)',
  height = 120,
  title,
  desc,
}: {
  days: readonly string[];
  values: readonly number[];
  color?: string;
  height?: number;
  title: string;
  desc: string;
}) {
  const titleId = useId();
  const descId = useId();
  const W = 640;
  const padB = 18;
  const maxY = Math.max(1, ...values);
  const x = scaleLinear().domain([0, Math.max(1, values.length - 1)]).range([0, W]);
  const y = scaleLinear().domain([0, maxY]).range([height - padB, 0]);
  // curveLinear, NOT curveMonotoneX: a smoothed curve invents plausible values between days we never
  // observed. The markers say "these are the samples".
  const l = line<number>().x((_, i) => x(i)).y((v) => y(v)).curve(curveLinear);
  const a = area<number>().x((_, i) => x(i)).y0(y(0)).y1((v) => y(v)).curve(curveLinear);
  const every = Math.max(1, Math.ceil(days.length / 10));

  return (
    <div style={{ overflowX: 'auto' }}>
      <svg
        viewBox={`0 0 ${W} ${height}`}
        style={{ width: '100%', minWidth: 320, height }}
        role="img"
        aria-labelledby={`${titleId} ${descId}`}
      >
        <title id={titleId}>{title}</title>
        <desc id={descId}>{desc}</desc>
        <path d={a([...values]) ?? undefined} fill={color} opacity={0.14} />
        <path d={l([...values]) ?? undefined} fill="none" stroke={color} strokeWidth={2} />
        {values.map((v, i) => (
          <circle key={i} cx={x(i)} cy={y(v)} r={2.5} fill={color}>
            <title>{`${days[i]}: ${v}`}</title>
          </circle>
        ))}
        {days.map((d, i) =>
          i % every === 0 ? (
            <text key={d} x={x(i)} y={height - 4} textAnchor="middle" style={{ fontSize: 10, fill: 'var(--text-muted)' }}>
              {d.slice(5)}
            </text>
          ) : null,
        )}
      </svg>
    </div>
  );
}

/* ---- ranked bars ----------------------------------------------------------------------------- */

/**
 * HTML, not SVG — deliberately. These rows are text with a proportion behind them, and text
 * truncation, wrapping and selection are what the DOM already does well. The existing `byTool` bars
 * in Analytics already had this shape.
 */
export function RankedBars({ rows }: {
  rows: readonly { key: string; label: ReactNode; value: number; note?: ReactNode }[];
}) {
  const top = Math.max(1, ...rows.map((r) => r.value));
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      {rows.map((r) => (
        <div key={r.key} style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12 }}>
          <span style={{ flex: '0 0 128px', minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', color: 'var(--text-2)' }}>
            {r.label}
          </span>
          <div style={{ flex: 1, height: 8, background: 'var(--panel-2)', borderRadius: 'var(--r-full)', overflow: 'hidden', minWidth: 40 }}>
            <div
              data-fill=""
              style={{
                width: `${(r.value / top) * 100}%`,
                height: '100%',
                background: 'color-mix(in srgb, var(--accent) 65%, transparent)',
                borderRadius: 'var(--r-full)',
              }}
            />
          </div>
          <span className="mono" style={{ flex: '0 0 48px', textAlign: 'right', color: 'var(--text-muted)' }}>
            {r.value}
          </span>
          {r.note != null && <span className="muted" style={{ flex: '0 0 auto' }}>{r.note}</span>}
        </div>
      ))}
    </div>
  );
}

/* ---- error copy ------------------------------------------------------------------------------ */

/**
 * Upstream failures, told apart.
 *
 * The gateway deliberately PRESERVES the distinction between 401/403/502/503 (`app.ts` comments say
 * so: collapsing them is how a membership refusal gets read as an outage) — and Analytics then threw
 * it away, rendering one "Analytics service unavailable." for every cause. This is where that
 * distinction reaches the reader.
 */
export function describeQueryError(err: unknown, subject: string): string {
  const msg = err instanceof Error ? err.message : String(err ?? '');
  // `ApiError` carries the status as a FIELD (api/http.ts) — its `message` is the server's text and often
  // has no number in it at all. Reading the field is the difference between five distinct messages
  // and one fallback; the regex is only for errors that are not ApiError.
  const fromField = (err as { status?: unknown })?.status;
  const status = String(
    typeof fromField === 'number' ? fromField : (/\b(40[13]|429|50[023])\b/.exec(msg)?.[1] ?? ''),
  );
  switch (status) {
    case '401':
      return 'Your session expired — sign in again to see this.';
    case '403':
      return `You do not have access to ${subject} for this project.`;
    case '429':
      return 'Rate limited — this panel will retry on the next refresh.';
    case '503':
      return `${subject} is not configured in this environment.`;
    case '502':
    case '500':
      return `${subject} failed to answer. The rest of this page is unaffected.`;
    default:
      return msg || `Could not load ${subject}.`;
  }
}
