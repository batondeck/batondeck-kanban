/** Shared design-system components (from design/DESIGN.md). */
import { useEffect, useLayoutEffect, useRef, useState, type ComponentProps, type CSSProperties, type ReactNode } from 'react';
import type ReactMarkdown from 'react-markdown';
import type { Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import type { Priority, Status, Task } from './types.js';
import { isAgent } from './types.js';
import { Icon } from './icons.js';
import { ToolLogo, toolKey, type ToolKey } from './ToolLogo.js';
import { onOwnOrigin } from './api/media.js';

/** ReactMarkdown overrides for rendered task/comment markdown: links open in a new tab — a same-tab
 * navigation would dump the SPA (and any drafts) onto the linked site. An embedded attachment (a
 * signed /blobs URL) loads from this page's own origin, which the CSP's img-src allows (onOwnOrigin). */
export const mdComponents: Components = {
  a: ({ children, ...props }) => (
    <a {...props} target="_blank" rel="noreferrer">
      {children}
    </a>
  ),
  img: ({ src, node: _node, ...props }) => <img {...props} src={typeof src === 'string' ? onOwnOrigin(src) : src} />,
};

/**
 * The remark plugin list every rendered-markdown site shares.
 *
 * DECLARED ONCE so the surfaces cannot disagree about what markdown means. They already had: only
 * `chronicleBlocks.tsx` passed `remarkGfm`, so a pipe table in an agent-written brief rendered as a
 * flat paragraph on a ticket and as a table in the Chronicle — same bytes, two answers.
 *
 * Typed off ReactMarkdown's own props rather than importing from `unified`, which is transitive and
 * would read to knip as an undeclared dependency.
 */
export const mdPlugins: ComponentProps<typeof ReactMarkdown>['remarkPlugins'] = [remarkGfm];

const STATUS_COLOR: Record<Status, string> = {
  TODO: 'var(--ready)',
  IN_PROGRESS: 'var(--progress)',
  BLOCKED: 'var(--blocked)',
  REVIEW: 'var(--review)',
  DONE: 'var(--done)',
};
const PRIORITY_COLOR: Record<Priority, string> = {
  low: 'var(--text-muted)',
  normal: 'var(--ready)',
  high: 'var(--progress)',
  urgent: 'var(--blocked)',
};

export function StatusPill({ status }: { status: Status }) {
  const c = STATUS_COLOR[status];
  return (
    <span className="pill" style={{ color: c, background: `color-mix(in srgb, ${c} 18%, transparent)` }}>
      {status.replace('_', ' ')}
    </span>
  );
}

/** Poison (plan §Fold 1, ADR-0001): BLOCKED with the `poisoned` system label, which the retry policy
 *  writes beside blockedReason "poison". The label is what the task wire carries; blockedReason is read
 *  too, for a payload that has it. A dependency-blocked task carries neither. */
export function isPoisoned(t: Pick<Task, 'status' | 'labels'> & { blockedReason?: string | null }): boolean {
  return t.status === 'BLOCKED' && (t.labels.includes('poisoned') || t.blockedReason === 'poison');
}

/** The poison badge, on the Blocked lane's card and in the drawer. `claimAttempts` is absent at zero. */
export function PoisonBadge({ task }: { task: Pick<Task, 'claimAttempts'> }) {
  const n = task.claimAttempts ?? 0;
  return (
    <span className="badge poisoned" title="Quarantined after failed attempts: only a master can requeue it">
      Poisoned{n > 0 ? ` · ${n} attempt${n === 1 ? '' : 's'}` : ''}
    </span>
  );
}

export function PriorityDot({ priority }: { priority: Priority }) {
  return <span className="dot" title={priority} style={{ background: PRIORITY_COLOR[priority] }} />;
}

/**
 * `id` is the IDENTITY and always drives classification (agent vs human, brand logo). `label` is
 * optional DISPLAY — a person's name or email resolved from the account directory (T-319).
 *
 * They are two props on purpose. Before T-216 a member key WAS an email, so one string was both; now it
 * is an opaque `U-<uuid>` and the initials read `U8`. Passing the display through `id` to fix that would
 * have re-classified every agent as a human and dropped the 🤖 — `isAgent`/`toolKey` parse the id's
 * shape. So initials and the tooltip follow `label`, classification never does.
 *
 * The id stays reachable on hover even when a label is shown: two accounts may legitimately render the
 * same name, so no surface may become one where identity is unresolvable.
 */
export function Avatar({ id, label, active, tool, nameOnly }: { id: string; label?: string; active?: boolean; tool?: ToolKey | null; nameOnly?: boolean }) {
  // A `<tool>-...` name (or a client-derived tool) shows the tool's brand logo instead of the robot.
  const t = tool ?? toolKey(id);
  const agent = isAgent(id) || !!t;
  const shown = label?.trim() || id;
  const initials = shown.replace(/[^a-zA-Z0-9]/g, '').slice(0, 2).toUpperCase();
  // `nameOnly` (T-430): surfaces whose rule is "names, never account ids/emails" keep the id out of the DOM.
  const title = nameOnly || shown === id ? shown : `${shown} · ${id}`;
  return (
    <span className={`avatar ${agent ? 'agent' : 'human'}`} title={title} aria-label={`${agent ? 'agent' : 'human'} ${title}`}>
      {t ? <ToolLogo tool={t} size={14} /> : agent ? <Icon name="bot" size={14} /> : initials}
      {agent && <span className={`avatar-dot${active ? ' active' : ''}`} aria-hidden="true" />}
    </span>
  );
}

/**
 * The ONE truncating surface (T-51). Anywhere a chip, a chip row or a name would otherwise be cut to
 * `text-overflow: ellipsis`, wrap it here instead — board card labels, the assignee chip, presence
 * chips, dependency chips, activity actors. A second implementation of "clip it and hope" is a bug.
 *
 * Behaviour:
 *  - At rest the content is clipped, with a right-edge fade (only visible where content actually
 *    reaches the edge) and, for chip rows (`more`), a measured `+N` for the chips that don't fit.
 *  - On hover/focus — including the board's own `.card.selected` keyboard selection, and hover/focus
 *    of an enclosing button — a CSS `translateX` keyframe walks the content to its end, PAUSES there
 *    long enough to read, and returns. See `.marquee` in index.css.
 *  - `label` is the full, untruncated text: it is the element's accessible name AND its tooltip, so a
 *    screen reader and a mouse user both get everything regardless of what's visible. Omit it only
 *    when an enclosing button already carries the same name (avoids announcing it twice).
 *
 * The ONLY measured values are the scroll distance and its duration, written once to CSS custom
 * properties; the animation itself is a plain CSS keyframe on `transform`, so it stays on the
 * compositor and a React Query refetch re-render cannot restart it (no class or key changes).
 */
export function Marquee({
  label,
  more = false,
  children,
  className,
  style,
}: {
  /** Full text — becomes the accessible name + tooltip. Omit when an enclosing button supplies it. */
  label?: string;
  /** Chip rows: measure and show a `+N` badge for the chips that don't fit. */
  more?: boolean;
  children: ReactNode;
  className?: string;
  style?: CSSProperties;
}) {
  const clip = useRef<HTMLSpanElement>(null);
  const [hidden, setHidden] = useState(0);
  // No dep array on purpose: the content is arbitrary children, so there is nothing honest to key on.
  // ponytail: KNOWN CEILING — measure-every-commit. Fine because the WRITES are conditional: a row that
  // doesn't overflow never touches style, so after the first paint this is a pure (cheap) layout read.
  // Upgrade path: if a board ever renders enough rows for this to show in a profile, gate it behind a
  // ResizeObserver so the read happens on resize instead of on every commit.
  useLayoutEffect(() => {
    const el = clip.current;
    const track = el?.firstElementChild as HTMLElement | null;
    if (!el || !track || el.clientWidth === 0) return; // detached / display:none / jsdom (no layout)
    const overflow = Math.round(track.scrollWidth - el.clientWidth);
    // Only write when the value actually changes — an identical write mid-hover would restart the
    // interpolation and make a background refetch look like a stutter.
    const shift = overflow > 1 ? `${-overflow}px` : '';
    if (el.style.getPropertyValue('--marquee-shift') !== shift) {
      if (shift) {
        el.style.setProperty('--marquee-shift', shift);
        // Constant reading speed rather than a constant duration: a long row scrolls for longer, it
        // doesn't scroll faster. Clamped so a pathological row can't run for a minute.
        el.style.setProperty('--marquee-ms', `${Math.min(14000, 1800 + overflow * 22)}ms`);
      } else {
        el.style.removeProperty('--marquee-shift');
        el.style.removeProperty('--marquee-ms');
      }
    }
    if (!more) return;
    let n = 0;
    for (const c of Array.from(track.children) as HTMLElement[]) {
      if (c.offsetLeft + c.offsetWidth > el.clientWidth + 0.5) n += 1;
    }
    setHidden((p) => (p === n ? p : n));
  });
  return (
    <span
      ref={clip}
      className={['marquee', className].filter(Boolean).join(' ')}
      style={style}
      {...(label ? { role: 'group', 'aria-label': label, title: label } : {})}
    >
      <span className="marquee-track">{children}</span>
      {hidden > 0 && (
        <span className="marquee-more" aria-hidden="true">
          +{hidden}
        </span>
      )}
    </span>
  );
}

export function timeAgo(iso: string): string {
  const s = Math.max(0, Math.floor((Date.now() - new Date(iso).getTime()) / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

/** Minutes left on a lease, or null if none/expired. */
export function leaseLeft(expiresAt: string | undefined | null): string | null {
  if (!expiresAt) return null;
  const ms = new Date(expiresAt).getTime() - Date.now();
  return ms > 0 ? `${Math.ceil(ms / 60000)}m` : null;
}

/**
 * One verb per event type the server emits (`internal/domain/events.go`), read as "<actor> <verb> <task>".
 * `event-verbs.test.ts` holds this table to that file both ways: an event with no verb showed a person
 * the raw dotted identifier ("task.review_requested"), and the table still named the hosted product's
 * plugin and org events, which this server never emits (P-457).
 */
const EVENT_VERB: Record<string, string> = {
  'attachment.created': 'attached a file to',
  'attachment.updated': 'updated an attachment on',
  'board.created': 'created board',
  'chronicle.conflicts_flagged': 'flagged conflicts in a record',
  'chronicle.page_created': 'created a record',
  'chronicle.page_deleted': 'deleted a record',
  'chronicle.page_updated': 'updated a record',
  'chronicle.version_added': 'added a version of a record',
  'comment.added': 'commented on',
  'context.added': 'added context to',
  'dependency.added': 'linked',
  'dependency.removed': 'unlinked',
  'followup.acked': 'acked a follow-up on',
  'incident.delivery': 'received an alert for',
  'incident.resolved': 'resolved the alert on',
  'integration.created': 'created an integration',
  'integration.deleted': 'deleted an integration',
  'integration.secret_rotated': 'rotated an integration secret',
  'integration.updated': 'updated an integration',
  'memory.written': 'noted on',
  'project.created': 'created project',
  'run.discarded': 'discarded a run on',
  'run.opened': 'opened a run on',
  'run.picked': 'picked a run for',
  'runs.cancelled': 'cancelled the race on',
  'runs.started': 'started a race on',
  'sprint.abandoned': 'abandoned a sprint',
  'sprint.activated': 'started a sprint',
  'sprint.checkpoint': 'checkpointed a sprint',
  'sprint.closed': 'closed a sprint',
  'sprint.created': 'proposed a sprint',
  'sprint.self_approved': 'self-approved a sprint',
  'sprint.task_added': 'added to a sprint',
  'sprint.task_removed': 'removed from a sprint',
  'sprint.updated': 'updated a sprint',
  'summary.set': 'summarized',
  'task.anomaly_detected': 'flagged an anomaly on',
  'task.artifact_added': 'added an artifact to',
  'task.artifact_missing': 'left no artifact on',
  'task.artifact_retracted': 'retracted an artifact from',
  'task.blocked': 'blocked',
  'task.checkpoint_missing': 'left no checkpoint on',
  'task.claimed': 'claimed',
  'task.completed': 'completed',
  'task.created': 'created',
  'task.deleted': 'deleted',
  'task.dependency_cleared': 'unblocked',
  'task.failed': 'failed',
  'task.follow_up': 'added a follow-up to',
  'task.handoff': 'handed off',
  'task.lease_reaped': 'reaped the lease on',
  'task.moved': 'moved',
  'task.orchestrated': 'split into subtasks',
  'task.poisoned': 'marked as poisoned',
  'task.released': 'released',
  'task.reopened': 'reopened',
  'task.review_requested': 'requested review of',
  'task.self_approved': 'self-approved',
  'task.updated': 'updated',
};
/** The event types that have a verb, for the test that compares them with the server's. */
export const EVENT_VERB_TYPES = Object.keys(EVENT_VERB);
export function eventVerb(type: string): string {
  return EVENT_VERB[type] ?? type;
}

/**
 * A DISPLAY LOOKUP: opaque id → something a person can read.
 *
 * Two kinds of opaque id reach a feed, and neither is readable:
 *   `U-47f0d44f-7a04-4e44-a67f-a39601d12b79`  a human account (the T-216 identity cutover made these
 *                                             opaque on purpose — the core stores no email)
 *   `ingest:ing_ba3c6ec8c12cc970c3ae16e2f62ddbaf`  an on-call integration firing a webhook
 *
 * Both resolve from data the caller already holds: `memberDisplay` (a SIBLING of `project` on
 * `get_project`, derived server-side per read) and `list_integrations`. Neither is fetched here —
 * this function stays pure so it can be unit-tested without a network.
 */
export type DisplayNames = ReadonlyMap<string, string> | Readonly<Record<string, string>> | undefined;

const lookup = (names: DisplayNames, key: string): string | undefined => {
  if (!names) return undefined;
  return names instanceof Map ? names.get(key) : (names as Record<string, string>)[key];
};

/** An account id the core minted. Opaque by design; 38 characters of nothing to a reader. */
const ACCOUNT_ID = /^U-[0-9a-f-]{8,}$/i;
/** The ingest function stamps this when there is no agent and no human. */
const INGEST_ACTOR = /^ingest:(ing_[0-9a-f]+)$/;

/**
 * Who an activity event should be attributed to: the AGENT that performed it when present, otherwise
 * the human/identity actor — resolved to a name when `names` can, SHORTENED when it cannot.
 *
 * Shortening rather than printing the raw id is the point. `memberDisplay` names only PROJECT MEMBERS
 * (the core has a refusal test proving it cannot name a non-member), so a former member, a
 * cross-project actor, or a reader without the on-call permission all legitimately miss — and a wall
 * of full uuids is what was reported from staging. An elided id still says WHAT kind of actor it is.
 */
export function eventActor(e: { actor: string; agent?: string | null }, names?: DisplayNames): string {
  if (e.agent) return e.agent;

  const ing = INGEST_ACTOR.exec(e.actor);
  if (ing) {
    const id = ing[1]!;
    return lookup(names, id) ?? `ingest:${id.slice(0, 10)}…`;
  }

  const named = lookup(names, e.actor);
  // Emails render as their local part, matching the provenance line ("Created by alice"). The full
  // address stays available to any caller that wants it as a tooltip.
  if (named) return named.includes('@') ? named.split('@')[0]! : named;

  if (ACCOUNT_ID.test(e.actor)) return `${e.actor.slice(0, 10)}…`;
  return e.actor;
}

/**
 * A PERSON'S LABEL: an id that may be an account id, a login or an agent label → what a reader sees
 * (P-314). The surfaces that name one person rather than an event (a comment's author, a follow-up's
 * target, a notification's actor, a ticket's creator, a handoff's ends, the identity chip) use this,
 * so none of them prints a raw `U-<uuid>`.
 *
 *   - named by `names` (`memberDisplay`: the account's display name, else its login) → that name;
 *   - an account id nothing names → shortened, as `eventActor` shortens it (never the full id);
 *   - anything else (an agent label, a login stored as given) → itself.
 *
 * An email-shaped label renders as its local part, matching `eventActor` and the provenance line;
 * `full` keeps the whole address, for a tooltip or the chip.
 */
export function personName(id: string, names?: DisplayNames, opts?: { full?: boolean }): string {
  const label = lookup(names, id) ?? (ACCOUNT_ID.test(id) ? `${id.slice(0, 10)}…` : id);
  return !opts?.full && label.includes('@') ? label.split('@')[0]! : label;
}

/** The one topbar pattern: title + muted subtitle, spacer, right-aligned actions. */
export function PageHeader({ title, subtitle, actions }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="topbar">
      {/* Title and subtitle STACK. Side by side, the subtitle ate horizontal room the title needed —
          a long board name wrapped to three lines while "5 tasks" sat beside it holding a column open.
          Stacked, the name gets the full width of this cell and the count costs nothing.

          BOTH ARE ReactNode, not string. The ticket page needs an inline copy-link control beside the
          id, and a `string` subtitle forced the id to be the TITLE — which is how T-17 ended up
          rendering larger than the sentence describing the ticket. */}
      <div className="topbar-heading">
        <h1 style={{ fontSize: 16, margin: 0 }}>{title}</h1>
        {subtitle && <span className="muted" style={{ fontSize: 12 }}>{subtitle}</span>}
      </div>
      <div className="spacer" />
      {actions}
    </div>
  );
}

/** Centered empty-pane block: muted glyph, title, hint, optional action. */
export function EmptyState({ icon, title, hint, action }: { icon?: ReactNode; title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="empty-state">
      {icon && (
        <div className="empty-icon" aria-hidden="true">
          {icon}
        </div>
      )}
      <div className="empty-title">{title}</div>
      {hint && <div className="empty-hint">{hint}</div>}
      {action}
    </div>
  );
}

/** Inline two-click destructive confirm: first click arms (auto-disarms after 3s or blur), second fires. */
export function ConfirmButton({
  label,
  confirmLabel,
  onConfirm,
  className,
  style,
  disabled,
  title,
  describedBy,
}: {
  label: string;
  confirmLabel?: string;
  onConfirm: () => void;
  className?: string;
  style?: CSSProperties;
  /** Closed, as a role-gated control is (P-440): no arming, no confirm. */
  disabled?: boolean;
  title?: string;
  describedBy?: string;
}) {
  const [armed, setArmed] = useState(false);
  useEffect(() => {
    if (!armed) return;
    const t = setTimeout(() => setArmed(false), 3000);
    return () => clearTimeout(t);
  }, [armed]);
  return (
    <button
      className={[className, armed ? 'confirm-armed' : ''].filter(Boolean).join(' ') || undefined}
      style={style}
      disabled={disabled}
      title={title}
      aria-describedby={describedBy}
      onBlur={() => setArmed(false)}
      onClick={() => {
        if (armed) {
          setArmed(false);
          onConfirm();
        } else {
          setArmed(true);
        }
      }}
    >
      {armed ? (confirmLabel ?? 'Confirm?') : label}
    </button>
  );
}
