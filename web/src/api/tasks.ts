// The tasks area (internal/api/tasks.go): one task's read, patch and move.
import type { Task } from '../types.js';
import { req, P } from './http.js';

export const getTask = (projectId: string, taskId: string) => req<{ task: Task }>('GET', `${P(projectId)}/tasks/${taskId}`);

export const updateTask = (projectId: string, taskId: string, version: number, patch: Record<string, unknown>) =>
  req<{ task: Task }>('PATCH', `${P(projectId)}/tasks/${taskId}`, { version, patch });

export const moveTask = (projectId: string, taskId: string, version: number, args: Record<string, unknown>) =>
  req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/move`, { version, ...args });
