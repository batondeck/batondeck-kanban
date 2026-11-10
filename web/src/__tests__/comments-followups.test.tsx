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
import { readAllPages } from '../api/pages.js';

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
    wrap(<Comments projectId="P-1" taskId="T-1" mentionCandidates={[]} canComment />);
    expect(await screen.findByText('First pass is in')).toBeInTheDocument();
    expect(screen.getByText('Reply from will')).toBeInTheDocument();
    // The first read of the thread (the names read, P-314, is `GET /api/projects/P-1`).
    expect(calls.find((c) => c.startsWith(`GET ${base}/`))).toBe(`GET ${base}/comments?limit=200`);
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
    wrap(<FollowUps projectId="P-1" task={task} identity="u-will" canFollowUp canAck />);
    expect(await screen.findByText('Add a regression test')).toBeInTheDocument();
    expect(screen.getByText('Rename the flag')).toBeInTheDocument();
    expect(screen.getAllByText('→ u-will')).toHaveLength(2);
    expect(screen.getByText('↩ reopened')).toBeInTheDocument();
    expect(screen.getByText(/back to TODO/)).toBeInTheDocument();
    expect(calls.find((c) => c.startsWith(`GET ${base}/`))).toBe(`GET ${base}/follow-ups?limit=200`);
  });

  it('a holder below ack_follow_up\'s tier sees Ack closed, its reason shown and describing it, and a click sends nothing (P-444)', async () => {
    stub({
      [`GET ${base}/follow-ups`]: {
        followUps: [{ id: 'FU-1', taskId: 'T-1', projectId: 'P-1', body: 'Add a regression test', author: 'u-mona', target: 'u-will', status: 'open', reopened: false, ts }],
      },
    });
    const why = 'A viewer cannot acknowledge follow-ups.';
    wrap(<FollowUps projectId="P-1" task={task} identity="u-will" canFollowUp={false} canAck={false} ackClosedWhy={why} />);
    const ack = await screen.findByRole('button', { name: 'Ack' });
    expect(ack).toBeDisabled();
    // The title alone would give this description too: the describedby link and the visible text are
    // what P-444 adds.
    expect(ack).toHaveAccessibleDescription(why);
    expect(ack).toHaveAttribute('aria-describedby', 'ack-closed-FU-1');
    expect(screen.getByText(why)).toBeVisible();
    fireEvent.click(ack);
    expect(calls.filter((c) => !c.startsWith('GET '))).toEqual([]);
  });

  it('while the role loads, Ack is closed with a title only: no visible reason and no description link', async () => {
    stub({
      [`GET ${base}/follow-ups`]: {
        followUps: [{ id: 'FU-1', taskId: 'T-1', projectId: 'P-1', body: 'Add a regression test', author: 'u-mona', target: 'u-will', status: 'open', reopened: false, ts }],
      },
    });
    wrap(<FollowUps projectId="P-1" task={task} identity="u-will" canFollowUp={false} canAck={false} />);
    const ack = await screen.findByRole('button', { name: 'Ack' });
    expect(ack).toBeDisabled();
    expect(ack).toHaveAttribute('title', 'Checking your role');
    expect(ack).not.toHaveAttribute('aria-describedby');
    expect(screen.queryByText('Checking your role')).toBeNull();
  });

  it('POSITIVE CONTROL: a holder who may ack sees no reason, and Ack posts', async () => {
    stub({
      [`GET ${base}/follow-ups`]: {
        followUps: [{ id: 'FU-1', taskId: 'T-1', projectId: 'P-1', body: 'Add a regression test', author: 'u-mona', target: 'u-will', status: 'open', reopened: false, ts }],
      },
    });
    wrap(<FollowUps projectId="P-1" task={task} identity="u-will" canFollowUp canAck />);
    const ack = await screen.findByRole('button', { name: 'Ack' });
    expect(ack).toBeEnabled();
    expect(ack).not.toHaveAttribute('aria-describedby');
    fireEvent.click(ack);
    await vi.waitFor(() => expect(calls.some((c) => c.startsWith('POST ') && c.includes('FU-1'))).toBe(true));
  });
});

/**
 * The thread is read to its end. The routes answer one page (100 by default, 200 at most), oldest
 * first, with a `nextCursor`; the panels read only the first page, so on a ticket past 100 comments
 * (or follow-ups) the newest never appeared, a comment just posted among them.
 */
describe('a long thread is read to its end', () => {
  const N = 250;
  function paged(kind: 'comments' | 'follow-ups'): void {
    calls = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), 'http://x');
      calls.push(`GET ${url.pathname}${url.search}`);
      if (url.pathname !== `${base}/${kind}`) return { ok: true, status: 200, json: async () => ({}) } as Response;
      const limit = Math.min(Number(url.searchParams.get('limit') || 100), 200);
      const from = Number(url.searchParams.get('cursor') || 0);
      const rows = Array.from({ length: Math.max(0, Math.min(limit, N - from)) }, (_, i) => from + i + 1).map((n) =>
        kind === 'comments'
          ? { id: `CM-${n}`, taskId: 'T-1', author: 'agent-ada', body: `comment ${n}`, parentCommentId: null, mentions: [], ts }
          : { id: `FU-${n}`, taskId: 'T-1', author: 'u-will', target: 'agent-ada', body: `follow-up ${n}`, status: 'acked', reopened: false, ts });
      const next = from + rows.length < N ? String(from + rows.length) : undefined;
      const key = kind === 'comments' ? 'comments' : 'followUps';
      return { ok: true, status: 200, json: async () => ({ [key]: rows, ...(next ? { nextCursor: next } : {}) }) } as Response;
    }));
  }

  it('comments: the newest past the first page appear', async () => {
    paged('comments');
    wrap(<Comments projectId="P-1" taskId="T-1" mentionCandidates={[]} canComment />);
    expect(await screen.findByText('comment 250')).toBeInTheDocument();
    expect(screen.getByText('comment 1')).toBeInTheDocument();
  });

  it('follow-ups: the newest past the first page appear', async () => {
    paged('follow-ups');
    const t = { id: 'T-1', status: 'IN_PROGRESS', claim: null, assignee: null } as unknown as Task;
    wrap(<FollowUps projectId="P-1" task={t} identity="u-will" canFollowUp canAck />);
    expect(await screen.findByText('follow-up 250')).toBeInTheDocument();
  });
});

describe('readAllPages', () => {
  it('follows the cursor to the end, and says when its bound cut the list', async () => {
    const page = (cursor: string | undefined) => {
      const n = Number(cursor ?? 0);
      return Promise.resolve({ rows: [n], ...(n < 4 ? { nextCursor: String(n + 1) } : {}) });
    };
    expect(await readAllPages(page)).toEqual({ rows: [0, 1, 2, 3, 4], truncated: false });
    expect(await readAllPages(page, 2)).toEqual({ rows: [0, 1], truncated: true });
  });
});
