/**
 * The ticket-record panels — the ONE copy of each. Both detail surfaces render these: the drawer
 * (`TaskDrawer.tsx`, quick triage over the board) and the full page (`TaskPage.tsx`, the record).
 *
 * Every panel takes the shared `LoadedTaskDetail` from `useTaskDetail` and nothing else, so adding a
 * field means touching one file rather than two screens that quietly disagree.
 */
import { Fragment, useRef, useState } from 'react';
import { useDisplayNames } from '../../displayNames.js';
import { Icon } from '../../icons.js';
import ReactMarkdown from 'react-markdown';
import * as contextApi from '../../api/context.js';
import * as tasksApi from '../../api/tasks.js';
import { Avatar, Marquee, StatusPill, eventActor, eventVerb, mdComponents, mdPlugins, personName, timeAgo } from '../../components.js';
import { AppLinkCard, normalizeLink } from '../../AppLink.js';
import { Bar, Button, Cluster, KV, Note, Panel, Stack } from '../../ui.js';
import { Comments } from '../Comments.js';
import { FollowUps } from '../FollowUps.js';
import { LIMITS, type Artifact, type Task } from '../../types.js';
import type { LoadedTaskDetail } from './useTaskDetail.js';

const SCOLOR_DOT: Record<string, string> = { TODO: '--ready', IN_PROGRESS: '--progress', BLOCKED: '--blocked', REVIEW: '--review', DONE: '--done' };
export const statusDot = (s: string | undefined): string => `var(${(s && SCOLOR_DOT[s]) ?? '--text-muted'})`;

// customFields that are really task metadata (legacy agents write effort/rank/model there) — surfaced
// as compact header meta chips next to the modelHint chip instead of the deletable Fields rows.
export const META_CF: string[] = ['effort', 'rank', 'model'];

const label = { fontFamily: 'var(--mono)', fontSize: 10, letterSpacing: '0.04em', textTransform: 'uppercase' as const, color: 'var(--text-muted)' };

/** Panels are `.record` boxes; a bare id chip beside one keeps the mono/id treatment. */
type P = { d: LoadedTaskDetail };
type PNav = P & { onOpenTask?: (taskId: string) => void };

// Module-level (NOT defined inside a render): an inline component type would be recreated every
// render, forcing React to unmount/remount the chip — which drops focus and swallows clicks while the
// 1s lease countdown is re-rendering the surface.
export function DepChip({ id, linked, onOpen }: { id: string; linked?: Task; onOpen?: (taskId: string) => void }) {
  const chip = (
    <>
      <span className="dot" style={{ width: 6, height: 6, flex: '0 0 auto', background: statusDot(linked?.status) }} />
      <span className="mono" style={{ fontSize: 12, color: 'var(--text-2)', flex: '0 0 auto' }}>{id}</span>
      {/* Same truncation treatment as the board card labels — hover/focus the chip and the full title
          marquees past. No `label`: the enclosing button's title already carries it. */}
      {linked && (
        <Marquee style={{ fontSize: 12, color: 'var(--text)', maxWidth: 180 }}>{linked.title}</Marquee>
      )}
    </>
  );
  // Clickable when the surface can navigate — walks the dependency chain without leaving it.
  return onOpen ? (
    <button
      type="button"
      title={linked ? `${linked.title} — open ${id}` : `Open ${id}`}
      onClick={() => onOpen(id)}
      style={{ display: 'inline-flex', alignItems: 'center', gap: 6, padding: '2px 8px', borderRadius: 'var(--r-1)', background: 'var(--panel)', border: '1px solid var(--border)', cursor: 'pointer', font: 'inherit' }}
    >
      {chip}
    </button>
  ) : (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, padding: '2px 8px', borderRadius: 'var(--r-1)', background: 'var(--panel)', border: '1px solid var(--border)' }}>
      {chip}
    </span>
  );
}

/* ---- description + digest ------------------------------------------------------------------ */

export function DescriptionPanel({ d }: P) {
  const { task, projectId, taskId, editing, setEditing, draft, setDraft, startEdit, run } = d;
  return (
    <Panel title="Description" right={<span className="mono">markdown · updated {timeAgo(task.updatedAt)}</span>} pad>
      {editing ? (
        <>
          <textarea className="input" maxLength={LIMITS.description} style={{ width: '100%', minHeight: 100 }} value={draft.description} onChange={(e) => setDraft({ ...draft, description: e.target.value })} />
          <Button style={{ marginTop: 8 }} variant="primary" onClick={() => void run(tasksApi.updateTask(projectId, taskId, task.version, { title: draft.title, description: draft.description }), () => setEditing(false))}>Save</Button>
        </>
      ) : task.description ? (
        // Agents write markdown briefs — render them (same .md styling as comments) instead of raw text.
        <div className="md" style={{ color: 'var(--text-2)', fontSize: 14, lineHeight: 1.5 }} onDoubleClick={startEdit}>
          <ReactMarkdown components={mdComponents} remarkPlugins={mdPlugins}>{task.description}</ReactMarkdown>
        </div>
      ) : (
        <p className="muted" style={{ margin: 0, fontSize: 14 }} onDoubleClick={startEdit}>
          No description — click Edit (or double-click) to add the brief agents will work from.
        </p>
      )}
    </Panel>
  );
}

