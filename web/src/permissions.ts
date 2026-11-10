/**
 * WHAT THIS SESSION MAY SEE — the SPA's one reading of a permission (T-378).
 *
 * *** HIDING IS NOT ENFORCING, AND NOTHING IN THIS FILE IS A SECURITY CONTROL. ***
 *
 * The server decides. Every REST route checks the caller itself (`internal/api`, over
 * `internal/auth`), and every tool behind it is checked again by the service against its tier
 * (`internal/domain/grants.go`). What this file buys is that a caller is not shown a door they
 * cannot open — a UX property, not an authorization one.
 *
 * **So never delete a server-side check because the link is hidden.** A browser is not a trust
 * boundary: the SPA is code on the caller's machine, the session response is theirs to read, and a
 * route is one URL edit away whatever the nav renders.
 *
 * === FAIL CLOSED, AND WHY THE `?? []` IS THE LOAD-BEARING CHARACTER ===
 *
 * `/api/session` (`internal/api/session.go`) always sends `permissions`, but an absent array must
 * still read as "no permissions", never as "unrestricted": a fail-open reading would open every
 * gated screen to any session whose answer lacked the field, and it would look correct in review
 * because every screen would work.
 */

/** The workspace-admin slug. One definition; the core's `WORKSPACE_ADMIN_PERMISSION` is its twin. */
export const SUPERADMIN = 'batondeck:superadmin';

/**
 * === WHY `can(permissions, SUPERADMIN)` MAY STAND IN FOR THE `isAdmin` BOOLEAN (T-378 tail) ===
 *
 * The three screens that widen `/api/agents/sessions` to `all=true` read this slug rather than the
 * `isAdmin` alias, so the SPA has ONE oracle instead of two. That substitution is sound because
 * `/api/session` (`internal/api/session.go`) SYNTHESISES the slugs from the one fact it has: a
 * workspace admin's caller (`WorkspaceAdmin`) gets `isAdmin: true` and `permissions` holding
 * `batondeck:superadmin` and `batondeck:oncall`; anyone else gets `false` and none.
 *
 * *** IF THAT SYNTHESIS EVER GOES, THIS SUBSTITUTION BREAKS SILENTLY — a widened fetch narrows and
 * nothing errors. *** `agent-sessions-widening.test.tsx` asserts the SPA half; the server half is
 * `internal/api/session.go`.
 */

/**
 * THE ONE SOURCE OF TRUTH FOR WHICH ROUTES ARE GATED, and the thing the tests are generated from.
 *
 * A route absent from this map is UNGATED — visible to any signed-in caller. That is the correct
 * default for the eleven ordinary screens, which are scoped by project membership at the core rather
 * than by a workspace permission, and it is why this maps only the exceptions.
 *
 * *** ADD A ROUTE HERE AND ITS TEST CASE APPEARS AUTOMATICALLY. *** `permission-gating.test.tsx`
 * iterates this map rather than listing routes, so a new gated route cannot be added without being
 * asserted. A hardcoded list is precisely how four of the six `'conditional'` admin tools went
 * unasserted until T-370 went looking — the list and the thing it guards drift the moment they are
 * two objects.
 *
 * *** THE INVARIANT: AN ENTRY HERE MUST BE A SLUG `/api/session` SYNTHESISES. *** The server holds no
 * per-page permissions: a session has the two slugs above or none (`internal/api/session.go`). A
 * slug added here that the server never sends hides the screen from everyone, workspace admins
 * included, so a new gated page moves this map and that synthesis in ONE change. NOT MECHANICALLY
 * ENFORCED — no check binds the two files; it is a review obligation.
 */
export const ROUTE_PERMISSIONS: Readonly<Record<string, string>> = {
  // The one page-slug (non-superadmin) route. It is safe here only because `/api/session` SYNTHESISES
  // `batondeck:oncall` for a workspace admin alongside the superadmin slug — without that half, this
  // line hides the incident dashboard from every admin, which is precisely the lockout the warning
  // above describes. The two move together or not at all.
  '/oncall': 'batondeck:oncall',
};

/**
 * Does this session hold `slug`?
 *
 * ONE HELPER, NOT `permissions.includes(...)` AT EACH SCREEN. Nine call sites are nine chances for
 * the tenth to be written wrong, and a test can only catch the omission at a site it already knows
 * about. Centralising makes
 * the next slug one edit instead of nine, and makes the fail-closed reading impossible to forget.
 *
 * EXACT MATCH ONLY — no prefix, in either direction. `batondeck:admin` must never satisfy
 * `batondeck:superadmin`, and a future `batondeck:superadmin:write` must not be satisfied by holding
 * the shorter one.
 */
export function can(permissions: readonly string[] | undefined, slug: string): boolean {
  return (permissions ?? []).includes(slug);
}

/**
 * The permission a route requires, or `undefined` when it is ungated.
 *
 * Exact-path lookup, deliberately: `/oncall` is gated and `/projects/:id/boards/:id` is not, and a
 * prefix rule would have to decide which paths resemble which. An authorization lookup that infers
 * is an authorization lookup that surprises, so this one never will.
 *
 * A gated subtree gets ONE key, on its layout route, with its children mounted relatively under it:
 * `permission-gating.test.tsx` GENERATES a nav-agreement case per map key, demanding a sidebar link
 * whose href equals that key exactly, so a key per child would demand a sidebar entry per child.
 */
export function permissionFor(path: string): string | undefined {
  return ROUTE_PERMISSIONS[path];
}
