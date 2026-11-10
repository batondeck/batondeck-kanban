import { useMemo, useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import * as agentsApi from '../api/agents.js';
import * as boardsApi from '../api/boards.js';
import * as projectsApi from '../api/projects.js';
import { defaultBoardName } from '../api/boards.js';
import { useAuth } from '../auth.js';
import { SUPERADMIN, can } from '../permissions.js';
import { useProject } from '../projectContext.js';
import { useMyProjectRole } from '../displayNames.js';
import { closedWhy, mayCall } from '../toolFloors.js';
import { Avatar, PageHeader } from '../components.js';
import { fire, pushToast } from '../toast.js';
import { toolKey } from '../ToolLogo.js';
import { ConnectMcp } from '../connect-mcp.js';
import type { AgentSession } from '../types.js';

// Project identity colours — NeoPOP pop palette (500s). These are IDENTITY, not status: they mark
// "which project" on the card's left edge and must never be confused with the six status hues, so
// they are drawn from the pop palette rather than the state palette.
// ONE of the six moved with the ember rebrand, and only one. Measured against the new accent
// (OKLCH hue 36): the violets and the blue are 91-133 degrees clear and stay — they are IDENTITY,
// not the accent, and --review is violet too. But #FF8744 sat at hue 47, eleven degrees off ember,
// so a project's edge bar read as the brand colour. #00C5D4 takes its slot at the same lightness and
// chroma, in the gap between --done (154) and --ready (263) that nothing else occupies.
const PALETTE = ['#6A35FF', '#00C5D4', '#3BFFAD', '#AA3FFF', '#3F6FD9', '#FF426F'];

export function Projects() {
  const qc = useQueryClient();
  const nav = useNavigate();
  const { permissions, ui } = useAuth();
  const { projectId: selected, projects: roster, setProjectId } = useProject();
  const [name, setName] = useState('');
  const [connectOpen, setConnectOpen] = useState(false);
  const projects = useQuery({ queryKey: ['projects'], queryFn: () => projectsApi.listProjects() });
  const list = projects.data?.projects ?? [];

  // The sidebar switcher scopes this grid: "All projects" ('') shows every PROJECT card; a selected
  // project shows THAT project's BOARDS instead (a project has many boards).
  const selectedName = roster.find((p) => p.projectId === selected)?.name ?? '';
  // A board is `create_board`, tier write (P-440): below it "New board" is closed and says why, and
  // opening a project with no board shows its empty board list instead of creating one.
  const selectedRole = useMyProjectRole(selected);
  const canNewBoard = mayCall(selectedRole, 'create_board');
  const newBoardWhy = closedWhy(selectedRole, 'create_board', 'create boards');

  // The card's members/avatars/colour ride along on `list_projects` — ONE request for the whole grid,
  // the same shape as the sessions fetch below.
  //
  // This used to be a `useQueries` fan-out of one `getProject` per project. With 19 projects that was
  // 19 concurrent `/api/projects/P-*` requests fired inside 18ms; measured in prod they all landed on
  // ONE core instance (containerConcurrency 80 >> 19, so Cloud Run never scales out) and every one took
  // 1.52-2.12s, against 0.086-0.233s for the same endpoints once the burst had drained. Self-inflicted
  // contention, on every mount of this screen.

  const boardsQ = useQuery({
    queryKey: ['boards', selected],
    queryFn: () => boardsApi.listBoards(selected),
    enabled: !!selected,
  });
  const boards = boardsQ.data?.boards ?? [];

  // One workspace-wide sessions fetch for the whole grid → agent avatars per card, grouped by the
  // project each agent last touched. Same pattern/cache key as ActivityLog's client-logo lookup.
  // `all` is a WIDENING, not a gate: the core enforces it, and a member simply gets their own
  // sessions. It reads the permission rather than the `isAdmin` alias so the SPA has one oracle —
  // sound only because the BFF synthesises the slug for allowlist admins (permissions.ts, SUPERADMIN).
  // The key carries it: it is the same query with a different scope, and sharing one key across both
  // would serve an admin a member-scoped payload from cache.
  const allSessions = can(permissions, SUPERADMIN);
  const sessionsQ = useQuery({
    queryKey: ['agentSessions', allSessions],
    queryFn: () => agentsApi.agentSessions(ui.sessionsLimit, allSessions),
    staleTime: 60000,
  });
  const agentsByProject = useMemo(() => {
    const m = new Map<string, Map<string, AgentSession>>(); // projectId → agentName → newest session
    for (const s of sessionsQ.data?.sessions ?? []) {
      if (!s.lastProjectId || !s.agentName) continue;
      const per = m.get(s.lastProjectId) ?? new Map<string, AgentSession>();
      const prev = per.get(s.agentName);
      if (!prev || Date.parse(s.lastSeenAt) > Date.parse(prev.lastSeenAt)) per.set(s.agentName, s);
      m.set(s.lastProjectId, per);
    }
    return m;
  }, [sessionsQ.data]);

  const create = useMutation({
    // Color is assigned once at creation — a random palette pick persisted to settings (no on-card
    // picker; older projects without a stored color keep the id-hash fallback below).
    mutationFn: async (n: string) => {
      const { projectId } = await projectsApi.createProject(n);
      await projectsApi.updateSettings(projectId, { color: PALETTE[Math.floor(Math.random() * PALETTE.length)] }).catch(() => {});
      return projectId;
    },
    onSuccess: () => {
      setName('');
      void qc.invalidateQueries({ queryKey: ['projects'] });
    },
  });

  const createBoard = useMutation({
    mutationFn: (n: string) => boardsApi.createBoard(selected, n),
    onSuccess: () => {
      setName('');
      void qc.invalidateQueries({ queryKey: ['boards', selected] });
    },
  });

  // The projects being opened right now. A double click ran `open` twice: both runs found no board
  // and both created one, so a new project started with two identical boards.
  const opening = useRef(new Set<string>());
  async function open(projectId: string): Promise<void> {
    if (opening.current.has(projectId)) return;
    opening.current.add(projectId);
    try {
      const projectName = list.find((p) => p.projectId === projectId)?.name;
      const { boards } = await boardsApi.listBoards(projectId);
      if (!boards[0] && !mayCall(roster.find((p) => p.projectId === projectId)?.role, 'create_board')) {
        setProjectId(projectId);
        return;
      }
      const board = boards[0] ?? (await boardsApi.createBoard(projectId, defaultBoardName(projectName))).board;
      nav(`/projects/${projectId}/boards/${board.id}`);
    } finally {
      opening.current.delete(projectId);
    }
  }

  return (
    <>
      <PageHeader
        title={selected ? selectedName || 'Boards' : 'Projects'}
        subtitle={
          selected ? 'Boards in this project — click one to open it.' : 'Manage active orchestrations and shared workspaces.'
        }
        actions={
          selected ? (
            <>
              <input
                className="input"
                placeholder="New board name"
                aria-label="New board name"
                value={name}
                disabled={!canNewBoard}
                aria-describedby={newBoardWhy ? 'projects-new-board-closed' : undefined}
                onChange={(e) => setName(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter' && name && canNewBoard && !createBoard.isPending) createBoard.mutate(name); }}
              />
              <button
                className="btn"
                disabled={!canNewBoard || !name || createBoard.isPending}
                title={newBoardWhy}
                aria-describedby={newBoardWhy ? 'projects-new-board-closed' : undefined}
                onClick={() => createBoard.mutate(name)}
              >
                New board
              </button>
              {newBoardWhy && <span id="projects-new-board-closed" className="muted" style={{ fontSize: 12 }}>{newBoardWhy}</span>}
            </>
          ) : (
            <>
              <input
                className="input"
                placeholder="New project name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter' && name && !create.isPending) create.mutate(name); }}
              />
              <button className="btn" disabled={!name || create.isPending} onClick={() => create.mutate(name)}>New project</button>
            </>
          )
        }
      />
      <div className="page">
        <div className="grid">
          {(selected ? boardsQ.isLoading : projects.isLoading) &&
            [0, 1, 2].map((i) => (
              <div key={i} className="proj-card">
                <div className="skeleton" style={{ width: '60%', height: 14, marginBottom: 12 }} />
                <div className="skeleton" style={{ width: 120, height: 20, marginBottom: 10 }} />
                <div className="skeleton" style={{ width: 90, height: 10 }} />
              </div>
            ))}

          {/* A project is selected → show ITS boards as cards. */}
          {selected &&
            boards.map((b) => {
              const accent = PALETTE[[...b.id].reduce((a, c) => a + c.charCodeAt(0), 0) % PALETTE.length];
              return (
                <div key={b.id} className="proj-card" onClick={() => nav(`/projects/${selected}/boards/${b.id}`)}>
                  <div className="accent" style={{ background: accent }} />
                  <div className="head">
                    <div className="title" style={{ fontSize: 14, fontWeight: 600, flex: 1, margin: 0 }}>{b.name}</div>
                  </div>
                  <div className="muted" style={{ fontSize: 11, marginTop: 12 }}>
                    {b.columns.length} lane{b.columns.length === 1 ? '' : 's'}
                  </div>
                  <div
                    className="mono"
                    title="Click to copy"
                    style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 10, cursor: 'pointer' }}
                    onClick={(e) => {
                      e.stopPropagation();
                      void navigator.clipboard.writeText(b.id).then(() => pushToast('Copied', 'info'));
                    }}
                  >
                    {b.id}
                  </div>
                </div>
              );
            })}
          {selected && boardsQ.data && boards.length === 0 && (
            <p className="muted">{canNewBoard ? 'No boards in this project yet — create one.' : 'No boards in this project yet.'}</p>
          )}

          {/* "All projects" → show every project card. */}
          {!selected &&
            list.map((p) => {
              const members = Object.keys(p.members ?? {});
              // T-319: label the avatar with the person, not the `U-` account id it is keyed on.
              const memberDisplay = p.memberDisplay ?? {};
              const agents = [...(agentsByProject.get(p.projectId)?.values() ?? [])];
              const customColor = p.color ?? null;
              const accent = customColor ?? PALETTE[[...p.projectId].reduce((a, c) => a + c.charCodeAt(0), 0) % PALETTE.length];
              return (
                <div key={p.projectId} className="proj-card" onClick={() => fire(open(p.projectId))}>
                  <div className="accent" style={{ background: accent }} />
                  <div className="head">
                    <div className="title" style={{ fontSize: 14, fontWeight: 600, flex: 1, margin: 0 }}>{p.name}</div>
                  </div>
                  <div className="row" style={{ display: 'flex', gap: 4, alignItems: 'center', marginTop: 12 }}>
                    {members.slice(0, 6).map((m) => <Avatar key={m} id={m} label={memberDisplay[m]} />)}
                    {agents.slice(0, 4).map((s) => <Avatar key={s.agentName} id={s.agentName} tool={toolKey(s.agentName, s.clientName)} />)}
                    {(members.length > 1 || agents.length > 0) && (
                      <span className="muted" style={{ fontSize: 11, marginLeft: 4 }}>
                        {members.length} member{members.length === 1 ? '' : 's'}
                        {agents.length > 0 && ` · ${agents.length} agent${agents.length === 1 ? '' : 's'}`}
                      </span>
                    )}
                  </div>
                  <div
                    className="mono"
                    title="Click to copy"
                    style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 10, cursor: 'pointer' }}
                    onClick={(e) => {
                      e.stopPropagation();
                      void navigator.clipboard.writeText(p.projectId).then(() => pushToast('Copied', 'info'));
                    }}
                  >
                    {p.projectId}
                  </div>
                </div>
              );
            })}
          {!selected && projects.data && list.length === 0 && (
            <section aria-label="Get started" className="proj-card" style={{ gridColumn: '1 / -1', cursor: 'default' }}>
              <div className="title" style={{ fontSize: 14, fontWeight: 600, margin: '0 0 10px' }}>No projects yet. Three steps to a working board:</div>
              <ol style={{ margin: 0, paddingLeft: 20, display: 'flex', flexDirection: 'column', gap: 8, fontSize: 14 }}>
                <li>Create a project: type a name above and press New project. Its board is created when you first open it.</li>
                <li>
                  <Link to="/settings#cli-tokens" style={{ color: 'var(--accent-tint)' }}>Create a token</Link> for your agent in
                  Settings → CLI tokens.
                </li>
                <li>
                  <button className="btn secondary" style={{ fontSize: 12, padding: '4px 10px' }} onClick={() => setConnectOpen(true)}>
                    Connect an agent
                  </button>{' '}
                  with that token.
                </li>
              </ol>
            </section>
          )}
        </div>
      </div>
      {connectOpen && <ConnectMcp onClose={() => setConnectOpen(false)} />}
    </>
  );
}
