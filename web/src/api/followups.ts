// The follow-up routes (internal/api/followups.go, R2.1); R1.12 copied the signatures from api.ts.
import type { FollowUp, Task } from '../types.js';
import { req, P } from './http.js';

// follow-ups (directed directives to the task's current holder; reopen-capable)
export const listFollowUps = (projectId: string, taskId: string, openOnly = false) =>
  req<{ followUps: FollowUp[]; nextCursor?: string }>(
    'GET',
    `${P(projectId)}/tasks/${taskId}/follow-ups${openOnly ? '?openOnly=true' : ''}`,
  );

export const addFollowUp = (projectId: string, taskId: string, body: string, reopen: boolean) =>
  req<{ followUp: FollowUp; task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/follow-ups`, { body, reopen });

export const ackFollowUp = (projectId: string, taskId: string, followUpId: string) =>
  req<{ followUp: FollowUp }>('POST', `${P(projectId)}/tasks/${taskId}/follow-ups/${followUpId}/ack`);
