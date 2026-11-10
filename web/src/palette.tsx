/**
 * The ⌘K command palette (Phase 2), built on cmdk. Screens publish context actions (act on the current
 * selection) through a tiny external store; the palette renders navigation, those actions, sub-pages
 * (Set status →, Set priority →), quick-create, and task search — all keyboard-first.
 */
import { useEffect, useMemo, useState, useSyncExternalStore } from 'react';
import { useNavigate } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { Command, defaultFilter } from 'cmdk';
import * as boardsApi from './api/boards.js';
import * as mediaApi from './api/media.js';
import * as projectsApi from './api/projects.js';
import { defaultBoardName } from './api/boards.js';
import { fire } from './toast.js';
import { useProject } from './projectContext.js';
import { useShortcuts } from './shortcuts.js';
import { mayCall } from './toolFloors.js';
import { searchChronicle, searchValue } from './chronicleSearch.js';
import type { ChroniclePage } from './types.js';

/** Chronicle hits per kind. Truncation is stated in the heading — a silent cut is a false absence. */
const CHRONICLE_SHOWN = 6;

/**
 * cmdk's filter, with the ticket search's hits never filtered out.
 *
 * The server matched those (on title and description), so cmdk's own pass over the row's text must
 * not veto one, but the commands around them must stay filtered. The palette turned cmdk's filtering
 * OFF whenever the search had answered, which listed every command unfiltered with the selection on
 * the first: Enter then ran the board's "Open T-n" action, or went to Projects, instead of the match.
 * A hit is ranked by how well its row matches what was typed, and never below TASK_HIT_FLOOR, which
 * sits under a command matched at a word start (about 0.89), so "settings" still selects Go to Settings.
 */
const TASK_HIT = '\u0001task-hit';
const TASK_HIT_FLOOR = 0.5;
export function paletteFilter(value: string, search: string, keywords?: string[]): number {
  if (!keywords?.includes(TASK_HIT)) return defaultFilter(value, search, keywords);
  return Math.max(TASK_HIT_FLOOR, defaultFilter(value, search, keywords.filter((k) => k !== TASK_HIT)));
}

export interface PaletteAction {
  id: string;
  label: string;
  kbd?: string;
  run?: () => void;
  /** When present, selecting the action opens a sub-page of these options. */
  children?: Array<{ label: string; run: () => void }>;
  /**
   * Why the caller may not take this action (P-440): it is listed, disabled, with this beside it, and
   * runs nothing. A screen sets it from `closedWhy` in toolFloors.ts and leaves out `run` and `children`.
   */
  closedWhy?: string;
}
export interface PaletteContext {
  projectId?: string;
  boardId?: string;
  actions: PaletteAction[];
  openTask?: (taskId: string) => void;
  /**
   * Chronicle records the current screen has already loaded, searched CLIENT-SIDE by the palette.
   *
   * Published whole rather than pre-filtered: the query lives here, not on the screen. `⌘K` is
   * already this palette (`shortcuts.tsx` binds it at `global` scope and `Scope` has no deeper slot a
   * screen could claim), so Chronicle search is a group in it — a second overlay on the same chord
   * would be two behaviours behind one key.
   */
  records?: ChroniclePage[];
}

// ---- tiny external store so any screen can publish context without prop-drilling ----
let ctx: PaletteContext = { actions: [] };
let pendingOpen: { pageId?: string } | null = null;
const subs = new Set<() => void>();
/** Imperatively open the palette (optionally straight onto a sub-page, e.g. 'status'). */
export function openPalette(pageId?: string): void {
  pendingOpen = { pageId };
  subs.forEach((f) => f());
}
export function consumePendingOpen(): { pageId?: string } | null {
  const p = pendingOpen;
  pendingOpen = null;
  return p;
}
export function publishPalette(next: PaletteContext): void {
  ctx = next;
  subs.forEach((f) => f());
}
export function clearPalette(): void {
  publishPalette({ actions: [] });
}
function usePaletteContext(): PaletteContext {
  return useSyncExternalStore(
    (cb) => {
      subs.add(cb);
      return () => subs.delete(cb);
    },
    () => ctx,
  );
}

