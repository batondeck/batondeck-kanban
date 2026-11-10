/**
 * THE ROLE EACH WEB WRITE NEEDS, in one table (P-440).
 *
 * Every write the SPA makes is a REST route that calls one MCP tool's service method, and that method's
 * `authorize` checks the caller's project role against the tool's tier: `toolPosture` in
 * `internal/domain/grants.go`, read through `TierMinRole` in `internal/domain/roles.go` (write = worker,
 * master = master, structural = member, admin = admin). This table copies those tiers for the tools a
 * screen offers a control for, because the SPA cannot import Go. A tool added to a screen joins this
 * table with its tier from grants.go; a tier changed there changes here. `tool-floors.test.ts` pins the
 * values.
 *
 * The lease-guarded outcomes (complete, block, release, hand off) have no tier: the held lease is the
 * check, so they are not here and their controls show only to the lease holder.
 *
 * Closing a control is not enforcing it: the server still decides, and still refuses.
 */
import { NOT_A_MEMBER, roleAtLeast } from './displayNames.js';

export type Floor = 'viewer' | 'worker' | 'master' | 'member' | 'admin';

export const TOOL_FLOOR = {
  // TierWrite: a worker or above.
  add_comment: 'worker',
  add_context_item: 'worker',
  ack_follow_up: 'worker',
  attach_file: 'worker',
  claim_task: 'worker',
  create_chronicle_page: 'worker',
  edit_chronicle_page: 'worker',
  set_summary: 'worker',
  write_memory: 'worker',
  // TierMaster. add_column, create_board and delete_chronicle_page are here by the owner's ruling of
  // 2026-11-10 (they were write, as in conductor).
  add_column: 'master',
  add_dependency: 'master',
  add_follow_up: 'master',
  add_subtask: 'master',
  cancel_runs: 'master',
  create_board: 'master',
  create_task: 'master',
  delete_chronicle_page: 'master',
  move_task: 'master',
  open_run: 'master',
  pick_run: 'master',
  remove_dependency: 'master',
  start_runs: 'master',
  update_sprint: 'master',
  update_task: 'master',
  // TierStructural: a member or above.
  create_integration: 'member',
  delete_integration: 'member',
  remove_column: 'member',
  rotate_integration_secret: 'member',
  update_column: 'member',
  update_integration: 'member',
  // TierAdmin.
  add_member: 'admin',
  remove_member: 'admin',
  update_settings: 'admin',
} as const satisfies Record<string, Floor>;

export type GatedTool = keyof typeof TOOL_FLOOR;

/** May a caller holding `role` call `tool`? Fails closed: an absent or unknown role may call nothing. */
export function mayCall(role: string | undefined, tool: GatedTool): boolean {
  return roleAtLeast(role, TOOL_FLOOR[tool]);
}

/**
 * Why a control that calls `tool` is closed to `role`, as one sentence ending "… `what`.", or
 * `undefined` when it is open or while the role is still loading (the control is closed then too, with
 * no reason yet, as P-432 does). `what` is the action in the reader's words: "assign tasks". A caller
 * who is not a member of the project (`NOT_A_MEMBER`, P-443) is told so, whatever the tool's tier.
 */
export function closedWhy(role: string | undefined, tool: GatedTool, what: string): string | undefined {
  if (role === undefined || mayCall(role, tool)) return undefined;
  if (role === NOT_A_MEMBER) return notMemberWhy(what);
  switch (TOOL_FLOOR[tool]) {
    case 'worker':
      return role === 'viewer' ? `A viewer cannot ${what}.` : `Your role cannot ${what}.`;
    case 'master':
      return `Only a master can ${what}.`;
    case 'member':
      return `Only a member or an admin can ${what}.`;
    case 'admin':
      return `Only a project admin can ${what}.`;
    default:
      return `Your role cannot ${what}.`;
  }
}

/** The reason for a caller who is not a member of the project (P-443): "… cannot `what`." */
export function notMemberWhy(what: string): string {
  return `You are not a member of this project, so you cannot ${what}.`;
}
