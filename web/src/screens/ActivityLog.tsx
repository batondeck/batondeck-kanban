import { useEffect, useMemo, useState } from 'react';
import { useDisplayNames, useWorkspaceNames } from '../displayNames.js';
import { Link } from 'react-router';
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query';
import * as agentsApi from '../api/agents.js';
import * as auditApi from '../api/audit.js';
import * as boardsApi from '../api/boards.js';
import * as feedApi from '../api/feed.js';
import * as mediaApi from '../api/media.js';
import * as streamApi from '../api/stream.js';
import { WaitLoader } from '../brand.js';
import { useAuth } from '../auth.js';
import { SUPERADMIN, can } from '../permissions.js';
import { Avatar, PageHeader, PriorityDot, StatusPill, eventActor, eventVerb, timeAgo } from '../components.js';
import { PickProject } from '../PickProject.js';
import { useProject } from '../projectContext.js';
import { toolByAgent } from '../agentStatus.js';
import { composeResults, descriptionSnippet, looksLikeTaskId, parseTicketQuery } from '../ticketSearch.js';
import type { ActivityEvent, Task } from '../types.js';
import { Panel, Stack } from '../ui.js';

/** Human-readable label for a date-bucket key (YYYY-MM-DD). */
function dayLabel(key: string): string {
  const today = new Date();
  const todayKey = today.toISOString().slice(0, 10);
  const yest = new Date(today.getTime() - 86400000).toISOString().slice(0, 10);
  if (key === todayKey) return 'Today';
  if (key === yest) return 'Yesterday';
  return new Date(`${key}T00:00:00`).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' });
}

/** Group events (already newest-first) into ordered date buckets. */
function groupByDay(events: ActivityEvent[]): Array<{ key: string; items: ActivityEvent[] }> {
  const sorted = [...events].sort((a, b) => new Date(b.ts).getTime() - new Date(a.ts).getTime());
  const out: Array<{ key: string; items: ActivityEvent[] }> = [];
  let current: { key: string; items: ActivityEvent[] } | null = null;
  for (const e of sorted) {
    const key = new Date(e.ts).toISOString().slice(0, 10);
    if (!current || current.key !== key) {
      current = { key, items: [] };
      out.push(current);
    }
    current.items.push(e);
  }
  return out;
}

const CRITICAL = new Set(['task.blocked']);

/** Quick filter chips shown above search results; clicking toggles the token in the query. */
const FILTER_CHIPS = ['status:done', 'status:in-progress', 'assignee:me', 'priority:high'];