/** The agent-written rolling summary (`set_summary`). Editable in place. */
export function DigestPanel({ d }: P) {
  const { task, projectId, taskId, ctx, run } = d;
  const [summaryDraft, setSummaryDraft] = useState<string | null>(null);
  return (
    <Panel title="Agent digest" pad>
      <div onDoubleClick={() => summaryDraft === null && setSummaryDraft(task.contextSummary)}>
        {summaryDraft === null ? (
          <p style={{ margin: 0, fontSize: 14, lineHeight: 1.5, color: 'var(--text-2)' }}>
            {ctx?.summary || task.contextSummary || <span className="muted">No digest yet — agents post a rolling summary here as they work (via <span className="mono">set_summary</span>). Double-click to add one.</span>}
          </p>
        ) : (
          <>
            <textarea className="input" maxLength={LIMITS.summary} style={{ width: '100%', minHeight: 70 }} value={summaryDraft} onChange={(e) => setSummaryDraft(e.target.value)} />
            <Cluster gap={4} style={{ marginTop: 6 }}>
              <Button variant="primary" onClick={() => void run(contextApi.setSummary(projectId, taskId, task.version, summaryDraft), () => setSummaryDraft(null))}>Save summary</Button>
              <Button onClick={() => setSummaryDraft(null)}>Cancel</Button>
            </Cluster>
          </>
        )}
      </div>
    </Panel>
  );
}

/* ---- lease + actions ------------------------------------------------------------------------ */

/** Who holds the ticket and for how much longer. Renders only while a claim is live. */
export function LeasePanel({ d }: P) {
  const { task, countdown } = d;
  if (!task.claim) return null;
  return (
    <Panel title="Lease" right={<span className="mono">held · {countdown.expired ? 'expired' : `${countdown.label} left`}</span>}>
      <div className="record-body" style={{ paddingBottom: 8 }}>
        <Bar pct={countdown.frac * 100} tone={countdown.frac < 0.2 ? 'crit' : 'doing'} label={countdown.expired ? 'expired' : `${countdown.label} remaining`} />
      </div>
      <KV k="holder">
        <Cluster gap={4}>
          <Avatar id={task.claim.agent || task.claim.owner} active />
          <span>{task.claim.agent || task.claim.owner}</span>
          {task.claim.agent && task.claim.agent !== task.claim.owner && <span className="muted">· for {task.claim.owner}</span>}
        </Cluster>
      </KV>
      <KV k="leaseId"><span className="mono">{task.claim.leaseId}</span></KV>
      <KV k="on expiry">returns to <span className="role ready">To do</span>, unclaimed</KV>
    </Panel>
  );
}

/** Review ownership (T-52) — a ticket sitting in REVIEW must show WHO owns the review and how long it
 *  has waited, or it is an unowned lane again.
 *
 *  `completedBy` is the account id the core stored (`U-<hex>`), so it is named the way the header's
 *  "created by" and the comments name a person (P-431): the member's name, else their login, else the
 *  shortened id when nothing names the account. */
export function ReviewPanel({ d }: P) {
  const { task, names } = d;
  if (task.status !== 'REVIEW' || !task.review) return null;
  return (
    <Panel title="Review" pad>
      <Cluster gap={8}>
        {task.review.reviewer ? (
          <>
            <Avatar id={task.review.reviewer} />
            <span style={{ fontSize: 14 }}><strong>{personName(task.review.reviewer, names)}</strong> owns this review</span>
          </>
        ) : (
          <span style={{ fontSize: 14, color: 'var(--progress)' }}>Nobody owns this review — assign a reviewer.</span>
        )}
        <span className="muted" style={{ marginLeft: 'auto', fontSize: 11 }} title={task.review.requestedAt}>
          waiting {timeAgo(task.review.requestedAt)} · completed by {personName(task.review.completedBy, names)}
        </span>
      </Cluster>
    </Panel>
  );
}

