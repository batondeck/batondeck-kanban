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
 * An entry is only a hint: the ticket hook offers it only while the server says the live claim is
 * this identity's with no agent named, and the server checks it on every call.
 */
export interface LeaseEntry {
  leaseId: string;
  /** When the claim answered (ms), so a read of the task older than the claim does not drop it. */
  at: number;
}

const PREFIX = 'batondeck.lease:';
const mem = new Map<string, LeaseEntry | null>();
const keyOf = (identity: string, projectId: string, taskId: string): string => PREFIX + JSON.stringify([identity, projectId, taskId]);

export function recallLease(identity: string, projectId: string, taskId: string): LeaseEntry | null {
  const k = keyOf(identity, projectId, taskId);
  if (mem.has(k)) return mem.get(k) ?? null;
  let entry: LeaseEntry | null = null;
  try {
    const raw = localStorage.getItem(k);
    const v = raw ? (JSON.parse(raw) as Partial<LeaseEntry>) : null;
    if (v && typeof v.leaseId === 'string' && typeof v.at === 'number') entry = { leaseId: v.leaseId, at: v.at };
  } catch {
    // storage refused or a corrupt entry: nothing remembered
  }
  mem.set(k, entry);
  return entry;
}

export function rememberLease(identity: string, projectId: string, taskId: string, leaseId: string): void {
  const k = keyOf(identity, projectId, taskId);
  const entry: LeaseEntry = { leaseId, at: Date.now() };
  mem.set(k, entry);
  try {
    localStorage.setItem(k, JSON.stringify(entry));
  } catch {
    // the Map above still holds it for this page
  }
}

export function forgetLease(identity: string, projectId: string, taskId: string): void {
  const k = keyOf(identity, projectId, taskId);
  mem.set(k, null);
  try {
    localStorage.removeItem(k);
  } catch {
    // nothing stored
  }
}

/** Test seam: drop the in-page copy so the next read goes to storage, as a reload would. */
export function resetLeaseMemory(): void {
  mem.clear();
}
