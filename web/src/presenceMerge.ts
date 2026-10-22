import type { AgentSession } from './types';

/**
 * A presence frame carries the newest N live rows for the viewer, not their whole session list, so
 * a frame UPSERTS by `sessionKey` into what the screen already holds — newest first, nothing dropped.
 */
export function mergePresence(prev: AgentSession[] | undefined, frame: AgentSession[]): AgentSession[] {
  const byKey = new Map<string, AgentSession>();
  for (const s of prev ?? []) byKey.set(s.sessionKey, s);
  for (const s of frame) byKey.set(s.sessionKey, s);
  return [...byKey.values()].sort((a, b) => (a.lastSeenAt < b.lastSeenAt ? 1 : a.lastSeenAt > b.lastSeenAt ? -1 : 0));
}