/** What the ticket PRODUCED, in the agent's own words. The artifacts themselves live in FilesPanel. */
export function DeliveredPanel({ d }: P) {
  const { task } = d;
  if (!task.deliverable) return null;
  return (
    <Panel title="Delivered" pad>
      <div style={{ fontSize: 14, lineHeight: 1.5, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{task.deliverable}</div>
    </Panel>
  );
}

/* ---- graph ----------------------------------------------------------------------------------- */

export function DependenciesPanel({ d, onOpenTask }: PNav) {
  const { projectId, taskId, run, blockedByEdges, blocksEdges, relatesEdges, taskById } = d;
  const [depId, setDepId] = useState('');
  return (
    <Panel title="Dependencies" right={<span className="mono">blocks {blocksEdges.length} · blocked by {blockedByEdges.length}</span>} pad>
      <Stack gap={12}>
        <div>
          <h3 style={{ ...label, margin: '0 0 8px' }}>Blocked by</h3>
          {blockedByEdges.length === 0 ? (
            <p style={{ margin: 0, fontSize: 12, color: 'var(--text-2)', fontStyle: 'italic' }}>None</p>
          ) : (
            <Cluster gap={8} wrap>
              {blockedByEdges.map((e) => (
                <span key={e.id} style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                  <DepChip id={e.fromTaskId} linked={taskById.get(e.fromTaskId)} onOpen={onOpenTask} />
                  <Button title="remove" style={{ padding: '2px 8px' }} onClick={() => void run(contextApi.removeDependency(projectId, e.id))}>×</Button>
                </span>
              ))}
            </Cluster>
          )}
        </div>
        {relatesEdges.length > 0 && (
          <div>
            <h3 style={{ ...label, margin: '0 0 8px' }}>Related</h3>
            <Cluster gap={8} wrap>
              {relatesEdges.map((e) => (
                <DepChip key={e.id} id={e.fromTaskId === taskId ? e.toTaskId : e.fromTaskId} linked={taskById.get(e.fromTaskId === taskId ? e.toTaskId : e.fromTaskId)} onOpen={onOpenTask} />
              ))}
            </Cluster>
          </div>
        )}
        <div>
          <h3 style={{ ...label, margin: '0 0 8px' }}>Blocks</h3>
          {blocksEdges.length === 0 ? (
            <p style={{ margin: 0, fontSize: 12, color: 'var(--text-2)', fontStyle: 'italic' }}>None</p>
          ) : (
            <Cluster gap={8} wrap>
              {blocksEdges.map((e) => (
                <span key={e.id} style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
                  <DepChip id={e.toTaskId} linked={taskById.get(e.toTaskId)} onOpen={onOpenTask} />
                  <Button title="remove" style={{ padding: '2px 8px' }} onClick={() => void run(contextApi.removeDependency(projectId, e.id))}>×</Button>
                </span>
              ))}
            </Cluster>
          )}
        </div>
        <Cluster gap={4}>
          <input className="input" style={{ flex: 1 }} placeholder="blocked by task id (e.g. T-3)" value={depId} onChange={(e) => setDepId(e.target.value)} />
          <Button disabled={!depId} onClick={() => void run(contextApi.addDependency(projectId, depId, taskId), () => setDepId(''))}>Add</Button>
        </Cluster>
      </Stack>
    </Panel>
  );
}

export function SubtasksPanel({ d, onOpenTask }: PNav) {
  const { projectId, taskId, run, subs, subsDone, subsPct } = d;
  const [subTitle, setSubTitle] = useState('');
  return (
    <Panel title="Subtasks" right={subs.length > 0 ? <span className="mono">{subsDone} of {subs.length} complete</span> : undefined} pad>
      {subs.length > 0 && (
        <div style={{ width: '100%', background: 'var(--panel-2)', height: 4, overflow: 'hidden', marginBottom: 12 }}>
          <div style={{ background: 'var(--accent)', height: '100%', width: `${subsPct}%` }} />
        </div>
      )}
      <Stack gap={8}>
        {subs.map((s: Task) => {
          const done = s.status === 'DONE';
          // NOT a row-wide <label>: the row mixes two actions (checkbox = toggle done, title =
          // navigate), and a wrapping label would forward clicks on the id/pill/gaps to the
          // checkbox — silently marking a subtask DONE when the user meant to open it.
          return (
            <div key={s.id} style={{ display: 'flex', alignItems: 'flex-start', gap: 8 }}>
              <input
                type="checkbox"
                checked={done}
                aria-label={`Mark ${s.id} ${done ? 'not done' : 'done'}`}
                style={{ marginTop: 3, width: 14, height: 14, accentColor: 'var(--accent)', cursor: 'pointer' }}
                onChange={() => void run(tasksApi.moveTask(projectId, s.id, s.version, { toStatus: done ? 'IN_PROGRESS' : 'DONE' }))}
              />
              <span style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 14, minWidth: 0 }}>
                <span className="mono" style={{ fontSize: 11, color: 'var(--text-muted)' }}>{s.id}</span>
                {onOpenTask ? (
                  <button
                    type="button"
                    title={`Open ${s.id}`}
                    onClick={() => onOpenTask(s.id)}
                    style={{ padding: 0, background: 'none', border: 'none', cursor: 'pointer', font: 'inherit', textAlign: 'left', color: done ? 'var(--text-2)' : 'var(--text)', textDecoration: done ? 'line-through' : 'none' }}
                  >
                    {s.title}
                  </button>
                ) : (
                  <span style={{ color: done ? 'var(--text-2)' : 'var(--text)', textDecoration: done ? 'line-through' : 'none' }}>{s.title}</span>
                )}
                {(s.status === 'IN_PROGRESS' || s.status === 'BLOCKED' || s.status === 'REVIEW') && <StatusPill status={s.status} />}
              </span>
            </div>
          );
        })}
      </Stack>
      <Cluster gap={4} style={{ marginTop: 10 }}>
        <input className="input" style={{ flex: 1 }} placeholder="New subtask" value={subTitle} onChange={(e) => setSubTitle(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter' && subTitle) void run(contextApi.addSubtask(projectId, taskId, subTitle), () => setSubTitle('')); }} />
        <Button disabled={!subTitle} onClick={() => void run(contextApi.addSubtask(projectId, taskId, subTitle), () => setSubTitle(''))}>Add</Button>
      </Cluster>
    </Panel>
  );
}

/* ---- context ---------------------------------------------------------------------------------- */

/** Notes / decisions / fields. Link-kind items render in LinksPanel. */
export function ContextPanel({ d }: P) {
  const { projectId, taskId, task, ctx, run } = d;
  const [note, setNote] = useState('');
  // THE INCIDENT BRIEF IS SHOWN BY `IncidentPanel`, SO IT IS EXCLUDED HERE. It was rendered twice —
  // once formatted above, once as raw text in this list — which reads as duplicated data on the one
  // ticket type where a reader is scanning for the alert's detail.
  //
  // Gated on the ticket being an INCIDENT, not on the author prefix: an ordinary ticket that happens
  // to carry an `ingest:`-authored item has no Incident panel to show it, so hiding it here would
  // lose it entirely.
  const briefId =
    task.customFields['incident.fingerprint'] !== undefined ? incidentBrief(ctx)?.id : undefined;
  const items = (ctx?.items ?? []).filter((i) => i.kind !== 'link' && i.id !== briefId);
  return (
    <Panel
      title="Context items"
      right={<span className="mono">{items.length} · note / decision / field</span>}
      pad
      {...(briefId ? { footnote: 'The telemetry snapshot is shown in Incident, above.' } : {})}
    >
      <Stack gap={4}>
        {items.map((i) => (
          <div key={i.id} className="card" style={{ padding: 10 }}>
            <Cluster gap={4}>
              <Avatar id={i.author} /> <span className="chip">{i.kind}</span> <span className="muted" style={{ fontSize: 11 }}>{timeAgo(i.ts)}</span>
            </Cluster>
            <div style={{ marginTop: 4, fontSize: 14, whiteSpace: 'pre-wrap' }}>{i.body}</div>
          </div>
        ))}
      </Stack>
      <Cluster gap={4} style={{ marginTop: 8 }}>
        <input className="input" style={{ flex: 1 }} placeholder="Add a note…" value={note} onChange={(e) => setNote(e.target.value)} onKeyDown={(e) => { if (e.key === 'Enter' && note) void run(contextApi.addContextItem(projectId, taskId, 'note', note), () => setNote('')); }} />
        <Button disabled={!note} onClick={() => void run(contextApi.addContextItem(projectId, taskId, 'note', note), () => setNote(''))}>Add</Button>
      </Cluster>
    </Panel>
  );
}

/** External artifacts (Lucid, Google Docs, Figma, …) as smart link cards. */
export function LinksPanel({ d }: P) {
  const { projectId, taskId, ctx, run } = d;
  const [link, setLink] = useState('');
  const links = (ctx?.items ?? []).filter((i) => i.kind === 'link');
  return (
    <Panel title="Links" right={<span className="mono">{links.length}</span>} pad>
      <Stack gap={4}>
        {links.map((i) => (
          <AppLinkCard key={i.id} url={i.body} meta={`${i.author.split('@')[0]} · ${timeAgo(i.ts)}`} />
        ))}
      </Stack>
      <Cluster gap={4} style={{ marginTop: 8 }}>
        <input
          className="input"
          style={{ flex: 1 }}
          placeholder="Paste a link (Lucid, Google Docs, Figma…)"
          value={link}
          onChange={(e) => setLink(e.target.value)}
          onKeyDown={(e) => { const u = normalizeLink(link); if (e.key === 'Enter' && u) void run(contextApi.addContextItem(projectId, taskId, 'link', u), () => setLink('')); }}
        />
        <Button disabled={!normalizeLink(link)} onClick={() => { const u = normalizeLink(link); if (u) void run(contextApi.addContextItem(projectId, taskId, 'link', u), () => setLink('')); }}>Attach</Button>
      </Cluster>
      {link.trim().length > 0 && !normalizeLink(link) && (
        <p className="muted" style={{ fontSize: 11, margin: '4px 0 0' }}>Not a valid URL — paste a full http(s) link.</p>
      )}
    </Panel>
  );
}

export function MemoryPanel({ d }: P) {
  const { projectId, taskId, memQ, run } = d;
  const [memKey, setMemKey] = useState('');
  const [memVal, setMemVal] = useState('');
  const entries = memQ.data?.entries ?? [];
  return (
    <Panel title="Agent memory" right={<span className="mono">{entries.length}</span>} pad>
      <Stack gap={2}>
        {entries.map((m) => (
          <div key={m.id} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '4px 0', borderBottom: '1px dashed var(--border)' }}>
            <Cluster gap={8}>
              <span style={{ ...label, padding: '1px 6px', background: m.scope === 'agent' ? 'color-mix(in srgb, var(--accent) 10%, transparent)' : 'var(--panel)', color: m.scope === 'agent' ? 'var(--accent-tint)' : 'var(--text-2)' }}>{m.scope}</span>
              <span className="mono" style={{ fontSize: 12, color: 'var(--text)' }}>{m.key}</span>
            </Cluster>
            <span className="mono" style={{ fontSize: 12, color: 'var(--accent-tint)' }}>{JSON.stringify(m.value)}</span>
          </div>
        ))}
        {entries.length === 0 && <p className="muted" style={{ fontSize: 12, margin: 0 }}>No memory entries.</p>}
      </Stack>
      <Cluster gap={4} style={{ marginTop: 10 }}>
        <input className="input" style={{ width: 90 }} placeholder="key" value={memKey} onChange={(e) => setMemKey(e.target.value)} />
        <input className="input" style={{ flex: 1 }} placeholder="value" value={memVal} onChange={(e) => setMemVal(e.target.value)} />
        <Button disabled={!memKey} onClick={() => void run(contextApi.writeMemory(projectId, taskId, 'shared', memKey, memVal), () => { setMemKey(''); setMemVal(''); })}>Save</Button>
      </Cluster>
    </Panel>
  );
}

