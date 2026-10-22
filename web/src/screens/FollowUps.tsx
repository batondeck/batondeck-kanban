/**
 * Follow-ups (Phase 3): a directed-directive section in the task drawer. Unlike comments (open
 * discussion), a follow-up targets the task's current holder/assignee, notifies them, is surfaced in
 * get_task_context.openFollowUps, and can optionally REOPEN completed work (REVIEW/DONE → In Progress).
 *
 * Reads/writes through the gateway follow-up routes; invalidates the drawer's `followups`, `ctx`, `task`,
 * and `hist` queries on every mutation so the directive, its open/acked state, any reopen transition, and
 * the activity event all appear live. Matches design/screens/follow-ups.html.
 */
import { useMemo, useState } from 'react';
import { Icon } from '../icons.js';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../api.js';
import { Avatar, timeAgo } from '../components.js';
import type { FollowUp, Status, Task } from '../types.js';
import { Stack } from '../ui.js';

/** Mono pill used for Open (amber) / N-open badges and the Acked (green) badge. */
function MiniBadge({ tone, children }: { tone: 'open' | 'acked'; children: React.ReactNode }) {
  const color = tone === 'open' ? 'var(--progress)' : 'var(--done)';
  return (
    <span
      className="mono"
      style={{
        display: 'inline-flex', alignItems: 'center', gap: 3, fontSize: 10, letterSpacing: '0.04em', textTransform: 'uppercase',
        color, background: `color-mix(in srgb, ${color} 15%, transparent)`,
        border: `1px solid color-mix(in srgb, ${color} 30%, transparent)`, borderRadius: 'var(--r-1)', padding: '1px 6px',
      }}
    >
      {children}
    </span>
  );
}

function FollowUpItem({ fu, isHolder, onAck }: { fu: FollowUp; isHolder: boolean; onAck: () => void }) {
  const open = fu.status === 'open';
  return (
    <div
      style={{
        display: 'flex', gap: 12, padding: 12, borderRadius: 'var(--r-1)',
        background: open ? 'var(--surface-lowest)' : 'var(--panel)',
        border: '1px solid var(--border)',
        // Open follow-ups get an amber left rail (the centerpiece in the mockup).
        borderLeft: open ? '2px solid var(--progress)' : '1px solid var(--border)',
      }}
    >
      <Avatar id={fu.author} />
      <div style={{ flex: 1, minWidth: 0 }}>
        <p style={{ margin: 0, fontSize: 14, lineHeight: 1.5, color: 'var(--text)', whiteSpace: 'pre-wrap' }}>{fu.body}</p>
        <div style={{ display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: '4px 8px', marginTop: 8 }}>
          <span style={{ fontSize: 12, fontWeight: 500, color: 'var(--text-2)' }}>{fu.author}</span>
          <span className="mono" style={{ fontSize: 10, color: 'var(--text-muted)' }}>{timeAgo(fu.ts)}</span>
          {fu.target && (
            <span className="chip" style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }} title={`directed at ${fu.target}`}>
              <Avatar id={fu.target} />
              <span className="mono" style={{ fontSize: 11 }}>→ {fu.target}</span>
            </span>
          )}
          {fu.reopened && <span className="chip" title="this follow-up reopened the task">↩ reopened</span>}
          <span style={{ marginLeft: 'auto', display: 'inline-flex', gap: 6, alignItems: 'center' }}>
            {open ? <MiniBadge tone="open">Open</MiniBadge> : <MiniBadge tone="acked"><Icon name="check" size={11} /> Acked</MiniBadge>}
            {open && isHolder && (
              <button className="btn secondary" style={{ padding: '1px 8px', fontSize: 11 }} onClick={onAck}>Ack</button>
            )}
          </span>
        </div>
      </div>
    </div>
  );
}

