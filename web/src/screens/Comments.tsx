/**
 * Threaded task comments (T-32): a nested-reply discussion thread with markdown bodies and an @mention
 * autocomplete composer. Reads/writes through the gateway comment routes; invalidates the drawer's
 * `comments` + `hist` queries on post so the new comment (and its activity event) appear live.
 */
import { useMemo, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import ReactMarkdown from 'react-markdown';
import * as commentsApi from '../api/comments.js';
import { Avatar, mdComponents, mdPlugins, personName, timeAgo, type DisplayNames } from '../components.js';
import { useDisplayNames } from '../displayNames.js';
import type { Comment } from '../types.js';
import { Cluster, ReadFailed, Stack } from '../ui.js';
import { InlineLoader } from '../brand.js';

const label: React.CSSProperties = { fontSize: 11, fontWeight: 600, letterSpacing: 0.4, textTransform: 'uppercase', color: 'var(--text-muted)' };

/**
 * MENTIONS NAME PEOPLE, NOT ACCOUNT IDS (P-314). A member is a `U-<hex>` account id. The server
 * resolves an @token by that id, which names exactly one member, or by an alias (a login, a display
 * name, the local part of either), which every member sharing it answers to
 * (`internal/service/comments.go` mentionsOf, `internal/domain/mentions.go`). So a picked person is
 * posted by their id: the textarea shows `@<their name>`, the draft remembers where each pick sits
 * and whose it is (one entry per occurrence, so two people with one name stay two), each edit moves
 * the picks after it (placed by the caret as well as the text) and drops a pick it touched, and the
 * post replaces each pick still intact where it sits with `@<id>`. A dropped pick's text is posted as
 * typed, which the server resolves as before. An agent label is inserted and posted as itself. A
 * body's `@U-<hex>` renders as the name, else the shortened id, as text: a name is never read as
 * markdown, and code is left as written.
 */
const ACCOUNT_ID = /^U-[0-9a-f-]{8,}$/i;
/**
 * What extends a handle on the server (`mentionRE`): more handle characters that end in a letter or
 * digit, or an `@domain`. Text starting with this, right after an id, makes the server read another
 * handle, so the id is not the mention there.
 */
const HANDLE_TAIL = /^(?:[a-zA-Z0-9._%+-]*[a-zA-Z0-9]|@[a-zA-Z0-9.-]{1,251}\.[a-zA-Z]{2,24})/;
/** A character before an `@` that makes it part of a word, not a mention (the server's left boundary). */
const HANDLE_CHAR = /[a-zA-Z0-9._%+@-]/;
/** An `@<account id>` the server reads as that id: a boundary before it, no handle tail after it. */
const ACCOUNT_MENTION = /(^|[^a-zA-Z0-9._%+@-])@(U-[0-9a-f-]{7,}[0-9a-f])(?![a-zA-Z0-9._%+-]*[a-zA-Z0-9]|@[a-zA-Z0-9.-]{1,251}\.[a-zA-Z]{2,24})/gi;

/** A pick in the draft: `@<name>` starting at `at`, for the account `id`. */
export interface Pick { at: number; name: string; id: string }

/**
 * The picks after the draft changed from `prev` to `next`, with the caret at `caret` in `next`: a pick
 * before the edit stays, one after it moves with it, and one the edit touched is dropped (its text is
 * no longer what was picked).
 *
 * The text alone cannot place an edit when the changed text repeats its neighbour: deleting the first
 * "@Ann " of "@Ann @Ann Lee" reads as deleting "@Ann " after the first one just as well, which would
 * keep the deleted pick and drop the other. The caret settles it, because a keystroke, a paste or a
 * deleted selection leaves it at the end of what was typed (where the deletion was, for a deletion).
 * So the unchanged head ends by `caret - max(0, shift)`, where typed text began, and the unchanged
 * tail starts at the caret or later.
 */
export function movePicks(picks: Pick[], prev: string, next: string, caret: number): Pick[] {
  const shift = next.length - prev.length;
  const c = Math.min(Math.max(caret, 0), next.length);
  const headMax = Math.max(0, Math.min(prev.length, next.length, c - Math.max(0, shift)));
  const tailMax = next.length - c;
  let head = 0;
  while (head < headMax && prev[head] === next[head]) head++;
  let tail = 0;
  while (tail < tailMax && tail < prev.length - head && tail < next.length - head && prev[prev.length - 1 - tail] === next[next.length - 1 - tail]) tail++;
  const editEnd = prev.length - tail;
  return picks.flatMap((k) => {
    const end = k.at + 1 + k.name.length;
    if (end <= head) return [k];
    if (k.at >= editEnd) return [{ ...k, at: k.at + shift }];
    return [];
  });
}

/**
 * The body as posted: each pick whose `@<name>` is still intact where it was put (a mention boundary
 * before it, nothing after it the server would read as more handle) becomes `@<its id>`. Picks are applied from the end
 * so an earlier one's offset still holds.
 */
export function wireBody(body: string, picks: Pick[]): string {
  let out = body;
  for (const k of [...picks].sort((a, b) => b.at - a.at)) {
    const end = k.at + 1 + k.name.length;
    const intact = out.startsWith(`@${k.name}`, k.at)
      && (k.at === 0 || !HANDLE_CHAR.test(out[k.at - 1]!))
      && !HANDLE_TAIL.test(out.slice(end));
    if (intact) out = `${out.slice(0, k.at)}@${k.id}${out.slice(end)}`;
  }
  return out;
}

interface MdNode { type: string; value?: string; children?: MdNode[] }

/**
 * A remark step naming a body's `@<account id>` mentions in its text nodes only. The name lands in a
 * text node's value, so it renders as written (no markdown in it is read); `inlineCode` and `code`
 * nodes are not text nodes, so an id the author quoted as code stays as they wrote it.
 */
function nameMentions(names: DisplayNames) {
  const walk = (n: MdNode): void => {
    if (n.type === 'text' && n.value) {
      n.value = n.value.replace(ACCOUNT_MENTION, (_m, pre: string, id: string) => `${pre}@${personName(id, names)}`);
    }
    n.children?.forEach(walk);
  };
  return () => (tree: MdNode) => walk(tree);
}

interface TreeNode {
  comment: Comment;
  children: TreeNode[];
}

/** Build a parent→children tree from the flat, chronological comment list. Orphan replies (parent not in
 * the page) fall back to top-level so nothing is dropped. */
function buildThreads(comments: Comment[]): TreeNode[] {
  const byId = new Map<string, TreeNode>();
  for (const c of comments) byId.set(c.id, { comment: c, children: [] });
  const roots: TreeNode[] = [];
  for (const node of byId.values()) {
    const parentId = node.comment.parentCommentId;
    const parent = parentId ? byId.get(parentId) : undefined;
    if (parent) parent.children.push(node);
    else roots.push(node);
  }
  return roots;
}

function CommentNode({
  node,
  depth,
  onReply,
  names,
}: {
  node: TreeNode;
  depth: number;
  /** Absent when the caller cannot comment: a reply is a comment, so no Reply is offered (P-432). */
  onReply?: (c: Comment) => void;
  /** Account id → display name (P-314): the author is a `U-<hex>` id, never shown raw. */
  names: DisplayNames;
}) {
  const c = node.comment;
  const author = personName(c.author, names);
  const plugins = useMemo(() => [...(mdPlugins ?? []), nameMentions(names)], [names]);
  return (
    <div style={{ marginLeft: depth > 0 ? 16 : 0, borderLeft: depth > 0 ? '1px solid var(--border)' : undefined, paddingLeft: depth > 0 ? 12 : 0 }}>
      <div className="card" style={{ padding: 10 }}>
        <Cluster gap={4}>
          <Avatar id={c.author} label={author} nameOnly />
          <span style={{ fontSize: 12, fontWeight: 600 }}>{author}</span>
          <span className="muted" style={{ fontSize: 11 }}>{timeAgo(c.ts)}</span>
          {c.mentions.length > 0 && (
            <span className="chip" title={`mentioned ${c.mentions.map((m) => personName(m, names)).join(', ')}`}>@{c.mentions.length}</span>
          )}
          {onReply && (
            <button
              className="btn secondary"
              style={{ marginLeft: 'auto', padding: '1px 8px', fontSize: 11 }}
              onClick={() => onReply(c)}
            >
              Reply
            </button>
          )}
        </Cluster>
        <div className="md" style={{ marginTop: 6, fontSize: 14 }}>
          <ReactMarkdown components={mdComponents} remarkPlugins={plugins}>{c.body}</ReactMarkdown>
        </div>
      </div>
      {node.children.length > 0 && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginTop: 6 }}>
          {node.children.map((child) => (
            <CommentNode key={child.comment.id} node={child} depth={depth + 1} onReply={onReply} names={names} />
          ))}
        </div>
      )}
    </div>
  );
}

