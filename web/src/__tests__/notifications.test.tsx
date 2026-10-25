/**
 * Notifications screen (R2.3): renders the rows GET /api/notifications answers, newest first with the
 * unread ones marked, and "Mark all read" posts {all:true} to /api/notifications/read.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { Notifications } from '../screens/Notifications.js';
import type { Notification } from '../types.js';

const ROWS: Notification[] = [
  { id: 'N-3', identityId: 'u-grace', projectId: 'p1', boardId: 'b1', type: 'handoff', taskId: 'T-9', actor: 'u-cy', data: { toAgent: 'grace@x.test' }, read: true, ts: new Date().toISOString() },
  { id: 'N-2', identityId: 'u-grace', projectId: 'p1', boardId: 'b1', type: 'review_request', taskId: 'T-7', actor: 'u-ada', data: {}, read: false, ts: new Date().toISOString() },
  { id: 'N-1', identityId: 'u-grace', projectId: 'p1', boardId: 'b1', type: 'mention', taskId: 'T-3', actor: 'u-bob', data: { commentId: 'C-1' }, read: true, ts: new Date(Date.now() - 3_600_000).toISOString() },
];

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('Notifications', () => {
  it('renders the feed rows and marks all read', async () => {
    const posts: { url: string; body: unknown }[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'POST') {
        posts.push({ url: String(input), body: JSON.parse(String(init.body)) });
        return { ok: true, status: 200, json: async () => ({ updated: 1 }) } as Response;
      }
      return { ok: true, status: 200, json: async () => ({ notifications: ROWS, unread: 1 }) } as Response;
    }));
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <Notifications />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(await screen.findByText('u-ada asked you to review a ticket')).toBeInTheDocument();
    expect(screen.getByText('u-bob mentioned you in a comment')).toBeInTheDocument();
    expect(screen.getByText('u-cy handed you T-9')).toBeInTheDocument();
    expect(screen.getByText(/^T-7 · /)).toBeInTheDocument();
    expect(screen.getAllByText('new')).toHaveLength(1);
    fireEvent.click(screen.getByText('Mark all read'));
    await waitFor(() => expect(posts).toHaveLength(1));
    expect(posts[0]).toEqual({ url: '/api/notifications/read', body: { all: true } });
  });

  it('shows the empty state when the feed is empty', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ notifications: [], unread: 0 }) }) as Response));
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <Notifications />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(await screen.findByText("You're all caught up.")).toBeInTheDocument();
    expect(screen.getByText('Mark all read')).toBeDisabled();
  });
});
