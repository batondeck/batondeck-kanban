// The media area (internal/api/media.go): attachments and search.
import type { Attachment, Task } from '../types.js';
import { req, P } from './http.js';

/**
 * A signed blob URL (`…/blobs/<id>?exp=…&sig=…`) on this page's own origin. With public_url set the
 * server signs every blob URL at public_url, whatever origin the browser reached it at, and behind a
 * TLS proxy that sends no X-Forwarded-Proto it signs them as http://. The page's CSP allows only its
 * own origin for fetches and images, and /blobs answers no CORS, so such a URL could not take an
 * upload or show an image. The signature covers the purpose, the id and the expiry, not the host, so
 * the same path and query work here. Any other URL comes back as it was.
 */
export function onOwnOrigin(url: string): string {
  let u: URL;
  try {
    u = new URL(url, window.location.href);
  } catch {
    return url;
  }
  if (!/^\/blobs\/[^/]+$/.test(u.pathname) || !u.searchParams.has('exp') || !u.searchParams.has('sig')) return url;
  return `${window.location.origin}${u.pathname}${u.search}`;
}

export const listAttachments = (projectId: string, taskId: string) =>
  req<{ attachments: Attachment[] }>('GET', `${P(projectId)}/tasks/${taskId}/attachments`);

export const createAttachment = (projectId: string, taskId: string, body: { fileName: string; mimeType: string; kind: string; bytes: number }) =>
  req<{ attachmentId: string; uploadUrl: string; expiresAt: string }>('POST', `${P(projectId)}/tasks/${taskId}/attachments`, body);

export const search = (projectId: string, query: string, k?: number) =>
  req<{ results: Array<{ task: Task; score: number }> }>(
    'GET',
    `${P(projectId)}/search?${new URLSearchParams({ query, ...(k ? { k: String(k) } : {}) }).toString()}`,
  );
