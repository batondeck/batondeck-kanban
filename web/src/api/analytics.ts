// The operations summary over the tool_calls log (R2.11; conductor api.ts:742).
import type { OpsSummary } from '../types.js';
import { req } from './http.js';

export const analyticsOperations = (projectId: string, days = 7) =>
  req<OpsSummary>('GET', `/api/analytics/operations?projectId=${encodeURIComponent(projectId)}&days=${days}`);