export function FollowUps({ projectId, task, identity }: { projectId: string; task: Task; identity: string | null }) {
  const taskId = task.id;
  const qc = useQueryClient();
  // Live updates arrive via the board's SSE stream, which invalidates ['followups', projectId, …] on
  // every board change (the ui-backend keeps a stateful MCP session even under STATELESS_MCP=true, so
  // resource subscriptions fire) — this 30s poll is just a backstop. Only mounts while the drawer is open.
  const followUpsQ = useQuery({
    queryKey: ['followups', projectId, taskId],
    queryFn: () => api.listFollowUps(projectId, taskId),
    refetchInterval: 30000,
  });
  const [body, setBody] = useState('');
  const [reopen, setReopen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const followUps = followUpsQ.data?.followUps ?? [];
  const openCount = useMemo(() => followUps.filter((f) => f.status === 'open').length, [followUps]);

  // The signed-in human is "the holder" when they own the live claim or are the assignee — only then can
  // they ack a directive aimed at them (the core also gates this).
  const isHolder = (task.claim?.owner === identity) || (task.assignee === identity);
  // Reopen only makes sense from REVIEW/DONE; surface the toggle only there (matches the spec's no-op note).
  const reopenable: Status[] = ['REVIEW', 'DONE'];
  const canReopen = reopenable.includes(task.status);

  const refresh = (): void => {
    void qc.invalidateQueries({ queryKey: ['followups', projectId, taskId] });
    void qc.invalidateQueries({ queryKey: ['ctx', projectId, taskId] });
    void qc.invalidateQueries({ queryKey: ['task', projectId, taskId] });
    void qc.invalidateQueries({ queryKey: ['tasks', projectId] });
    void qc.invalidateQueries({ queryKey: ['hist', projectId] });
  };

  const send = async (): Promise<void> => {
    const text = body.trim();
    if (!text || busy) return;
    setBusy(true);
    setErr(null);
    try {
      await api.addFollowUp(projectId, taskId, text, canReopen && reopen);
      setBody('');
      setReopen(false);
      refresh();
    } catch (e) {
      setErr(String(e instanceof Error ? e.message : e));
    } finally {
      setBusy(false);
    }
  };

  const ack = async (id: string): Promise<void> => {
    setErr(null);
    try {
      await api.ackFollowUp(projectId, taskId, id);
      refresh();
    } catch (e) {
      setErr(String(e instanceof Error ? e.message : e));
    }
  };

  const target = task.claim?.agent || task.claim?.owner || task.assignee || null;

  return (
    <section>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 10 }}>
        <h3 style={{ fontSize: 14, fontWeight: 600, color: 'var(--text)', margin: 0 }}>Follow-ups</h3>
        {openCount > 0 && <MiniBadge tone="open">{openCount} open</MiniBadge>}
      </div>
      <p className="muted" style={{ fontSize: 12, margin: '-2px 0 12px', lineHeight: 1.5 }}>
        Follow-ups are directed at the task&apos;s current holder and can reopen completed work — unlike comments, which are open discussion.
      </p>

      <Stack gap={8}>
        {followUps.length === 0 && (
          <p style={{ margin: 0, fontSize: 12, color: 'var(--text-2)', fontStyle: 'italic' }}>No follow-ups yet.</p>
        )}
        {followUps.map((fu) => (
          <FollowUpItem key={fu.id} fu={fu} isHolder={isHolder} onAck={() => void ack(fu.id)} />
        ))}
      </Stack>

      {/* Composer */}
      <div style={{ background: 'var(--surface-lowest)', border: '1px solid var(--border)', borderRadius: 'var(--r-2)', padding: 12, marginTop: 12 }}>
        <textarea
          className="input"
          style={{ width: '100%', minHeight: 56, resize: 'vertical', fontFamily: 'inherit', background: 'var(--panel)' }}
          placeholder={target ? `Add a follow-up for ${target}…` : 'Add a follow-up…'}
          value={body}
          onChange={(e) => setBody(e.target.value)}
          onKeyDown={(e) => { if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') void send(); }}
        />
        <div style={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', gap: 12, marginTop: 10, paddingTop: 10, borderTop: '1px solid var(--border)' }}>
          {/* Reopen toggle — only offered when the task is in REVIEW/DONE (where reopen is meaningful). */}
          {canReopen ? (
            <label style={{ display: 'flex', alignItems: 'flex-start', gap: 8, cursor: 'pointer' }}>
              <button
                type="button"
                role="switch"
                aria-checked={reopen}
                onClick={() => setReopen((v) => !v)}
                style={{
                  position: 'relative', flex: '0 0 auto', width: 36, height: 20, borderRadius: 9999, border: 0, marginTop: 1,
                  background: reopen ? 'var(--accent)' : 'var(--panel-2)', transition: 'background .15s',
                }}
              >
                <span style={{ position: 'absolute', top: 2, left: 2, width: 16, height: 16, borderRadius: '50%', background: '#fff', transform: reopen ? 'translateX(16px)' : 'none', transition: 'transform .15s' }} />
              </button>
              <span style={{ display: 'flex', flexDirection: 'column' }}>
                <span style={{ fontSize: 12, fontWeight: 500, color: 'var(--text)', lineHeight: 1.2 }}>Reopen task</span>
                <span className="mono" style={{ fontSize: 10, color: 'var(--text-muted)', marginTop: 3, lineHeight: 1.3, maxWidth: 220 }}>
                  Reopening moves {taskId} back to In Progress for {target ?? 'the holder'}.
                </span>
              </span>
            </label>
          ) : (
            <span className="muted" style={{ fontSize: 11 }}>⌘↵ to send</span>
          )}
          <button className="btn" style={{ display: 'inline-flex', alignItems: 'center', gap: 6, flex: '0 0 auto' }} disabled={!body.trim() || busy} onClick={() => void send()}>
            Send
          </button>
        </div>
        {err && <p className="error" style={{ fontSize: 11, margin: '8px 0 0' }}>{err}</p>}
      </div>
    </section>
  );
}
