// The boards area (internal/api/boards.go): boards, columns, and a board's task list and create.
import type { Board, Task } from '../types.js';
import { req, P } from './http.js';

export const listBoards = (projectId: string) => req<{ boards: Board[] }>('GET', `${P(projectId)}/boards`);

export const createBoard = (projectId: string, name: string) => req<{ board: Board }>('POST', `${P(projectId)}/boards`, { name });

export const getBoard = (projectId: string, boardId: string) =>
  req<{ board: Board; taskCounts: Record<string, number> }>('GET', `${P(projectId)}/boards/${boardId}`);

export const addColumn = (projectId: string, boardId: string, name: string, status: string, wipLimit?: number | null) =>
  req<{ column: unknown }>('POST', `${P(projectId)}/boards/${boardId}/columns`, { name, status, wipLimit });

export const updateColumn = (projectId: string, boardId: string, columnId: string, patch: Record<string, unknown>) =>
  req<{ board: Board }>('PATCH', `${P(projectId)}/boards/${boardId}/columns/${columnId}`, patch);

export const removeColumn = (projectId: string, boardId: string, columnId: string) =>
  req<{ board: Board }>('DELETE', `${P(projectId)}/boards/${boardId}/columns/${columnId}`);

export const listTasks = (projectId: string, boardId: string, q: Record<string, string> = {}) =>
  req<{ tasks: Task[]; nextCursor?: string }>(
    'GET',
    `${P(projectId)}/boards/${boardId}/tasks?${new URLSearchParams(q).toString()}`,
  );

export const createTask = (projectId: string, boardId: string, body: Record<string, unknown>) =>
  req<{ task: Task }>('POST', `${P(projectId)}/boards/${boardId}/tasks`, body);

/**
 * The name a project's FIRST board gets when one is auto-created.
 *
 * It used to be the bare string "Board", at both call sites that auto-create. Reported from staging:
 * *"both projects have `Board` named board, not sure what it is"* — correct, and the switcher shows
 * only `board.name`, so two projects were indistinguishable by the one field on screen.
 *
 * Named after its project instead. The fallback keeps the old string for the case where the project
 * name is not to hand, which is better than inventing one.
 *
 * THIS FIXES NEW PROJECTS ONLY. Boards already called "Board" keep that name — renaming other
 * people's boards is not a display fix's business. Rename them from the board settings.
 */
export function defaultBoardName(projectName?: string | null): string {
  const n = (projectName ?? '').trim();
  return n ? `${n} board` : 'Board';
}