export function ActivityLog() {
  const { identity, isAdmin, permissions, ui } = useAuth();
  const { projectId, loading: projectsLoading } = useProject();

  const boardsQ = useQuery({
    queryKey: ['boards', projectId],
    queryFn: () => boardsApi.listBoards(projectId),
    enabled: !!projectId,
  });
  const boards = useMemo(() => boardsQ.data?.boards ?? [], [boardsQ.data]);
  const boardId = boards[0]?.id || '';

  // MCP session telemetry → client logos on activity rows. Identity-scoped for members; admins get the
  // workspace-wide view (all:true, admin-enforced at the core) so every agent's client resolves.
  // A WIDENING, not a gate — and it reads the permission, not the `isAdmin` alias, for the reason
  // stated next to `SUPERADMIN` in `permissions.ts`. Key and fn move together or the cache lies.
  const allSessions = can(permissions, SUPERADMIN);
  const sessionsQ = useQuery({
    queryKey: ['agentSessions', allSessions],
    queryFn: () => agentsApi.agentSessions(ui.sessionsLimit, allSessions),
    staleTime: 60000,
  });
  const toolMap = useMemo(() => toolByAgent(sessionsQ.data?.sessions ?? []), [sessionsQ.data]);

  /** Opaque account and integration ids → readable names. See `displayNames.ts`. */
  const displayNames = useDisplayNames(projectId);

  // ---- ticket search (Jira-ish): free text + status:/priority:/assignee:/label: filters ----
  const [q, setQ] = useState('');
  const [dq, setDq] = useState('');
  useEffect(() => {
    const t = setTimeout(() => setDq(q), ui.searchDebounceMs);
    return () => clearTimeout(t);
  }, [q, ui.searchDebounceMs]);
  const searching = dq.trim().length > 0;
  const parsed = useMemo(() => parseTicketQuery(dq, identity), [dq, identity]);
  // searchK is the display PAGE size — "Show more" grows the page count; a new query resets it.
  const [pages, setPages] = useState(1);
  useEffect(() => setPages(1), [dq, projectId]);

  // Free text → ranked server-side search (search_tasks) across the whole project. Always fetch the
  // server max (50); the display paginates by ui.searchK.
  const serverQ = useQuery({
    queryKey: ['ticketSearch', projectId, parsed.text],
    queryFn: () => mediaApi.search(projectId, parsed.text, 50),
    enabled: searching && !!projectId && parsed.text.length > 0,
  });
  // Filter-only queries have nothing to rank — list every board's tasks once and narrow locally.
  // Also used to augment id-like free text (`T-42`), which the server search doesn't index.
  const needAllTasks = searching && (parsed.text.length === 0 || looksLikeTaskId(parsed.text));
  const allTasksQ = useQuery({
    queryKey: ['allProjectTasks', projectId],
    queryFn: async () => {
      const perBoard = await Promise.all(boards.map((b) => boardsApi.listTasks(projectId, b.id, { limit: String(ui.taskPageLimit) })));
      return perBoard.flatMap((r) => r.tasks);
    },
    enabled: needAllTasks && !!projectId && boards.length > 0,
  });

  const results: Task[] = useMemo(() => {
    if (!searching) return [];
    return composeResults(parsed, serverQ.data?.results.map((r) => r.task), allTasksQ.data, 1000);
  }, [searching, parsed, serverQ.data, allTasksQ.data]);
  const shown = results.slice(0, pages * ui.searchK);
  // Disabled prerequisite queries report isLoading=false — count every query the current mode depends
  // on (id-like text needs the local scan too), or we'd flash "0 matching tickets" while they resolve,
  // and an infrastructure failure would masquerade as an empty result.
  const searchLoading =
    searching &&
    (projectsLoading ||
      (parsed.text.length > 0 && serverQ.isLoading) ||
      (needAllTasks && (boardsQ.isLoading || allTasksQ.isLoading)));
  const searchError =
    searching &&
    ((parsed.text.length > 0 && serverQ.isError) ||
      (needAllTasks && (boardsQ.isError || allTasksQ.isError)));

  /** Toggle a `key:value` chip in the query: append with a space if absent, remove if present. */
  const toggleChip = (chip: string) =>
    setQ((prev) => {
      const toks = prev.split(/\s+/).filter(Boolean);
      return (toks.includes(chip) ? toks.filter((t) => t !== chip) : [...toks, chip]).join(' ');
    });

  const qc = useQueryClient();
  // Multi-board projects merge every board's feed (bounded by the boards list — one fetch per board);
  // groupByDay re-sorts the merged set newest-first.
  const boardIds = useMemo(() => boards.map((b) => b.id), [boards]);
  const feedQ = useQuery({
    queryKey: ['feed', projectId, boardIds.join(',')],
    queryFn: async () => {
      const per = await Promise.all(boardIds.map((id) => feedApi.feed(projectId, id)));
      return { events: per.flatMap((r) => r.events) };
    },
    enabled: !!projectId && boardIds.length > 0,
  });

  // Live: refresh the feed on board SSE updates. SSE stays on the FIRST board only — one stream per
  // screen; other boards' events land on the next first-board tick or refetch.
  useEffect(() => {
    if (!projectId || !boardId) return;
    return streamApi.stream(projectId, boardId, () =>
      void qc.invalidateQueries({ queryKey: ['feed', projectId] }),
    );
  }, [projectId, boardId, qc]);

  const events = feedQ.data?.events ?? [];
  const groups = groupByDay(events);
  const loading = projectsLoading || boardsQ.isLoading || (feedQ.isLoading && !feedQ.data);
  const isError = boardsQ.isError || feedQ.isError;

  return (
    <>
      <PageHeader
        title="Workspace Log"
        subtitle="Real-time chronological feed of human and synthetic actions."
        actions={
          <span style={{ position: 'relative', display: 'inline-flex', alignItems: 'center' }}>
            <span
              aria-hidden="true"
              style={{ position: 'absolute', left: 10, fontSize: 14, color: 'var(--text-muted)', pointerEvents: 'none' }}
            >
              ⌕
            </span>
            <input
              className="input"
              type="search"
              style={{ width: 360, maxWidth: '60vw', paddingLeft: 28 }}
              placeholder="Search tickets…  status:done label:web fix auth"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              aria-label="Search tickets"
            />
          </span>
        }
      />

      <div className="page" style={{ maxWidth: 880, margin: '0 auto' }}>
        {searching ? (
          <>
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, padding: '8px 0 2px' }}>
              {FILTER_CHIPS.map((c) => {
                const active = q.split(/\s+/).includes(c);
                return (
                  <button
                    key={c}
                    className="label"
                    aria-pressed={active}
                    onClick={() => toggleChip(c)}
                    style={{
                      cursor: 'pointer',
                      textTransform: 'none',
                      ...(active
                        ? {
                            background: 'color-mix(in srgb, var(--accent) 12%, transparent)',
                            color: 'var(--accent-tint)',
                            borderColor: 'var(--accent)',
                          }
                        : {}),
                    }}
                  >
                    {c}
                  </button>
                );
              })}
            </div>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 10, padding: '4px 0 10px' }}>
              <span className="muted" style={{ fontSize: 12 }}>
                {searchLoading
                  ? 'Searching…'
                  : shown.length < results.length
                    ? `${shown.length} of ${results.length} matching tickets`
                    : `${results.length} matching ticket${results.length === 1 ? '' : 's'}`}
              </span>
              <span className="muted mono" style={{ fontSize: 11 }}>
                filters: status: priority: assignee: label: · assignee:me works
              </span>
            </div>
            {searchError && <p className="error" style={{ fontSize: 12 }}>Search failed.</p>}
            {!searchLoading && !searchError && results.length === 0 && (
              <p className="muted" style={{ fontSize: 14 }}>No tickets match.</p>
            )}
            <Stack gap={4}>
              {shown.map((t) => {
                const snip = t.description ? descriptionSnippet(t.description) : '';
                return (
                  <Link
                    key={t.id}
                    to={`/projects/${projectId}/boards/${t.boardId}?task=${t.id}`}
                    className="card"
                    style={{ display: 'flex', flexDirection: 'column', gap: 3, padding: '10px 12px', textDecoration: 'none', color: 'inherit' }}
                  >
                    <span style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                      <span className="mono" style={{ fontSize: 11, color: 'var(--text-2)', flex: '0 0 auto' }}>{t.id}</span>
                      <PriorityDot priority={t.priority} />
                      <span style={{ fontSize: 14, fontWeight: 500, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {t.title}
                      </span>
                      {t.labels.slice(0, 3).map((l) => (
                        <span key={l} className="label" style={{ textTransform: 'none', flex: '0 0 auto' }}>{l}</span>
                      ))}
                      <div style={{ flex: 1 }} />
                      {(t.assignee || t.claim?.agent) && <Avatar id={t.claim?.agent || t.assignee!} tool={toolMap.get(t.claim?.agent || t.assignee || '') ?? null} />}
                      <StatusPill status={t.status} />
                      <span className="muted mono" style={{ fontSize: 11, flex: '0 0 auto' }}>{timeAgo(t.updatedAt)}</span>
                    </span>
                    {snip && (
                      <span className="muted" style={{ fontSize: 12, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {snip}
                      </span>
                    )}
                  </Link>
                );
              })}
              {shown.length < results.length && (
                <button className="btn secondary" style={{ alignSelf: 'center', fontSize: 12, marginTop: 4 }} onClick={() => setPages((n) => n + 1)}>
                  Show more ({results.length - shown.length} remaining)
                </button>
              )}
            </Stack>
          </>
        ) : (
          <>
        {isError && <p className="error" style={{ fontSize: 12 }}>Couldn’t load the activity feed.</p>}
        {loading && !feedQ.data && <WaitLoader />}
        {/* On "All projects" there is no feed to be empty: ask for a project (P-457). */}
        {!projectId && !projectsLoading && <PickProject what="Activity feeds" none="Create a project and a board; what happens on it will appear here." />}
        {!!projectId && !loading && !isError && events.length === 0 && (
          <p className="muted" style={{ fontSize: 14 }}>No activity yet. Actions on the board will appear here.</p>
        )}

        {groups.map((g) => (
          <div key={g.key}>
            {/* Date divider */}
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 12,
                padding: '16px 0 8px',
                position: 'sticky',
                top: 0,
                background: 'var(--bg)',
                zIndex: 1,
              }}
            >
              <span
                className="mono"
                style={{
                  fontSize: 11,
                  letterSpacing: '0.06em',
                  textTransform: 'uppercase',
                  color: 'var(--text-muted)',
                  fontWeight: 600,
                }}
              >
                {dayLabel(g.key)}
              </span>
              <div style={{ flex: 1, height: 1, background: 'var(--border)' }} />
            </div>

            {g.items.map((e) => {
              const critical = CRITICAL.has(e.type);
              const name = eventActor(e, displayNames);
              return (
                <div
                  key={e.id}
                  className="row-compact"
                  style={{
                    margin: '0 -8px',
                    borderRadius: 'var(--r-1)',
                    borderBottom: '1px solid var(--border)',
                    background: critical ? 'color-mix(in srgb, var(--blocked) 8%, transparent)' : 'transparent',
                  }}
                >
                  <Avatar id={name} tool={toolMap.get(name) ?? null} />
                  <span
                    style={{
                      fontSize: 14,
                      fontWeight: 600,
                      flex: '0 1 auto',
                      minWidth: 0,
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                      color: critical ? 'var(--blocked)' : 'var(--text)',
                    }}
                  >
                    {name}
                  </span>
                  <span
                    style={{
                      fontSize: 14,
                      color: 'var(--text-muted)',
                      flex: '0 1 auto',
                      minWidth: 0,
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                    }}
                  >
                    {eventVerb(e.type)}
                  </span>
                  {e.taskId && (
                    <span
                      className="mono"
                      style={{
                        flex: '0 0 auto',
                        fontSize: 11,
                        color: 'var(--text-2)',
                        background: 'var(--panel-2)',
                        border: '1px solid var(--border)',
                        borderRadius: 'var(--r-1)',
                        padding: '1px 6px',
                      }}
                    >
                      {e.taskId}
                    </span>
                  )}
                  <div className="spacer" />
                  <span
                    className="mono"
                    style={{ flex: '0 0 auto', fontSize: 11, color: 'var(--text-muted)' }}
                    title={new Date(e.ts).toLocaleString()}
                  >
                    {timeAgo(e.ts)}
                  </span>
                </div>
              );
            })}
          </div>
        ))}
          </>
        )}
        {isAdmin && <AuditLog />}
      </div>
    </>
  );
}