/* ---- discussion ------------------------------------------------------------------------------- */

/** Comments + follow-ups. Both already own their headings, so the panel only supplies the record rule. */
export function DiscussionPanel({ d }: P) {
  const { projectId, taskId, task, identity, mentionCandidates, myRole, canComment } = d;
  return (
    <Panel title="Discussion" pad>
      <Stack gap={16}>
        <Comments
          projectId={projectId}
          taskId={taskId}
          mentionCandidates={mentionCandidates}
          canComment={canComment}
          closedWhy={!canComment && myRole !== undefined ? 'A viewer cannot comment.' : undefined}
        />
        <FollowUps projectId={projectId} task={task} identity={identity} />
      </Stack>
    </Panel>
  );
}

/* ---- files ------------------------------------------------------------------------------------- */

/**
 * One produced artifact. A url renders through the existing smart link card (favicon + forge name);
 * a bare ref (a commit sha or repo-relative path from a checkout with no remote) still renders — as
 * mono text with its kind, because evidence without a forge is still evidence.
 */
function ArtifactRow({ artifact }: { artifact: Artifact }) {
  const meta = [artifact.kind.toUpperCase(), artifact.ref, artifact.repo].filter(Boolean).join(' · ');
  if (artifact.url) return <AppLinkCard url={artifact.url} meta={meta} />;
  return (
    <div className="card" style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '8px 10px' }}>
      <span className="chip">{artifact.kind}</span>
      <span className="mono" style={{ fontSize: 12, minWidth: 0, overflowWrap: 'anywhere' }}>{artifact.ref}</span>
      {artifact.repo && <span className="muted" style={{ marginLeft: 'auto', fontSize: 11 }}>{artifact.repo}</span>}
    </div>
  );
}

