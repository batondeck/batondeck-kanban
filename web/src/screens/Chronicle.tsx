/**
 * Chronicle (CHS-4) — browse, read, edit inline, diff, revert.
 *
 * Built on the Intent vocabulary in `ui.tsx`, and that is not just house style here — it is the
 * closest fit in the codebase. PRD P-1 requires facts and opinions to be VISUALLY DISTINCT CLASSES
 * everywhere, expressed as a prop rather than a colour so the two cannot drift into looking alike.
 * A Chronicle block's provenance is exactly that distinction:
 *
 *     derived              -> Tag kind="fact"     a generated statement; its citation holds
 *     authored             -> Tag kind="opinion"  a person wrote it; it never claimed to be derived
 *     derived-then-edited  -> Tag kind="guard"    THE CITATION MAY NO LONGER HOLD
 *
 * The third is the load-bearing one: it is what tells a reader a sentence no longer matches the
 * evidence it still cites, and it is the entire reason full editing is safe here and unsafe in a
 * wiki. `guard` carries --blocked, the system's warning token, so it cannot quietly become a tint
 * the next time the palette moves.
 *
 * Talks ONLY to the gateway, never to the core.
 */
import { useEffect, useMemo, useState } from 'react';
import { Icon } from '../icons.js';
import { useInfiniteQuery, useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router';
import * as chronicleApi from '../api/chronicle.js';
import * as projectsApi from '../api/projects.js';
import { Avatar, EmptyState, PageHeader, timeAgo } from '../components.js';
import { displayAgentName } from '../agentStatus.js';
import { Button, Chip, Chips, Cluster, Empty, Note, Panel, Section, Stack, Tag } from '../ui.js';
import { ChronicleBlockView, sameBlockText } from '../chronicleBlocks.js';
import { useProject } from '../projectContext.js';
import { clearPalette, publishPalette } from '../palette.js';
import { railsOf, sortByDate, type SortField } from '../chronicleSearch.js';
import { CHRONICLE_KINDS, isAgent } from '../types.js';
import type {
  ChronicleBlock,
  ChronicleBlockInput,
  ChronicleConflicts,
  ChroniclePage,
  ChroniclePageKind,
  ChronicleVersion,
} from '../types.js';

/**
 * A NEW block's id, minted once and then carried for the life of the block.
 *
 * `a-` for authored, and structurally disjoint from the sweep's `<sectionKey><n>/<m>` scheme so a
 * hand-written block can never collide with a derived one — a duplicate id is refused by the tool,
 * and if it were not, the merge indexes by id and one of the pair would silently vanish.
 *
 * MINTED AT INSERT, NEVER AT SAVE. Ids are what make diff, history and the three-way merge work at
 * all: re-deriving them on every save (splitting a document into paragraphs, say) would give every
 * block a new identity each time, so every version would read as "everything deleted, everything
 * added" and the sweep would have nothing to match against.
 */
const newBlockId = (): string => `a-${crypto.randomUUID().replace(/-/g, '').slice(0, 12)}`;

/**
 * Fold the editor's `{id, text}` list back into full block inputs.
 *
 * The editor deliberately does not carry `citations` or `origin` — neither is editable here, and a
 * component that held them would be a component that could drop them. So they are looked up from the
 * PREVIOUS draft by id: an existing block keeps what it had, a newly inserted one starts with
 * nothing, and a removed one takes its citations with it because it is gone.
 *
 * `origin` is passed through only because the shape asks for it; `edit_chronicle_page` ignores it and
 * inherits the real one from the page's own history. That is what stops an editor forging `derived`.
 */
const mergeDraft = (
  prev: ChronicleBlockInput[],
  next: Array<{ id: string; text: string }>,
): ChronicleBlockInput[] => {
  const was = new Map(prev.map((b) => [b.id, b]));
  return next.map((b) => ({
    id: b.id,
    text: b.text,
    citations: was.get(b.id)?.citations ?? [],
    origin: was.get(b.id)?.origin ?? null,
  }));
};

/**
 * The block list editor, shared by the create screen and the inline editor on a page.
 *
 * Add / delete / move needs no new tool: `edit_chronicle_page` takes an arbitrary block list, so a
 * removed block is one the next version omits and a moved one is the same id in a different place.
 * Until this existed the editor could only retype the text of blocks that already existed, which for
 * a hand-written document means it could not be edited at all — only corrected.
 */
function BlockEditor({
  blocks,
  onChange,
}: {
  blocks: Array<{ id: string; text: string }>;
  onChange: (next: Array<{ id: string; text: string }>) => void;
}) {
  const at = (i: number, text: string) => onChange(blocks.map((b, j) => (j === i ? { ...b, text } : b)));
  const remove = (i: number) => onChange(blocks.filter((_, j) => j !== i));
  const insertAfter = (i: number) => {
    const next = [...blocks];
    next.splice(i + 1, 0, { id: newBlockId(), text: '' });
    onChange(next);
  };
  const move = (i: number, by: -1 | 1) => {
    const to = i + by;
    if (to < 0 || to >= blocks.length) return;
    const next = [...blocks];
    [next[i], next[to]] = [next[to]!, next[i]!];
    onChange(next);
  };
  return (
    <Stack gap={16}>
      {blocks.map((b, i) => (
        <Stack key={b.id} gap={4}>
          <textarea
            className="chr-edit"
            value={b.text}
            aria-label={`Block ${b.id}`}
            placeholder="Markdown — headings, lists and links all work."
            onChange={(e) => at(i, e.target.value)}
          />
          <Cluster gap={8} wrap>
            {/* Titles as well as glyphs: an icon-only row of four is unreadable to a screen reader
                and ambiguous to everyone else. */}
            <Button size="sm" aria-label={`Move ${b.id} up`} title="Move up" disabled={i === 0} onClick={() => move(i, -1)}>
              ↑
            </Button>
            <Button
              size="sm"
              aria-label={`Move ${b.id} down`}
              title="Move down"
              disabled={i === blocks.length - 1}
              onClick={() => move(i, 1)}
            >
              ↓
            </Button>
            <Button size="sm" aria-label={`Add a block after ${b.id}`} onClick={() => insertAfter(i)}>
              + Add below
            </Button>
            {/* No confirm: a removed block is only absent from the DRAFT until Save, and the version
                it came from keeps it forever. Cancel is the undo. */}
            <Button size="sm" variant="danger" aria-label={`Remove ${b.id}`} onClick={() => remove(i)}>
              Remove
            </Button>
          </Cluster>
        </Stack>
      ))}
      {/* The empty case needs its own door — with no blocks there is no row to "add below". */}
      {!blocks.length && (
        <Button onClick={() => onChange([{ id: newBlockId(), text: '' }])}>Add the first block</Button>
      )}
    </Stack>
  );
}

/** T-430: `CV-ae1e8dfeed45` reads as noise in a byline — show `v ae1e8df`, full id on the title. */
const shortVersion = (id: string): string => `v ${id.replace(/^CV-/, '').slice(0, 7)}`;

/** Display NAME only for the byline — an email keeps its name part, an unresolved account id shows nothing raw. */
const bylineName = (id: string, names?: Record<string, string>): string => {
  const raw = (names?.[id] ?? id).trim() || id;
  const named = raw.includes('@') ? raw.split('@')[0]! : raw;
  return /^U-/.test(named) ? 'a person' : named;
};

/**
 * T-430 — one avatar chip per collaborator on a version, tooltips carrying display NAMES only.
 *
 * The response shape bounds "collaborators" at two: `agent` (display name, when the write came through
 * the agent surface) and `author` (the caller identity). A human author beside an agent is the
 * collaborated case and gets its own chip; an agent-shaped author (service account) is the agent's own
 * identity wearing a different string, so it stays one chip.
 */
function BylineChips({ v, names }: { v: { author: string; agent: string | null }; names?: Record<string, string> }) {
  const chips: Array<{ id: string; name: string }> = [];
  if (v.agent) chips.push({ id: v.agent, name: displayAgentName(v.agent) });
  if (!v.agent || (!isAgent(v.author) && v.author !== v.agent))
    chips.push({ id: v.author, name: bylineName(v.author, names) });
  return (
    <Cluster gap={4} className="chr-byline">
      {chips.map((c) => (
        <Avatar key={c.id} id={c.id} label={c.name} nameOnly />
      ))}
    </Cluster>
  );
}

/**
 * The INDEX row's byline — one name, not the avatar chips (T-514).
 *
 * `BylineChips` is right for a page header, where a Cluster of Avatars has room; a dense index row is
 * a single `<span>` and a flex container inside it is invalid nesting. What matters is reusing the
 * NAME RESOLUTION rather than the layout: `bylineName` keeps the email name-part and turns an
 * unresolved `U-…` into "a person", and `displayAgentName` names an agent — restating either here is
 * how a raw account id ends up rendered in one of the two places.
 *
 * The agent wins when there is one, for the same reason `BylineChips` puts it first: an agent-shaped
 * author is the agent's own identity wearing a different string.
 */
const rowByline = (v: { author: string; agent: string | null }): string =>
  v.agent ? displayAgentName(v.agent) : bylineName(v.author);

/** Index — browse every record in the project, or across every visible project at the "All" scope. */
export function Chronicle() {
  const { projectId, projects, loading, setProjectId } = useProject();
  /**
   * PAGED, and the paging is VISIBLE — see the `nextCursor` block below the list.
   *
   * This was a plain `useQuery` rendering one page with no "more" indicator. For a feature whose
   * entire pitch is checkable evidence, a silently truncated index is a FALSE ABSENCE claim: the
   * reader scans it, does not find the decision, and concludes it was never recorded. Being wrong
   * loudly ("there are more") is recoverable; being wrong silently is not.
   */
  const q = useInfiniteQuery({
    queryKey: ['chronicle', projectId],
    enabled: !!projectId,
    initialPageParam: '',
    queryFn: ({ pageParam }) => chronicleApi.listChroniclePages(projectId, pageParam || undefined),
    // Absent `nextCursor` means the list is complete — that is the contract, so `null` (stop) is the
    // default and only an explicit cursor continues.
    getNextPageParam: (last) => last.nextCursor ?? null,
  });

  // "All projects": one first-page fetch per visible project. A failing project stays an inline
  // error in its own group (rule 9a) — the other projects' records still render.
  const fanout = useQueries({
    queries: projects.map((p) => ({
      queryKey: ['chronicle-all', p.projectId],
      enabled: !projectId,
      queryFn: () => chronicleApi.listChroniclePages(p.projectId),
    })),
  });

  // `q.data.pages` is react-query's list of RESPONSES; each response carries its own `pages` of
  // records. Flattened once here so nothing downstream has to know the list arrived in instalments.
  const records = useMemo(() => q.data?.pages.flatMap((r) => r.pages) ?? [], [q.data]);

  const [kindFilter, setKindFilter] = useState<ChroniclePageKind | 'all'>('all');
  const [newestFirst, setNewestFirst] = useState(true);
  const [sortField, setSortField] = useState<SortField>('added');

  // ⌘K is the app's one command palette; Chronicle publishes what it has loaded and the palette
  // searches it client-side. Cleared on unmount so another screen's ⌘K does not list these.
  useEffect(() => {
    publishPalette({ projectId, actions: [], records });
    return () => clearPalette();
  }, [projectId, records]);

  // COUNTS ONLY WHEN THE INDEX IS COMPLETE AND THE JOIN IS NON-EMPTY. The list pages at 100 against
  // 238 records, so a count over what is loaded renders `authz (13)` on first paint and climbs as the
  // reader pages — a wrong number a reader believes. `railsOf` returns `count: null` for both that
  // case and the untopiced corpus, and the chip renders bare.
  const rails = useMemo(() => railsOf(records, !q.hasNextPage), [records, q.hasNextPage]);

  const kinds = useMemo(() => {
    const seen = new Set(records.map((p) => p.kind));
    return CHRONICLE_KINDS.filter((k) => seen.has(k));
  }, [records]);

  const byKind = useMemo(() => {
    const out: Record<string, ChroniclePage[]> = {};
    for (const p of records) {
      if (kindFilter !== 'all' && p.kind !== kindFilter) continue;
      (out[p.kind] ??= []).push(p);
    }
    for (const k of Object.keys(out)) out[k] = sortByDate(out[k]!, newestFirst, sortField);
    return out;
  }, [records, kindFilter, newestFirst, sortField]);

  const shown = useMemo(() => Object.values(byKind).reduce((n, ps) => n + ps.length, 0), [byKind]);

  return (
    <div className="page">
      <PageHeader
        title="Chronicle"
        subtitle="Why this system is shaped the way it is — derived from the board, and written by hand where it needs to be."
        actions={projectId ? <Link to="/chronicle/new">New record</Link> : undefined}
      />
      {q.isError && <Note>{(q.error as Error).message}</Note>}
      {/* The default "All projects" scope LISTS every visible project's records (T-429) — it used to
          ask for a project instead. "No projects at all" and "projects but no records" stay distinct. */}
      {!projectId && !loading && !projects.length && (
        <EmptyState icon={<Icon name="book" size={22} />} title="No projects yet" hint="You are not a member of any project yet." />
      )}
      {!projectId && !loading && projects.length > 0 && (
        <>
          {fanout.every((r) => r.isSuccess && !r.data.pages.length) && (
            <EmptyState
              icon={<Icon name="book" size={22} />}
              title="No records yet"
              hint="None of your projects has records yet. Choose one from the switcher to write the first."
            />
          )}
          <Stack gap={16}>
            {projects.map((p, i) => {
              const r = fanout[i]!;
              if (r.isError)
                return (
                  <Panel key={p.projectId} title={p.name} pad>
                    <Note>Could not load this project&rsquo;s records: {(r.error as Error).message}</Note>
                  </Panel>
                );
              if (!r.data?.pages.length) return null;
              return (
                <Panel key={p.projectId} title={p.name} right={r.data.pages.length}>
                  {r.data.pages.map((pg) => (
                    // Scope the switcher on click so the existing per-project page route can resolve.
                    <Link
                      key={`${p.projectId}/${pg.id}`}
                      className="line-row"
                      to={`/chronicle/${pg.id}`}
                      onClick={() => setProjectId(p.projectId)}
                    >
                      <span className="line-row-label">
                        {pg.title} {pg.conflict && <Tag kind="guard">sweep conflict</Tag>}
                        {/* Same reasoning as the conflict tag beside it: a record that has been
                            superseded must not read as current anywhere it is listed. Per-row and so
                            honest even here, where only the first page is loaded — unlike a count. */}
                        {pg.supersededBy?.length ? <Tag kind="shadow">superseded</Tag> : null}
                      </span>
                      <span className="line-row-value muted">{pg.slug}</span>
                    </Link>
                  ))}
                  {/* First page only at this scope — truncation stated in words, per the house rule. */}
                  {r.data.nextCursor && (
                    <Note>
                      <Cluster gap={8} wrap>
                        <span role="status">This list is partial — more records exist in {p.name}.</span>
                        <Button size="sm" onClick={() => setProjectId(p.projectId)}>
                          Open {p.name}
                        </Button>
                      </Cluster>
                    </Note>
                  )}
                </Panel>
              );
            })}
          </Stack>
        </>
      )}
      {q.isSuccess && !records.length && (
        <EmptyState
          icon={<Icon name="book" size={22} />}
          title="No records yet"
          hint="Records arrive when a sweep ingests them from the repo — or you can write one yourself."
          // An empty state with nothing to press is a dead end, and this one used to be exactly that:
          // it explained that a sweep would eventually produce something and offered no way to act.
          action={<Link to="/chronicle/new">Write the first record</Link>}
        />
      )}
      {/* NAVIGATION, and it only exists because the sweep now hosts the topic and feature pages: the
          index used to render one group because one kind existed. Each rail entry IS a record — it
          links to the page that lists that topic's records rather than filtering this list. */}
      {['topic', 'feature'].map((k) => {
        const rs = rails.filter((r) => r.kind === k);
        return rs.length ? (
          <Panel key={k} title={k === 'topic' ? 'Topics' : 'Features'} right={rs.length} pad>
            <Cluster gap={8} wrap>
              {rs.map((r) => (
                <Link
                  key={r.id}
                  className="chip"
                  to={`/chronicle/${r.id}`}
                  title={r.title}
                  /* THE COUNT NEEDS ITS OWN ACCESSIBLE NAME, and finding that out cost a red test.
                     The accessible-name algorithm TRIMS each child's text before concatenating, so a
                     leading space inside the count span vanishes and `authz` + ` 0` is announced as
                     "authz0" — a different word, not a topic and a number. An explicit label states
                     the relationship in words instead of relying on punctuation surviving. It still
                     opens with the visible key, so voice control ("click authz") keeps working. */
                  aria-label={r.count === null ? r.key : `${r.key}, ${r.count} records`}
                >
                  {r.key}
                  {/* `null` is no number at all, never `(0)` — see `railsOf`. A count of 0 is not
                      reachable and would not be honest: it means the rows predate the topic field. */}
                  {r.count !== null && <span className="muted"> ({r.count})</span>}
                </Link>
              ))}
            </Cluster>
          </Panel>
        ) : null;
      })}
      {records.length > 0 && (
        <Cluster gap={12} wrap>
          <Chips>
            <Chip on={kindFilter === 'all'} onClick={() => setKindFilter('all')}>
              all kinds
            </Chip>
            {kinds.map((k) => (
              <Chip key={k} on={kindFilter === k} onClick={() => setKindFilter(k)}>
                {k}
              </Chip>
            ))}
          </Chips>
          {/* "Added"/"Updated", never "Date": both are ingest-side timestamps, not the record's own
              `date:` frontmatter. Labelling either Date would claim a fact the index does not have.
              Updated is the head version's ts — it moves when a sweep re-ingests or a human edits. */}
          <Button size="sm" onClick={() => setSortField((f) => (f === 'added' ? 'updated' : 'added'))}>
            {sortField === 'added' ? 'Added' : 'Updated'}
          </Button>
          <Button size="sm" onClick={() => setNewestFirst((v) => !v)}>
            {newestFirst ? '↓ newest first' : '↑ oldest first'}
          </Button>
        </Cluster>
      )}
      <Stack gap={16}>
        {Object.entries(byKind).map(([kind, ps]) => (
          <Panel key={kind} title={kind} right={ps.length}>
            {ps.map((p) => (
              <Link key={p.id} className="line-row" to={`/chronicle/${p.id}`}>
                <span className="line-row-label">
                  {p.title}{' '}
                  {/* The persisted sweep-conflict flag (T-298): the queryable signal, visible where a
                      human actually browses — it survives the sweep session that found it. */}
                  {p.conflict && <Tag kind="guard">sweep conflict</Tag>}
                  {/* T-514. SUPERSEDED, NOT VOID — `emit.py` deliberately leaves `status: accepted`
                      alone, because §6.1 permits exactly one mutation of an accepted record and this
                      is it. So the row is marked and stays fully legible: no strikethrough, no
                      "obsolete", nothing that tells a reader to stop reading. It names WHICH record
                      replaced it, because "superseded" with no successor is a dead end. */}
                  {p.supersededBy?.length ? (
                    <Tag kind="shadow">superseded by {p.supersededBy.join(', ')}</Tag>
                  ) : null}
                  {/* Coverage is the generator's own verdict on how much of the ticket it could see.
                      Absent/null renders NOTHING — it is a third state (the record declared none),
                      and defaulting it to `partial` would badge 238 pages with a claim none made. */}
                  {p.coverage ? (
                    <Tag kind={p.coverage === 'full' ? 'fact' : 'opinion'}>{p.coverage}</Tag>
                  ) : null}
                </span>
                <span className="line-row-value muted">
                  {p.slug} · {timeAgo(sortField === 'updated' ? (p.updatedAt ?? p.createdAt) : p.createdAt)}
                  {/* WHO LANDED THE HEAD VERSION — deliberately NOT joined to the timestamp above as
                      "updated 2m ago by X". A metadata refresh bumps `updatedAt` without moving the
                      head, so the two can legitimately describe different events; the separator keeps
                      them two facts rather than one sentence that is sometimes false. */}
                  {p.updatedBy ? <> · by {rowByline(p.updatedBy)}</> : null}
                </span>
              </Link>
            ))}
          </Panel>
        ))}
      </Stack>
      {/* The truncation, stated in words. "Load more" alone is not enough: a reader who never scrolls
          to a button still has to be told the list they just scanned was not all of it. */}
      {q.hasNextPage && (
        <Note>
          <Cluster gap={8} wrap>
            {/* A kind filter hides rows the count still includes, so under one the sentence names
                BOTH numbers — "Showing 100 records" above three visible ones is the same false
                reading the unfiltered wording exists to prevent. Unfiltered text is untouched. */}
            <span role="status">
              {kindFilter === 'all' ? (
                <>
                  Showing {records.length} record{records.length === 1 ? '' : 's'} — this list is partial, more exist.
                </>
              ) : (
                <>
                  Showing {shown} {kindFilter} record{shown === 1 ? '' : 's'} of {records.length} loaded — this list is
                  partial, more exist.
                </>
              )}
            </span>
            <Button size="sm" onClick={() => void q.fetchNextPage()} disabled={q.isFetchingNextPage}>
              {q.isFetchingNextPage ? 'Loading…' : 'Load more'}
            </Button>
          </Cluster>
        </Note>
      )}
    </div>
  );
}

/**
 * A title, turned into a slug. Lowercase, dashes, nothing else — the shape `create_chronicle_page`
 * refuses anything outside of.
 *
 * DERIVED, THEN EDITABLE, and it stops following the title the moment anyone touches it. A slug is
 * the record's IDENTITY (the page id is a hash of it) and there is no rename, so someone who has
 * chosen `notes/deploys` must not have it silently rewritten by a later word in the heading.
 */
const slugify = (title: string): string =>
  title
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 80);

