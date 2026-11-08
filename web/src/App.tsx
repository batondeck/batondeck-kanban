import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Navigate, NavLink, Outlet, Route, Routes, useLocation, useNavigate } from 'react-router';
import { useAuth } from './auth.js';
import { Avatar, personName } from './components.js';
import { useWorkspaceNames } from './displayNames.js';
import { ProjectProvider, useProject } from './projectContext.js';
import { ToastHost, fire } from './toast.js';
import { SignIn } from './screens/SignIn.js';
import { Projects } from './screens/Projects.js';
import { Board } from './screens/Board.js';
import { TaskPage } from './screens/TaskPage.js';
import { Settings } from './screens/Settings.js';
import { Agents } from './screens/Agents.js';
import { Chronicle, ChronicleHistory, ChronicleNew, ChroniclePageView } from './screens/Chronicle.js';
import { Analytics } from './screens/Analytics.js';
import { Reports } from './screens/Reports.js';
import Oncall from './screens/Oncall.js';
import { ActivityLog } from './screens/ActivityLog.js';
import { Notifications, NotificationsNavLink } from './screens/Notifications.js';
import { ShortcutProvider, ShortcutHelp, useShortcuts } from './shortcuts.js';
import { CommandPalette } from './palette.js';
import { ErrorBoundary } from './errors.js';
import { can, permissionFor } from './permissions.js';
import { getTheme, setTheme, type Theme } from './theme.js';
import { BootLoader, BrandLockup } from './brand.js';

/* THEMES ARE BACK, AND THIS TIME THE MECHANISM CAN ACT (T-519; owner ask 2026-08-26).

   The banner this replaces recorded the T-398 decision of 2026-08-10: Bone, NeoPOP and NeoPOP-light
   were revamp explorations offered to every user as though supported, only `intent` shipped, and
   `intent` IS `:root` — so `applyTheme` could only ever DELETE an attribute nothing set, and the 34
   lines doing it were provably inert. Deleting them was right.

   What changed is the premise, not the verdict: TWO complete palettes ship now (light on `:root`,
   dark on `[data-theme="dark"]` plus `prefers-color-scheme`), both contrast-measured by
   `contrast.test.ts` and both held to surface-set completeness by `tokens.test.ts`. A control with
   two real destinations is a choice; the one T-398 removed was not.

   `theme.ts` is the ONLY code that writes `data-theme` — `initTheme()` at boot in main.tsx, plus a
   pre-bundle inline script in index.html so a dark-mode user does not get a white flash. The stale
   `bd.theme3` key from the retired system is still simply ignored rather than migrated; the new key
   is `bd.theme` and its three values are `light` / `dark` / absent (= follow the OS). */

function GlobalKeys() {
  const nav = useNavigate();
  useShortcuts('global', [
    { keys: 'g p', label: 'Go to Projects', run: () => { void nav('/projects'); } },
    { keys: 'g a', label: 'Go to Agents', run: () => { void nav('/agents'); } },
    { keys: 'g n', label: 'Go to Analytics', run: () => { void nav('/analytics'); } },
    { keys: 'g r', label: 'Go to Reports', run: () => { void nav('/reports'); } },
    { keys: 'g l', label: 'Go to Activity', run: () => { void nav('/activity'); } },
    { keys: 'g c', label: 'Go to Chronicle', run: () => { void nav('/chronicle'); } },
    { keys: 'g s', label: 'Go to Settings', run: () => { void nav('/settings'); } },
    { keys: 'g o', label: 'Go to On-call', run: () => { void nav('/oncall'); } },
  ], []);
  return null;
}

// 16px feather-style nav glyphs (stroke currentColor); multi-subpath d strings keep each icon one <path>.
const ICONS = {
  projects: 'M4 4h6v16H4zM14 4h6v10h-6z',
  activity: 'M22 12h-4l-3 9L9 3l-3 9H2',
  chronicle: 'M4 19.5A2.5 2.5 0 0 1 6.5 17H20M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z',
  agents: 'M9 2v3M15 2v3M9 19v3M15 19v3M2 9h3M2 15h3M19 9h3M19 15h3M5 5h14v14H5z',
  analytics: 'M18 20V10M12 20V4M6 20v-6',
  reports: 'M4 4h11l5 5v11a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1zM8 17v-4M12 17v-6M16 17v-2',
  // A BROADCAST MAST, NOT A BELL. This was a bell until 2026-08-29 and `NotificationsNavLink` draws
  // its own bell two rows below it in the same nav group — two different `d` strings that render as
  // the same 16px silhouette, so the only thing telling On-call from Notifications was the label.
  // A mast also says what the page IS: telemetry arriving from outside, not messages for you.
  // Keep it distinguishable from every other entry here at 16px before changing it.
  oncall: 'M12 21v-7M9 21h6M12 14a2 2 0 1 0 0-4 2 2 0 0 0 0 4M7.8 14.2a6 6 0 0 1 0-8.4M16.2 5.8a6 6 0 0 1 0 8.4',
  settings: 'M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1 14h6M9 8h6M17 16h6',
};

function NavIcon({ d }: { d: string }) {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d={d} />
    </svg>
  );
}

