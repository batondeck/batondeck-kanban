/**
 * The lease ids this browser's claims were given, per signed-in identity and ticket.
 *
 * WHY THIS EXISTS. Only the claim's own answer carries the lease id: `task.claim` (on the task, the
 * board's list and the stream) names the owner and the expiry, never the id
 * (internal/service/contract_tasks.go). Complete, Block, Hand off and Release all need it. Kept in
 * the ticket hook's state, it died with the drawer (keyed per task: closing it, j/k, a reload), and
 * the claim's holder lost every outcome button until the lease lapsed and the reap counted a failed
 * attempt against the task.
 *
 * localStorage, so a reload or another tab of the same browser keeps it; a Map beside it, so a
 * browser that refuses storage (private mode, blocked site data) still keeps it while the page lives.
 * Storage is read on every recall and a miss is never remembered, so a tab that showed the ticket
 * before another tab claimed it picks the lease up (`onLeaseStorage` re-renders it when that tab
 * writes). An entry is only a hint: the ticket hook offers it only while the server says the live
 * claim is this identity's with no agent named, and the server checks it on every call.
 */
export interface LeaseEntry {
  leaseId: string;
  /** When the claim answered (ms), so a read of the task older than the claim does not drop it. */
  at: number;
}

const PREFIX = 'batondeck.lease:';
/** The leases storage refused to hold, for this page only. */
const mem = new Map<string, LeaseEntry>();
const keyOf = (identity: string, projectId: string, taskId: string): string => PREFIX + JSON.stringify([identity, projectId, taskId]);

/** The storage key of one identity's lease on one ticket (another tab writes the same key). */
export const leaseStorageKey = keyOf;

export function recallLease(identity: string, projectId: string, taskId: string): LeaseEntry | null {
  const k = keyOf(identity, projectId, taskId);
  try {
    const raw = localStorage.getItem(k);
    const v = raw ? (JSON.parse(raw) as Partial<LeaseEntry>) : null;
    if (v && typeof v.leaseId === 'string' && typeof v.at === 'number') return { leaseId: v.leaseId, at: v.at };
  } catch {
    // storage refused or a corrupt entry: the Map's copy, if any
  }
  return mem.get(k) ?? null;
}

export function rememberLease(identity: string, projectId: string, taskId: string, leaseId: string): void {
  const k = keyOf(identity, projectId, taskId);
  const entry: LeaseEntry = { leaseId, at: Date.now() };
  try {
    localStorage.setItem(k, JSON.stringify(entry));
    mem.delete(k);
  } catch {
    mem.set(k, entry); // this page still holds it
  }
}

export function forgetLease(identity: string, projectId: string, taskId: string): void {
  const k = keyOf(identity, projectId, taskId);
  mem.delete(k);
  try {
    localStorage.removeItem(k);
  } catch {
    // nothing stored
  }
}

/** Calls cb when another tab of this browser stores or drops a lease; returns the unsubscribe. */
export function onLeaseStorage(cb: () => void): () => void {
  const on = (e: StorageEvent): void => {
    if (e.key === null || e.key.startsWith(PREFIX)) cb();
  };
  window.addEventListener('storage', on);
  return () => window.removeEventListener('storage', on);
}

/** Test seam: drop the in-page copies (the leases storage refused), as a reload would. */
export function resetLeaseMemory(): void {
  mem.clear();
}
