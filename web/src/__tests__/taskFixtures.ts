/**
 * The ONE ticket fixture, shared by the drawer and the page tests.
 *
 * Extracted from `taskdrawer.test.tsx` when TaskPage gained coverage: two copies of a routing fake
 * is how two surfaces end up asserted against different backends, which is exactly the class of
 * drift the shared `TicketBody`/`TicketActions` components exist to remove. If the page and the
 * drawer are meant to render the same record, they must be fed the same record.
 */
import { vi } from 'vitest';
import type { Task } from '../types.js';

export const now = Date.parse('2026-08-27T12:00:00.000Z');

export const task: Task = {
  id: 'T-1',
  projectId: 'p1',
  boardId: 'b1',
  columnId: 'c1',
  title: 'Ship the parser',
  description: 'Fix the **tokenizer** first — see [the spec](https://example.com/spec)',
  status: 'TODO',
  priority: 'high',
  assignee: null,
  labels: ['backend'],
  requiredCapabilities: ['python'],
  estimate: 3,
  dueAt: new Date(now - 86400000).toISOString(), // yesterday → overdue
  customFields: {},
  parentTaskId: null,
  order: 'a0',
  blockedBy: ['T-9'],
  blocks: [],
  contextSummary: '',
  version: 3,
  claim: null,
  createdBy: 'alice@example.com',
  createdAt: '2026-08-01T00:00:00.000Z',
  updatedAt: new Date(now - 60000).toISOString(),
  modelHint: { model: 'claude-opus-4-8', effort: 'high', rationale: 'deep refactor across modules' },
};

export const blocker: Task = { ...task, id: 'T-9', title: 'Upstream schema', status: 'IN_PROGRESS', blockedBy: [], dueAt: null };
export const subtask: Task = { ...task, id: 'T-2', title: 'Write tests', status: 'IN_PROGRESS', parentTaskId: 'T-1', blockedBy: [], dueAt: null };

/** Route the drawer's gateway reads by URL; unmatched paths get an empty-ish 200. */
export function routeFetch(overrides: Record<string, unknown> = {}): typeof fetch {
  const routes: Array<[RegExp, unknown]> = [
    [/\/tasks\/T-1$/, { task }],
    [/\/tasks\/T-1\/context$/, {
      summary: '', fields: {}, items: [], memory: [],
      deps: { blockedBy: ['T-9'], blocks: [], edges: [{ id: 'e1', fromTaskId: 'T-9', toTaskId: 'T-1', type: 'blocks' }] },
    }],
    [/\/tasks\/T-1\/subtasks$/, { tasks: [subtask] }],
    [/\/tasks\/T-1\/memory$/, { entries: [] }],
    [/\/tasks\/T-1\/attachments$/, { attachments: [] }],
    [/\/tasks\/T-1\/comments(\?|$)/, { comments: [] }],
    [/\/tasks\/T-1\/follow-ups(\?|$)/, { followUps: [] }],
    [/\/tasks\/T-1\/runs$/, { runs: [] }],
    [/\/boards\/b1\/feed(\?|$)/, { events: [] }],
    [/\/boards\/b1\/tasks\?/, { tasks: [task, blocker, subtask] }],
    [/\/boards\/b1$/, { board: { id: 'b1', projectId: 'p1', name: 'Board', columns: [{ id: 'c1', name: 'To do', order: 1, status: 'TODO', wipLimit: null }, { id: 'c2', name: 'Building', order: 2, status: 'IN_PROGRESS', wipLimit: null }] }, taskCounts: {} }],
    [/\/projects\/p1$/, { project: { id: 'p1', name: 'P1', members: { 'alice@example.com': 'admin' }, settings: { reviewEnabled: true, quotaEnabled: false, txnCap: null } } }],
    [/\/agents\/sessions/, { sessions: [] }],
    // The session the project fixture's member row names, so a surface under an AuthProvider reads
    // alice's role (admin) rather than failing closed on no identity (P-209).
    [/\/api\/session$/, { identity: 'alice@example.com', csrfToken: null }],
  ];
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    for (const [re, body] of Object.entries(overrides)) {
      if (new RegExp(re).test(url)) {
        const b = body as { status?: number; json?: unknown };
        return { ok: (b.status ?? 200) < 400, status: b.status ?? 200, json: async () => b.json ?? {} } as Response;
      }
    }
    const hit = routes.find(([re]) => re.test(url));
    return { ok: true, status: 200, json: async () => hit?.[1] ?? {} } as Response;
  }) as unknown as typeof fetch;
}

