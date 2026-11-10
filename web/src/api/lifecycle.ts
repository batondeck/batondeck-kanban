// The lifecycle area (internal/api/lifecycle.go): claim, release, complete, block, handoff.
import type { Task } from '../types.js';
import { req, P } from './http.js';

/**
 * How long a claim made in the browser holds (s). The REST surface has no heartbeat (only an agent
 * renews, over MCP), so a browser claim on the server's 5-minute default lapsed while its holder was
 * still working: Mark complete answered LEASE_EXPIRED and the reap counted a failed attempt. An hour
 * is well inside claim_task's bound (a day); Release gives it back sooner.
 */
export const BROWSER_LEASE_SECONDS = 3600;

export const claimTask = (projectId: string, taskId: string) =>
  req<{ leaseId: string; task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/claim`, { leaseSeconds: BROWSER_LEASE_SECONDS });

export const releaseTask = (projectId: string, taskId: string, leaseId: string) =>
  req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/release`, { leaseId });

export const completeTask = (projectId: string, taskId: string, leaseId: string) =>
  req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/complete`, { leaseId });

export const blockTask = (projectId: string, taskId: string, leaseId: string, reason: string) =>
  req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/block`, { leaseId, reason });

export const handoffTask = (projectId: string, taskId: string, leaseId: string, toAgent: string, memoryNote: string) =>
  req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/handoff`, { leaseId, toAgent, memoryNote });