const fmtBytes = (b: number): string => (b >= 1048576 ? `${(b / 1048576).toFixed(1)} MB` : b >= 1024 ? `${Math.round(b / 1024)} KB` : `${b} B`);

/** Bound artifacts (the evidence) + uploaded attachments (the files), per mock task.html. */
export function FilesPanel({ d }: P) {
  const { task, attQ, uploadFile } = d;
  const [dragOver, setDragOver] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const artifacts = task.artifacts ?? [];
  const attachments = attQ.data?.attachments ?? [];
  return (
    <Panel title="Artifacts & attachments" right={<span className="mono">{artifacts.length + attachments.length}</span>} pad>
      {artifacts.length > 0 && (
        <Stack gap={4} style={{ marginBottom: 12 }}>
          {artifacts.map((a, i) => <ArtifactRow key={`${a.kind}-${a.url ?? a.ref ?? i}`} artifact={a} />)}
        </Stack>
      )}
      {artifacts.length === 0 && attachments.length === 0 && <p className="muted" style={{ fontSize: 12, margin: '0 0 8px' }}>None.</p>}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
        {attachments.map((a) => {
          const tile = (
            <>
              <div style={{ aspectRatio: '16 / 9', background: 'var(--panel)', border: '1px solid var(--border)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 4, overflow: 'hidden' }}>
                {a.thumbUri ? (
                  <img src={a.thumbUri} alt="" style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
                ) : (
                  <>
                    <Icon name={a.kind === 'image' ? 'image' : a.kind === 'audio' ? 'audio' : a.kind === 'video' ? 'video' : 'file'} size={18} />
                    <span className="mono" style={{ fontSize: 10, color: 'var(--text-2)' }}>{a.mimeType}</span>
                  </>
                )}
              </div>
              <p className="mono" style={{ fontSize: 10, color: a.status === 'failed' ? 'var(--blocked)' : 'var(--text-muted)', margin: '4px 0 0' }}>
                {a.kind} · {fmtBytes(a.bytes)}{a.status !== 'ready' ? ` · ${a.status}` : ''}
              </p>
            </>
          );
          // Signed download URLs open in a new tab — same-tab navigation would dump the SPA onto GCS.
          return a.downloadUrl ? (
            <a key={a.id} href={a.downloadUrl} target="_blank" rel="noreferrer" style={{ display: 'block' }}>{tile}</a>
          ) : (
            <div key={a.id}>{tile}</div>
          );
        })}
      </div>
      <input
        ref={fileInputRef}
        type="file"
        style={{ display: 'none' }}
        onChange={(e) => {
          const file = e.target.files?.[0];
          if (file) uploadFile(file);
          e.target.value = '';
        }}
      />
      <button
        type="button"
        onClick={() => fileInputRef.current?.click()}
        onDragOver={(e) => { e.preventDefault(); setDragOver(true); }}
        onDragLeave={() => setDragOver(false)}
        onDrop={(e) => {
          e.preventDefault();
          setDragOver(false);
          const file = e.dataTransfer.files?.[0];
          if (file) uploadFile(file);
        }}
        style={{
          marginTop: 10, width: '100%', cursor: 'pointer', textAlign: 'center',
          display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 6,
          padding: '20px 16px', font: 'inherit',
          border: `1.5px dashed ${dragOver ? 'var(--accent)' : 'var(--outline-variant)'}`,
          background: dragOver ? 'color-mix(in srgb, var(--accent) 10%, var(--surface-lowest))' : 'var(--surface-lowest)',
          transition: 'border-color .15s, background .15s',
        }}
      >
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="var(--accent)" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
          <path d="M12 16V4M7 9l5-5 5 5" />
          <path d="M5 16v2a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-2" />
        </svg>
        <span style={{ fontSize: 14, color: 'var(--text-2)' }}>
          <strong style={{ color: 'var(--text)' }}>Drop a file</strong> here, or <span style={{ color: 'var(--accent-tint)' }}>click to browse</span>
        </span>
        <span className="mono" style={{ fontSize: 10, color: 'var(--text-2)' }}>images, audio, video & docs</span>
      </button>
    </Panel>
  );
}

/* ---- the rail ----------------------------------------------------------------------------------- */

/**
 * Incident provenance (spec §10) — rendered ONLY for a ticket an on-call integration filed.
 *
 * It reads the flat `incident.*` scalars (`customFields` values are scalars by contract, which is
 * why the shape is flat rather than nested) plus the incident CONTEXT ITEM the ingest transaction
 * wrote — that item is the telemetry brief `get_task_context` hands the responding agent, so the
 * human and the agent are reading the same thing rather than two summaries that can disagree.
 *
 * Severity is deliberately absent: it IS `task.priority`, already editable in the properties rail.
 * A second severity here would be a number nobody keeps in sync.
 */
/**
 * The telemetry snapshot the ingest CREATE txn wrote: the EARLIEST ingest-authored context item.
 *
 * ONE EXPRESSION, TWO CALLERS — `IncidentPanel` renders it, `ContextPanel` excludes it. They read the
 * same rule because they were written apart and drifted: the brief ended up rendered twice, and the
 * next change to the rule would have moved only one of them.
 *
 * THE EARLIEST INGEST ITEM, NOT THE FIRST ONE IN THE ARRAY. Context items come back newest-first
 * (`orderBy('ts','desc')`), and the RESOLVE path writes its own ingest-authored note — so `find`
 * returned "Alert resolved upstream at …" the moment an alert cleared, replacing the labels,
 * annotations and links this exists to surface, exactly when someone is reading the ticket to
 * understand what happened. Only the CREATE path writes a telemetry snapshot, so the snapshot is the
 * earliest ingest item; chosen by `ts` rather than by position so array order cannot matter.
 */
export function incidentBrief(
  ctx: LoadedTaskDetail['ctx'],
): NonNullable<LoadedTaskDetail['ctx']>['items'][number] | undefined {
  const ingestItems = (ctx?.items ?? []).filter((i) => i.author.startsWith('ingest:'));
  return ingestItems.reduce<(typeof ingestItems)[number] | undefined>(
    (earliest, i) => (earliest === undefined || i.ts < earliest.ts ? i : earliest),
    undefined,
  );
}

export function IncidentPanel({ d }: P) {
  const { task, ctx } = d;
  const cf = task?.customFields ?? {};
  const fingerprint = cf['incident.fingerprint'];
  if (fingerprint === undefined) return null; // not an incident ticket — render nothing at all

  const status = String(cf['incident.alertStatus'] ?? 'firing');
  const occurrences = Number(cf['incident.occurrences'] ?? 1);
  const source = String(cf['incident.source'] ?? 'unknown');
  // The brief the ingest txn wrote, identified by its author rather than by position: an operator's
  // own note added later must never be mistaken for the telemetry snapshot.
  //
  // THE EARLIEST INGEST ITEM, NOT THE FIRST ONE IN THE ARRAY. Context items come back newest-first
  // (`orderBy('ts','desc')`), and the RESOLVE path writes its own ingest-authored note — so `find`
  // returned "Alert resolved upstream at …" the moment an alert cleared, replacing the labels,
  // annotations and links this panel exists to show, exactly when someone is reading the ticket to
  // understand what happened. Only the CREATE path writes a telemetry snapshot, so the snapshot is
  // the earliest ingest item; chosen by `ts` rather than by position so array order cannot matter.
  const brief = incidentBrief(ctx);

  return (
    // `pad` — its children are chips and PROSE, which is exactly what the prop is for. Without it
    // the status chips, the fingerprint line and the whole markdown brief sat ~27px left of the
    // "INCIDENT" head above them, flush against the panel rule. Reported from staging as a markdown
    // gutter fault; it is not the markdown, it is the panel. Every sibling that renders prose
    // (Description, Digest, Review, Delivered, Context, Links, Memory, Discussion) already passes it.
    <Panel title="Incident" pad>
      <Stack gap={8}>
        <Cluster gap={8} wrap>
          <span className="pill"><><span className={status === 'resolved' ? 'dot-resolved' : 'dot-firing'} aria-hidden="true" />{status === 'resolved' ? ' resolved upstream' : ' firing'}</></span>
          <span className="pill">source: {source}</span>
          <span className="pill">
            {occurrences} occurrence{occurrences === 1 ? '' : 's'}
          </span>
        </Cluster>
        <div style={{ fontSize: 12 }}>
          <span className="muted">fingerprint </span>
          <span className="mono">{String(fingerprint)}</span>
        </div>
        {status === 'resolved' && (
          <Note>
            The alert cleared upstream. The ticket stays open until someone closes it — a resolved
            alert is not a fixed cause.
          </Note>
        )}
        {brief ? (
          // MARKDOWN, because `ticketSnapshot` (src/domain/incident.ts) COMPOSES markdown: the status
          // line is `**FIRING**`, and labels/annotations/links are `- ` bullet lists. A <pre> printed
          // the asterisks literally on every incident ticket.
          //
          // `remarkGfm` buys exactly one thing here — autolink literals, so the snapshot's bare-URL
          // `links:` bullets are clickable. The `- k=v` bullets render fine without it.
          <div className="md" style={{ fontSize: 12 }}>
            <ReactMarkdown components={mdComponents} remarkPlugins={mdPlugins}>
              {brief.body}
            </ReactMarkdown>
          </div>
        ) : (
          <span className="muted" style={{ fontSize: 12 }}>
            No telemetry snapshot on this ticket.
          </span>
        )}
      </Stack>
    </Panel>
  );
}

/**
 * A list-of-strings field, edited as CHIPS.
 *
 * It was a comma-separated text input, which is the wrong shape twice over: a set of values rendered
 * as one string gives no affordance for removing ONE of them, and the reader cannot tell where a
 * value ends and the separator begins. Chips make each value its own object, with its own ✕.
 *
 * Enter or comma commits; Backspace on an empty box removes the last chip, which is the convention
 * every tag input uses and the reason people try it.
 */
function ChipField({
  values,
  placeholder,
  onChange,
}: {
  values: string[];
  placeholder: string;
  onChange: (next: string[]) => void;
}) {
  const [draft, setDraft] = useState('');
  const commit = (raw: string): void => {
    const parts = raw.split(',').map((x) => x.trim()).filter(Boolean);
    const next = [...values];
    for (const v of parts) if (!next.includes(v)) next.push(v);
    if (next.length !== values.length) onChange(next);
    setDraft('');
  };
  return (
    <div
      className="input"
      style={{ display: 'flex', flexWrap: 'wrap', gap: 4, alignItems: 'center', minHeight: 32, height: 'auto', paddingTop: 4, paddingBottom: 4 }}
    >
      {values.map((v) => (
        <span key={v} className="chip" style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
          {v}
          <button
            type="button"
            aria-label={`Remove ${v}`}
            onClick={() => onChange(values.filter((x) => x !== v))}
            style={{ border: 'none', background: 'none', cursor: 'pointer', color: 'inherit', font: 'inherit', lineHeight: 1, padding: 0 }}
          >
            ×
          </button>
        </span>
      ))}
      <input
        value={draft}
        placeholder={values.length ? '' : placeholder}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ',') { e.preventDefault(); commit(draft); }
          else if (e.key === 'Backspace' && !draft && values.length) onChange(values.slice(0, -1));
        }}
        onBlur={() => draft && commit(draft)}
        style={{ border: 'none', outline: 'none', background: 'none', font: 'inherit', color: 'inherit', flex: 1, minWidth: 80, padding: 0 }}
      />
    </div>
  );
}

