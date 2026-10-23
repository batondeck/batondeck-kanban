// Owner: R2.11 fills these bodies; R1.12 copied the signatures from api.ts, which R1.14 splits onto them.
import type { OpsSummary } from '../types.js';
import { req } from './http.js';

export const analyticsOperations = (projectId: string, days = 7) =>
  req<OpsSummary>('GET', `/api/analytics/operations?projectId=${encodeURIComponent(projectId)}&days=${days}`);