/** Write a record by hand. */
export function ChronicleNew() {
  const { projectId, projects, loading } = useProject();
  const nav = useNavigate();
  const qc = useQueryClient();
  const [title, setTitle] = useState('');
  const [slug, setSlug] = useState('');
  /** Null until someone edits the slug; after that the title stops driving it. See `slugify`. */
  const [slugTouched, setSlugTouched] = useState(false);
  const [kind, setKind] = useState<ChroniclePageKind>('topic');
  const [blocks, setBlocks] = useState<Array<{ id: string; text: string }>>([{ id: newBlockId(), text: '' }]);
  const [note, setNote] = useState<string | null>(null);
  /** ONE key per form instance: a double-click or a retried POST replays the same create instead of
   *  bouncing the second attempt off the taken-slug refusal. */
  const [idempotencyKey] = useState(() => crypto.randomUUID());

  const effectiveSlug = slugTouched ? slug : slugify(title);

  const create = useMutation({
    mutationFn: () =>
      chronicleApi.createChroniclePage(projectId, {
        kind,
        slug: effectiveSlug,
        title: title.trim(),
        // Empty blocks are DROPPED rather than sent. The editor starts with one so there is
        // somewhere to type; posting it when nobody did would open every record with a blank
        // paragraph, and a record of nothing but blanks is refused below instead.
        blocks: blocks.filter((b) => b.text.trim()).map((b) => ({ id: b.id, text: b.text })),
        idempotencyKey,
      }),
    onSuccess: async ({ pageId }) => {
      await qc.invalidateQueries({ queryKey: ['chronicle', projectId] });
      await nav(`/chronicle/${pageId}`);
    },
    onError: (e: Error) => setNote(e.message),
  });

  // Same trap as the other two screens: with no project scoped there is nothing to create INTO, and
  // the API path itself is per-project. Ask for the scope rather than posting into the empty string.
  if (!projectId && !loading)
    return (
      <EmptyState
        icon={<Icon name="book" size={22} />}
        title="Pick a project to write in"
        hint={
          projects.length
            ? 'Records belong to a project. Choose one from the switcher in the sidebar.'
            : 'You are not a member of any project yet.'
        }
      />
    );

  // `!!projectId` is load-bearing, not belt-and-braces: while the projects query is unresolved it is
  // still '' (projectContext validates the stored id against the fetched list), so a fast submit
  // used to POST into `/api/projects//chronicle`. The EmptyState above only catches the SETTLED
  // no-project case — mid-load, this is the only guard.
  const ready = !!projectId && !!title.trim() && !!effectiveSlug && blocks.some((b) => b.text.trim());

  return (
    <div className="page">
      <PageHeader
        title="New record"
        subtitle="Written by hand — every block will be marked `authored`, and nothing will claim to be derived."
        actions={
          <Cluster gap={8}>
            <Link to="/chronicle">Cancel</Link>
            <Button variant="primary" disabled={!ready || create.isPending} onClick={() => create.mutate()}>
              {create.isPending ? 'Creating…' : 'Create record'}
            </Button>
          </Cluster>
        }
      />
      {note && <Note>{note}</Note>}
      <Panel pad>
        <Stack gap={12}>
          <label htmlFor="chr-title">Title</label>
          <input
            id="chr-title"
            className="input"
            value={title}
            placeholder="How we deploy"
            onChange={(e) => setTitle(e.target.value)}
          />
          <label htmlFor="chr-slug">
            Slug — the record&rsquo;s permanent identity. It cannot be changed later.
          </label>
          <input
            id="chr-slug"
            className="input"
            value={effectiveSlug}
            placeholder="notes/how-we-deploy"
            onChange={(e) => {
              setSlugTouched(true);
              setSlug(e.target.value);
            }}
          />
          <label htmlFor="chr-kind">Kind</label>
          <select
            id="chr-kind"
            className="input"
            value={kind}
            onChange={(e) => setKind(e.target.value as ChroniclePageKind)}
          >
            {CHRONICLE_KINDS.map((k) => (
              <option key={k} value={k}>
                {k}
              </option>
            ))}
          </select>
        </Stack>
      </Panel>
      <Section>Content</Section>
      <Panel pad>
        <BlockEditor blocks={blocks} onChange={setBlocks} />
      </Panel>
    </div>
  );
}

