// The comment routes (internal/api/comments.go, R2.1); R1.12 copied the signatures from api.ts.
import type { Comment } from '../types.js';
import { req, P } from './http.js';
import { PAGE_LIMIT, pageQuery, readAllPages } from './pages.js';

export const listComments = (projectId: string, taskId: string, page: { limit?: number; cursor?: string } = {}) =>
  req<{ comments: Comment[]; nextCursor?: string }>('GET', `${P(projectId)}/tasks/${taskId}/comments${pageQuery(page)}`);

/** The whole thread, oldest first: every page (see ./pages.ts); `truncated` when the bound cut it. */
export const listAllComments = async (projectId: string, taskId: string): Promise<{ comments: Comment[]; truncated: boolean }> => {
  const { rows, truncated } = await readAllPages((cursor) =>
    listComments(projectId, taskId, { limit: PAGE_LIMIT, ...(cursor ? { cursor } : {}) }).then((r) => ({ rows: r.comments, nextCursor: r.nextCursor })));
  return { comments: rows, truncated };
};

export const addComment = (projectId: string, taskId: string, body: string, parentCommentId?: string) =>
  req<{ comment: Comment }>('POST', `${P(projectId)}/tasks/${taskId}/comments`, {
    body,
    ...(parentCommentId ? { parentCommentId } : {}),
  });
