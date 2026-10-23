/**
 * The incident panel (spec §10) — the properties that matter are both about ABSENCE:
 *
 *  1. It renders NOTHING on an ordinary ticket. A provenance panel that appears empty on every
 *     hand-filed ticket trains people to ignore it.
 *  2. The `incident.*` keys do NOT appear as deletable Fields rows. Deleting `incident.fingerprint`
 *     silently unbinds the ticket from its alert, after which every re-fire mints a NEW ticket —
 *     a data-loss shape with no error, which is exactly the kind a UI test can catch and a type
 *     cannot.
 */
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ContextPanel, IncidentPanel, PropertiesPanel } from '../screens/task/panels.js';
import type { LoadedTaskDetail } from '../screens/task/useTaskDetail.js';

const task = (customFields: Record<string, string | number | boolean>): LoadedTaskDetail =>
  ({
    projectId: 'P-1',
    taskId: 'T-1',
    task: {
      id: 'T-1',
      projectId: 'P-1',
      boardId: 'B-1',
      columnId: 'c1',
      title: 'CheckoutLatencyHigh',
      description: '',
      status: 'TODO',
      priority: 'urgent',
      assignee: null,
      labels: ['incident'],
      requiredCapabilities: [],
      customFields,
      version: 1,
      blockedBy: [],
      blocks: [],
      contextSummary: '',
      createdBy: 'ingest:ing_x',
      createdAt: new Date().toISOString(),
      updatedBy: 'ingest:ing_x',
      updatedAt: new Date().toISOString(),
    },
    ctx: {
      items: [
        { id: 'CI-1', taskId: 'T-1', kind: 'note', body: 'p99 latency 4.2s > 1.5s', author: 'ingest:ing_x', ts: new Date().toISOString() },
        { id: 'CI-2', taskId: 'T-1', kind: 'note', body: 'a human note', author: 'someone@x.test', ts: new Date().toISOString() },
      ],
    },
    run: () => {},
    // PropertiesPanel reads two react-query handles off the hook. Empty results, not fakes of the
    // panel's own logic: the filter under test is real, only the network is absent.
    agentsQ: { data: { sessions: [] } },
    histQ: { data: { events: [] } },
  }) as unknown as LoadedTaskDetail;

const incidentFields = {
  'incident.fingerprint': '9f2ac41d',
  'incident.source': 'grafana',
  'incident.integrationId': 'ing_x',
  'incident.occurrences': 7,
  'incident.alertStatus': 'firing',
};

describe('IncidentPanel', () => {
  it('renders provenance for an incident ticket — fingerprint, source, occurrences, the telemetry brief', () => {
    render(<IncidentPanel d={task(incidentFields)} />);
    expect(screen.getByText('9f2ac41d')).toBeTruthy();
    expect(screen.getByText(/source: grafana/)).toBeTruthy();
    expect(screen.getByText(/7 occurrences/)).toBeTruthy();
    // The brief is identified by its ingest author, not by position — a human note must not be
    // mistaken for the telemetry snapshot.
    expect(screen.getByText(/p99 latency 4.2s/)).toBeTruthy();
    expect(screen.queryByText(/a human note/)).toBeNull();
  });

  it('says the alert cleared WITHOUT implying the ticket is done', () => {
    render(<IncidentPanel d={task({ ...incidentFields, 'incident.alertStatus': 'resolved' })} />);
    expect(screen.getByText(/resolved upstream/)).toBeTruthy();
    expect(screen.getByText(/a resolved alert is not a fixed cause/i)).toBeTruthy();
  });

  it('renders NOTHING for an ordinary ticket', () => {
    const { container } = render(<IncidentPanel d={task({ effort: 'S' })} />);
    expect(container.textContent).toBe('');
  });
});

describe('PropertiesPanel', () => {
  it('does not offer incident.* keys as deletable Fields rows', () => {
    render(<PropertiesPanel d={task({ ...incidentFields, ownField: 'keep-me' })} />);
    // The operator's own custom field is still editable…
    expect(screen.getByText('ownField')).toBeTruthy();
    // …but the correlation keys are not, because deleting one unbinds the ticket from its alert.
    expect(screen.queryByText('incident.fingerprint')).toBeNull();
    expect(screen.queryByText('incident.occurrences')).toBeNull();
  });
});

/**
 * THE PANEL MUST KEEP SHOWING THE TELEMETRY AFTER THE ALERT RESOLVES.
 *
 * Context items arrive NEWEST-FIRST, and the resolve path writes its own `ingest:`-authored note. So
 * picking "the first ingest-authored item" swapped the labels/annotations/links for
 * "Alert resolved upstream at …" the moment an alert cleared — precisely when someone opens the
 * ticket to understand what happened. Only the CREATE path writes a snapshot, so it is the EARLIEST
 * ingest item.
 */
