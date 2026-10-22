/**
 * THE GAP THIS CLOSES: "no browser has rendered any of this."
 *
 * Every deliverable in the streaming lane has carried that caveat. The gateway was proven to write
 * correct SSE frames to a real socket (`gateway/ui-backend/test/sse-last-hop.test.ts`), and `api.ts` was
 * proven to parse them — but NOTHING covered the last step the user actually sees: a frame arriving and
 * the UI RE-RENDERING from it. That step is why the polls were deleted, so leaving it untested meant the
 * whole feature rested on an assumption.
 *
 * It is deliberately a rendering test and not a click-through against staging. A one-off browser check
 * proves today and guards nothing; this runs in the gate forever. What it cannot see is real network
 * behaviour and real CSS — a live smoke test is still worth doing once, and is NOT replaced by this.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { NotificationsNavLink } from '../screens/Notifications.js';

/** A fake EventSource that hands the test the server's side of the connection. */
class FakeES {
  static last: FakeES | undefined;
  listeners = new Map<string, Array<(e: MessageEvent<string>) => void>>();
  readyState = 1;
  constructor(public url: string) {
    FakeES.last = this;
  }
  addEventListener(type: string, fn: (e: MessageEvent<string>) => void): void {
    const l = this.listeners.get(type) ?? [];
    l.push(fn);
    this.listeners.set(type, l);
  }
  close(): void {
    this.readyState = 2;
  }
  /** Push one SSE frame, exactly as the gateway would. */
  emit(type: string, data: unknown): void {
    for (const fn of this.listeners.get(type) ?? []) fn(new MessageEvent(type, { data: JSON.stringify(data) }));
  }
}

function mount(initialUnread: number) {
  vi.stubGlobal('EventSource', FakeES);
  vi.stubGlobal(
    'fetch',
    vi.fn(async () =>
      new Response(JSON.stringify({ notifications: [], unread: initialUnread }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    ),
  );
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <NotificationsNavLink />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  FakeES.last = undefined;
});

describe('the SPA re-renders from a stream frame (the last untested step)', () => {
  it('opens the IDENTITY-keyed stream, not a board stream — this screen has no board', async () => {
    mount(0);
    await waitFor(() => expect(FakeES.last).toBeDefined());
    expect(FakeES.last!.url, 'the nav badge must use /api/stream; a board-keyed URL would 404 here').toBe('/api/stream');
  });

  // *** THE ONE THAT CLOSES THE GAP. *** A pushed frame must change what the user sees, with no refetch.
  it('POSITIVE CONTROL: a `notifications` frame updates the unread badge', async () => {
    mount(0);
    await waitFor(() => expect(FakeES.last).toBeDefined());
    // Badge is hidden at zero — that is the pre-state the assertion below moves off.
    expect(screen.queryByText('7')).toBeNull();

    FakeES.last!.emit('notifications', { notifications: [], unread: 7 });

    await waitFor(() => expect(screen.getByText('7')).toBeInTheDocument());
  });

  it('a later frame supersedes an earlier one, so the badge tracks the stream', async () => {
    mount(0);
    await waitFor(() => expect(FakeES.last).toBeDefined());
    FakeES.last!.emit('notifications', { notifications: [], unread: 3 });
    await waitFor(() => expect(screen.getByText('3')).toBeInTheDocument());
    FakeES.last!.emit('notifications', { notifications: [], unread: 0 });
    await waitFor(() => expect(screen.queryByText('3')).toBeNull());
  });

  it('the >99 cap still applies to a streamed value, not just a fetched one', async () => {
    mount(0);
    await waitFor(() => expect(FakeES.last).toBeDefined());
    FakeES.last!.emit('notifications', { notifications: [], unread: 250 });
    await waitFor(() => expect(screen.getByText('99+')).toBeInTheDocument());
  });

  // A malformed frame must never wedge the stream or crash the nav — the nav is mounted app-wide.
  it('a malformed frame is survived, and the next good frame still lands', async () => {
    mount(0);
    await waitFor(() => expect(FakeES.last).toBeDefined());
    for (const fn of FakeES.last!.listeners.get('notifications') ?? []) {
      fn(new MessageEvent('notifications', { data: '{not json' }));
    }
    FakeES.last!.emit('notifications', { notifications: [], unread: 5 });
    await waitFor(() => expect(screen.getByText('5')).toBeInTheDocument());
  });
});
