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

/**
 * The device directory's frame is its WHOLE live list (api/stream.ts, `device:<pid>` rows), so its rows
 * are replaced, not upserted: a process that left the directory leaves the list. Other rows stay.
 */
export function replaceDevicePresence(prev: AgentSession[] | undefined, frame: AgentSession[]): AgentSession[] {
  return mergePresence((prev ?? []).filter((s) => !s.sessionKey.startsWith('device:')), frame);
}

/**
 * A REST answer for a key presence frames also write (`/api/agents/sessions` never lists `device:`
 * rows): the list as answered, with the device rows the cache holds kept. A frame is sent on open and
 * then only when the directory changes, so a list landing after one would otherwise drop a live
 * session until the next change (P-197). Pass the cache as it is when the answer lands, not when the
 * request left, so a frame that arrived meanwhile is kept too.
 */
export function keepCachedDevices(fresh: AgentSession[], cached: AgentSession[] | undefined): AgentSession[] {
  return mergePresence(fresh, (cached ?? []).filter((s) => s.sessionKey.startsWith('device:')));
}
