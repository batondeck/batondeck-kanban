/**
 * The shared socket streams ONE board, and a subscriber naming another board is told so.
 *
 * `/api/stream?projectId&boardId` carries that board's changes only (internal/api/stream.go drops
 * every other board's), and `desiredUrl()` scopes the one socket to the first subscriber naming a
 * board. A second board's subscriber used to get the FIRST board's `board` frames written into its
 * own cache keys and its `updated` nudges, and was told the stream was live, so it never polled.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import * as streamApi from '../api/stream.js';

class FakeES {
  static instances: FakeES[] = [];
  readyState = 0;
  listeners = new Map<string, Array<(e: { data?: string }) => void>>();
  constructor(public url: string) {
    FakeES.instances.push(this);
  }
  addEventListener(t: string, fn: (e: { data?: string }) => void): void {
    this.listeners.set(t, [...(this.listeners.get(t) ?? []), fn]);
  }
  close(): void {
    this.readyState = 2;
  }
  emit(t: string, data?: unknown): void {
    for (const fn of this.listeners.get(t) ?? []) fn(data === undefined ? {} : { data: JSON.stringify(data) });
  }
  open(): void {
    this.readyState = 1;
    this.emit('open');
  }
}

afterEach(() => {
  FakeES.instances = [];
  vi.unstubAllGlobals();
});

const frame = (boardId: string) => ({ board: { board: { id: boardId }, taskCounts: {} }, tasks: { tasks: [] }, feed: { events: [] } });

describe('one board per socket', () => {
  it('a second board subscriber gets none of the first board’s frames and is told it is not live', () => {
    vi.stubGlobal('EventSource', FakeES);
    const a = { onUpdate: vi.fn(), onLive: vi.fn(), onBoard: vi.fn(), onResync: vi.fn() };
    const b = { onUpdate: vi.fn(), onLive: vi.fn(), onBoard: vi.fn(), onResync: vi.fn() };
    const stopA = streamApi.stream('p1', 'B-1', a.onUpdate, a.onLive, undefined, a.onBoard, a.onResync);
    const stopB = streamApi.stream('p1', 'B-2', b.onUpdate, b.onLive, undefined, b.onBoard, b.onResync);
    try {
      expect(FakeES.instances).toHaveLength(1);
      const es = FakeES.instances[0]!;
      expect(es.url).toContain('boardId=B-1');
      es.open();
      es.emit('board', frame('B-1'));
      es.emit('updated', { uri: 'batondeck://p1/board/B-1/feed', count: 1 });
      es.emit('resync', {});
      expect(a.onBoard).toHaveBeenCalledTimes(1);
      expect(a.onUpdate).toHaveBeenCalledTimes(1);
      expect(a.onResync).toHaveBeenCalledTimes(1);
      expect(a.onLive).toHaveBeenLastCalledWith(true);
      expect(b.onBoard, 'B-1’s snapshot written into B-2’s cache').not.toHaveBeenCalled();
      expect(b.onUpdate).not.toHaveBeenCalled();
      expect(b.onResync).not.toHaveBeenCalled();
      expect(b.onLive, 'B-2 must poll: its board is not on this socket').toHaveBeenCalledWith(false);
      expect(b.onLive).not.toHaveBeenCalledWith(true);
    } finally {
      stopB();
      stopA();
    }
  });

  it('when the first board leaves, the socket is rebuilt for the second, which is then live and fed', () => {
    vi.stubGlobal('EventSource', FakeES);
    const a = { onUpdate: vi.fn(), onLive: vi.fn(), onBoard: vi.fn() };
    const b = { onUpdate: vi.fn(), onLive: vi.fn(), onBoard: vi.fn() };
    const stopA = streamApi.stream('p1', 'B-1', a.onUpdate, a.onLive, undefined, a.onBoard);
    const stopB = streamApi.stream('p1', 'B-2', b.onUpdate, b.onLive, undefined, b.onBoard);
    try {
      FakeES.instances[0]!.open();
      stopA();
      const es = FakeES.instances.at(-1)!;
      expect(es.url).toContain('boardId=B-2');
      es.open();
      expect(b.onLive).toHaveBeenLastCalledWith(true);
      es.emit('board', frame('B-2'));
      expect(b.onBoard).toHaveBeenCalledTimes(1);
    } finally {
      stopB();
    }
  });

  it('CONTROL: two subscribers of the SAME board share the socket and both get its frames', () => {
    vi.stubGlobal('EventSource', FakeES);
    const a = vi.fn();
    const b = vi.fn();
    const stopA = streamApi.stream('p1', 'B-1', vi.fn(), undefined, undefined, a);
    const stopB = streamApi.stream('p1', 'B-1', vi.fn(), undefined, undefined, b);
    try {
      const es = FakeES.instances.at(-1)!;
      es.open();
      es.emit('board', frame('B-1'));
      expect(a).toHaveBeenCalledTimes(1);
      expect(b).toHaveBeenCalledTimes(1);
    } finally {
      stopB();
      stopA();
    }
  });

  it('CONTROL: a subscriber with no board still gets presence and the socket’s liveness', () => {
    vi.stubGlobal('EventSource', FakeES);
    const onPresence = vi.fn();
    const onLive = vi.fn();
    const stopU = streamApi.userStream({ onPresence, onLive });
    const stopA = streamApi.stream('p1', 'B-1', vi.fn());
    try {
      const es = FakeES.instances.at(-1)!;
      es.open();
      es.emit('presence', { sessions: [] });
      expect(onPresence).toHaveBeenCalledTimes(1);
      expect(onLive).toHaveBeenLastCalledWith(true);
    } finally {
      stopA();
      stopU();
    }
  });
});
