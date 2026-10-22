/**
 * Client-side Chronicle search, rails and ordering. No new API: the index endpoint already returns
 * every field read here, so a reader searching 238 records issues zero extra requests.
 *
 * THE TOPIC AXIS IS REAL NOW (T-514), and this header used to say the opposite. `ChroniclePage` did
 * not carry the record's topics, coverage or `superseded_by` — they stopped at the emitter — so
 * `topic` here could only mean "the facet of a topic PAGE", rails shipped with no counts, and both
 * limits were written down rather than faked. The three fields are plumbed through now, so searching
 * `authz` finds the authz topic page AND every record that declares it, and a rail can count.
 *
 * `facetOf` survives unchanged and still means what it meant: the segment of a topic/feature page's
 * own slug. It is the rail's KEY; `topics` is the record's MEMBERSHIP. Joining the two is what a
 * count is, and it is the one thing the row could not express before.
 */
import type { ChroniclePage, ChroniclePageKind } from './types.js';

/**
 * The row fields a search reads. A narrow shape so a test can build one without a whole page.
 *
 * `topics` is OPTIONAL here while the domain type has it required: the fixtures in ten web suites
 * build a `Searchable` by hand, and a record with no topics is a legitimate row (238 of them predate
 * the field), so absent must behave as "declares none" rather than as a type error.
 */
export interface Searchable {
  kind: ChroniclePageKind;
  slug: string;
  title: string;
  topics?: readonly string[];
}

/**
 * The facet a topic or feature page is ABOUT — `topic/authz` -> `authz`, `feature/t-115` -> `t-115`.
 *
 * Null for every other kind. An ADR row's own membership is `topics`, not this: a record belongs to
 * the topics its frontmatter declares, and is not itself "about" a facet.
 */
export function facetOf(p: Searchable): string | null {
  if (p.kind !== 'topic' && p.kind !== 'feature') return null;
  const rest = p.slug.slice(p.slug.indexOf('/') + 1);
  return p.slug.includes('/') && rest ? rest : null;
}

/** Title, slug and the record's declared topics — the whole haystack, in one place (see `searchValue`). */
const haystack = (p: Searchable): string => `${p.title} ${p.slug} ${(p.topics ?? []).join(' ')}`;

/**
 * Every term must appear in title, slug or topics — `wake doorbell` is AND, matching the ticket search.
 *
 * TOPICS ARE A THIRD FIELD AND NOT A SUBSTRING OF THE OTHER TWO, which is what makes them worth
 * adding: `facetOf` was deliberately excluded from this haystack because it IS a slice of the slug,
 * so it could match nothing new. A record's `topics` are declared in its frontmatter and appear
 * nowhere in its slug (`adr/t-162`) or usually its title — so searching `authz` now returns the 31
 * records that claim the topic alongside the topic page itself, which is the thing a reader wanted.
 */
export function matchesChronicle(p: Searchable, query: string): boolean {
  const terms = query.toLowerCase().trim().split(/\s+/).filter(Boolean);
  if (!terms.length) return false;
  const hay = haystack(p).toLowerCase();
  return terms.every((t) => hay.includes(t));
}

export function searchChronicle<T extends Searchable>(pages: readonly T[], query: string): T[] {
  return query.trim() ? pages.filter((p) => matchesChronicle(p, query)) : [];
}

/**
 * What cmdk filters on, so its own pass can never veto a match this module made.
 *
 * The palette runs BOTH filters — `matchesChronicle` picks the rows, and cmdk then scores them again.
 * A row whose `value` was just its visible title would be silently dropped by that second pass on any
 * slug-only hit, which is a filter vetoing a filter and reads as "search is broken" with nothing to
 * find. Keeping the two haystacks THE SAME TEXT is what makes the second pass a no-op — which is why
 * this shares `haystack` with the matcher rather than restating it. Adding topics to one and not the
 * other reintroduces exactly that bug, one field along.
 */
export function searchValue(p: Searchable): string {
  return haystack(p);
}

export interface Rail {
  id: string;
  kind: 'topic' | 'feature';
  key: string;
  title: string;
  /**
   * How many ADR records declare this topic — or NULL when no honest number exists (see `railsOf`).
   * Null is rendered as no number at all, never as `(0)`.
   */
  count: number | null;
}

