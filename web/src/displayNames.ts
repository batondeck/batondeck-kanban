/**
 * ONE PLACE THAT TURNS OPAQUE IDS INTO NAMES, for every surface that renders an activity feed.
 *
 * WHAT WAS WRONG. Four screens render `eventActor(e)` — Activity, Analytics, the board's feed and the
 * ticket's Activity panel — and every one printed the raw id, because none of them fetched the data
 * that names it. Reported from staging twice: first the on-call rows
 * (`ingest:ing_ba3c6ec8c12cc970c3ae16e2f62ddbaf`), then the human ones
 * (`U-47f0d44f-7a04-4e44-a67f-a39601d12b79`). The second is the T-216 identity cutover working as
 * designed: the core stores an opaque account id and no email.
 *
 * WHERE THE NAMES COME FROM, and neither is a new endpoint:
 *   - `get_project` returns `memberDisplay` — a SIBLING of `project`, derived server-side per read,
 *     mapping account id → email. It names PROJECT MEMBERS only; the core carries a refusal test
 *     proving it cannot name a non-member, which is a privacy boundary, not a gap to work around.
 *   - `list_integrations` gives `ing_…` → the operator's own name for that webhook.
 *
 * `get_project` is best-effort — it can fail for any ordinary reason, and a feed must never fail or
 * show an error because a LABEL could not load. `retry: false`, and `eventActor` falls back to a
 * shortened id.
 *
 * *** `list_integrations` IS ROLE-GATED BEFORE IT IS CALLED, AND THAT IS NOT AN OPTIMISATION. ***
 *
 * This docblock previously said the tool "is gated on the on-call permission and 403s for a member
 * without it", and treated the 403 as a harmless best-effort miss. Both halves were wrong, and the
 * combination was an alert-noise bug:
 *
 *   - `batondeck:oncall` gates the SPA PAGE, *deliberately not* this API — stated verbatim in
 *     `gateway/ui-backend/test/permission-coverage.test.ts`. The real guard is the tool's tier floor:
 *     `requireMaster` at `src/tools/integrations.ts:670`.
 *   - A refusal is therefore NOT inert. `src/auth/index.ts` answers `Requires master … (have: worker)`,
 *     `src/security/signals.ts` matches `/^Requires /` and emits an escalation signal, and
 *     `infra/security-rules/alerts/escalation-deny.yaml` pages CRITICAL on >3 in 5 minutes.
 *
 * This hook mounts on the four busiest screens (Board, Activity, Analytics, the ticket Activity
 * panel), so four `viewer`/`worker` humans opening a board inside one window would have paged the
 * project owner — an alert firing on entirely honest traffic, which engineering rule 19 exists to
 * refuse. The fix is to not make the call, never to raise the threshold.
 *
 * A caller below `master` simply gets no integration names; `eventActor` elides the id instead. That
 * is the same degradation the 403 produced, without the signal.
 *
 * ONE HOOK RATHER THAN FOUR COPIES: react-query dedupes by key, so four mounted consumers share two
 * requests, and a fix to the merge rule lands everywhere at once.
 */
import { useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import * as oncallApi from './api/oncall.js';
import * as projectsApi from './api/projects.js';
import { useProject } from './projectContext.js';

/**
 * Mirrors `ROLE_RANK` in `src/domain/types.ts`. Duplicated rather than imported because the SPA does
 * not depend on the core package; `role-rank-parity` in the web suite pins the two together.
 */
const ROLE_RANK: Record<string, number> = { viewer: 0, worker: 1, master: 2, member: 3, admin: 4 };

/** Long: names change far more slowly than a feed does, and a stale label is harmless. */
const STALE_MS = 300_000;

/**
 * May this caller call `list_integrations` WITHOUT being refused?
 *
 * `master` is the floor the tool enforces (`src/tools/integrations.ts` `requireMaster`), and a
 * refusal is not inert: the core answers `Requires master … (have: worker)`, `signals.ts` classifies
 * it as an escalation, and `escalation-deny.yaml` files on >3 in 5 minutes. So the call has to be
 * gated BEFORE it is made, at EVERY call site — a review found two more after the first fix
 * (`screens/Oncall.tsx`, which also refetches every 60s and so fires with no interaction at all, and
 * `screens/oncall/OncallSetup.tsx`'s deleted-integrations read).
 *
 * Exported as a hook rather than copied, because the first fix covered one of three sites and read
 * as complete.
 *
 * Fails CLOSED: an unknown role string ranks -1 and is refused.
 */
export function useMayReadIntegrations(projectId: string | undefined): boolean {
  const { projects } = useProject();
  const role = projects.find((p) => p.projectId === projectId)?.role;
  return role !== undefined && (ROLE_RANK[role] ?? -1) >= ROLE_RANK.master;
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

  return useMemo(() => {
    const names: Record<string, string> = { ...(projectQ.data?.memberDisplay ?? {}) };
    for (const i of integrationsQ.data?.integrations ?? []) {
      // Keyed by the BARE integration id: `eventActor` strips the `ingest:` prefix before looking up,
      // so the same map serves both id shapes without the caller knowing which is which.
      if (i.name) names[i.id] = i.name;
    }
    return names;
  }, [projectQ.data, integrationsQ.data]);
}