export function Comments({
  projectId,
  taskId,
  mentionCandidates,
  canComment,
  closedWhy,
}: {
  projectId: string;
  taskId: string;
  mentionCandidates: string[];
  /**
   * May the caller post here? `add_comment` is tier write, so a viewer may not (P-432). Required, so a
   * new caller decides rather than inheriting an open composer. Closed, the composer and Reply are
   * disabled and `closedWhy` says why; the server still refuses whatever reaches it.
   */
  canComment: boolean;
  /** The reason shown beside the closed composer; absent while the caller's role is still loading. */
  closedWhy?: string;
}) {
  const qc = useQueryClient();
  const names = useDisplayNames(projectId);
  const commentsQ = useQuery({
    queryKey: ['comments', projectId, taskId],
    queryFn: () => commentsApi.listComments(projectId, taskId),
  });
  const [body, setBody] = useState('');
  const [replyTo, setReplyTo] = useState<Comment | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const taRef = useRef<HTMLTextAreaElement>(null);

  // @mention autocomplete: when the caret follows an "@partial", suggest the candidates whose name
  // matches it. A candidate is an account id or an agent label; it is shown and matched by its name.
  const [mentionQuery, setMentionQuery] = useState<string | null>(null);
  // Each person this draft picked, where their name sits (one entry per occurrence).
  const [picks, setPicks] = useState<Pick[]>([]);
  // Two people can share a name: those get a hint, the full login when the web holds it (a member
  // with no display name), else the shortened id.
  const candidates = useMemo(() => {
    const named = mentionCandidates.map((id) => ({ id, name: personName(id, names) }));
    const count = new Map<string, number>();
    for (const c of named) count.set(c.name, (count.get(c.name) ?? 0) + 1);
    return named.map((c) => {
      if ((count.get(c.name) ?? 0) < 2) return { ...c, hint: null };
      const full = personName(c.id, names, { full: true });
      return { ...c, hint: full !== c.name ? full : ACCOUNT_ID.test(c.id) ? `${c.id.slice(0, 10)}…` : null };
    });
  }, [mentionCandidates, names]);
  const suggestions = useMemo(() => {
    if (mentionQuery == null) return [];
    const q = mentionQuery.toLowerCase();
    return candidates.filter((c) => c.name.toLowerCase().includes(q)).slice(0, 6);
  }, [mentionQuery, candidates]);

  // `editEnd` is where the change left the selection's end: the caret after typing or deleting, and the
  // end of the restored text when an undo selects it.
  const onBodyChange = (text: string, caret: number, editEnd: number): void => {
    setPicks((ks) => movePicks(ks, body, text, editEnd));
    setBody(text);
    const before = text.slice(0, caret);
    const m = /(?:^|\s)@([\w.@%+-]*)$/.exec(before);
    setMentionQuery(m ? m[1] ?? '' : null);
  };

  const insertMention = ({ id, name }: { id: string; name: string }): void => {
    const account = ACCOUNT_ID.test(id);
    const handle = account ? name : id; // an agent label is its own handle
    const ta = taRef.current;
    const caret = ta ? ta.selectionStart : body.length;
    // A function replacement: a name is text, and `$1` or `$&` in it must not be expanded.
    const before = body.slice(0, caret).replace(/@([\w.@%+-]*)$/, () => `@${handle} `);
    const next = before + body.slice(caret);
    const at = before.length - handle.length - 2;
    // An agent label is posted as inserted, so it is no pick: its name (personName cuts "bot@ci" to
    // "bot") is not its text.
    setPicks((ks) => [...movePicks(ks, body, next, before.length), ...(account ? [{ at, name, id }] : [])]);
    setBody(next);
    setMentionQuery(null);
    queueMicrotask(() => ta?.focus());
  };

  const post = async (): Promise<void> => {
    const text = wireBody(body, picks).trim();
    if (!text || busy || !canComment) return;
    setBusy(true);
    setErr(null);
    try {
      await commentsApi.addComment(projectId, taskId, text, replyTo?.id);
      setBody('');
      setPicks([]);
      setReplyTo(null);
      await qc.invalidateQueries({ queryKey: ['comments', projectId, taskId] });
      void qc.invalidateQueries({ queryKey: ['hist', projectId] });
    } catch (e) {
      setErr(String(e instanceof Error ? e.message : e));
    } finally {
      setBusy(false);
    }
  };

  const threads = useMemo(() => buildThreads(commentsQ.data?.comments ?? []), [commentsQ.data]);

  return (
    <section>
      <h3 style={{ ...label, margin: '0 0 8px' }}>Comments</h3>
      <Stack gap={4}>
        {/* A failed read says so; "No comments yet." only once a read has answered with none. */}
        {commentsQ.isError && (
          <ReadFailed what="the comments" error={commentsQ.error} stale={!!commentsQ.data} onRetry={() => void commentsQ.refetch()} />
        )}
        {commentsQ.isSuccess && threads.length === 0 && (
          <p style={{ margin: 0, fontSize: 12, color: 'var(--text-2)', fontStyle: 'italic' }}>No comments yet.</p>
        )}
        {threads.map((t) => (
          <CommentNode key={t.comment.id} node={t} depth={0} onReply={canComment ? setReplyTo : undefined} names={names} />
        ))}
      </Stack>

      <div style={{ marginTop: 8, position: 'relative' }}>
        {replyTo && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 11, marginBottom: 4 }}>
            <span className="muted">Replying to {personName(replyTo.author, names)}</span>
            <button className="btn secondary" style={{ padding: '0 6px' }} onClick={() => setReplyTo(null)}>×</button>
          </div>
        )}
        <textarea
          ref={taRef}
          className="input"
          style={{ width: '100%', minHeight: 64, resize: 'vertical', fontFamily: 'inherit' }}
          placeholder="Write a comment… markdown supported, @mention a teammate"
          disabled={!canComment}
          aria-describedby={closedWhy ? 'comment-closed' : undefined}
          value={body}
          onChange={(e) => onBodyChange(e.target.value, e.target.selectionStart, e.target.selectionEnd)}
          onKeyDown={(e) => {
            if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') void post();
            if (e.key === 'Escape') {
              // Escape with the @mention popup VISIBLE only dismisses the popup — stopPropagation keeps
              // the drawer's window-level Escape handler from also blurring the composer mid-sentence.
              // With no visible popup (no query, or zero matches) Escape falls through to the blur.
              if (mentionQuery != null && suggestions.length > 0) e.stopPropagation();
              setMentionQuery(null);
            }
          }}
        />
        {suggestions.length > 0 && (
          <div className="assign-menu" style={{ position: 'absolute', left: 8, bottom: 44, zIndex: 20 }}>
            {suggestions.map((s) => (
              <button key={s.id} className="assign-item" onClick={() => insertMention(s)}>
                <Avatar id={s.id} label={s.name} nameOnly /> <span className="mention-name" style={{ fontSize: 12 }}>{s.name}</span>
                {s.hint && <span className="muted" style={{ fontSize: 11 }}> {s.hint}</span>}
              </button>
            ))}
          </div>
        )}
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 6 }}>
          {/* A disabled button is not a busy affordance: it says "you cannot press this", not "it is
              working". On a slow post the row went silent and people pressed again. `aria-busy` plus
              the mark carries it without the label jumping width. */}
          <button className="btn" disabled={!canComment || !body.trim() || busy} aria-busy={busy} onClick={() => void post()}
                  title={closedWhy} aria-describedby={closedWhy ? 'comment-closed' : undefined}
                  style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
            {busy && <InlineLoader size={16} label="Posting" />}
            {busy ? 'Posting…' : replyTo ? 'Reply' : 'Comment'}
          </button>
          {closedWhy ? (
            <span id="comment-closed" className="muted" style={{ fontSize: 12 }}>{closedWhy}</span>
          ) : (
            <span className="muted" style={{ fontSize: 11 }}>⌘↵ to send</span>
          )}
          {err && <span className="error" style={{ fontSize: 11 }}>{err}</span>}
        </div>
      </div>
    </section>
  );
}