/** Global project switcher — hidden while loading or when the user has no projects. */
export function ProjectSwitcher() {
  const { projectId, setProjectId, projects, loading } = useProject();
  if (loading || projects.length === 0) return null;
  return (
    <select className="proj-switch" aria-label="Project" value={projectId} onChange={(e) => setProjectId(e.target.value)}>
      <option value="">All projects</option>
      {projects.map((p) => (
        <option key={p.projectId} value={p.projectId}>
          {p.name}
        </option>
      ))}
    </select>
  );
}

/** Footer identity row → popover menu (Appearance, Docs, Help, Sign out). */
function Foot() {
  const { identity, displayName, logout } = useAuth();
  // The chip names the person, never the `U-<hex>` account id (P-314): the session's own
  // `displayName` first (it is there even for an account in no project), then any project's
  // `memberDisplay`, then the shortened id.
  const workspace = useWorkspaceNames();
  const me = identity ? personName(identity, displayName ? { ...workspace, [identity]: displayName } : workspace, { full: true }) : null;
  // Theme state is back (T-519) and now means something: TWO complete palettes ship, so the control
  // has somewhere to go. T-398 removed its predecessor because only one palette existed and the state
  // could only ever write the same value back — that reasoning held then and does not now.
  // Seeded from `theme.ts`, which is also what `initTheme()` read at boot, so the menu opens showing
  // the palette actually on screen rather than a default.
  const [theme, setThemeState] = useState<Theme>(() => getTheme());
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', close);
    return () => document.removeEventListener('mousedown', close);
  }, [open]);
  return (
    <div className="foot" ref={ref}>
      {open && (
        <div className="avatar-menu" role="menu">
          {/* APPEARANCE. Three radios, not a two-state switch: `system` (follow the OS) is a distinct
              state from `light`, and collapsing them would pin the app to one palette the first time
              anyone touched the control. `theme.ts` owns the attribute — see the note there. */}
          <span className="nav-group-label" style={{ padding: '6px 9px 2px' }}>Appearance</span>
          <div role="radiogroup" aria-label="Appearance" className="theme-switch">
            {(['light', 'dark', 'system'] as const).map((t) => (
              <button
                key={t}
                role="radio"
                aria-checked={theme === t}
                className={theme === t ? 'on' : ''}
                onClick={() => { setTheme(t); setThemeState(t); }}
              >
                {t === 'light' ? 'Light' : t === 'dark' ? 'Dark' : 'System'}
              </button>
            ))}
          </div>
          <a href="https://github.com/batondeck/batondeck-kanban#readme" target="_blank" rel="noreferrer" role="menuitem">Docs</a>
          <a href="https://github.com/batondeck/batondeck-kanban/issues" target="_blank" rel="noreferrer" role="menuitem">Help</a>
          <button role="menuitem" onClick={() => fire(logout())}>Sign out</button>
        </div>
      )}
      <button className="identity-row" title={me ?? undefined} aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
        {identity && me && <Avatar id={identity} label={me} nameOnly />}
        <span className="identity-email">{me}</span>
      </button>
    </div>
  );
}

function Shell() {
  // NAV AND ROUTES MUST DECIDE THE SAME WAY. This read `isAdmin` while the routes below read the
  // permission — the same "seen vs callable" divergence T-366's review caught in the core, reproduced
  // here in one file. During the migration window it is not hypothetical: `permissions` is absent
  // until the BFF lands, so the routes refuse while `isAdmin` may still be true, and the nav would
  // offer a link to a door that refuses — the precise thing `permissions.ts` says this exists to
  // prevent. One predicate, both places.
  const { permissions } = useAuth();
  return (
    <div className="shell">
      <nav className="nav">
        {/* THE VERSION IS GONE ON PURPOSE. It was a hardcoded "v1.0" string that no release ever
            updated, so it stated a fact that had been wrong since the first deploy — worse than
            saying nothing. The real version is on the release, not in the chrome. */}
        <div className="brand"><BrandLockup height={18} /></div>
        {/* CAPTIONED. Unlabelled, this reads as a BOARD picker — especially against Settings' board
            picker, and doubly so when a project happens to be named "Board 1". Reported from staging
            as "why do we need this dropdown when we have that one"; they select different things and
            nothing on screen said so. */}
        <div className="nav-group">
          <span className="nav-group-label">Project</span>
          <ProjectSwitcher />
        </div>
        <div className="nav-group">
          <span className="nav-group-label">Work</span>
          <NavLink to="/projects"><NavIcon d={ICONS.projects} />Projects</NavLink>
          <NavLink to="/agents"><NavIcon d={ICONS.agents} />Agents</NavLink>
          <NavLink to="/chronicle"><NavIcon d={ICONS.chronicle} />Chronicle</NavLink>
        </div>
        <div className="nav-group">
          <span className="nav-group-label">Observe</span>
          <NavLink to="/activity"><NavIcon d={ICONS.activity} />Activity</NavLink>
          <NavLink to="/analytics"><NavIcon d={ICONS.analytics} />Analytics</NavLink>
          <NavLink to="/reports"><NavIcon d={ICONS.reports} />Reports</NavLink>
          {can(permissions, permissionFor('/oncall')!) && <NavLink to="/oncall"><NavIcon d={ICONS.oncall} />On-call</NavLink>}
          <NotificationsNavLink />
        </div>
        <div className="nav-group">
          <span className="nav-group-label">Configure</span>
          <NavLink to="/settings"><NavIcon d={ICONS.settings} />Settings</NavLink>
        </div>
        <Foot />
      </nav>
      <div className="main">
        {/* Scoped to the routed screen so a crashed Board leaves the nav, the switcher and every other
            screen usable. `key` is load-bearing: without it a boundary that has caught once keeps
            rendering its fallback forever, and clicking away from the broken screen appears to do
            nothing — remounting per path is what makes navigation the escape hatch. */}
        <ErrorBoundary key={useLocation().pathname}>
          <Outlet />
        </ErrorBoundary>
      </div>
      <GlobalKeys />
      <CommandPalette />
      <ShortcutHelp />
      <ToastHost />
    </div>
  );
}


