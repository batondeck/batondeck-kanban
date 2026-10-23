// Owner: R2.1 fills these bodies; R1.12 copied the signatures from api.ts, which R1.14 splits onto them.
import type { Comment } from '../types.js';
import { req, P } from './http.js';

export const listComments = (projectId: string, taskId: string) =>
  req<{ comments: Comment[]; nextCursor?: string }>('GET', `${P(projectId)}/tasks/${taskId}/comments`);

export const addComment = (projectId: string, taskId: string, body: string, parentCommentId?: string) =>
  req<{ comment: Comment }>('POST', `${P(projectId)}/tasks/${taskId}/comments`, {
    body,
    ...(parentCommentId ? { parentCommentId } : {}),
  });