/** One page: read, edit inline, delete, reach its history. */
export function ChroniclePageView() {
  const { projectId } = useProject();
  const { pageId = '' } = useParams();
  const nav = useNavigate();
  const qc = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<ChronicleBlockInput[]>([]);
  const [note, setNote] = useState<string | null>(null);
  /**
   * The slug typed into the delete confirmation, or null when the panel is closed.
   *
   * TYPED, not a two-click `ConfirmButton`, and this is the one place in the app that asks for that.
   * Every other destructive-looking action here is recoverable — an edit is a new version, a revert
   * is a new version, a removed block still lives in the version it came from. This one destroys the
   * page, its whole history and its stored bodies, with nothing left to revert to. A mis-click and a
   * decision should not cost the same number of gestures.
   */
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  /**
   * A save came back STALE — someone landed a version while this draft was open.
   *
   * An explicit flag, NOT `head.id !== editingFrom`. The head moving is normal (a background refetch
   * picks up anyone's commit) and by itself costs the reader nothing; it only becomes a conflict when
   * they try to land text on top of it. Deriving it would throw the conflict UI at people who have not
   * pressed anything.
   */
  const [conflict, setConflict] = useState(false);
  /**
   * The version this draft was started from — captured when the editor OPENS and never re-read.
   *
   * Reading the head at save time defeats the guard it feeds. `expectedParentVersionId` means "the
   * text I edited"; if a background refetch lands while someone is typing, the live head silently
   * becomes a version they never saw, the STALE check matches, and their save overwrites the other
   * person's work — the exact lost update the check exists to prevent, made invisible by the very
   * mechanism meant to surface it. Pinning it here means a concurrent save is reported instead.
   */
  const [editingFrom, setEditingFrom] = useState<string | null>(null);

  const q = useQuery({
    queryKey: ['chronicle-page', projectId, pageId],
    enabled: !!projectId && !!pageId,
    queryFn: () => chronicleApi.getChroniclePage(projectId, pageId),
  });

  // T-430: the byline shows NAMES, and names live in the project's memberDisplay (T-319). Shared
  // query key with Settings/Admin, so this is one cached read, not a new fan-out.
  const proj = useQuery({
    queryKey: ['project', projectId],
    enabled: !!projectId,
    queryFn: () => projectsApi.getProject(projectId),
  });
  const names = proj.data?.memberDisplay;

  /**
   * slug -> page id, so a record citing `adr/t-494` in prose becomes a link to it (T-513).
   * Its OWN key, never the index's `['chronicle', projectId]` — that is a useInfiniteQuery and a
   * plain useQuery on the same key fights it. FIRST PAGE ONLY, hence: unresolved stays plain text.
   */
  const slugs = useQuery({
    queryKey: ['chronicle-slugs', projectId],
    enabled: !!projectId,
    queryFn: () => chronicleApi.listChroniclePages(projectId),
  });
  const slugMap = useMemo(
    () => new Map((slugs.data?.pages ?? []).map((p) => [p.slug, p.id])),
    [slugs.data],
  );
  const resolveSlug = useMemo(() => (slug: string) => slugMap.get(slug) ?? null, [slugMap]);

  /**
   * The parent version is an ARGUMENT, not a closure read.
   *
   * The rebase path below saves against a version that is not yet in `editingFrom` — a `setState`
   * scheduled in the same handler has not landed by the time `mutationFn` runs, so reading state here
   * would post the OLD parent and STALE again forever. Passing it explicitly makes "which version am
   * I landing on" a decision at the call site, which is precisely where it belongs.
   */
  const save = useMutation({
    mutationFn: ({ parent, blocks }: { parent: string; blocks: ChronicleBlockInput[] }) =>
      chronicleApi.editChroniclePage(projectId, pageId, parent, blocks),
    onSuccess: async () => {
      setEditing(false);
      setEditingFrom(null);
      setNote(null);
      setConflict(false);
      await qc.invalidateQueries({ queryKey: ['chronicle-page', projectId, pageId] });
    },
    onError: async (e: Error & { code?: string }) => {
      if (e.code !== 'STALE') {
        setNote(e.message);
        return;
      }
      // STALE means someone committed while this draft was open. The old behaviour said "your text is
      // still here" and then offered two actions that both destroyed it: Save retried the SAME pinned
      // parent (STALE forever, never rebased) and Cancel re-seeded the draft from the refetched head.
      // A message promising safety next to two doors that lose the work is worse than losing it fast.
      //
      // So: go and GET the other version, and show it. A person cannot choose between their text and
      // someone else's without seeing both.
      setConflict(true);
      setNote(null);
      await qc.invalidateQueries({ queryKey: ['chronicle-page', projectId, pageId] });
    },
  });

  const head = q.data?.head ?? null;
  const page = q.data?.page ?? null;
  const drifted = q.data?.drifted ?? [];
  /** The persisted sweep-conflict state (T-298), with THEIRS attached per item. */
  const sweepConflicts: ChronicleConflicts | null = q.data?.conflicts ?? null;

  /**
   * The human's per-block choice while resolving a sweep conflict: 'keep' (what the merge landed —
   * the KEPT-AND-LABELED default) or 'alt' (the other side: the generator's text for a `conflict`,
   * a deletion for the two deletion-shaped kinds). Landing with all defaults is itself meaningful —
   * it writes a human-attributed version that CONFIRMS the auto-pick and clears the flag.
   */
  const [resolution, setResolution] = useState<Record<string, 'keep' | 'alt'>>({});

  /**
   * Landing the resolution is JUST an edit — a new version through the existing path, parented on
   * the head the human was looking at. The server clears the page's conflict state on any
   * human-landed version, so no resolve endpoint exists and none is needed. Deliberately separate
   * from `save`: that mutation's STALE handling drives the EDITOR's conflict flow (draft, rebase),
   * and there is no draft here — on STALE the honest move is refetch-and-review-again.
   */
  const land = useMutation({
    mutationFn: ({ parent, blocks }: { parent: string; blocks: ChronicleBlockInput[] }) =>
      chronicleApi.editChroniclePage(projectId, pageId, parent, blocks),
    onSuccess: async () => {
      setResolution({});
      setNote(null);
      await qc.invalidateQueries({ queryKey: ['chronicle-page', projectId, pageId] });
    },
    onError: async (e: Error & { code?: string }) => {
      setNote(
        e.code === 'STALE'
          ? 'Someone landed a version while you were deciding — the page has been refreshed; review the conflict again.'
          : e.message,
      );
      await qc.invalidateQueries({ queryKey: ['chronicle-page', projectId, pageId] });
    },
  });

  /**
   * Destroy the record. Nothing here is optimistic and nothing is cached forward: on success we leave
   * for the index, having dropped BOTH cached queries, because a page that resolves to nothing is the
   * one state this screen has no honest way to render.
   */
  const destroy = useMutation({
    // The head the confirmation DISPLAYED pins the delete: a version landed mid-confirm answers
    // STALE instead of destroying work the confirmer never saw.
    mutationFn: () => chronicleApi.deleteChroniclePage(projectId, pageId, q.data?.head?.id),
    onSuccess: async () => {
      qc.removeQueries({ queryKey: ['chronicle-page', projectId, pageId] });
      await qc.invalidateQueries({ queryKey: ['chronicle', projectId] });
      await nav('/chronicle');
    },
    onError: (e: Error) => setNote(e.message),
  });

  /** Compose the final block list from the head plus the per-block choices. */
  const composeResolution = (from: ChronicleVersion, conflicts: ChronicleConflicts): ChronicleBlockInput[] => {
    const items = new Map(conflicts.items.map((i) => [i.blockId, i]));
    const out: ChronicleBlockInput[] = [];
    for (const b of from.blocks) {
      const item = items.get(b.id);
      const pick = item ? (resolution[b.id] ?? 'keep') : 'keep';
      if (!item || pick === 'keep') {
        out.push({ id: b.id, text: b.text, citations: b.citations, origin: b.origin });
        continue;
      }
      if (item.kind === 'conflict' && item.theirs) {
        // Take the generator's side: its text AND its citations travel together. Provenance is the
        // server's call — landing verbatim generator text may still read `derived-then-edited`
        // until the next sweep re-derives it, because an edit inherits the parent's origin.
        out.push({
          id: b.id,
          text: item.theirs.text,
          citations: item.theirs.citations,
          origin: item.theirs.origin,
        });
      }
      // The two deletion-shaped kinds: the alternative IS an absence — emit nothing.
    }
    return out;
  };

  /**
   * THEIRS — the version that landed under this draft, once the refetch has actually delivered it.
   *
   * Null while that fetch is in flight or has failed, and the rebase button is gated on it. Offering
   * "save on top of theirs" before theirs is on screen would be the silent rebase `editingFrom` exists
   * to prevent: the user would be consenting to overwrite text they have never seen.
   */
  const theirs = conflict && head && head.id !== editingFrom ? head : null;

  /** Seed the editor from what is on screen, and pin the version it came from. */
  const openEditor = (from: ChronicleVersion) => {
    setDraft(from.blocks.map((b) => ({ id: b.id, text: b.text, citations: b.citations, origin: b.origin })));
    setEditingFrom(from.id);
    setEditing(true);
    // An open delete confirmation dies here: editing outranks it (the (13) invariant), and leaving
    // it armed meant Edit-after-Delete rendered a live "Delete permanently" beside an open draft.
    setConfirmDelete(null);
    setConflict(false);
    setNote(null);
  };

  // Same trap as the index, one screen over: with no project scoped the query is DISABLED, and a
  // disabled react-query stays `isPending` forever — so a spinner here would never resolve. Ask for
  // the scope before claiming to be loading anything. This one stays OUTSIDE the guard below: with no
  // project there is no page to edit, so there is no draft to protect.
  if (!projectId)
    return (
      <EmptyState
        icon={<Icon name="book" size={22} />}
        title="Pick a project to read its Chronicle"
        hint="Records belong to a project. Choose one from the switcher in the sidebar."
      />
    );

  // *** AN OPEN EDITOR OUTRANKS EVERY WHOLE-SCREEN STATE BELOW. ***
  //
  // The draft lives in this component and NOWHERE else — not in the cache, not on the server. These
  // three returns used to fire unconditionally, so with `staleTime: 30_000` + `refetchOnWindowFocus`,
  // switching tabs mid-edit swapped the editor for "Loading…", and (with `retry: false`) a single
  // failed background refetch pinned `isError` and left an error Note with no Save button at all. The
  // text still existed in memory and nothing on screen could reach it.
  //
  // Each return is still correct for a READER and none of them is weakened: "still fetching" and
  // "fetched, and there is nothing here" remain different answers (the core returns `{page: null}` for
  // an unknown page, so a bad link must say "No such record" rather than spin forever). They are just
  // no longer allowed to unmount somebody's unsaved words. While editing, a stale read or a failed
  // refresh is reported INLINE below instead.
  if (!editing) {
    if (q.isError) return <Note>{(q.error as Error).message}</Note>;
    if (q.isPending) return <EmptyState icon={<Icon name="book" size={22} />} title="Loading…" />;
    if (!page || !head)
      return (
        <EmptyState
          icon={<Icon name="book" size={22} />}
          title="No such record"
          hint="This Chronicle page does not exist, or it belongs to a project you cannot read."
        />
      );
  }

  return (
    <div className="page">
      {/* `page`/`head` are nullable ONLY on the editing path — a reader never gets past the guard
          above without them. Coalesced rather than asserted so a page deleted under an open editor
          still renders the draft instead of crashing the screen that is holding it. */}
      <PageHeader
        title={page?.title ?? 'Chronicle page'}
        subtitle={page?.slug}
        actions={
          <Cluster gap={8}>
            <Link to={`/chronicle/${pageId}/history`}>History</Link>
            {editing ? (
              <>
                {/* In a conflict this button is the only way OUT, and it throws the draft away — so it
                    says so. It used to say "Cancel" beside a message promising the text was safe. */}
                <Button
                  variant={conflict ? 'danger' : 'secondary'}
                  onClick={() => {
                    setEditing(false);
                    setEditingFrom(null);
                    setConflict(false);
                    setNote(null);
                  }}
                  disabled={save.isPending}
                >
                  {conflict ? 'Discard my changes' : 'Cancel'}
                </Button>
                {/* Hidden in a conflict: retrying the SAME pinned parent is guaranteed to STALE again,
                    and a button whose only outcome is the error you are already reading is a trap.
                    The way forward is in the conflict panel, where theirs is on screen next to mine. */}
                {!conflict && (
                  <Button
                    variant="primary"
                    onClick={() => save.mutate({ parent: editingFrom!, blocks: draft })}
                    disabled={save.isPending}
                  >
                    {save.isPending ? 'Saving…' : 'Save version'}
                  </Button>
                )}
              </>
            ) : (
              <>
                {/* Pin the parent version HERE, at the moment the editor opens — see `editingFrom`. */}
                {head && <Button onClick={() => openEditor(head)}>Edit</Button>}
                {/* HIDDEN WHILE EDITING, like the sweep-conflict panel and for the same reason: an
                    open editor holds the only copy of somebody's draft, and nothing may take the
                    screen out from under it — least of all the one action that cannot be undone. */}
                {page && (
                  <Button variant="danger" onClick={() => setConfirmDelete('')}>
                    Delete
                  </Button>
                )}
              </>
            )}
          </Cluster>
        }
      />

      {/*
        THE ONE IRREVERSIBLE ACTION ON THIS SURFACE, so it asks for the slug rather than a second click.
        Everything else here is additive — an edit is a version, a revert is a version, a removed block
        survives in the version it came from. This destroys the page, its whole history and its stored
        bodies, and there is nothing left to revert to.
      */}
      {/* `!editing` is the belt to openEditor's braces: even if some future path opens the editor
          without clearing the confirm state, the one irreversible panel never renders over a draft. */}
      {!editing && confirmDelete !== null && page && (
        <Panel danger title="Delete this record permanently?" pad>
          <Stack gap={8}>
            <span role="alert">
              This destroys the page, every version of it and every stored copy. It cannot be undone
              and a revert cannot bring it back. If you only want it to say nothing, edit it and remove
              its blocks instead — that keeps the history.
              {page.sourcePath !== null && ' This record is generated from the repository, so the next sweep will recreate it.'}
            </span>
            <label htmlFor="chr-confirm-slug">
              Type <code>{page.slug}</code> to confirm.
            </label>
            <input
              id="chr-confirm-slug"
              className="input"
              value={confirmDelete}
              autoComplete="off"
              onChange={(e) => setConfirmDelete(e.target.value)}
            />
            <Cluster gap={8} wrap>
              <Button onClick={() => setConfirmDelete(null)} disabled={destroy.isPending}>
                Cancel
              </Button>
              <Button
                variant="danger"
                disabled={confirmDelete !== page.slug || destroy.isPending}
                onClick={() => destroy.mutate()}
              >
                {destroy.isPending ? 'Deleting…' : 'Delete permanently'}
              </Button>
            </Cluster>
          </Stack>
        </Panel>
      )}

      {/* Declared coverage. A page that states what it could NOT vouch for is the point, not a
          footnote — so it is a `danger` Panel, the loudest container the system has. */}
      {drifted.length > 0 && (
        <Panel danger title="Citations may no longer hold" pad>
          <span role="status">
            {drifted.length} block{drifted.length === 1 ? '' : 's'} {drifted.length === 1 ? 'has' : 'have'} been
            edited since generation — {drifted.length === 1 ? 'its citation' : 'their citations'} may no longer hold.
          </span>
        </Panel>
      )}
      {note && <Note>{note}</Note>}

      {/* A refresh that failed UNDER an open editor is a footnote, not a screen. It used to replace
          everything, taking the only copy of the draft with it. */}
      {editing && q.isError && <Note>Could not refresh this record: {(q.error as Error).message}</Note>}

      {/*
        THE CONFLICT: theirs, mine, and a way forward.

        Both sides on screen is what makes the rebase button honest — `editingFrom` exists to stop a
        save landing on a version its author never saw, and the fix for that is to SHOW them the
        version, not to drop the check. Once theirs is rendered, "save on top of it" is an informed
        choice rather than a silent overwrite.
      */}
      {conflict && (
        <Panel danger title="Someone else saved while you were editing" pad>
          <Stack gap={8}>
            <span role="alert">
              {theirs
                ? `Version ${theirs.id} landed after you opened ${editingFrom}. Nothing you wrote has been sent. Read theirs below, then choose.`
                : `A newer version landed after you opened ${editingFrom}. Nothing you wrote has been sent, and it is still in the editor below — but their version is not on screen yet, so there is nothing to compare against.`}
            </span>
            {theirs?.blocks.map((b) => {
              const mine = draft.find((d) => d.id === b.id);
              const changed = !sameBlockText(mine?.text, b.text);
              return (
                <div key={b.id} className={changed ? 'chr-diff-row is-changed' : 'chr-diff-row'}>
                  <pre aria-label={`Theirs ${b.id}`}>{b.text}</pre>
                  <pre aria-label={`Mine ${b.id}`}>{mine?.text ?? '— absent —'}</pre>
                </div>
              );
            })}
            <Cluster gap={8} wrap>
              <Button
                variant="primary"
                disabled={!theirs || save.isPending}
                onClick={() => theirs && save.mutate({ parent: theirs.id, blocks: draft })}
              >
                {save.isPending ? 'Saving…' : `Keep my text — save it on top of ${theirs?.id ?? 'theirs'}`}
              </Button>
              {/* Without this, "theirs failed to load" is a DEAD END — a disabled rebase beside a
                  discard, which is this very finding wearing a different hat. `refetchOnWindowFocus`
                  would eventually rescue it, but a recovery nobody can see is not one. It also covers
                  a refetch that SUCCEEDS and still returns the old head (read-after-write lag): the
                  honest answer to "not on screen yet" is the same either way. */}
              {!theirs && (
                <Button onClick={() => void q.refetch()} disabled={q.isFetching}>
                  {q.isFetching ? 'Fetching theirs…' : 'Try again — load their version'}
                </Button>
              )}
              {/* Explicitly destructive, explicitly chosen. `openEditor` re-seeds from theirs, so the
                  editor stays open on their text and the draft is gone by request, not by accident. */}
              <Button variant="danger" disabled={!theirs || save.isPending} onClick={() => theirs && openEditor(theirs)}>
                Take theirs instead — discard my text
              </Button>
            </Cluster>
          </Stack>
        </Panel>
      )}

      {/*
        THE SWEEP CONFLICT — the doorway CHS §5 promises ("a human lands the final version"), T-298.

        A sweep auto-resolved a contested regeneration; the outcome stands on the page, labeled, and
        this panel is where a human supersedes it. Both sides are on screen per block; every choice
        defaults to what the merge kept (kept-and-labeled), so landing untouched is an explicit
        CONFIRMATION. The landing write is the ordinary edit path — the server clears the flag on any
        human-landed version.

        HIDDEN WHILE THE EDITOR IS OPEN, deliberately: the (13) invariant says nothing outranks an
        open editor's draft, and a resolution landed mid-edit would race the draft's own Save (which
        also clears the flag server-side). A reader sees it; an editor finishes editing first.
      */}
      {!editing && sweepConflicts && head && (
        <Panel danger title="A sweep conflicted with edits on this page" pad>
          <Stack gap={12}>
            <span role="status">
              The generator and a person changed the same content. What is on the page is the
              automatic pick — review each block below and land the final version.
            </span>
            {sweepConflicts.items.map((i) => {
              const current = head.blocks.find((b) => b.id === i.blockId);
              const pick = resolution[i.blockId] ?? 'keep';
              const altText =
                i.kind === 'conflict'
                  ? (i.theirs?.text ?? '— unavailable —')
                  : i.kind === 'conflict-restored-theirs'
                    ? '— delete it again (a person had deleted this block; the sweep restored it) —'
                    : '— drop it (the generator no longer emits this block) —';
              const keepLabel = i.kind === 'conflict-restored-theirs' ? 'Keep it restored' : 'Keep current';
              const altLabel =
                i.kind === 'conflict'
                  ? "Take the generator's"
                  : i.kind === 'conflict-restored-theirs'
                    ? 'Delete it again'
                    : 'Drop it';
              return (
                <Stack key={i.blockId} gap={8}>
                  <div className="chr-diff-row is-changed">
                    <pre aria-label={`Current ${i.blockId}`}>{current?.text ?? '— absent —'}</pre>
                    <pre aria-label={`Sweep ${i.blockId}`}>{altText}</pre>
                  </div>
                  <Cluster gap={8} wrap>
                    <Button
                      size="sm"
                      variant={pick === 'keep' ? 'primary' : 'secondary'}
                      aria-pressed={pick === 'keep'}
                      onClick={() => setResolution({ ...resolution, [i.blockId]: 'keep' })}
                    >
                      {keepLabel}
                    </Button>
                    <Button
                      size="sm"
                      variant={pick === 'alt' ? 'primary' : 'secondary'}
                      aria-pressed={pick === 'alt'}
                      onClick={() => setResolution({ ...resolution, [i.blockId]: 'alt' })}
                    >
                      {altLabel}
                    </Button>
                  </Cluster>
                </Stack>
              );
            })}
            <Cluster gap={8} wrap>
              <Button
                variant="primary"
                disabled={land.isPending}
                onClick={() => land.mutate({ parent: head.id, blocks: composeResolution(head, sweepConflicts) })}
              >
                {land.isPending ? 'Landing…' : 'Land the final version'}
              </Button>
              <span className="muted">Landing writes a new version in your name and clears this flag.</span>
            </Cluster>
          </Stack>
        </Panel>
      )}

      <Panel
        pad
        footnote={
          // T-430: collaborator avatar chips (names on the tooltip, never emails or account ids) +
          // a short version label — the full id stays reachable on the label's title.
          head ? (
            <Cluster gap={8} wrap>
              <BylineChips v={head} names={names} />
              <span>{timeAgo(head.ts)}</span>
              <span title={head.id}>{shortVersion(head.id)}</span>
            </Cluster>
          ) : undefined
        }
      >
        <Stack gap={16}>
          {editing ? (
            // ADD / REMOVE / REORDER, not just retype. A block the draft drops is simply absent from
            // the next version; a block it gains is a new id, which the server resolves to `authored`
            // because nothing in the page's history vouches for it. Citations and origin ride along
            // on the blocks that already had them — see the merge in `save` below.
            <BlockEditor blocks={draft} onChange={(next) => setDraft(mergeDraft(draft, next))} />
          ) : (
            (() => {
              /* T-491 — architecture-first reading order, presentation-only. The sweep's block ids
                 are section-scoped (`cov0/1`, `ctx1/2`, `ev1/1`), so the page can lead with context,
                 decision and rejections and fold evidence + the coverage line into a details element
                 — the derived text itself is never touched. Authored blocks (`a-…`) stay in place. */
              const blocks = head?.blocks ?? [];
              const folded = blocks.filter((b) => /^(ev|cov)/.test(b.id));
              const lead = blocks.filter((b) => !/^(ev|cov)/.test(b.id));
              const render = (b: ChronicleBlock) => (
                <ChronicleBlockView key={b.id} block={b} resolveSlug={resolveSlug} />
              );
              return (
                <>
                  {lead.map(render)}
                  {folded.length > 0 ? (
                    <details className="chr-evidence">
                      <summary>Evidence & coverage ({folded.length})</summary>
                      <Stack gap={16}>{folded.map(render)}</Stack>
                    </details>
                  ) : null}
                </>
              );
            })()
          )}
        </Stack>
      </Panel>
    </div>
  );
}