/**
 * The audit log (list_audit_log over GET /api/audit), the workspace admin's alone: the server refuses
 * anyone else, so it is not rendered for them. Newest first, last 90 days; the action filter is the
 * tool's exact-match `action`.
 */
export function AuditLog() {
  const [action, setAction] = useState('');
  // Pages of 100, older ones on Load more through the nextCursor each page answers.
  const q = useInfiniteQuery({
    queryKey: ['auditLog', action],
    initialPageParam: '',
    queryFn: ({ pageParam }) => auditApi.listAuditLog({ action: action.trim() || undefined, limit: 100, cursor: pageParam || undefined }),
    getNextPageParam: (last) => last.nextCursor || null,
  });
  const entries = q.data?.pages.flatMap((p) => p.entries) ?? [];
  // A name beside each account id the caller's projects can name (P-457). The id stays: this is the
  // forensic record, and two accounts may share a name. Subjects are stored lower-cased (P-441), so
  // the lookup ignores case.
  const names = useWorkspaceNames();
  const byLower = useMemo(() => Object.fromEntries(Object.entries(names).map(([id, n]) => [id.toLowerCase(), n])), [names]);
  const account = (id: string, suffix = '') => {
    const name = byLower[id.toLowerCase()];
    return name
      ? <><span>{name}</span> <span className="mono muted" style={{ fontSize: 11 }}>{id}{suffix}</span></>
      : <span className="mono">{id}{suffix}</span>;
  };
  return (
    <section id="audit-log" style={{ marginTop: 24 }}>
      <Panel
        title="Audit log"
        right={
          <input
            className="input"
            style={{ width: 220 }}
            placeholder="action, e.g. task.deleted"
            aria-label="Filter the audit log by action"
            value={action}
            onChange={(e) => setAction(e.target.value)}
          />
        }
      >
        {q.isError && <p className="error" style={{ fontSize: 12, margin: '8px 12px' }}>Couldn’t load the audit log.</p>}
        {q.isLoading && <WaitLoader />}
        {!q.isLoading && !q.isError && entries.length === 0 && (
          <p className="muted" style={{ fontSize: 12, padding: 12, margin: 0 }}>No audit rows in the last {q.data?.pages[0]?.days ?? 90} days.</p>
        )}
        {entries.length > 0 && (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ whiteSpace: 'nowrap' }}>
              <thead>
                <tr><th>When</th><th>Action</th><th>Actor</th><th>Subject</th><th>Result</th><th>Data</th></tr>
              </thead>
              <tbody>
                {entries.map((e, i) => (
                  <tr key={`${e.ts}-${i}`} data-testid="audit-row">
                    <td className="muted" title={new Date(e.ts).toLocaleString()}>{timeAgo(e.ts)}</td>
                    <td className="mono">{e.action}</td>
                    <td>{account(e.actor, e.agent ? ` (${e.agent})` : '')}</td>
                    <td>{e.subject ? account(e.subject) : '—'}</td>
                    <td>{e.result}{e.errorCode ? ` ${e.errorCode}` : ''}</td>
                    <td className="mono" style={{ fontSize: 11, color: 'var(--text-muted)' }}>{e.data === '{}' ? '' : e.data}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {q.hasNextPage && (
          <button className="btn secondary" style={{ margin: '8px 12px' }} disabled={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
            {q.isFetchingNextPage ? 'Loading…' : 'Load more'}
          </button>
        )}
      </Panel>
    </section>
  );
}
