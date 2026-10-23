// The context area (internal/api/context.go): context items, summary, subtasks, memory, dependencies.
import type { Attachment, ContextItem, DependencyEdge, FollowUp, MemoryEntry, Task } from '../types.js';
import { req, P, markLocalEdit } from './http.js';

export const taskContext = (projectId: string, taskId: string) =>
  req<{ summary: string; fields: Record<string, unknown>; items: ContextItem[]; deps: { blockedBy: string[]; blocks: string[]; edges: DependencyEdge[] }; attachments?: Attachment[]; memory: MemoryEntry[]; openFollowUps?: FollowUp[]; openFollowUpCount?: number; nextCursor?: string }>(
    'GET',
    `${P(projectId)}/tasks/${taskId}/context`,
  );

export const addContextItem = (projectId: string, taskId: string, kind: string, body: string) =>
  req<{ item: ContextItem }>('POST', `${P(projectId)}/tasks/${taskId}/context-items`, { kind, body });

export const setSummary = (projectId: string, taskId: string, version: number, summary: string) =>
  req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/summary`, { version, summary });

export const listSubtasks = (projectId: string, taskId: string) => req<{ tasks: Task[] }>('GET', `${P(projectId)}/tasks/${taskId}/subtasks`);

export const addSubtask = (projectId: string, taskId: string, title: string) =>
  req<{ task: Task }>('POST', `${P(projectId)}/tasks/${taskId}/subtasks`, { title });

export const readMemory = (projectId: string, taskId: string) => req<{ entries: MemoryEntry[] }>('GET', `${P(projectId)}/tasks/${taskId}/memory`);

export const writeMemory = (projectId: string, taskId: string, scope: string, key: string, value: unknown) =>
  req<{ entryId: string }>('POST', `${P(projectId)}/tasks/${taskId}/memory`, { scope, key, value });

export const addDependency = (projectId: string, fromTaskId: string, toTaskId: string, type = 'blocks') => {
  // Not a /tasks/<id> path, but it bumps BOTH endpoints' versions — mark them so neither flashes.
  markLocalEdit(fromTaskId);
  markLocalEdit(toTaskId);
  return req<{ edge: unknown }>('POST', `${P(projectId)}/dependencies`, { fromTaskId, toTaskId, type });
};

export const removeDependency = (projectId: string, edgeId: string) =>
  req<{ ok: true }>('DELETE', `${P(projectId)}/dependencies/${edgeId}`);
