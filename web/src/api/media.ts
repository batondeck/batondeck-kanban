// The media area (internal/api/media.go): attachments and search.
import type { Attachment, Task } from '../types.js';
import { req, P } from './http.js';

export const listAttachments = (projectId: string, taskId: string) =>
  req<{ attachments: Attachment[] }>('GET', `${P(projectId)}/tasks/${taskId}/attachments`);

export const createAttachment = (projectId: string, taskId: string, body: { fileName: string; mimeType: string; kind: string; bytes: number }) =>
  req<{ attachmentId: string; uploadUrl: string; expiresAt: string }>('POST', `${P(projectId)}/tasks/${taskId}/attachments`, body);

export const search = (projectId: string, query: string, k?: number) =>
  req<{ results: Array<{ task: Task; score: number }> }>(
    'GET',
    `${P(projectId)}/search?${new URLSearchParams({ query, ...(k ? { k: String(k) } : {}) }).toString()}`,
  );
