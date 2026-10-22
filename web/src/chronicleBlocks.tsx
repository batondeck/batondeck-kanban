/**
 * Chronicle BLOCK RENDERING — one stored block turned into what a reader sees (T-513).
 * Split out of `screens/Chronicle.tsx`; the seam is `ChronicleBlockView`.
 */
import React, { createContext, useContext, useEffect, useMemo, useState } from 'react';
import ReactMarkdown from 'react-markdown';
import { Link } from 'react-router';
import { mdComponents, mdPlugins } from './components.js';
import { Cluster, Tag, type TagKind } from './ui.js';
import type { ChronicleBlock } from './types.js';

/**
 * The mapping the file header explains. Kept as data so a new state cannot be added silently.
 * `derived` maps to NO chip (T-491): it is ~95% of every generated record, so a per-block tag was
 * pure noise — a derived block's provenance IS its citation, rendered as the small grey reference
 * below the text. The two states a reader must be WARNED about keep their chips.
 */
const PROVENANCE: Record<ChronicleBlock['state'], { kind: TagKind; label: string } | null> = {
  derived: null,
  authored: { kind: 'opinion', label: 'authored' },
  'derived-then-edited': { kind: 'guard', label: 'edited — citation may no longer hold' },
};

/**
 * Renders a ```mermaid fence as a diagram (T-491). The emitter inlines each feature's diagram as a
 * fence precisely because "a link to a .mmd does not render" — and until this component, neither did
 * the fence. `mermaid` is ~500KB, so it loads lazily on first sight and never rides the main bundle;
 * a load or parse failure falls back to the fenced SOURCE, never a blank block.
 */
function Mermaid({ text }: { text: string }) {
  const [svg, setSvg] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);
  const idRef = useMemo(() => `chr-mmd-${Math.random().toString(36).slice(2)}`, []);
  useEffect(() => {
    let live = true;
    import('mermaid')
      .then(async (m) => {
        m.default.initialize({ startOnLoad: false, securityLevel: 'strict', theme: 'neutral' });
        const { svg: out } = await m.default.render(idRef, text);
        if (live) setSvg(out);
      })
      .catch(() => { if (live) setFailed(true); });
    return () => { live = false; };
  }, [text, idRef]);
  if (failed) return <pre className="chr-mermaid-src"><code>{text}</code></pre>;
  if (!svg) return <div className="chr-mermaid" aria-busy="true" />;
  // Mermaid's output is SVG it generated from the fence under securityLevel: 'strict'.
  return <div className="chr-mermaid" dangerouslySetInnerHTML={{ __html: svg }} />;
}

/**
 * NEVER add an `allow-*` token to the frame below: `allow-scripts` re-enables execution, and with
 * `allow-same-origin` the spec calls it equivalent to removing the sandbox. The CSP is defence in
 * depth, not the boundary. Why a sandbox and not a sanitizer: CHANGELOG T-513.
 */
const FRAME_CSP = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:";

const frameDoc = (html: string): string =>
  `<!doctype html><html><head><meta charset="utf-8">` +
  `<meta http-equiv="Content-Security-Policy" content="${FRAME_CSP}">` +
  `<style>html{color-scheme:light dark}body{margin:0;font:13px/1.6 system-ui,sans-serif}</style>` +
  `</head><body>${html}</body></html>`;

/** Would this markup put anything on screen? A blank frame reads as an empty record. */
function rendersSomething(html: string): boolean {
  if (!html.trim()) return false;
  try {
    const doc = new DOMParser().parseFromString(html, 'text/html');
    if (doc.querySelector('parsererror')) return false;
    return !!doc.body?.textContent?.trim() || (doc.body?.children.length ?? 0) > 0;
  } catch {
    return false;
  }
}

/** Degrades to the SOURCE, the way `Mermaid` degrades to its fence. */
function HtmlFrame({ text }: { text: string }) {
  const ok = useMemo(() => rendersSomething(text), [text]);
  if (!ok) return <pre className="chr-html-src"><code>{text}</code></pre>;
  return (
    // Height is CAPPED and the frame's own document scrolls. It cannot be measured and grown to fit:
    // without `allow-same-origin` there is no reach into the frame, which is the point.
    <iframe
      className="chr-html"
      title="Embedded HTML from this record"
      sandbox=""
      referrerPolicy="no-referrer"
      loading="lazy"
      srcDoc={frameDoc(text)}
    />
  );
}

/** A hast/mdast node, read structurally. Local so this file does not depend on `hast` typings. */
type MdNode = {
  type?: string;
  tagName?: string;
  value?: string;
  url?: string;
  title?: string;
  properties?: Record<string, unknown>;
  children?: MdNode[];
};

const textOf = (n: MdNode): string => n.value ?? (n.children ?? []).map(textOf).join('');

