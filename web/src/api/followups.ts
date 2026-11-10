// The follow-up routes (internal/api/followups.go, R2.1); R1.12 copied the signatures from api.ts.
import type { FollowUp, Task } from '../types.js';
import { req, P } from './http.js';
import { PAGE_LIMIT, pageQuery, readAllPages } from './pages.js';

// follow-ups (directed directives to the task's current holder; reopen-capable)
export const listFollowUps = (projectId: string, taskId: string, openOnly = false, page: { limit?: number; cursor?: string } = {}) =>
  req<{ followUps: FollowUp[]; nextCursor?: string }>(
    'GET',
    `${P(projectId)}/tasks/${taskId}/follow-ups${pageQuery({ openOnly, ...page })}`,
  );

/** Every follow-up, oldest first: every page (see ./pages.ts); `truncated` when the bound cut it. */
export const listAllFollowUps = async (projectId: string, taskId: string): Promise<{ followUps: FollowUp[]; truncated: boolean }> => {
  const { rows, truncated } = await readAllPages((cursor) =>
    listFollowUps(projectId, taskId, false, { limit: PAGE_LIMIT, ...(cursor ? { cursor } : {}) }).then((r) => ({ rows: r.followUps, nextCursor: r.nextCursor })));
  return { followUps: rows, truncated };
};

export const addFollowUp = (projectId: string, taskId: string, body: string, reopen: boolean) =>
  req<{ followUp: FollowUp; task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/follow-ups`, { body, reopen });

export const ackFollowUp = (projectId: string, taskId: string, followUpId: string) =>
  req<{ followUp: FollowUp }>('POST', `${P(projectId)}/tasks/${taskId}/follow-ups/${followUpId}/ack`);
