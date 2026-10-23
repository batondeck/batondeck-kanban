// The feed area (internal/api/feed.go): a board's activity feed. /api/stream lives in ./stream.ts.
import type { ActivityEvent } from '../types.js';
import { req, P } from './http.js';

export const feed = (projectId: string, boardId: string) => req<{ events: ActivityEvent[] }>('GET', `${P(projectId)}/boards/${boardId}/feed`);
