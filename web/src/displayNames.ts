/**
 * ONE PLACE THAT TURNS OPAQUE IDS INTO NAMES, for every surface that renders an activity feed.
 *
 * WHAT WAS WRONG. Four screens render `eventActor(e)` — Activity, Analytics, the board's feed and the
 * ticket's Activity panel — and every one printed the raw id, because none of them fetched the data
 * that names it. Reported from staging twice: first the on-call rows
 * (`ingest:I-ba3c6ec8c12cc970c3ae16e2f62ddbaf`), then the human ones
 * (`U-47f0d44f-7a04-4e44-a67f-a39601d12b79`). The second is the T-216 identity cutover working as
 * designed: the core stores an opaque account id and no email.
 *
 * WHERE THE NAMES COME FROM, and neither is a new endpoint:
 *   - `get_project` returns `memberDisplay` — a SIBLING of `project`, derived server-side per read,
 *     mapping account id → email. It names PROJECT MEMBERS only; the core carries a refusal test
 *     proving it cannot name a non-member, which is a privacy boundary, not a gap to work around.
 *   - `list_integrations` gives `I-…` → the operator's own name for that webhook.
 *
 * `get_project` is best-effort — it can fail for any ordinary reason, and a feed must never fail or
 * show an error because a LABEL could not load. `retry: false`, and `eventActor` falls back to a
 * shortened id.
 *
 * *** `list_integrations` IS GATED BEFORE IT IS CALLED, AND THAT IS NOT AN OPTIMISATION. ***
 *
 * `batondeck:oncall` gates the SPA PAGE, not this API. The API's guard is the tool's posture in
 * `internal/domain/grants.go`: workspace admin required (`AdminOnly: "only"`), then project role
 * master or above, both checked by `authorize` in `internal/service/authz.go`. A caller who fails
 * either is answered 403 FORBIDDEN. This hook mounts on the four busiest screens (Board, Activity,
 * Analytics, the ticket Activity panel), so an ungated call would put a refused request on every
 * one of them for every reader who is not both. The fix is to not make the call.
 *
 * Such a caller simply gets no integration names; `eventActor` elides the id instead. That is the
 * same degradation the 403 produced, without the refused request.
 *
 * ONE HOOK RATHER THAN FOUR COPIES: react-query dedupes by key, so four mounted consumers share two
 * requests, and a fix to the merge rule lands everywhere at once.
 */
import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import * as oncallApi from './api/oncall.js';
import * as projectsApi from './api/projects.js';
import { useAuth } from './auth.js';
import { useProject } from './projectContext.js';

/**
 * Mirrors `roleRank` in `internal/domain/roles.go`. Duplicated because the SPA cannot import Go;
 * `ui-polish.test.tsx` pins the values.
 */
const ROLE_RANK: Record<string, number> = { viewer: 0, worker: 1, master: 2, member: 3, admin: 4 };

/**
 * Does `role` rank at or above `floor`? The SPA's one reading of a tier floor (`TierMinRole` in
 * `internal/domain/roles.go`: read = viewer, write = worker, master = master). Fails CLOSED: an
 * absent or unknown role ranks -1 and passes no floor, the way `RoleRank` does on the server.
 * Hiding a control is not enforcing it; the server still decides.
 */
export function roleAtLeast(role: string | undefined, floor: 'viewer' | 'worker' | 'master' | 'member' | 'admin'): boolean {
  return role !== undefined && (ROLE_RANK[role] ?? -1) >= ROLE_RANK[floor];
}

/**
 * The role `useMyProjectRole` answers once the roster has loaded and has no row for the project: the
 * caller is not a member of it (P-443). A workspace admin can open such a project's pages by URL, and
 * the server refuses their writes there; before this the controls said "Checking your role" for good.
 * It is no role `ROLE_RANK` knows, so `roleAtLeast` ranks it -1 and it passes no floor.
 */
export const NOT_A_MEMBER = 'not-a-member';

/**
 * The caller's role in `projectId`, as `list_projects` gives it on the roster `ProjectProvider` holds
 * (no request of its own). `undefined` while the roster loads (or when it failed), and `NOT_A_MEMBER`
 * once it has loaded without a row for the project (P-443). Both pass no floor in `roleAtLeast`: a
 * screen gating a write on it fails closed (P-432); `closedWhy` gives the second its reason.
 */
