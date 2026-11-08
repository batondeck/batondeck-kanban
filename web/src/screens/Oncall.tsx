/**
 * `/oncall` — the incident-response surface (spec §10). ONE screen owns everything: the metrics and
 * open incidents up front, the integrations and their delivery logs behind the gear.
 *
 * *** EVERY READ HERE TOLERATES AN ABSENT OR EMPTY RESPONSE. *** This screen renders inside
 * `permission-gating.test.tsx`'s positive control, whose fetch stub answers EVERY path with one
 * generic `{notifications:[],unread:0,sessions:[],boards:[],projects:[]}` payload — and the screen
 * mounts inside `Shell`'s ErrorBoundary, so a throw here removes `permission-granted` and the
 * failure reads as a PERMISSION bug rather than a rendering one. Guard every field access.
 */
import { useMemo, useState } from 'react';
import { useMayReadIntegrations } from '../displayNames.js';
import { Icon } from '../icons.js';
import { useQuery } from '@tanstack/react-query';
import * as oncallApi from '../api/oncall.js';
import * as projectsApi from '../api/projects.js';
import type { Delivery, Integration } from '../types.js';
import { Cluster, Empty, Note, Panel, Stack, Tile, Tiles } from '../ui.js';
import { EmptyState, PageHeader, timeAgo } from '../components.js';
import { OncallSetup } from './oncall/OncallSetup.js';

function sourceLabel(s: string): string {
  return s === 'pagerduty' ? 'PagerDuty' : s.charAt(0).toUpperCase() + s.slice(1);
}

/** One integration's health, derived from the delivery page we already fetched — no extra call. */
function Health({ projectId, integration }: { projectId: string; integration: Integration }) {
  const q = useQuery({
    queryKey: ['oncall-health', projectId, integration.id],
    queryFn: () => oncallApi.listDeliveries(projectId, integration.id, undefined, 25),
    refetchInterval: 30_000,
    retry: false,
  });
  const rows: Delivery[] = Array.isArray(q.data?.deliveries) ? q.data.deliveries : [];
  const last = rows[0];
  const rejected = rows.filter((r) => r.kind === 'rejected').length;
  return (
    <tr>
      <td>{integration.name}</td>
      <td>{sourceLabel(integration.source)}</td>
      <td>
        <span className="pill">{integration.enabled ? 'Enabled' : 'Disabled'}</span>
      </td>
      <td>{String(integration.routing?.['mode'] ?? '—')}</td>
      <td>{integration.lifecycle}</td>
      <td>
        {last ? (
          <>
            {timeAgo(last.ts)} · {last.result}
          </>
        ) : (
          '—'
        )}
      </td>
      <td>{rejected > 0 ? `${rejected} refused` : 'ok'}</td>
    </tr>
  );
}

export default function Oncall() {
  const [setupOpen, setSetupOpen] = useState(false);
  const projectsQ = useQuery({
    queryKey: ['projects'],
    queryFn: () => projectsApi.listProjects(),
    retry: false,
  });
  // `listProjects` returns membership rows (projectId + name + role), not full Project docs.
  const projects = Array.isArray(projectsQ.data?.projects) ? projectsQ.data.projects : [];
  const [projectId, setProjectId] = useState<string>('');
  const activeProject = projectId || projects[0]?.projectId || '';

  const mayReadIntegrations = useMayReadIntegrations(activeProject);
  const integrationsQ = useQuery({
    queryKey: ['oncall-integrations', activeProject],
    queryFn: () => oncallApi.listIntegrations(activeProject),
    // Gated on workspace admin AND project master (useMayReadIntegrations), the two checks the
    // service makes. With the 60s refetch below, an ungated read is refused once a minute without
    // the reader touching anything.
    enabled: Boolean(activeProject) && mayReadIntegrations,
    refetchInterval: 60_000,
    retry: false,
  });
  const integrations: Integration[] = Array.isArray(integrationsQ.data?.integrations)
    ? integrationsQ.data.integrations
    : [];

  const stats = useMemo(() => {
    const enabled = integrations.filter((i) => i.enabled).length;
    const bySource = new Set(integrations.map((i) => i.source)).size;
    const broadcasting = integrations.filter((i) => i.routing?.['mode'] === 'broadcast').length;
    return { total: integrations.length, enabled, bySource, broadcasting };
  }, [integrations]);

  // `className="page"` is the app's 16px gutter (index.css `.page`), and 10 of the 12 routed screens
  // carry it. This one did not, so On-call rendered flush against the sidebar while every screen
  // beside it was inset — reported from staging as "alignment is not consistent with rest of the app".
  return (
    <Stack gap={16} className="page">
      <PageHeader
        title="On-call"
        subtitle="Telemetry webhooks become incident tickets, assigned by each integration's routing or broadcast to the project's agents."
        actions={
          <button className="btn secondary" onClick={() => setSetupOpen(true)} aria-label="On-call setup">
            <Icon name="settings" size={12} /> Setup
          </button>
        }
      />

      {projects.length > 1 && (
        <Cluster gap={8}>
          <label htmlFor="oncall-project">Project</label>
          {/* Named apart from the sidebar's "Project" select, which is on the same screen (P-433). */}
          <select
            id="oncall-project"
            className="input"
            aria-label="On-call project"
            value={activeProject}
            onChange={(e) => setProjectId(e.target.value)}
          >
            {projects.map((p) => (
              <option key={p.projectId} value={p.projectId}>
                {p.name}
              </option>
            ))}
          </select>
        </Cluster>
      )}

      {!activeProject ? (
        <EmptyState
          title="No project yet"
          hint="On-call integrations are configured per project. Create a project first."
        />
      ) : (
        <>
          <Tiles>
            <Tile label="Integrations" value={String(stats.total)} />
            <Tile label="Enabled" value={String(stats.enabled)} />
            <Tile label="Sources" value={String(stats.bySource)} />
            <Tile label="Broadcasting" value={String(stats.broadcasting)} />
          </Tiles>

          <Note>
            Incident metrics — MTTA, MTTR, incidents by source — arrive with the analytics section.
            The delivery health below is read live from each integration&apos;s log.
          </Note>

          <Panel title="Delivery health">
            {integrations.length === 0 ? (
              <Empty>
                No integrations yet. Open <b>Setup</b> to create one and paste its endpoint into
                Grafana, Dynatrace, Datadog, PagerDuty, Alertmanager — or any sender that can sign a
                request.
              </Empty>
            ) : (
              /* SEVEN nowrap columns cannot fit a narrow viewport, and without a wrapper the
                 overflow is CLIPPED rather than scrolled — the same defect the delivery log had in
                 the drawer. `.chr-table-wrap` is the in-repo pattern; index.css:603 states why. */
              <div style={{ overflowX: 'auto' }}>
                <table style={{ whiteSpace: 'nowrap' }}>
                  <thead>
                    <tr>
                      <th>Integration</th>
                      <th>Source</th>
                      <th>Status</th>
                      <th>Routing</th>
                      <th>Lifecycle</th>
                      <th>Last delivery</th>
                      <th>Health</th>
                    </tr>
                  </thead>
                  <tbody>
                    {integrations.map((i) => (
                      <Health key={i.id} projectId={activeProject} integration={i} />
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Panel>
        </>
      )}

      {setupOpen && activeProject && (
        <OncallSetup
          projectId={activeProject}
          integrations={integrations}
          onClose={() => {
            setSetupOpen(false);
            void integrationsQ.refetch();
          }}
        />
      )}
    </Stack>
  );
}
