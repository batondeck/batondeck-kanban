import { useEffect, useState } from 'react';
import { Icon } from '../icons.js';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router';
import * as agentsApi from '../api/agents.js';
import * as boardsApi from '../api/boards.js';
import * as feedApi from '../api/feed.js';
import * as projectsApi from '../api/projects.js';
import * as streamApi from '../api/stream.js';
import { WaitLoader } from '../brand.js';
import { useAuth } from '../auth.js';
import { Avatar, EmptyState, PageHeader, leaseLeft, timeAgo } from '../components.js';
import { useProject } from '../projectContext.js';
import { displayAgentName, isSessionOnline } from '../agentStatus.js';
import { ToolLogo, toolKey, toolLabel } from '../ToolLogo.js';
import { isAgent } from '../types.js';
import { ConnectMcp } from '../connect-mcp.js';
import { Cluster, Panel, Stack, Tile, Tiles } from '../ui.js';
import type { ActivityEvent, AgentSession, Task } from '../types.js';
import { replaceDevicePresence } from '../presenceMerge.js';

export function Agents() {
  const { ui } = useAuth();
  const { projectId } = useProject();
  const [connectOpen, setConnectOpen] = useState(false);

  const qc = useQueryClient();
  const boardsQ = useQuery({ queryKey: ['boards', projectId], queryFn: () => boardsApi.listBoards(projectId), enabled: !!projectId });

  // Composed here from get_project and each board's tasks and feed: no overview route is served
  // (ui-backend's aggregate had no lane). One fetch per board, bounded by the boards list, as
  // ActivityLog's feed is.
  const boardIds = (boardsQ.data?.boards ?? []).map((b) => b.id);
  const data = useQuery({
    queryKey: ['agentsdata', projectId, boardIds.join(',')],
    enabled: !!projectId && !!boardsQ.data,
    queryFn: async () => {
      const [p, perBoard] = await Promise.all([
        projectsApi.getProject(projectId),
        Promise.all(boardIds.map((id) => Promise.all([boardsApi.listTasks(projectId, id, { limit: String(ui.taskPageLimit) }), feedApi.feed(projectId, id)]))),
      ]);
      return {
        project: p.project,
        memberDisplay: p.memberDisplay ?? {},
        tasks: perBoard.flatMap(([t]) => t.tasks),
        events: perBoard.flatMap(([, f]) => f.events),
      };
    },
  });

  // MCP connection telemetry — global (not project-scoped): every agent that has connected over MCP,
  // logged by the core on initialize + each tool call.
  //
  // NO POLL. This screen is a standalone route, so no board stream is mounted here; it opens the
  // identity-keyed `/api/stream` below instead and the gateway pushes presence snapshots down it. The
  // initial fetch stays so the table renders before the first snapshot arrives.
  const sessionsQ = useQuery({
    queryKey: ['agentSessions'],
    queryFn: () => agentsApi.agentSessions(ui.sessionsLimit),
  });
  // Presence + notifications for a screen with no board open. Snapshots land straight in the cache, so
  // they cost zero HTTP.
  useEffect(
    () =>
      streamApi.userStream({
        onPresence: (sessions) => {
          // A frame is this device's whole live list for the viewer: its rows are replaced (presenceMerge.ts).
          const upsert = (old: { sessions: AgentSession[] } | undefined) => ({
            sessions: replaceDevicePresence(old?.sessions, sessions),
          });
          qc.setQueryData<{ sessions: AgentSession[] }>(['agentSessions'], upsert);
          // Board.tsx keys the same data lowercase — a pre-existing split. Write both so the two screens
          // cannot disagree about who is online.
          qc.setQueryData<{ sessions: AgentSession[] }>(['agentsessions'], upsert);
        },
      }),
    [qc],
  );
  const sessions = sessionsQ.data?.sessions ?? [];
  // T-558: the SHARED rule, not a second copy of the threshold. This was an inline re-derivation under
  // a comment promising it agreed with the presence rail; it did, until wake attachment became a second
  // input and only one of the two learned about it.
  const isOnline = (s: AgentSession): boolean => isSessionOnline(s, Date.now());
  const onlineSessions = sessions.filter(isOnline);
  const totalOps = sessions.reduce((a, s) => a + s.requestCount, 0);
  const topTools = (tc: Record<string, number>): string =>
    Object.entries(tc).sort((a, b) => b[1] - a[1]).slice(0, 3).map(([t, n]) => `${t}·${n}`).join('  ') || '—';

  // Push-over-poll: refresh the aggregate on any board's SSE update instead of a 10s timer.
  useEffect(() => {
    if (!projectId) return;
    const unsubs = (boardsQ.data?.boards ?? []).map((b) =>
      streamApi.stream(projectId, b.id, () => void qc.invalidateQueries({ queryKey: ['agentsdata', projectId] })),
    );
    return () => { for (const u of unsubs) u(); };
  }, [projectId, boardsQ.data, qc]);

  const project = data.data?.project;
  const tasks = data.data?.tasks ?? [];
  const events = data.data?.events ?? [];
  const members = Object.entries(project?.members ?? {});
  // T-319: member keys are opaque `U-<uuid>` account ids since T-216; this maps the ones that resolve to
  // a person. Absent = no better label than the id, which is what this table showed before.
  const memberDisplay = data.data?.memberDisplay ?? {};
  const activeClaims = tasks.filter((t: Task) => t.claim && leaseLeft(t.claim.expiresAt));
  const claimByOwner = new Map<string, Task>();
  for (const t of activeClaims) if (t.claim) claimByOwner.set(t.claim.owner, t);

  // Capabilities surfaced per identity: union of requiredCapabilities on tasks they touch.
  const capsByOwner = new Map<string, Set<string>>();
  for (const t of tasks) {
    const owners = [t.assignee, t.claim?.owner, t.createdBy].filter(Boolean) as string[];
    for (const o of owners) {
      const set = capsByOwner.get(o) ?? new Set<string>();
      for (const c of t.requiredCapabilities ?? []) set.add(c);
      capsByOwner.set(o, set);
    }
  }

  const handoffs = events.filter((e: ActivityEvent) => e.type === 'task.handoff').slice(0, 10);
  const lastActivityByOwner = new Map<string, string>();
  for (const e of events) {
    if (!lastActivityByOwner.has(e.actor)) lastActivityByOwner.set(e.actor, e.ts);
  }
  const agentCount = members.filter(([id]) => isAgent(id)).length;

  const idTag = (id: string) => {
    const norm = id.replace(/[^a-zA-Z0-9]/g, '').toUpperCase();
    return `${norm.slice(0, 1)}-${norm.slice(-2)}`;
  };

  const capChip = (c: string) => (
    <span key={c} className="label" style={{ textTransform: 'none' }}>{c}</span>
  );

  return (
    <>
      <PageHeader
        title="Agents"
        subtitle="Real-time telemetry for agents and humans sharing the board."
        actions={<button className="btn" style={{ fontSize: 14 }} onClick={() => setConnectOpen(true)}>+ Connect MCP</button>}
      />
      <div className="page">
        {data.isError && <p className="error" style={{ fontSize: 12 }}>Couldn’t load agent activity.</p>}
        {data.isLoading && !data.data && <WaitLoader />}

        <Cluster gap={16} align="flex-start" wrap>
          {/* Main column */}
          <Stack gap={16} style={{ flex: 1, minWidth: 0 }}>
            {/* Stat cards — driven by live MCP session telemetry (+ task claims) */}
            <Tiles>
              <Tile label="Agents online" value={onlineSessions.length} />
              <Tile label="Tasks claimed" value={activeClaims.length} note={`${agentCount} agents`} />
              <Tile label="Sessions" value={sessions.length} />
              <Tile label="Operations" value={totalOps} />
            </Tiles>

            {/* MCP Sessions — the connection log: every agent/client that has connected over MCP */}
            <Panel
              title="MCP sessions"
              right={
                <Cluster gap={12}>
                  <span>{sessions.length} logged · <span style={{ color: 'var(--done)' }}>{onlineSessions.length} online</span></span>
                  {/* YOUR sessions, not the admin console. This linked to /admin, which is the
                      workspace-wide view and is admin-only — so the one person who could click it was
                      sent somewhere that manages EVERYONE's sessions, and everyone else got no way to
                      stop their own at all. `list_agent_sessions` is caller-scoped, so this table is
                      already the caller's own; Settings is where they can end them. Admin keeps the
                      workspace-wide view, reached from the admin nav. */}
                  <Link to="/settings#sessions">Manage your sessions →</Link>
                </Cluster>
              }
            >
              <div style={{ overflowX: 'auto' }}>
                <table style={{ whiteSpace: 'nowrap' }}>
                  <thead>
                    <tr>
                      <th>Agent</th>
                      <th>Client</th>
                      <th>Auth</th>
                      <th style={{ textAlign: 'right' }}>Conns</th>
                      <th style={{ textAlign: 'right' }}>Calls</th>
                      <th>Top operations</th>
                      <th style={{ textAlign: 'right' }} title="The change-log cursor of the last doorbell written to a process on this device">Delivered</th>
                      <th style={{ textAlign: 'right' }}>Last seen</th>
                    </tr>
                  </thead>
                  <tbody>
                    {sessions.map((s) => {
                      const online = isOnline(s);
                      const name = displayAgentName(s.agentName, s.clientName);
                      const tk = toolKey(name, s.clientName);
                      return (
                        <tr key={s.sessionKey} style={online ? { background: 'var(--surface-low)' } : undefined}>
                          <td>
                            <span style={{ display: 'inline-flex', gap: 10, alignItems: 'center' }}>
                              <Avatar id={name} active={online} tool={tk} />
                              <span style={{ display: 'inline-flex', flexDirection: 'column', minWidth: 0 }}>
                                <span style={{ fontSize: 14, fontWeight: 600, color: online ? 'var(--text)' : 'var(--text-2)' }}>{name}</span>
                                <span className="mono" style={{ fontSize: 10, color: 'var(--text-muted)', maxWidth: 220, overflow: 'hidden', textOverflow: 'ellipsis' }} title={s.identityId}>{s.identityId}</span>
                              </span>
                            </span>
                          </td>
                          <td className="mono" style={{ fontSize: 11, color: 'var(--text-2)' }}>
                            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                              {tk && <ToolLogo tool={tk} size={13} />}
                              {s.clientName ? `${s.clientName}${s.clientVersion ? ` ${s.clientVersion}` : ''}` : tk ? toolLabel(tk) : '—'}
                            </span>
                          </td>
                          {/* "Auth", not "Type", and NOT a role-badge. This column shows how the
                              session AUTHENTICATED, and it was rendering `oauth_user` as `USER` in a
                              badge styled like a role — so every row read "USER", which is not a role
                              anyone holds and told you nothing (an AGENT connecting under a human's
                              OAuth token is not a user). `oauth` vs `service` is the actual distinction
                              the field carries; the member's real role lives on the project. */}
                          <td><span className="chip" title={s.kind === 'oauth_user' ? 'Connected with a human’s OAuth token' : s.kind === 'service_account' ? 'Connected as a service account' : s.kind}>{s.kind === 'oauth_user' ? 'oauth' : s.kind === 'service_account' ? 'service' : s.kind}</span></td>
                          <td style={{ textAlign: 'right' }} className="mono">{s.sessionCount}</td>
                          <td style={{ textAlign: 'right' }} className="mono">{s.requestCount}</td>
                          <td className="mono" style={{ fontSize: 11, color: 'var(--text-muted)' }} title={Object.entries(s.toolCounts).map(([t, n]) => `${t}: ${n}`).join('\n')}>{topTools(s.toolCounts)}</td>
                          {/* A device process's last delivered doorbell (wake_sessions.last_cursor): one
                              never rung reads "none", so a silent session is visible. MCP rows have none. */}
                          <td style={{ textAlign: 'right' }} className="mono" data-testid="delivered-cursor">
                            {s.lastDeliveredCursor === undefined ? '—' : s.lastDeliveredCursor > 0 ? `#${s.lastDeliveredCursor}` : 'none'}
                          </td>
                          <td style={{ textAlign: 'right', fontSize: 12, color: online ? 'var(--done)' : 'var(--text-muted)' }} title={new Date(s.lastSeenAt).toLocaleString()}>
                            {online ? <span className="dot-online" aria-hidden="true" /> : null}{timeAgo(s.lastSeenAt)}
                          </td>
                        </tr>
                      );
                    })}
                    {sessions.length === 0 && !sessionsQ.isLoading && (
                      <tr><td colSpan={8}><EmptyState icon="⌘" title="No MCP sessions yet" hint="Agents that connect over MCP will appear here." /></td></tr>
                    )}
                  </tbody>
                </table>
              </div>
            </Panel>

            {/* Identities table */}
            <Panel title="Identities" right={members.length}>
              <div style={{ overflowX: 'auto' }}>
                <table style={{ whiteSpace: 'nowrap' }}>
                  <thead>
                    <tr>
                      <th>Actor</th>
                      <th>Capabilities</th>
                      <th>Active Lease</th>
                      <th style={{ textAlign: 'right' }}>Last Activity</th>
                    </tr>
                  </thead>
                  <tbody>
                    {members.map(([id, role]) => {
                      const t = claimByOwner.get(id);
                      const active = !!t;
                      const caps = Array.from(capsByOwner.get(id) ?? []).slice(0, 4);
                      const last = lastActivityByOwner.get(id);
                      // A human member resolves to their name/email; an agent has no account and keeps
                      // its own name. `id` still classifies (agent vs human) — only the LABEL changes.
                      const name = displayAgentName(memberDisplay[id] ?? id);
                      return (
                        <tr key={id} style={active ? { background: 'var(--surface-low)', position: 'relative' } : undefined}>
                          <td style={{ position: 'relative' }}>
                            {active && <span style={{ position: 'absolute', left: 0, top: 0, bottom: 0, width: 2, background: 'var(--accent)' }} />}
                            <span style={{ display: 'inline-flex', gap: 10, alignItems: 'center' }}>
                              <Avatar id={id} label={name} active={active} />
                              <span style={{ display: 'inline-flex', alignItems: 'baseline', gap: 8 }}>
                                <span style={{ fontSize: 14, fontWeight: 600, color: active ? 'var(--text)' : 'var(--text-2)' }}>{name}</span>
                                <span className="mono" style={{ fontSize: 11, color: 'var(--text-muted)' }}>[{idTag(id)}]</span>
                              </span>
                            </span>
                          </td>
                          <td>
                            {caps.length ? (
                              <span style={{ display: 'inline-flex', gap: 6, flexWrap: 'wrap' }}>{caps.map(capChip)}</span>
                            ) : (
                              <span className="role-badge">{role}</span>
                            )}
                          </td>
                          <td>
                            {t && t.claim ? (
                              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 12 }}>
                                <span className="mono" style={{ fontSize: 11, color: 'var(--text)', background: 'var(--surface-highest)', border: '1px solid var(--outline-variant)', borderRadius: 'var(--r-2)', padding: '2px 6px' }}>{t.id}</span>
                                <span className="mono" style={{ fontSize: 11, color: 'var(--progress)', letterSpacing: '0.05em' }}><Icon name="lock" size={11} /> {leaseLeft(t.claim.expiresAt)}</span>
                              </span>
                            ) : (
                              <span className="muted" style={{ fontSize: 12, fontStyle: 'italic' }}>{isAgent(id) ? 'Idle' : 'Observing'}</span>
                            )}
                          </td>
                          <td style={{ textAlign: 'right', color: 'var(--text-muted)', fontSize: 12 }}>{last ? timeAgo(last) : '—'}</td>
                        </tr>
                      );
                    })}
                    {members.length === 0 && !data.isLoading && (
                      <tr><td colSpan={4} className="muted" style={{ fontSize: 12 }}>No identities in this project yet.</td></tr>
                    )}
                  </tbody>
                </table>
              </div>
            </Panel>
          </Stack>

          {/* Right rail — Recent Handoffs timeline */}
          <aside style={{ width: 300, flex: '0 0 300px' }}>
            <div className="card" style={{ display: 'flex', flexDirection: 'column', minHeight: 360 }}>
              <h3 style={{ fontSize: 16, fontWeight: 600, margin: '0 0 16px', display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                Recent Handoffs
                <span aria-hidden className="muted" style={{ fontSize: 14 }}>⇄</span>
              </h3>
              <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 16, position: 'relative' }}>
                {handoffs.length > 0 && (
                  <div style={{ position: 'absolute', left: 11, top: 8, bottom: 8, width: 1, background: 'var(--border)', zIndex: 0 }} />
                )}
                {handoffs.map((e) => {
                  const to = (e.data as { toAgent?: string } | null)?.toAgent;
                  return (
                    <div key={e.id} style={{ display: 'flex', alignItems: 'flex-start', gap: 12, position: 'relative', zIndex: 1 }}>
                      <span style={{ width: 24, height: 24, borderRadius: '50%', background: 'var(--surface-highest)', border: '2px solid var(--panel)', display: 'inline-flex', alignItems: 'center', justifyContent: 'center', flex: '0 0 auto', fontSize: 11, color: 'var(--text-2)', marginTop: 2 }} aria-hidden>⇄</span>
                      <div style={{ flex: 1, minWidth: 0 }}>
                        <div style={{ fontSize: 12, color: 'var(--text-2)' }}>
                          <span className="mono" style={{ color: 'var(--text)' }}>{displayAgentName(e.actor)}</span>
                          <span style={{ color: 'var(--text-muted)', margin: '0 6px' }}>→</span>
                          <span className="mono" style={{ color: 'var(--text)' }}>{to ? displayAgentName(to) : '—'}</span>
                        </div>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginTop: 3 }}>
                          {e.taskId && <span className="mono" style={{ fontSize: 10, color: 'var(--accent-tint)', background: 'color-mix(in srgb, var(--accent) 12%, transparent)', padding: '1px 5px', borderRadius: 'var(--r-1)' }}>{e.taskId}</span>}
                          <span style={{ fontSize: 10, color: 'var(--text-muted)' }}>· {timeAgo(e.ts)}</span>
                        </div>
                      </div>
                    </div>
                  );
                })}
                {handoffs.length === 0 && <EmptyState icon="⇄" title="No handoffs yet" hint="Agent-to-agent handoffs will show up here." />}
              </div>
            </div>
          </aside>
        </Cluster>
      </div>
      {connectOpen && <ConnectMcp onClose={() => setConnectOpen(false)} />}
    </>
  );
}
