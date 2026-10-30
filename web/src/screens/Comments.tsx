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
import { Cluster, Stack } from '../ui.js';
import { InlineLoader } from '../brand.js';

const label: React.CSSProperties = { fontSize: 11, fontWeight: 600, letterSpacing: 0.4, textTransform: 'uppercase', color: 'var(--text-muted)' };

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
  onReply: (c: Comment) => void;
  /** Account id → display name (P-314): the author is a `U-<hex>` id, never shown raw. */
  names: DisplayNames;
}) {
  const c = node.comment;
  const author = personName(c.author, names);
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
          <button
            className="btn secondary"
            style={{ marginLeft: 'auto', padding: '1px 8px', fontSize: 11 }}
            onClick={() => onReply(c)}
          >
            Reply
          </button>
        </Cluster>
        <div className="md" style={{ marginTop: 6, fontSize: 14 }}>
          <ReactMarkdown components={mdComponents} remarkPlugins={mdPlugins}>{c.body}</ReactMarkdown>
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
}: {
  projectId: string;
  taskId: string;
  mentionCandidates: string[];
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

  // @mention autocomplete: when the caret follows an "@partial", suggest matching candidates.
  const [mentionQuery, setMentionQuery] = useState<string | null>(null);
  const suggestions = useMemo(() => {
    if (mentionQuery == null) return [];
    const q = mentionQuery.toLowerCase();
    return mentionCandidates.filter((c) => c.toLowerCase().includes(q)).slice(0, 6);
  }, [mentionQuery, mentionCandidates]);

  const onBodyChange = (text: string, caret: number): void => {
    setBody(text);
    const before = text.slice(0, caret);
    const m = /(?:^|\s)@([\w.@%+-]*)$/.exec(before);
    setMentionQuery(m ? m[1] ?? '' : null);
  };

  const insertMention = (handle: string): void => {
    const ta = taRef.current;
    const caret = ta ? ta.selectionStart : body.length;
    const before = body.slice(0, caret).replace(/@([\w.@%+-]*)$/, `@${handle} `);
    const next = before + body.slice(caret);
    setBody(next);
    setMentionQuery(null);
    queueMicrotask(() => ta?.focus());
  };

  const post = async (): Promise<void> => {
    const text = body.trim();
    if (!text || busy) return;
    setBusy(true);
    setErr(null);
    try {
      await commentsApi.addComment(projectId, taskId, text, replyTo?.id);
      setBody('');
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
        {threads.length === 0 && (
          <p style={{ margin: 0, fontSize: 12, color: 'var(--text-2)', fontStyle: 'italic' }}>No comments yet.</p>
        )}
        {threads.map((t) => (
          <CommentNode key={t.comment.id} node={t} depth={0} onReply={setReplyTo} names={names} />
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
          value={body}
          onChange={(e) => onBodyChange(e.target.value, e.target.selectionStart)}
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
              <button key={s} className="assign-item" onClick={() => insertMention(s)}>
                <Avatar id={s} /> <span style={{ fontSize: 12 }}>{s}</span>
              </button>
            ))}
          </div>
        )}
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginTop: 6 }}>
          {/* A disabled button is not a busy affordance: it says "you cannot press this", not "it is
              working". On a slow post the row went silent and people pressed again. `aria-busy` plus
              the mark carries it without the label jumping width. */}
          <button className="btn" disabled={!body.trim() || busy} aria-busy={busy} onClick={() => void post()}
                  style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
            {busy && <InlineLoader size={16} label="Posting" />}
            {busy ? 'Posting…' : replyTo ? 'Reply' : 'Comment'}
          </button>
          <span className="muted" style={{ fontSize: 11 }}>⌘↵ to send</span>
          {err && <span className="error" style={{ fontSize: 11 }}>{err}</span>}
        </div>
      </div>
    </section>
  );
}
