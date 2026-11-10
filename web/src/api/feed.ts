// The feed area (internal/api/feed.go): a board's activity feed. /api/stream lives in ./stream.ts.
import type { ActivityEvent } from '../types.js';
import { req, P } from './http.js';
import { PAGE_LIMIT, pageQuery, readAllPages } from './pages.js';

/** One page of the board's feed (newest first; 50 by default), or of one task's events (`taskId`). */
export const feed = (projectId: string, boardId: string, page: { taskId?: string; limit?: number; cursor?: string } = {}) =>
  req<{ events: ActivityEvent[]; nextCursor?: string }>('GET', `${P(projectId)}/boards/${boardId}/feed${pageQuery(page)}`);

/** How many pages of a ticket's own events its Activity reads (newest first). */
const TASK_FEED_PAGES = 5;

/**
 * A ticket's own events, newest first: the feed narrowed to the task, read to its end or to
 * `TASK_FEED_PAGES` pages (`truncated` then). Filtering the board's newest page by task lost every
 * event of an older ticket once the board had moved on.
 */
export const taskFeed = async (projectId: string, boardId: string, taskId: string): Promise<{ events: ActivityEvent[]; truncated: boolean }> => {
  const { rows, truncated } = await readAllPages(
    (cursor) => feed(projectId, boardId, { taskId, limit: PAGE_LIMIT, ...(cursor ? { cursor } : {}) }).then((r) => ({ rows: r.events, nextCursor: r.nextCursor })),
    TASK_FEED_PAGES,
  );
  return { events: rows, truncated };
};