export function PropertiesPanel({ d, defaultOpen = true }: P & { defaultOpen?: boolean }) {
  const { task, projectId, taskId, run, agentsQ, histQ } = d;
  const [cf, setCf] = useState({ k: '', v: '' });
  // These inputs save on blur, and the drawer's Escape handler blurs the focused field — without this
  // reset, Escape would COMMIT the pending edit instead of canceling it.
  const escapeResets = (original: string) => (e: React.KeyboardEvent<HTMLInputElement>): void => {
    if (e.key === 'Escape') e.currentTarget.value = original;
  };
  return (
    <Panel title="Details" right={<span className="mono">v{task.version}</span>} pad collapsible defaultOpen={defaultOpen}>
      <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '6px 10px', alignItems: 'center', fontSize: 14 }}>
        <span className="muted">Priority</span>
        <select className="input" value={task.priority}
          onChange={(e) => void run(tasksApi.updateTask(projectId, taskId, task.version, { priority: e.target.value }))}>
          {['low', 'normal', 'high', 'urgent'].map((p) => <option key={p} value={p}>{p}</option>)}
        </select>
        <span className="muted">Assignee</span>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
          <input className="input" list="agent-names" key={`as${task.version}`} defaultValue={task.assignee ?? ''} placeholder="agent name, email, or SA"
            onKeyDown={escapeResets(task.assignee ?? '')}
            onBlur={(e) => { const v = e.target.value.trim(); if (v !== (task.assignee ?? '')) void run(tasksApi.updateTask(projectId, taskId, task.version, { assignee: v || null })); }} />
          <datalist id="agent-names">
            {[...new Set([
              ...(agentsQ.data?.sessions ?? []).map((s) => s.agentName),
              // Also surface agents seen on THIS board's feed — they may be owned by other members.
              ...((histQ.data?.events ?? []).map((e) => e.agent).filter(Boolean) as string[]),
            ])].sort().map((n) => <option key={n} value={n} />)}
          </datalist>
          <span className="muted" style={{ fontSize: 11 }}>Pick an agent to route this ticket to its work loop.</span>
        </div>
        <span className="muted">Due</span>
        <input className="input" type="date" key={`due${task.version}`} defaultValue={task.dueAt ? task.dueAt.slice(0, 10) : ''}
          onKeyDown={escapeResets(task.dueAt ? task.dueAt.slice(0, 10) : '')}
          onBlur={(e) => {
            // Compare the DAY the input shows, not the re-serialized ISO: a dueAt that carries a
            // time component would otherwise never round-trip, so every blur (including the
            // Escape-reset) would fire a silent midnight-UTC normalization write.
            const v = e.target.value;
            if (v === (task.dueAt ? task.dueAt.slice(0, 10) : '')) return;
            const iso = v ? new Date(v).toISOString() : null;
            void run(tasksApi.updateTask(projectId, taskId, task.version, { dueAt: iso }));
          }} />
        <span className="muted">Estimate</span>
        <input className="input" type="number" key={`est${task.version}`} defaultValue={task.estimate ?? ''}
          onKeyDown={escapeResets(task.estimate != null ? String(task.estimate) : '')}
          onBlur={(e) => { const v = e.target.value.trim(); const n = v ? Number(v) : null; if (n !== null && !Number.isFinite(n)) return; if (n !== task.estimate) void run(tasksApi.updateTask(projectId, taskId, task.version, { estimate: n })); }} />
        <span className="muted">Labels</span>
        <ChipField
          values={task.labels}
          placeholder="add a label"
          onChange={(labels) => void run(tasksApi.updateTask(projectId, taskId, task.version, { labels }))}
        />
        <span className="muted">Capabilities</span>
        <ChipField
          values={task.requiredCapabilities}
          placeholder="add a capability"
          onChange={(requiredCapabilities) => void run(tasksApi.updateTask(projectId, taskId, task.version, { requiredCapabilities }))}
        />
      </div>
      {/* CUSTOM FIELDS, on the SAME `auto 1fr` grid as the fields above.
          They were a free-floating row — a chip, a bare value and a ✕ with no alignment — so a custom
          field did not read as a field at all. Sharing the grid makes "test 1 / value 1" line up under
          Priority and Labels, which is what it is. */}
      {(() => {
        const rows = Object.entries(task.customFields).filter(
          ([k]) => !META_CF.includes(k) && !k.startsWith('incident.'),
        );
        return (
          <div style={{ marginTop: 10, borderTop: '1px solid var(--border)', paddingTop: 10 }}>
            {rows.length > 0 && (
              <div style={{ display: 'grid', gridTemplateColumns: 'auto 1fr auto', gap: '6px 10px', alignItems: 'center', fontSize: 14, marginBottom: 8 }}>
                {rows.map(([k, v]) => (
                  <Fragment key={k}>
                    <span className="muted" style={{ overflowWrap: 'anywhere' }}>{k}</span>
                    <span style={{ overflowWrap: 'anywhere' }}>{String(v)}</span>
                    <button
                      type="button"
                      aria-label={`Remove ${k}`}
                      title={`Remove ${k}`}
                      className="btn secondary"
                      style={{ padding: '0 8px', lineHeight: '22px' }}
                      onClick={() => { const next = { ...task.customFields }; delete next[k]; void run(tasksApi.updateTask(projectId, taskId, task.version, { customFields: next })); }}
                    >
                      ×
                    </button>
                  </Fragment>
                ))}
              </div>
            )}
            <Cluster gap={4}>
              <input className="input" style={{ width: 110 }} placeholder="field" value={cf.k} onChange={(e) => setCf({ ...cf, k: e.target.value })} />
              <input className="input" style={{ flex: 1, minWidth: 80 }} placeholder="value" value={cf.v} onChange={(e) => setCf({ ...cf, v: e.target.value })} />
              <Button disabled={!cf.k} onClick={() => void run(tasksApi.updateTask(projectId, taskId, task.version, { customFields: { ...task.customFields, [cf.k]: cf.v } }), () => setCf({ k: '', v: '' }))}>Add</Button>
            </Cluster>
          </div>
        );
      })()}
    </Panel>
  );
}

