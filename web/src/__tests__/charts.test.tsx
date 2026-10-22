/**
 * The chart primitives.
 *
 * WHAT JSDOM CAN PROVE HERE: the exact `d` attribute d3 produced, element counts, ARIA names,
 * `<title>`/`<desc>` text, and the data-table cells. Those are computed values, not measured ones.
 *
 * WHAT IT CANNOT PROVE, and no assertion below claims: that a chart is visible, correctly sized,
 * legible, non-overlapping, or right in either palette. That needs a browser.
 *
 * The `describeQueryError` cases are the anti-regression for a real defect in Analytics: the gateway
 * deliberately preserves 401/403/502/503 (its own comment says collapsing them is how a membership
 * refusal reads as an outage) and the UI then flattened every one into "Analytics service
 * unavailable."
 */
import { describe, it, expect } from 'vitest';
import { render, screen, cleanup, within } from '@testing-library/react';
import { AreaLine, ChartFrame, Donut, RankedBars, StackedBars, describeQueryError } from '../charts.js';

const slices = [
  { key: 'READY', label: 'Ready', value: 3, color: 'var(--ready)' },
  { key: 'DONE', label: 'Done', value: 1, color: 'var(--done)' },
  { key: 'BLOCKED', label: 'Blocked', value: 0, color: 'var(--blocked)' },
];

describe('Donut', () => {
  it('draws one arc per NON-ZERO slice', () => {
    const { container } = render(<Donut slices={slices} total={4} totalLabel="tasks" />);
    // Three slices, one of them zero — a zero-width arc is a rendering artefact, not data.
    expect(container.querySelectorAll('path')).toHaveLength(2);
    for (const p of container.querySelectorAll('path')) {
      expect(p.getAttribute('d'), 'd3 must have produced a real arc path').toMatch(/^M[-\d.]/);
    }
  });

  it('is an image with an accessible name and a described breakdown', () => {
    render(<Donut slices={slices} total={4} totalLabel="tasks" />);
    const svg = screen.getByRole('img');
    expect(svg.querySelector('title')?.textContent).toBe('tasks: 4');
    // The description carries every value AND its share, so the SVG is not the only way to read it.
    expect(svg.querySelector('desc')?.textContent).toBe('Ready 3 (75%), Done 1 (25%)');
  });

  it('legend is a list carrying the same numbers', () => {
    render(<Donut slices={slices} total={4} totalLabel="tasks" />);
    const items = screen.getAllByRole('listitem');
    expect(items).toHaveLength(2);
    expect(items[0]!.textContent).toContain('Ready');
    expect(items[0]!.textContent).toContain('3 · 75%');
  });

  it('renders nothing but the frame when every slice is zero', () => {
    const { container } = render(
      <Donut slices={[{ key: 'a', label: 'A', value: 0, color: 'var(--ready)' }]} total={0} totalLabel="tasks" />,
    );
    expect(container.querySelectorAll('path')).toHaveLength(0);
    expect(container.querySelector('desc')?.textContent).toBe('no data');
  });
});

describe('StackedBars', () => {
  const days = ['2026-08-01', '2026-08-02', '2026-08-03'];
  const series = [
    { key: 'READY', label: 'Ready', color: 'var(--ready)', values: [1, 0, 2] },
    { key: 'DONE', label: 'Done', color: 'var(--done)', values: [0, 3, 1] },
  ];

  it('draws a rect per non-zero segment', () => {
    const { container } = render(<StackedBars days={days} series={series} title="t" desc="d" />);
    // 6 cells, 2 of which are zero → 4 rects.
    expect(container.querySelectorAll('rect')).toHaveLength(4);
  });

  it('offers a data table whose cells match the input exactly', () => {
    render(<StackedBars days={days} series={series} title="t" desc="d" />);
    const table = screen.getByRole('table');
    const rows = within(table).getAllByRole('row');
    expect(rows[1]!.textContent).toBe('2026-08-0110');
    expect(rows[2]!.textContent).toBe('2026-08-0203');
    expect(rows[3]!.textContent).toBe('2026-08-0321');
  });

  it('caps the bar width so a single-day chart is not one full-width slab', () => {
    // FOUND ON STAGING, not in jsdom: a board created today rendered `created per day` as a 640px
    // block. A band scale hands one item the entire range, which stops looking like a bar chart.
    const { container } = render(
      <StackedBars days={['2026-08-28']} series={[{ key: 'READY', label: 'R', color: 'var(--ready)', values: [5] }]} title="t" desc="d" />,
    );
    const w = Number(container.querySelector('rect')!.getAttribute('width'));
    expect(w).toBeGreaterThan(0);
    expect(w, 'one day must not consume the whole plot width').toBeLessThan(100);
  });

  it('still fills the width when there are many days', () => {
    // The positive control for the cap: it must not shrink a dense chart into a corner.
    const days = Array.from({ length: 30 }, (_, i) => `2026-08-${String(i + 1).padStart(2, '0')}`);
    const { container } = render(
      <StackedBars days={days} series={[{ key: 'R', label: 'R', color: 'var(--ready)', values: days.map(() => 1) }]} title="t" desc="d" />,
    );
    const rects = [...container.querySelectorAll('rect')];
    const rightmost = Math.max(...rects.map((r) => Number(r.getAttribute('x')) + Number(r.getAttribute('width'))));
    expect(rightmost, 'a 30-day chart should still span most of the plot').toBeGreaterThan(500);
  });

  it('carries its title and description for assistive tech', () => {
    render(<StackedBars days={days} series={series} title="Created per day" desc="3 days" />);
    const svg = screen.getByRole('img');
    expect(svg.querySelector('title')?.textContent).toBe('Created per day');
    expect(svg.querySelector('desc')?.textContent).toBe('3 days');
  });
});