/**
 * The topic and feature rails: every such page that exists, in facet order.
 *
 * COUNTS ARE GATED ON `complete`, AND THAT GATE IS THE WHOLE DESIGN. The index is PAGINATED at 100
 * against 238 records, so a count taken over the rows currently loaded renders `authz (13)` on first
 * paint and changes as the reader pages — a number a reader believes and that is wrong, which the
 * previous version of this file refused to ship for exactly that reason. The resolution is not a
 * denormalised count on the row (a second source of truth that drifts the moment a sweep adds a
 * record); it is to derive the join the client already has and SUPPRESS it until the client holds all
 * of it. Caller passes `!hasNextPage`.
 *
 * ADR RECORDS ONLY, so the number agrees with the page the chip links to: `topics/<key>.md`'s records
 * table is built from `check.group_topics(recs)` over ADR records, and counting topic pages that
 * happen to declare a topic would make the rail disagree with its own destination.
 *
 * AND `complete` IS NOT ENOUGH ON ITS OWN — ZERO IS NEVER AN HONEST COUNT HERE. `complete` says the
 * client holds every ROW; it says nothing about whether any row carries `topics`. Every page ingested
 * before T-514 normalises to `topics: []`, so on today's corpus the gate is satisfied, the join is
 * empty, and `?? 0` renders `authz (0)` on every rail — the same wrong-number-a-reader-believes this
 * design exists to refuse, arrived at from the other side. A missing key in `counts` therefore stays
 * NULL rather than becoming 0. Nothing is lost: `counts` only ever holds values >= 1, and a topic
 * PAGE exists only because `check.group_topics(recs)` found a record declaring it — so a genuine
 * "all its records retired" zero is not reachable, and an observed zero always means "these rows have
 * not been re-swept yet". This paragraph replaces a comment that called that zero worth showing.
 *
 * FEATURE RAILS STAY BARE, and that is a known gap rather than a zero: a record's feature is the
 * `feature:` frontmatter field, which is NOT carried on the row (T-514 plumbed topics, coverage and
 * supersession only). Counting them from `topics` would be a different number wearing the right
 * label. Filed rather than guessed; the two kinds render in separate panels, so the asymmetry reads
 * as "topics are counted" rather than "features are zero".
 */
export function railsOf(pages: readonly ChroniclePage[], complete = false): Rail[] {
  const counts = new Map<string, number>();
  if (complete)
    for (const p of pages)
      if (p.kind === 'adr')
        for (const t of p.topics ?? []) counts.set(t, (counts.get(t) ?? 0) + 1);

  const out: Rail[] = [];
  for (const p of pages) {
    const key = facetOf(p);
    if (key && (p.kind === 'topic' || p.kind === 'feature'))
      out.push({
        id: p.id,
        kind: p.kind,
        key,
        title: p.title,
        count: complete && p.kind === 'topic' ? (counts.get(key) ?? null) : null,
      });
  }
  return out.sort((a, b) => a.key.localeCompare(b.key));
}

/**
 * Order by when the page entered Chronicle — `createdAt`, NOT the decision's own date.
 *
 * The record's `date:` frontmatter is the day the decision was made; the row carries only ingest
 * timestamps. They usually agree in ORDER and they are not the same fact, which is why the control is
 * labelled "Added" in the UI rather than "Date".
 */
/** Which timestamp the list is ordered by. `added` is ingest time; `updated` is the head version's. */
export type SortField = 'added' | 'updated';

export function sortByDate<T extends { createdAt: string; updatedAt?: string }>(
  pages: readonly T[],
  newestFirst: boolean,
  field: SortField = 'added',
): T[] {
  // updatedAt falls back to createdAt: a page ingested once and never edited has them equal, and a
  // row with no updatedAt must still sort somewhere deterministic rather than sinking to the bottom.
  const at = (p: T) => (field === 'updated' ? (p.updatedAt ?? p.createdAt) : p.createdAt);
  return [...pages].sort((a, b) =>
    newestFirst ? at(b).localeCompare(at(a)) : at(a).localeCompare(at(b)),
  );
}
