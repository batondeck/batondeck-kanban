import type { Sprint, SprintStatus } from '../types.js';
import { req, P } from './http.js';

// sprints (T-257). DEFINITION is agent-driven — create_sprint and set_sprint stay MCP-only, because
// deciding what an objective contains is the planning act, not a click. What the SPA owns is the
// LIFECYCLE: approve (the proposal task, through the ordinary REVIEW→DONE control) and then the
// status changes below. T-330: activation had no button anywhere, so the ceremony was half
// built — a human could approve and then had no way to start the thing they had approved.
export const listSprints = (projectId: string, boardId: string) =>
  req<{ sprints: Sprint[] }>('GET', `${P(projectId)}/sprints?boardId=${encodeURIComponent(boardId)}`);

/**
 * PROPOSED→ACTIVE|ABANDONED, ACTIVE→CLOSED|ABANDONED. `version` is the sprint's, not a task's.
 *
 * The core owns every rule this can hit — admin role, the bound-and-DONE proposal task, the
 * self-approval rung, the fixed transition table — and returns `warnings` rather than throwing on
 * the soft ones (closing with non-terminal members; approving your own proposal under 'warn').
 * The caller MUST surface those: a warning the UI swallows is the same as no warning.
 */
export const updateSprint = (
  projectId: string,
  sprintId: string,
  version: number,
  patch: { status?: SprintStatus; name?: string; goal?: string; acceptance?: string },
) =>
  req<{ sprint: Sprint; warnings?: string[] }>('POST', `${P(projectId)}/sprints/${sprintId}`, { version, patch });
