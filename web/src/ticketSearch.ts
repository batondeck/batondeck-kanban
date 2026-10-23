/**
 * Jira-ish ticket query parsing for the Activity page's search bar. A query mixes free text with
 * `key:value` filters — `status:in-progress priority:high,urgent label:web assignee:claude fix auth`.
 * Free text is ranked server-side (search_tasks); filters narrow client-side.
 */
import type { Status, Task } from './types.js';

export interface TicketQuery {
  text: string;
  status: Status[];
  priority: string[];
  assignee: string[];
  label: string[];
}

const STATUS_ALIAS: Record<string, Status> = {
  todo: 'TODO',
  ready: 'TODO',
  in_progress: 'IN_PROGRESS',
  inprogress: 'IN_PROGRESS',
  wip: 'IN_PROGRESS',
  blocked: 'BLOCKED',
  review: 'REVIEW',
  done: 'DONE',
};

export function parseTicketQuery(raw: string, identity?: string | null): TicketQuery {
  const out: TicketQuery = { text: '', status: [], priority: [], assignee: [], label: [] };
  const words: string[] = [];
  for (const tok of raw.trim().split(/\s+/).filter(Boolean)) {
    const m = tok.match(/^(status|priority|assignee|label):(.+)$/i);
    if (!m) {
      words.push(tok);
      continue;
    }
    const key = m[1]!.toLowerCase();
    const values = m[2]!.split(',').filter(Boolean);
    for (const v of values) {
      if (key === 'status') {
        // Unknown value → keep it verbatim; it then matches NOTHING (parity with priority:bogus),
        // instead of silently vanishing and turning the query into select-all.
        out.status.push(STATUS_ALIAS[v.toLowerCase().replace(/-/g, '_')] ?? (v.toUpperCase() as Status));
      } else if (key === 'priority') out.priority.push(v.toLowerCase());
      else if (key === 'assignee') out.assignee.push(v.toLowerCase() === 'me' && identity ? identity.toLowerCase() : v.toLowerCase());
      else out.label.push(v.toLowerCase());
    }
  }
  out.text = words.join(' ');
  return out;
}

export function hasFilters(q: TicketQuery): boolean {
  return q.status.length + q.priority.length + q.assignee.length + q.label.length > 0;
}

/** Apply the parsed filters (not the free text) to one task. */
export function matchesFilters(t: Task, q: TicketQuery): boolean {
  if (q.status.length && !q.status.includes(t.status)) return false;
  if (q.priority.length && !q.priority.includes(t.priority)) return false;
  if (q.assignee.length) {
    // Substring match: `assignee:claude` should hit "claude-pixpoc-main" and SA emails alike.
    const who = (t.assignee ?? t.claim?.agent ?? '').toLowerCase();
    if (!q.assignee.some((a) => who.includes(a))) return false;
  }
  if (q.label.length) {
    const labels = t.labels.map((l) => l.toLowerCase());
    if (!q.label.every((want) => labels.some((l) => l.includes(want)))) return false;
  }
  return true;
}

/** Client-side free-text match, for filter-only mode's optional trailing words. */
export function matchesText(t: Task, text: string): boolean {
  if (!text) return true;
  const hay = `${t.id} ${t.title} ${t.description} ${t.labels.join(' ')}`.toLowerCase();
  return text
    .toLowerCase()
    .split(/\s+/)
    .every((w) => hay.includes(w));
}

/** One-line plain-text preview of a markdown description for search-result rows. */
export function descriptionSnippet(md: string, max = 110): string {
  const flat = md
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1') // links/images → their text
    .replace(/[#*_`>~|]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
  return flat.length <= max ? flat : `${flat.slice(0, max).trimEnd()}…`;
}

/** Does the free text look like a ticket id (T-…)? Server-side search doesn't index ids, so id-like
 * queries also scan the local task list. */
export function looksLikeTaskId(text: string): boolean {
  return /^t-[\w-]+$/i.test(text.trim());
}

/**
 * Compose the final result list from the two sources: server-ranked hits (free-text mode) and the
 * locally-listed tasks (filter-only mode, and id-lookup augmentation). Pure — unit-testable.
 */
export function composeResults(q: TicketQuery, serverTasks: Task[] | undefined, allTasks: Task[] | undefined, cap: number): Task[] {
  let base: Task[];
  if (q.text) {
    base = serverTasks ?? [];
    if (looksLikeTaskId(q.text)) {
      // Prepend direct id/text matches from the local list (deduped) — `T-42` must find T-42.
      const seen = new Set(base.map((t) => t.id));
      base = [...(allTasks ?? []).filter((t) => matchesText(t, q.text) && !seen.has(t.id)), ...base];
    }
  } else {
    base = [...(allTasks ?? [])].sort((a, b) => new Date(b.updatedAt).getTime() - new Date(a.updatedAt).getTime());
  }
  return base.filter((t) => matchesFilters(t, q)).slice(0, cap);
}
