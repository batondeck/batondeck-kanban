// Owner: R2.2 fills these bodies; R1.12 copied the signatures from api.ts.
import type { RunView, Task } from '../types.js';
import { req, P } from './http.js';

// runs (race a task across N agents; pick the winner)
export const listRuns = (projectId: string, taskId: string) =>
  req<{ runs: RunView[] }>('GET', `${P(projectId)}/tasks/${taskId}/runs`);

export const startRuns = (projectId: string, taskId: string, version: number, opts: { count?: number; agents?: string[]; brief?: string }) =>
  req<{ task: Task; runs: Task[] }>('POST', `${P(projectId)}/tasks/${taskId}/runs`, { version, ...opts });

export const openRun = (projectId: string, taskId: string, opts: { agent?: string; brief?: string }) =>
  req<{ run: Task }>('POST', `${P(projectId)}/tasks/${taskId}/runs/open`, opts);

export const pickRun = (projectId: string, taskId: string, runTaskId: string, version: number) =>
  req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/runs/pick`, { runTaskId, version });

export const cancelRuns = (projectId: string, taskId: string, version: number) =>
  req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/runs/cancel`, { version });