describe('IncidentPanel — which context item is the telemetry', () => {
  const withResolveNote = () => {
    const d = task({ ...incidentFields, 'incident.alertStatus': 'resolved' }) as unknown as {
      ctx: { items: Array<Record<string, string>> };
    };
    // Newest-first, as getTaskContext returns them: the resolve note precedes the snapshot.
    d.ctx.items = [
      {
        id: 'CI-9',
        taskId: 'T-1',
        kind: 'note',
        body: 'Alert resolved upstream at 2026-08-25T10:00:00Z. The ticket stays open.',
        author: 'ingest:ing_x',
        ts: '2026-08-25T10:00:00.000Z',
      },
      {
        id: 'CI-1',
        taskId: 'T-1',
        kind: 'note',
        body: 'p99 latency 4.2s > 1.5s',
        author: 'ingest:ing_x',
        ts: '2026-08-25T09:00:00.000Z',
      },
    ];
    return d as unknown as LoadedTaskDetail;
  };

  it('shows the telemetry snapshot, not the resolve note', () => {
    render(<IncidentPanel d={withResolveNote()} />);
    expect(screen.getByText(/p99 latency 4\.2s/)).toBeTruthy();
    expect(screen.queryByText(/Alert resolved upstream at/)).toBeNull();
  });
});

/**
 * THE BRIEF IS MARKDOWN, AND IT IS SHOWN ONCE.
 *
 * Reported from staging: an incident ticket showed `**TEST** Wiring check` with the asterisks
 * printed, and the SAME body appeared a second time under Context items. Two independent halves:
 *
 *  - `src/domain/incident.ts` COMPOSES markdown (`**${status}** ${title}`, `- k=v` bullets, a
 *    bare-URL `links:` list) and `IncidentPanel` rendered it in a raw `<pre>`;
 *  - `ContextPanel` filtered only `kind !== 'link'`, so the ingest-authored brief fell through and
 *    was rendered again as plain text.
 *
 * Neither is visible to a typecheck, and no test drove either panel with a markdown body.
 */
describe('the telemetry brief renders as markdown, exactly once', () => {
  const MD = [
    '**FIRING** CheckoutLatencyHigh',
    'severity: critical · source: grafana',
    '',
    'links:',
    '- https://grafana.example/d/abc',
  ].join('\n');

  const withMarkdownBrief = () => {
    const d = task(incidentFields) as unknown as { ctx: { items: Array<Record<string, string>> } };
    d.ctx.items = [
      { id: 'CI-1', taskId: 'T-1', kind: 'note', body: MD, author: 'ingest:ing_x', ts: '2026-08-01T00:00:00.000Z' },
      { id: 'CI-2', taskId: 'T-1', kind: 'note', body: 'a human note', author: 'someone@x.test', ts: '2026-08-02T00:00:00.000Z' },
    ];
    return d as unknown as LoadedTaskDetail;
  };

  it('parses the markdown instead of printing the asterisks', () => {
    const { container } = render(<IncidentPanel d={withMarkdownBrief()} />);
    // The status word is EMPHASISED, not wrapped in literal asterisks.
    expect(screen.getByText('FIRING').tagName).toBe('STRONG');
    expect(container.textContent).not.toContain('**');
  });

  it('makes the snapshot\'s bare-URL links clickable — what remark-gfm buys here', () => {
    render(<IncidentPanel d={withMarkdownBrief()} />);
    // An autolink literal is GFM-only, so this is the assertion that proves remarkPlugins is wired
    // rather than merely passed.
    expect(screen.getByRole('link', { name: /grafana\.example/ }).getAttribute('href')).toBe(
      'https://grafana.example/d/abc',
    );
  });

  it('ContextPanel omits the brief that Incident already shows, and keeps every other item', () => {
    render(<ContextPanel d={withMarkdownBrief()} />);
    expect(screen.queryByText(/CheckoutLatencyHigh/)).toBeNull();
    expect(screen.getByText('a human note')).toBeTruthy();
    expect(screen.getByText(/shown in Incident, above/i)).toBeTruthy();
  });

  it('POSITIVE CONTROL: an ordinary ticket keeps its ingest-authored item', () => {
    // The exclusion is gated on `incident.fingerprint`, NOT on the author prefix — a ticket with no
    // Incident panel has nowhere else to show the item, so hiding it would lose it entirely.
    const d = task({ effort: 'S' }) as unknown as { ctx: { items: Array<Record<string, string>> } };
    d.ctx.items = [
      { id: 'CI-9', taskId: 'T-1', kind: 'note', body: 'from an ingest job', author: 'ingest:ing_x', ts: '2026-08-01T00:00:00.000Z' },
    ];
    render(<ContextPanel d={d as unknown as LoadedTaskDetail} />);
    expect(screen.getByText('from an ingest job')).toBeTruthy();
  });
});