/**
 * A bare record reference in prose — 842 of them across the records, 0 markdown links (T-513).
 * The leading group refuses a match preceded by a word char, `/` or `-`, which keeps a file path
 * like `docs/chronicle/adr/0001-….md` from being linkified halfway through.
 * THE NAMESPACES ARE THE ONES THE EMITTER MINTS: `adr/` (`sweep.py:575`), `topic/` and `feature/`
 * (`emit.py:631,635`). Plural `topics`/`features` is a repo path and can never resolve.
 */
const RECORD_SLUG = /(^|[^\w/-])((?:adr|topic|feature)\/[a-z0-9][a-z0-9-]*)/g;

/** Split one text node around every slug that RESOLVES. Returns [] when none did. */
function splitSlugs(value: string, resolve: (slug: string) => string | null): MdNode[] {
  const out: MdNode[] = [];
  let last = 0;
  for (const m of value.matchAll(RECORD_SLUG)) {
    const slug = m[2]!;
    const id = resolve(slug);
    // An unresolved slug CLAIMS NOTHING: the index is paged, so "not in what I loaded" is not
    // "does not exist". It stays plain text — never a dead link, never a "missing record" label.
    if (!id) continue;
    const at = m.index + m[1]!.length;
    if (at > last) out.push({ type: 'text', value: value.slice(last, at) });
    out.push({ type: 'link', url: `/chronicle/${id}`, title: slug, children: [{ type: 'text', value: slug }] });
    last = at + slug.length;
  }
  if (!out.length) return [];
  if (last < value.length) out.push({ type: 'text', value: value.slice(last) });
  return out;
}

/** remark plugin: turn resolvable record slugs in prose into links. Code and links are left alone. */
const recordLinks = (resolve: (slug: string) => string | null) => () => (tree: MdNode) => {
  const walk = (node: MdNode): void => {
    if (!node.children || node.type === 'link') return;
    const out: MdNode[] = [];
    let changed = false;
    for (const child of node.children) {
      if (child.type !== 'text' || !child.value) {
        walk(child);
        out.push(child);
        continue;
      }
      const parts = splitSlugs(child.value, resolve);
      if (!parts.length) { out.push(child); continue; }
      changed = true;
      out.push(...parts);
    }
    if (changed) node.children = out;
  };
  walk(tree);
};

/**
 * The lines a reader must not skim past. ANCHORED to where the marker OPENS a block: `REJECTED`
 * occurs 222 times in the records but only 7 of those open one, so matching anywhere would
 * decorate 215 sentences discussing the word. Case is exact — records shout these markers.
 */
const CALLOUT = /^\s*(coverage:\s*(?:partial|full)|NOT VERIFIED|REJECTED)\b/;

const calloutTone = (text: string): 'warn' | 'ok' | null => {
  const m = CALLOUT.exec(text);
  if (!m) return null;
  return /^coverage:\s*full$/.test(m[1]!) ? 'ok' : 'warn';
};

/** True inside a callout already — stops a blockquote nesting a second one in its `<p>`. */
const InCallout = createContext(false);

function Callout({ tone, children }: { tone: 'warn' | 'ok'; children: React.ReactNode }) {
  return (
    <InCallout.Provider value={true}>
      <div className={`chr-callout is-${tone}`} role="note">{children}</div>
    </InCallout.Provider>
  );
}

/** The language on a fence, read off the `<code>` hast node under a `<pre>`. */
const fenceLang = (node: MdNode | undefined): string | null => {
  const code = node?.children?.find((c) => c.tagName === 'code');
  const cls = code?.properties?.['className'];
  const list = Array.isArray(cls) ? cls.map(String) : typeof cls === 'string' ? [cls] : [];
  for (const c of list) {
    const m = /^language-([\w+-]+)$/.exec(c);
    if (m) return m[1]!;
  }
  return null;
};

/** `## Not verified` -> `not-verified`, scoped by block id so two blocks cannot share a DOM id. */
const anchorId = (blockId: string, text: string): string =>
  `${blockId.replace(/[^a-zA-Z0-9]+/g, '-')}-${text.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 60)}`;

/**
 * The markdown component set for ONE block. Per-block because heading anchors need the block id:
 * two blocks can both carry `## Evidence`, and a shared DOM id makes one anchor unreachable.
 */