export function CommandPalette() {
  const nav = useNavigate();
  const pctx = usePaletteContext();
  const [open, setOpen] = useState(false);
  const [page, setPage] = useState<PaletteAction | null>(null); // a sub-page (children) or null = root
  const [query, setQuery] = useState('');

  useShortcuts('global', [
    { keys: '⌘k', label: 'Command palette', run: () => setOpen((o) => !o) },
    { keys: '/', label: 'Search', run: () => setOpen(true) },
  ], []);

  useEffect(() => {
    if (!open) {
      setPage(null);
      setQuery('');
    }
  }, [open]);

  // imperative opens (keyboard accelerators like s/p jump straight to a sub-page)
  useEffect(() => {
    const onPing = (): void => {
      const pending = consumePendingOpen();
      if (!pending) return;
      setOpen(true);
      if (pending.pageId) {
        const target = ctx.actions.find((a) => a.id === pending.pageId && a.children);
        if (target) setPage(target);
      }
    };
    subs.add(onPing);
    return () => void subs.delete(onPing);
  }, []);

  const projectsQ = useQuery({ queryKey: ['projects'], queryFn: () => projectsApi.listProjects(), enabled: open });

  // Ticket search: global project selection (works on any screen), query debounced ~250ms.
  const { projectId, setProjectId } = useProject();
  const [debounced, setDebounced] = useState('');
  useEffect(() => {
    const t = setTimeout(() => setDebounced(query), 250);
    return () => clearTimeout(t);
  }, [query]);
  const searchQ = useQuery({
    queryKey: ['palette-search', projectId, debounced],
    queryFn: () => mediaApi.search(projectId, debounced, 8),
    enabled: open && !page && !!projectId && debounced.trim().length > 0,
  });

  const close = (): void => setOpen(false);
  const pick = (run?: () => void): void => {
    close();
    run?.();
  };

  // Chronicle: grouped by kind so 238 records read as an index rather than a wall. `value` carries
  // slug and facet as well as the title, so cmdk's own pass cannot veto a slug-only match.
  const chronicle = useMemo(() => {
    const hits = searchChronicle(pctx.records ?? [], query);
    const out = new Map<string, ChroniclePage[]>();
    for (const h of hits) out.set(h.kind, [...(out.get(h.kind) ?? []), h]);
    return [...out.entries()];
  }, [pctx.records, query]);

  const goto = useMemo(
    () => [
      { label: 'Go to Projects', to: '/projects', kbd: 'g p' },
      { label: 'Go to Agents', to: '/agents', kbd: 'g a' },
      { label: 'Go to Analytics', to: '/analytics', kbd: 'g n' },
      { label: 'Go to Activity', to: '/activity', kbd: 'g l' },
      { label: 'Go to Settings', to: '/settings', kbd: 'g s' },
    ],
    [],
  );

  if (!open) return null;
  return (
    <div className="palette-overlay" onClick={close}>
      <div className="palette" onClick={(e) => e.stopPropagation()}>
        <Command label="Command palette" filter={paletteFilter}>
          <Command.Input
            autoFocus
            value={query}
            onValueChange={setQuery}
            placeholder={page ? `${page.label}…` : 'Type a command or search…'}
            onKeyDown={(e) => {
              if (e.key === 'Escape') {
                e.preventDefault();
                if (page) setPage(null);
                else close();
              }
              if (e.key === 'Backspace' && !query && page) setPage(null);
            }}
          />
          <Command.List>
            <Command.Empty>No results.</Command.Empty>

            {page ? (
              <Command.Group heading={page.label}>
                {(page.children ?? []).map((c) => (
                  <Command.Item key={c.label} onSelect={() => pick(c.run)}>
                    {c.label}
                  </Command.Item>
                ))}
              </Command.Group>
            ) : (
              <>
                {pctx.actions.length > 0 && (
                  <Command.Group heading="Task">
                    {pctx.actions.map((a) =>
                      a.closedWhy ? (
                        <Command.Item key={a.id} value={a.label} disabled aria-describedby={`palette-closed-${a.id}`}>
                          {a.label}
                          <span id={`palette-closed-${a.id}`} className="muted" style={{ fontSize: 12 }}>{a.closedWhy}</span>
                          {a.kbd && <span className="kbd">{a.kbd}</span>}
                        </Command.Item>
                      ) : (
                        <Command.Item key={a.id} onSelect={() => (a.children ? (setPage(a), setQuery('')) : pick(a.run))}>
                          {a.label}
                          {a.children ? <span style={{ color: 'var(--text-muted)' }}>›</span> : null}
                          {a.kbd && <span className="kbd">{a.kbd}</span>}
                        </Command.Item>
                      ),
                    )}
                  </Command.Group>
                )}

                <Command.Group heading="Go to">
                  {goto.map((g) => (
                    <Command.Item key={g.to} onSelect={() => pick(() => nav(g.to))}>
                      {g.label}
                      <span className="kbd">{g.kbd}</span>
                    </Command.Item>
                  ))}
                  {(projectsQ.data?.projects ?? []).map((p) => (
                    <Command.Item key={p.projectId} onSelect={() => pick(() => fire(openProject(p.projectId, nav, p.name, mayCall(p.role, 'create_board'), setProjectId)))}>
                      Open project: {p.name}
                    </Command.Item>
                  ))}
                </Command.Group>

                {chronicle.map(([kind, ps]) => (
                  <Command.Group key={kind} heading={`Chronicle · ${kind} (${ps.length})`}>
                    {ps.slice(0, CHRONICLE_SHOWN).map((p) => (
                      <Command.Item
                        key={p.id}
                        value={searchValue(p)}
                        onSelect={() => pick(() => void nav(`/chronicle/${p.id}`))}
                      >
                        {p.title}
                        <span className="mono" style={{ fontSize: 11, color: 'var(--text-muted)' }}>{p.id}</span>
                      </Command.Item>
                    ))}
                  </Command.Group>
                ))}

                {searchQ.isFetching && <Command.Loading>Searching…</Command.Loading>}
                {searchQ.data && searchQ.data.results.length > 0 && (
                  <Command.Group heading="Tasks">
                    {searchQ.data.results.slice(0, 8).map(({ task: t }) => (
                      <Command.Item
                        key={t.id}
                        value={`${t.id} ${t.title}`}
                        keywords={[TASK_HIT]}
                        onSelect={() => pick(() => void nav(`/projects/${projectId}/boards/${t.boardId}?task=${t.id}`))}
                      >
                        Open <span className="mono" style={{ fontSize: 11 }}>{t.id}</span> — {t.title}
                      </Command.Item>
                    ))}
                  </Command.Group>
                )}
              </>
            )}
          </Command.List>
        </Command>
      </div>
    </div>
  );
}

/**
 * Opens the project's first board, creating one when it has none. Creating is `create_board`, tier master
 * (P-440): a caller below it is taken to the project's (empty) board list instead, which says why.
 */
async function openProject(
  projectId: string,
  nav: (to: string) => void,
  projectName: string | undefined,
  mayCreateBoard: boolean,
  select: (projectId: string) => void,
): Promise<void> {
  const { boards } = await boardsApi.listBoards(projectId);
  if (!boards[0] && !mayCreateBoard) {
    select(projectId);
    nav('/projects');
    return;
  }
  const board = boards[0] ?? (await boardsApi.createBoard(projectId, defaultBoardName(projectName))).board;
  nav(`/projects/${projectId}/boards/${board.id}`);
}