/** One compact line per event. Collapsed by default — it can grow long and dominate the surface. */
export function ActivityPanel({ d }: P) {
  const { history, taskId } = d;
  const displayNames = useDisplayNames(d.projectId);
  const [open, setOpen] = useState(false);
  return (
    <Panel
      // The whole head is the disclosure control — one target, and its accessible name stays
      // "Recent Activity" whether it is open or shut.
      title={
        <button type="button" onClick={() => setOpen((v) => !v)} aria-expanded={open}
          style={{ background: 'none', border: 'none', padding: 0, cursor: 'pointer', font: 'inherit', color: 'inherit', letterSpacing: 'inherit' }}>
          <span aria-hidden="true" style={{ display: 'inline-block', transform: open ? 'rotate(90deg)' : 'none', transition: 'transform .12s' }}><Icon name="chevronRight" size={11} /></span>{' '}
          Recent Activity
        </button>
      }
      right={<span className="mono">{history.length}</span>}
      pad
    >
      {open ? (
        <Stack gap={2}>
          {history.map((e) => (
            <div key={e.id} style={{ display: 'flex', alignItems: 'center', gap: 7, fontSize: 12, lineHeight: 1.6, minWidth: 0 }}>
              <Avatar id={eventActor(e, displayNames)} />
              <Marquee label={eventActor(e, displayNames)} style={{ color: 'var(--text)', fontWeight: 500, maxWidth: 140 }}>{eventActor(e, displayNames)}</Marquee>
              <span style={{ color: 'var(--text-muted)', whiteSpace: 'nowrap' }}>{eventVerb(e.type)}</span>
              {e.taskId && e.taskId !== taskId && <span className="mono" style={{ fontSize: 11, color: 'var(--text-2)', whiteSpace: 'nowrap' }}>{e.taskId}</span>}
              <span style={{ flex: 1 }} />
              <span className="mono" style={{ fontSize: 10, color: 'var(--text-muted)', whiteSpace: 'nowrap' }} title={new Date(e.ts).toLocaleString()}>{timeAgo(e.ts)}</span>
            </div>
          ))}
          {history.length === 0 && <p className="muted" style={{ fontSize: 12, margin: 0 }}>No activity yet.</p>}
        </Stack>
      ) : (
        <p className="muted" style={{ fontSize: 12, margin: 0 }}>{history.length} event{history.length === 1 ? '' : 's'} on this ticket.</p>
      )}
    </Panel>
  );
}