function blockComponents(blockId: string) {
  const heading = (Tag: 'h2' | 'h3' | 'h4') =>
    function Heading({ node, children, ...props }: React.ComponentProps<'h2'> & { node?: MdNode }) {
      const id = anchorId(blockId, textOf(node ?? {}));
      return (
        <Tag {...props} id={id} className="chr-h">
          {children}
          {/* `tabIndex={-1}`, never `aria-hidden`: hiding a FOCUSABLE element from assistive tech
              is a WCAG violation. The glyph is decoration; the heading text is what gets read. */}
          <a className="chr-anchor" href={`#${id}`} title="Link to this section" tabIndex={-1}>#</a>
        </Tag>
      );
    };

  const blockish = <T extends 'p' | 'blockquote' | 'li'>(Tag: T) =>
    function Blockish({ node, children, ...props }: React.ComponentProps<T> & { node?: MdNode }) {
      const nested = useContext(InCallout);
      const tone = nested ? null : calloutTone(textOf(node ?? {}));
      if (!tone) return React.createElement(Tag, props as object, children);
      // A list-item callout goes INSIDE the <li>. `<ul>`/`<ol>` admit only `li`, `script` and
      // `template`, so wrapping the item puts a <div> where the content model forbids one.
      return Tag === 'li'
        ? React.createElement(Tag, props as object, <Callout tone={tone}>{children}</Callout>)
        : <Callout tone={tone}>{React.createElement(Tag, props as object, children)}</Callout>;
    };

  return {
    ...mdComponents,
    a: ({ href, children, ...props }: React.ComponentProps<'a'>) =>
      // Router navigation: `mdComponents.a` opens every link in a new tab, which is right for an
      // outbound reference and wrong for a record in this same app.
      href?.startsWith('/chronicle/') ? (
        <Link className="chr-record-link" to={href} title={props.title ?? undefined}>{children}</Link>
      ) : (
        <a {...props} href={href} target="_blank" rel="noreferrer">{children}</a>
      ),
    h2: heading('h2'),
    h3: heading('h3'),
    h4: heading('h4'),
    p: blockish('p'),
    blockquote: blockish('blockquote'),
    li: blockish('li'),
    table: (props: React.ComponentProps<'table'>) => (
      <div className="chr-table-wrap"><table {...props} /></div>
    ),
    pre: ({ node, children, ...props }: React.ComponentProps<'pre'> & { node?: MdNode }) => {
      const lang = fenceLang(node);
      // Both render their OWN container, and each fallback is itself a <pre> — so the default
      // wrapper would nest a <pre> in a <pre>.
      if (lang === 'mermaid' || lang === 'html') return <>{children}</>;
      if (!lang) return <pre {...props}>{children}</pre>;
      // The language, made visible — 42 non-mermaid fences rendered identically before this.
      return <div className="chr-code" data-lang={lang}><pre {...props}>{children}</pre></div>;
    },
    code: (props: React.ComponentProps<'code'> & { className?: string }) => {
      const lang = /language-(\w+)/.exec(props.className ?? '')?.[1];
      if (lang === 'mermaid') return <Mermaid text={String(props.children ?? '')} />;
      if (lang === 'html') return <HtmlFrame text={String(props.children ?? '')} />;
      const Base = (mdComponents as Record<string, React.ElementType>)['code'];
      return Base ? <Base {...props} /> : <code {...props} />;
    },
  };
}

function Citations({ block }: { block: ChronicleBlock }) {
  if (!block.citations.length) return null;
  return (
    <Cluster gap={8} wrap className="chr-cites">
      {block.citations.map((c) => (
        // The full triple is what RESOLVES; the short id is only what we show. A bare `T-52` is not
        // unique even within one project, so the link carries the triple.
        // THE TICKET, not the board: a citation exists to answer "where did this sentence come
        // from", and a board link makes the reader hunt for the ticket the record already named.
        // /tasks/:taskId opens the task with its detail pane (App.tsx TaskPage route).
        <Link
          key={`${c.projectId}/${c.boardId}/${c.taskId}`}
          to={`/projects/${c.projectId}/boards/${c.boardId}/tasks/${c.taskId}`}
          title={`${c.taskId} — open the ticket this is derived from`}
        >
          {c.label ?? c.taskId}
        </Link>
      ))}
    </Cluster>
  );
}

/** Nothing resolves — the default when a page has no slug map yet. Every slug stays plain text. */
const NO_RESOLVE = (): null => null;

/** ONE stored block, rendered. The page decides which blocks lead and fold; this decides looks. */
export function ChronicleBlockView({
  block,
  resolveSlug = NO_RESOLVE,
}: {
  block: ChronicleBlock;
  /** slug -> page id, for internal record links. Absent means no link is offered, never a dead one. */
  resolveSlug?: (slug: string) => string | null;
}) {
  const chip = PROVENANCE[block.state];
  const components = useMemo(() => blockComponents(block.id), [block.id]);
  // Composed off the SHARED list rather than holding its own `remarkGfm`: the ticket surfaces and the
  // Chronicle rendered the same bytes two ways until `mdPlugins` existed, and a second reference here
  // is how that comes back.
  const plugins = useMemo(() => [...(mdPlugins ?? []), recordLinks(resolveSlug)], [resolveSlug]);
  return (
    <div className={block.state === 'derived-then-edited' ? 'chr-block is-drifted' : 'chr-block'}>
      {chip ? <Tag kind={chip.kind}>{chip.label}</Tag> : null}
      {/* MARKDOWN, for derived and authored blocks alike — with GFM, because every evidence table
          the sweep emits is a GFM pipe table and rendered as a flat paragraph without it (T-491,
          measured on the hosted page). */}
      <ReactMarkdown components={components} remarkPlugins={plugins}>{block.text}</ReactMarkdown>
      <Citations block={block} />
    </div>
  );
}
