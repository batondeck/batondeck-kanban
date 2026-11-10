/**
 * The board keeps a pull-mode agent's presence current while it is open.
 *
 * An MCP session (an agent that polls claim_next each turn: Cursor, a claude.ai connector) is online
 * while its `lastSeenAt` is within `presenceOfflineAfterMs`, and only `/api/agents/sessions` carries
 * it. The stream's presence frame is the device directory's entries (CLI processes on this machine),
 * sent on open and on a directory change, never this list. The board fetched the list once: 75 s
 * later the header read "no agents online" though the agent had called the server seconds before, and
 * an agent that connected after the page loaded never appeared.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { Board } from '../screens/Board.js';
import { hydrateUiSettings } from '../settings.js';
import type { AgentSession } from '../types.js';

class FakeES {
  readyState = 0;
  addEventListener(): void {}
  close(): void {
    this.readyState = 2;
  }
}
const board = { id: 'b1', projectId: 'p1', name: 'Board', columns: [{ id: 'c1', name: 'To do', order: 1, status: 'TODO', wipLimit: null }] };
const mcp = (lastSeenAt: string): AgentSession => ({
  sessionKey: 'S-1', agentId: 'cursor-1', agentName: 'cursor-1', identityId: 'U-1', kind: 'api_token',
  firstSeenAt: '2026-10-30T00:00:00.000Z', lastSeenAt, sessionCount: 1, requestCount: 3, toolCounts: {}, lastDeliveredCursor: 0,
} as AgentSession);

let active = false; // has the agent called the server since the page loaded?
let listCalls = 0;

beforeEach(() => {
  listCalls = 0;
  active = false;
  // A short offline window, so the poll that follows it is quick to observe.
  hydrateUiSettings({ presenceOfflineAfterMs: 2000 });
  vi.stubGlobal('EventSource', FakeES);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (/\/agents\/sessions/.test(url)) {
        listCalls++;
        // Before: its last request was an hour ago (offline). After: it called the server just now.
        const lastSeenAt = new Date(Date.now() - (active ? 0 : 3_600_000)).toISOString();
        return { ok: true, status: 200, json: async () => ({ sessions: [mcp(lastSeenAt)] }) } as Response;
      }
      const routes: Array<[RegExp, unknown]> = [
        [/\/boards\/b1\/tasks\?/, { tasks: [] }],
        [/\/boards\/b1\/feed$/, { events: [] }],
        [/\/boards\/b1$/, { board, taskCounts: {} }],
        [/\/boards$/, { boards: [board] }],
      ];
      return { ok: true, status: 200, json: async () => routes.find(([re]) => re.test(url))?.[1] ?? {} } as Response;
    }),
  );
});

afterEach(() => {
  cleanup();
  hydrateUiSettings(undefined);
  vi.unstubAllGlobals();
});

describe('the board re-reads the agent sessions while it is open', () => {
  it('an agent that calls the server after the page loaded shows online without a reload', async () => {
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MemoryRouter initialEntries={['/projects/p1/boards/b1']}>
          <Routes>
            <Route path="/projects/:projectId/boards/:boardId" element={<Board />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    await waitFor(() => expect(screen.getByText('no agents online')).toBeInTheDocument());
    expect(listCalls).toBe(1);
    active = true; // the agent's next claim_next
    await waitFor(() => expect(screen.getByText('1 agent online')).toBeInTheDocument(), { timeout: 3000 });
    expect(listCalls).toBeGreaterThan(1);
  });
});
