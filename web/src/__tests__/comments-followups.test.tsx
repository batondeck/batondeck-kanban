/**
 * The drawer's Comments and FollowUps sections (R2.1) render the rows the BFF returns, from the
 * routes internal/api/{comments,followups}.go serve, and post to them. Fixture rows only: the real
 * surface is observed in the browser against the binary.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { Comments } from '../screens/Comments.js';
import { FollowUps } from '../screens/FollowUps.js';
import type { Task } from '../types.js';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

let calls: string[] = [];

function stub(routes: Record<string, unknown>): void {
  calls = [];
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input);
    calls.push(`${init?.method ?? 'GET'} ${path}${init?.body ? ' ' + String(init.body) : ''}`);
    const body = routes[`${init?.method ?? 'GET'} ${path.split('?')[0]}`] ?? {};
    return { ok: true, status: 200, json: async () => body } as Response;
  }));
}

function wrap(node: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

const base = '/api/projects/P-1/tasks/T-1';
const ts = '2026-10-24T08:00:00.000Z';

describe('Comments', () => {
  it('renders the thread the BFF returns, a reply under its parent, and posts a new comment', async () => {
    stub({
      [`GET ${base}/comments`]: {
        comments: [
          { id: 'CM-1', taskId: 'T-1', author: 'agent-ada', body: 'First pass is in', parentCommentId: null, mentions: [], ts },
          { id: 'CM-2', taskId: 'T-1', author: 'u-will', body: 'Reply from will', parentCommentId: 'CM-1', mentions: [], ts },
        ],
      },
      [`POST ${base}/comments`]: { comment: { id: 'CM-3', taskId: 'T-1', author: 'u-will', body: 'New', parentCommentId: null, mentions: [], ts } },
    });
    wrap(<Comments projectId="P-1" taskId="T-1" mentionCandidates={[]} />);
    expect(await screen.findByText('First pass is in')).toBeInTheDocument();
    expect(screen.getByText('Reply from will')).toBeInTheDocument();
    expect(calls[0]).toBe(`GET ${base}/comments`);
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'New' } });
    fireEvent.click(screen.getByRole('button', { name: /comment/i }));
    await vi.waitFor(() => expect(calls.some((c) => c === `POST ${base}/comments {"body":"New"}`)).toBe(true));
  });
});

describe('FollowUps', () => {
  const task = { id: 'T-1', status: 'DONE', assignee: 'u-will', claim: null } as unknown as Task;
  it('renders open and acked directives with their target, and offers the reopen on a DONE task', async () => {
    stub({
      [`GET ${base}/follow-ups`]: {
        followUps: [
          { id: 'FU-1', taskId: 'T-1', projectId: 'P-1', body: 'Add a regression test', author: 'u-mona', target: 'u-will', status: 'open', reopened: true, ts },
          { id: 'FU-2', taskId: 'T-1', projectId: 'P-1', body: 'Rename the flag', author: 'u-mona', target: 'u-will', status: 'acked', reopened: false, ts },
        ],
      },
    });
    wrap(<FollowUps projectId="P-1" task={task} identity="u-will" />);
    expect(await screen.findByText('Add a regression test')).toBeInTheDocument();
    expect(screen.getByText('Rename the flag')).toBeInTheDocument();
    expect(screen.getAllByText('→ u-will')).toHaveLength(2);
    expect(screen.getByText('↩ reopened')).toBeInTheDocument();
    expect(screen.getByText(/back to TODO/)).toBeInTheDocument();
    expect(calls[0]).toBe(`GET ${base}/follow-ups`);
  });
});
