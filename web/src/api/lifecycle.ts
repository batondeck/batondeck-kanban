// The lifecycle area (internal/api/lifecycle.go): claim, release, complete, block, handoff.
import type { Task } from '../types.js';
import { req, P } from './http.js';

export const claimTask = (projectId: string, taskId: string) =>
  req<{ leaseId: string; task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/claim`, {});

export const releaseTask = (projectId: string, taskId: string, leaseId: string) =>
  req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/release`, { leaseId });

export const completeTask = (projectId: string, taskId: string, leaseId: string) =>
  req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/complete`, { leaseId });

export const blockTask = (projectId: string, taskId: string, leaseId: string, reason: string) =>
  req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/block`, { leaseId, reason });

export const handoffTask = (projectId: string, taskId: string, leaseId: string, toAgent: string, memoryNote: string) =>
  req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/handoff`, { leaseId, toAgent, memoryNote });
