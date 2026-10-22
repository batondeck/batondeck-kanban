/**
 * WHAT THIS SESSION MAY SEE — the SPA's one reading of a permission (T-378).
 *
 * *** HIDING IS NOT ENFORCING, AND NOTHING IN THIS FILE IS A SECURITY CONTROL. ***
 *
 * The gateway decides. Every route below is also gated at the BFF, and every tool behind it is gated
 * again in the core (`src/server/admin-tools.ts` makes the same statement about its own `tools/list`
 * filter, and for the same reason). What this file buys is that a caller is not shown a door they
 * cannot open — a UX property, not an authorization one.
 *
 * **So never delete a server-side check because the link is hidden.** A browser is not a trust
 * boundary: the SPA is code on the caller's machine, the session response is theirs to read, and a
 * route is one URL edit away whatever the nav renders. If this file were the only gate, `/admin`
 * would be open to anyone who typed it.
 *
 * === FAIL CLOSED, AND WHY THE `?? []` IS THE LOAD-BEARING CHARACTER ===
 *
 * `permissions` is ABSENT from `/api/session` until the BFF lands it. An absent array must read as
 * "no permissions", never as "unrestricted" — the fail-open shape here would ship a gate that is
 * wide open at exactly the moment it looks new and careful, and it would look correct in review
 * because every screen would work.
 *
 * The consequence during the migration is deliberate and worth stating: until the BFF ships,
 * `can()` answers false for everything, so every permission-gated route refuses. `isAdmin` survives
 * as a derived alias for ONE RELEASE so the migration can be incremental — see `ROUTE_PERMISSIONS`.
 */

/** The workspace-admin slug. One definition; the core's `WORKSPACE_ADMIN_PERMISSION` is its twin. */
export const SUPERADMIN = 'batondeck:superadmin';

/**
 * === WHY `can(permissions, SUPERADMIN)` MAY STAND IN FOR THE `isAdmin` BOOLEAN (T-378 tail) ===
 *
 * The three screens that widen `/api/agents/sessions` to `all=true` read this slug rather than the
 * `isAdmin` alias, so the SPA has ONE oracle instead of two. That substitution is only sound because
 * of one expression in the BFF's `/api/session`, which SYNTHESISES the slug rather than deriving the
 * boolean from it. Re-derive it, never quote a line number:
 *
 *   grep -n 'WORKSPACE_ADMIN_PERMISSION\] : resolved' gateway/ui-backend/src/app.ts
 *
 * `isAdmin` is `check_access.status === 'admin'` — the permission **or** the `WORKSPACE_ADMINS`
 * allowlist — and the array gets the slug appended whenever that is true. So the two are equal
 * *including for allowlist-only admins, which is every admin in production today*. Derive `isAdmin`
 * from `permissions` instead and every one of them reports false; that inversion is why the BFF
 * refused the shape T-377 originally specified.
 *
 * *** IF THAT SYNTHESIS EVER GOES, THIS SUBSTITUTION BREAKS SILENTLY — a widened fetch narrows and
 * nothing errors. *** `agent-sessions-widening.test.tsx` asserts the SPA half; the BFF half is pinned
 * on its own side. The coupling is stated here because the two files are in different fences.
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
 * === T-434 RE-TOOK THE PAGE SLUGS AND THIS MAP DELIBERATELY DID NOT MOVE ===
 *
 * The gateway now evaluates a candidate wider map (`SHADOW_PREFIXES` in
 * `gateway/ui-backend/src/permissions.ts`) beside the live one and LOGS the divergence. It enforces
 * nothing new unless `PAGE_GATE_SHADOW_ENFORCE` is on, and it is off everywhere.
 *
 * *** THE INVARIANT: AN ENTRY HERE MUST BE A SUBSET OF WHAT THE GATEWAY ACTUALLY ENFORCES. *** Adding
 * `/settings`, `/agents`, `/notifications`, `/projects` or `/activity` here today would HIDE a screen
 * the gateway would happily serve — the seen-versus-callable divergence inverted, which is the same
 * defect as the other direction and reads as caution rather than as a bug.
 *
 * So whoever flips that flag moves three things in ONE change, and the third is the one that gets
 * forgotten: this map, the gateway's `PREFIXES`, and `/api/session`'s slug SYNTHESIS in
 * `gateway/ui-backend/src/app.ts` — because an allowlist-only admin (every admin in production) holds
 * `batondeck:superadmin` by synthesis and no page slug, so a page slug added here alone hides the
 * screen from exactly the people who would fix it. `permissions.ts`'s "A SECOND ASYMMETRY" block is
 * the gateway-side statement of the same coupling. NOT MECHANICALLY ENFORCED — no check binds these
 * three files; it is a review obligation.
 */
export const ROUTE_PERMISSIONS: Readonly<Record<string, string>> = {
  // The FIRST page-slug (non-superadmin) route. It is safe here only because `/api/session`
  // SYNTHESISES `batondeck:oncall` for allowlist admins alongside the superadmin slug — without that
  // half, this line hides the incident dashboard from every admin in production, which is precisely
  // the lockout the warning above describes. The two move together or not at all.
  '/oncall': 'batondeck:oncall',
};

/**
 * Does this session hold `slug`?
 *
 * ONE HELPER, NOT `permissions.includes(...)` AT EACH SCREEN. The same argument as
 * `clearSessionCookies` in the gateway: nine call sites are nine chances for the tenth to be written
 * wrong, and a test can only catch the omission at a site it already knows about. Centralising makes
 * the next slug one edit instead of nine, and makes the fail-closed reading impossible to forget.
 *
 * EXACT MATCH ONLY — no prefix, in either direction. `batondeck:admin` must never satisfy
 * `batondeck:superadmin`, and a future `batondeck:superadmin:write` must not be satisfied by holding
 * the shorter one. The core's `hasWorkspacePermission` has the same property and a test pinning it.
 */
export function can(permissions: readonly string[] | undefined, slug: string): boolean {
  return (permissions ?? []).includes(slug);
}

/**
 * The permission a route requires, or `undefined` when it is ungated.
 *
 * Exact-path lookup, deliberately: `/admin` is gated and `/projects/:id/boards/:id` is not, and a
 * prefix rule would have to decide which of those `/admin-ish` paths resembles. An authorization
 * lookup that infers is an authorization lookup that surprises, so this one never will.
 *
 * THIS PARAGRAPH USED TO SAY "if a gated subtree ever appears, add its routes explicitly". `/studio`
 * is that subtree, and following the advice would have been wrong — so it is corrected here rather
 * than left for the next person to obey. `permission-gating.test.tsx` GENERATES a nav-agreement case
 * per map key, demanding a rendered sidebar link whose href equals that key exactly; eight keys means
 * eight sidebar entries for one feature. A gated subtree therefore gets ONE key on its layout route,
 * with children mounted relatively underneath it — which keeps this lookup exact, because the
 * children never appear here at all. The advice was written before that generated case existed.
 */
export function permissionFor(path: string): string | undefined {
  return ROUTE_PERMISSIONS[path];
}