describe('AreaLine', () => {
  it('plots one marker per sample and a linear path through them', () => {
    const { container } = render(
      <AreaLine days={['2026-08-01', '2026-08-02', '2026-08-03']} values={[1, 0, 5]} title="t" desc="d" />,
    );
    expect(container.querySelectorAll('circle')).toHaveLength(3);
    const path = container.querySelectorAll('path')[1]!.getAttribute('d')!;
    // curveLinear produces straight segments (L), never cubic ones (C). A smoothed curve would
    // invent values between days we never sampled.
    expect(path).toMatch(/^M/);
    expect(path).toContain('L');
    expect(path).not.toContain('C');
  });

  it('renders a zero-valued window as a flat line rather than throwing', () => {
    const { container } = render(<AreaLine days={['2026-08-01', '2026-08-02']} values={[0, 0]} title="t" desc="d" />);
    expect(container.querySelectorAll('circle')).toHaveLength(2);
  });
});

describe('RankedBars', () => {
  it('scales every row against the largest by default', () => {
    const { container } = render(
      <RankedBars rows={[{ key: 'a', label: 'A', value: 10 }, { key: 'b', label: 'B', value: 5 }]} />,
    );
    const fills = [...container.querySelectorAll('[data-fill]')].map((d) => (d as HTMLElement).style.width);
    expect(fills).toEqual(['100%', '50%']);
  });

  // The `max` prop this used to exercise is GONE (rule 16): nothing in the app passed it, so the
  // option existed only so this test could pass it. What remains is the real behaviour — the widest
  // row sets the scale.
  it('a single row fills the track, so the scale comes from the data', () => {
    const { container } = render(<RankedBars rows={[{ key: 'a', label: 'A', value: 5 }]} />);
    expect((container.querySelector('[data-fill]') as HTMLElement).style.width).toBe('100%');
  });
});

describe('ChartFrame', () => {
  it('shows the window in EVERY state, including while loading', () => {
    for (const state of ['loading', 'error', 'empty', 'ready'] as const) {
      cleanup();
      render(
        <ChartFrame title="Status" window="Snapshot · now" state={state}>
          <p>chart</p>
        </ChartFrame>,
      );
      expect(screen.getByText(/Snapshot · now/), `window missing in the ${state} state`).toBeTruthy();
    }
  });

  it('renders children only when ready', () => {
    render(<ChartFrame title="S" window="w" state="loading"><p>chart</p></ChartFrame>);
    expect(screen.queryByText('chart')).toBeNull();
    cleanup();
    render(<ChartFrame title="S" window="w" state="ready"><p>chart</p></ChartFrame>);
    expect(screen.getByText('chart')).toBeTruthy();
  });
});

describe('describeQueryError', () => {
  it('tells the upstream failures apart', () => {
    const seen = new Set<string>();
    for (const [status, expected] of [
      ['401', /session expired/i],
      ['403', /do not have access/i],
      ['429', /rate limited/i],
      ['503', /not configured/i],
      ['502', /failed to answer/i],
    ] as const) {
      const msg = describeQueryError(new Error(`request failed: ${status}`), 'analytics');
      expect(msg, `${status} produced the wrong copy`).toMatch(expected);
      seen.add(msg);
    }
    // FIVE DISTINCT strings — the point is that a membership refusal cannot read as an outage.
    expect(seen.size).toBe(5);
  });

  it('falls back to the message rather than inventing a cause', () => {
    expect(describeQueryError(new Error('socket hang up'), 'analytics')).toBe('socket hang up');
    expect(describeQueryError(null, 'analytics')).toBe('Could not load analytics.');
  });
});