export function useMyProjectRole(projectId: string | undefined): string | undefined {
  const { projects, ready } = useProject();
  if (!projectId) return undefined;
  const row = projects.find((p) => p.projectId === projectId);
  if (row) return row.role;
  return ready ? NOT_A_MEMBER : undefined;
}

/** Long: names change far more slowly than a feed does, and a stale label is harmless. */
const STALE_MS = 300_000;

/**
 * May this caller call `list_integrations` WITHOUT being refused?
 *
 * The server makes TWO checks, and this mirrors both (P-248). `list_integrations` is
 * `AdminOnly: "only"` with tier master (`internal/domain/grants.go`), so `authorize`
 * (`internal/service/authz.go`) first refuses anyone who is not a WORKSPACE admin ("Workspace admin
 * required"), and only then checks the caller's role in the project against master. Gating on the
 * project role alone sent every project master who is not a workspace admin a 403 on each screen
 * this hook mounts on. `isAdmin` on `/api/session` is the same flag (`caller.WorkspaceAdmin`,
 * `internal/api/session.go`).
 *
 * The call is gated BEFORE it is made, at EVERY call site: `useDisplayNames` below,
 * `screens/Oncall.tsx` (which also refetches every 60s, so it fires with no interaction at all) and
 * `screens/oncall/OncallSetup.tsx`'s deleted-integrations read. Exported as a hook rather than
 * copied, because the first fix covered one of three sites and read as complete.
 *
 * Fails CLOSED: a non-admin session, an absent project row or an unknown role string (ranked -1) is
 * refused.
 */
export function useMayReadIntegrations(projectId: string | undefined): boolean {
  const { isAdmin } = useAuth();
  const { projects } = useProject();
  const role = projects.find((p) => p.projectId === projectId)?.role;
  return isAdmin && role !== undefined && (ROLE_RANK[role] ?? -1) >= ROLE_RANK.master;
}

/**
 * Every name the caller's project list already carries (P-314): `list_projects` returns
 * `memberDisplay` on each row, and `ProjectProvider` holds that list app-wide under `['projects']`,
 * so this costs no request. It is the map for a surface that is not inside one project (the
 * notifications list, the identity chip), and the floor under `useDisplayNames`: it names a person
 * the caller shares ANY project with, which is no wider than what `list_projects` already shows them.
 * A later row never overwrites an earlier one's name; they are the same account's label.
 */
export function useWorkspaceNames(): Record<string, string> {
  const { projects } = useProject();
  return useMemo(() => {
    const names: Record<string, string> = {};
    for (const p of projects) {
      for (const [id, name] of Object.entries(p.memberDisplay ?? {})) if (name && !(id in names)) names[id] = name;
    }
    return names;
  }, [projects]);
}

export function useDisplayNames(projectId: string | undefined): Record<string, string> {
  const projectQ = useQuery({
    queryKey: ['project-member-display', projectId],
    queryFn: () => projectsApi.getProject(projectId!),
    enabled: !!projectId,
    staleTime: STALE_MS,
    retry: false,
  });

  const mayReadIntegrations = useMayReadIntegrations(projectId);

  const integrationsQ = useQuery({
    queryKey: ['oncall-integrations-names', projectId],
    queryFn: () => oncallApi.listIntegrations(projectId!),
    enabled: !!projectId && mayReadIntegrations,
    staleTime: STALE_MS,
    retry: false,
  });

  const workspace = useWorkspaceNames();

  return useMemo(() => {
    // The project's own read wins over the workspace floor: it is per read, so it is the freshest.
    const names: Record<string, string> = { ...workspace, ...(projectQ.data?.memberDisplay ?? {}) };
    for (const i of integrationsQ.data?.integrations ?? []) {
      // Keyed by the BARE integration id: `eventActor` strips the `ingest:` prefix before looking up,
      // so the same map serves both id shapes without the caller knowing which is which.
      if (i.name) names[i.id] = i.name;
    }
    return names;
  }, [workspace, projectQ.data, integrationsQ.data]);
}