/**
 * A ROUTE THE CALLER MAY NOT HAVE — rendered as an explicit refusal, never as a bounce (T-378).
 *
 * *** WHY NOT `<Navigate to="/projects">`, WHICH IS WHAT THIS REPLACED. *** A silent redirect is
 * indistinguishable from a broken link, a stale bookmark, and a bug. A caller who types `/oncall` and
 * lands on `/projects` cannot tell whether they lack the permission, the page moved, or the app
 * failed — and neither can whoever they report it to. The distinguishable answer is the useful one.
 *
 * *** THIS HIDES, IT DOES NOT ENFORCE. *** The BFF gates these routes and the core gates the tools
 * behind them. See the header of `permissions.ts` — never remove a server check because this exists.
 *
 * Fails CLOSED by construction: `can()` reads `?? []`, so a session whose `permissions` have not
 * arrived refuses rather than admits.
 */
function RequirePermission({ slug, children }: { slug: string; children: ReactNode }) {
  const { permissions } = useAuth();
  // The granted branch is MARKED, and that attribute exists for one reason worth stating: without it
  // the positive control can only assert the REFUSAL is absent, which is equally true when the route
  // renders nothing at all. Measured — with the gate changed to `return null` on the granted branch,
  // all 17 cases still passed. An assertion that cannot distinguish "admitted" from "blank screen" is
  // not a positive control, and a blank screen is the failure rule 9a exists for.
  if (can(permissions, slug)) return <div data-testid="permission-granted" style={{ display: 'contents' }}>{children}</div>;
  return (
    <div className="center muted" role="status" data-testid="permission-refused">
      <h2>Not available to this account</h2>
      <p>
        This page needs the <code>{slug}</code> permission. Your account does not hold it.
      </p>
      <p>
        If that is unexpected, a workspace admin can grant it — access is not something this page can
        request for you.
      </p>
    </div>
  );
}

export function App() {
  // `isAdmin` is deliberately NOT destructured any more: every route decision here now goes
  // through `RequirePermission`. The alias still exists on the context for the screens that
  // have not migrated yet (T-378 tail) — this file is simply no longer one of them.
  const { identity, loading } = useAuth();
  if (loading) return <BootLoader caption="Signing you in…" />;
  if (!identity) return <SignIn />;
  return (
    <ShortcutProvider>
    <ProjectProvider>
    <Routes>
      <Route element={<Shell />}>
        <Route path="/" element={<Navigate to="/projects" replace />} />
        <Route path="/projects" element={<Projects />} />
        <Route path="/projects/:projectId/boards/:boardId" element={<Board />} />
        <Route path="/projects/:projectId/boards/:boardId/tasks/:taskId" element={<TaskPage />} />
        <Route path="/agents" element={<Agents />} />
        <Route path="/chronicle" element={<Chronicle />} />
        {/* BEFORE the `:pageId` route for readability only — react-router ranks a static segment
            above a dynamic one regardless of order, and a real page id is `CP-<hash>`, so `new`
            could never have been mistaken for one. */}
        <Route path="/chronicle/new" element={<ChronicleNew />} />
        <Route path="/chronicle/:pageId" element={<ChroniclePageView />} />
        <Route path="/chronicle/:pageId/history" element={<ChronicleHistory />} />
        <Route path="/analytics" element={<Analytics />} />
        <Route path="/reports" element={<Reports />} />
        <Route path="/activity" element={<ActivityLog />} />
        <Route path="/notifications" element={<Notifications />} />
        <Route path="/settings" element={<Settings />} />
        {/* GATED ROUTES. The slug comes from `ROUTE_PERMISSIONS`, so the map and the router cannot
            disagree — and `permission-gating.test.tsx` iterates that same map, so adding a gated
            route here without adding it there fails rather than shipping unasserted. */}
        <Route
          path="/oncall"
          element={
            <RequirePermission slug={permissionFor('/oncall')!}>
              <Oncall />
            </RequirePermission>
          }
        />
        <Route path="*" element={<Navigate to="/projects" replace />} />
      </Route>
    </Routes>
    </ProjectProvider>
    </ShortcutProvider>
  );
}