/** History: version list, diff between any two, revert. */
export function ChronicleHistory() {
  const { projectId } = useProject();
  const { pageId = '' } = useParams();
  const qc = useQueryClient();

  // T-430: names for the byline chips — same cached read as the page view.
  const proj = useQuery({
    queryKey: ['project', projectId],
    enabled: !!projectId,
    queryFn: () => projectsApi.getProject(projectId),
  });
  const names = proj.data?.memberDisplay;
  const [params, setParams] = useSearchParams();
  const a = params.get('a');
  const b = params.get('b');

  /**
   * PAGED, and the paging is VISIBLE — the index's pattern (see `Chronicle` above), applied to
   * history for the same reason: a version list that silently ends at 50 claims "there is no earlier
   * version", which is a false absence about the very record this screen exists to prove. Same
   * contract too: `nextCursor` present means more exist; absent means complete, so `null` (stop) is
   * the default and only an explicit cursor continues.
   */
  const list = useInfiniteQuery({
    queryKey: ['chronicle-history', projectId, pageId],
    enabled: !!projectId && !!pageId,
    initialPageParam: '',
    queryFn: ({ pageParam }) => chronicleApi.listChronicleVersions(projectId, pageId, pageParam || undefined),
    getNextPageParam: (last) => last.nextCursor ?? null,
  });

  // react-query's list of RESPONSES, flattened once — newest first, later pages are strictly older,
  // so `versions[0]` stays the live head no matter how many pages have loaded.
  const versions = useMemo(() => list.data?.pages.flatMap((r) => r.versions) ?? [], [list.data]);

  // A diff loads EXACTLY two versions — the history list is metadata only, so this stays cheap.
  const pair = useQuery({
    queryKey: ['chronicle-diff', projectId, pageId, a, b],
    enabled: !!projectId && !!a && !!b,
    queryFn: async () => {
      const [l, r] = await Promise.all([
        chronicleApi.getChronicleVersion(projectId, pageId, a!),
        chronicleApi.getChronicleVersion(projectId, pageId, b!),
      ]);
      return { left: l.version, right: r.version };
    },
  });

  const revert = useMutation({
    mutationFn: (versionId: string) => chronicleApi.revertChroniclePage(projectId, pageId, versionId),
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: ['chronicle-history', projectId, pageId] });
      await qc.invalidateQueries({ queryKey: ['chronicle-page', projectId, pageId] });
    },
  });

  const diff = useMemo(() => {
    const l = pair.data?.left;
    const r = pair.data?.right;
    if (!l || !r) return null;
    const ids = [...new Set([...l.blocks.map((x) => x.id), ...r.blocks.map((x) => x.id)])];
    return ids.map((id) => {
      const lb = l.blocks.find((x) => x.id === id);
      const rb = r.blocks.find((x) => x.id === id);
      return { id, lb, rb, changed: !sameBlockText(lb?.text, rb?.text) };
    });
  }, [pair.data]);

  return (
    <div className="page">
      <PageHeader title="History" actions={<Link to={`/chronicle/${pageId}`}>Back to page</Link>} />
      {list.isError && <Note>{(list.error as Error).message}</Note>}
      {revert.isError && <Note>{(revert.error as Error).message}</Note>}

      <Panel title="Versions" right={versions.length} scroll>
        {!versions.length && <Empty>No versions yet.</Empty>}
        {versions.map((v) => (
          <div key={v.id} className="line-row">
            <span className="line-row-label">
              <Tag kind={v.source === 'sweep' ? 'fact' : 'opinion'}>{v.source === 'sweep' ? 'generated' : 'edited'}</Tag>
              {/* T-430: chips, not raw identities — the byline rule holds here too. */}
              <BylineChips v={v} names={names} />
            </span>
            <span className="line-row-value">
              <Cluster gap={8}>
                <span className="muted" title={v.id}>{shortVersion(v.id)} · {timeAgo(v.ts)} · {v.blockCount} blocks</span>
                <Button size="sm" onClick={() => setParams({ a: v.id, b: versions[0]!.id })}>
                  Compare with latest
                </Button>
                {/* A revert WRITES A NEW VERSION — nothing is destroyed, so it needs no confirmation. */}
                <Button size="sm" onClick={() => revert.mutate(v.id)} disabled={revert.isPending}>
                  Revert to this
                </Button>
              </Cluster>
            </span>
          </div>
        ))}
      </Panel>

      {/* The truncation, stated in words — the index's indicator verbatim, because a reader who never
          scrolls to a button still has to be told the history they just scanned was not all of it. */}
      {list.hasNextPage && (
        <Note>
          <Cluster gap={8} wrap>
            <span role="status">
              Showing {versions.length} version{versions.length === 1 ? '' : 's'} — this list is partial, more exist.
            </span>
            <Button size="sm" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>
              {list.isFetchingNextPage ? 'Loading…' : 'Load more'}
            </Button>
          </Cluster>
        </Note>
      )}

      {/*
        CHS §7 promises a diff between ANY two versions, and the mechanism has always supported it —
        the `?a=&b=` params drive the pair query, and the per-version resource exists precisely for
        this. Only the UI stopped at "Compare with latest" (b hardcoded to the head). These two
        pickers close that gap; the row shortcut stays because latest IS the common case, and both
        paths write the same URL state, so they cannot disagree.
      */}
      {versions.length > 0 && (
        <>
          <Section>Compare any two versions</Section>
          <Cluster gap={8} wrap>
            <select
              className="input"
              aria-label="From version"
              value={a ?? ''}
              onChange={(e) => setParams(b ? { a: e.target.value, b } : { a: e.target.value })}
            >
              <option value="" disabled>
                from…
              </option>
              {versions.map((v) => (
                <option key={v.id} value={v.id}>
                  {v.id} · {v.source === 'sweep' ? 'generated' : 'edited'} · {timeAgo(v.ts)}
                </option>
              ))}
            </select>
            <span aria-hidden>→</span>
            <select
              className="input"
              aria-label="To version"
              value={b ?? ''}
              onChange={(e) => setParams(a ? { a, b: e.target.value } : { b: e.target.value })}
            >
              <option value="" disabled>
                to…
              </option>
              {versions.map((v) => (
                <option key={v.id} value={v.id}>
                  {v.id} · {v.source === 'sweep' ? 'generated' : 'edited'} · {timeAgo(v.ts)}
                </option>
              ))}
            </select>
          </Cluster>
        </>
      )}

      {diff && (
        <>
          <Section>
            {a} → {b}
          </Section>
          <Panel pad>
            <Stack gap={8}>
              {diff.map((d) => (
                <div key={d.id} className={d.changed ? 'chr-diff-row is-changed' : 'chr-diff-row'}>
                  <pre>{d.lb?.text ?? '— absent —'}</pre>
                  <pre>{d.rb?.text ?? '— absent —'}</pre>
                </div>
              ))}
            </Stack>
          </Panel>
        </>
      )}
    </div>
  );
}
