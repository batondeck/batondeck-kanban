/**
 * Every row of a paged list route, following `nextCursor` to its end.
 *
 * The list routes answer one page at a time (comments and follow-ups: 100 by default, 200 at most,
 * oldest first, internal/store/comments.go and followups.go), so a surface that read only the first
 * page lost everything after it: on a ticket past 100 comments the newest never appeared. Bounded by
 * `maxPages`: past it the rest is left unread and `truncated` says so, so the surface can say the
 * list is cut instead of implying it is whole.
 */
export const PAGE_LIMIT = 200;
export const MAX_PAGES = 25;

export async function readAllPages<T>(
  page: (cursor: string | undefined) => Promise<{ rows: T[]; nextCursor?: string | undefined }>,
  maxPages = MAX_PAGES,
): Promise<{ rows: T[]; truncated: boolean }> {
  const rows: T[] = [];
  let cursor: string | undefined;
  for (let i = 0; i < maxPages; i++) {
    const p = await page(cursor);
    rows.push(...(p.rows ?? []));
    if (!p.nextCursor) return { rows, truncated: false };
    cursor = p.nextCursor;
  }
  return { rows, truncated: true };
}

/** `?limit=…&cursor=…` (and any extra keys), empty values left out. */
export function pageQuery(params: Record<string, string | number | boolean | undefined>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== '' && v !== false) q.set(k, String(v));
  const s = q.toString();
  return s ? `?${s}` : '';
}
