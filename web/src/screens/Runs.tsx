/**
 * Runs (Phase 3): the race section in the task drawer. A racing parent fans out into N run children
 * (each a real claimable task). Workers claim/work/complete a run like any task; a lead PICKS the winner,
 * which promotes its deliverable onto the parent and cancels the losers.
 *
 * Reads list_runs through the gateway; pick/cancel mutate the PARENT (version-checked). Invalidates the
 * drawer's `runs`, `task`, `ctx`, and `hist` queries so the live race state, winner highlight, and parent
 * advance appear live. Matches design/screens/runs.html.
 */
import { useState } from 'react';
import { Icon } from '../icons.js';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import * as runsApi from '../api/runs.js';
import { Avatar, timeAgo } from '../components.js';
import type { RunView, Status, Task } from '../types.js';

// A run is "submitted" (pickable) when it carries a deliverable — the core sets this on complete_task,
// landing the run in REVIEW/DONE. Running = an active, non-terminal run still being worked.
function runState(r: RunView): 'submitted' | 'running' | 'discarded' {
  if (r.status === 'CANCELLED') return 'discarded';
  if (r.hasDeliverable || r.status === 'REVIEW' || r.status === 'DONE') return 'submitted';
  return 'running';
}

function StatePill({ state }: { state: 'submitted' | 'running' | 'discarded' }) {
  const map = {
    running: { color: 'var(--progress)', text: 'Running' },
    submitted: { color: 'var(--review)', text: 'Submitted' },
    discarded: { color: 'var(--text-muted)', text: 'Discarded' },
  } as const;
  const { color, text } = map[state];
  return (
    <span
      style={{
        display: 'inline-flex', alignItems: 'center', gap: 6, padding: '2px 8px', borderRadius: 'var(--r-2)',
        color, background: `color-mix(in srgb, ${color} 15%, transparent)`, border: `1px solid color-mix(in srgb, ${color} 25%, transparent)`,
        fontSize: 12, fontWeight: 500,
      }}
    >
      <span className="dot" style={{ width: 6, height: 6, background: color }} />
      {text}
    </span>
  );
}

function RunCard({ run, racing, won, onPick }: { run: RunView; racing: boolean; won: boolean; onPick: () => void }) {
  const state = runState(run);
  const agent = run.leaseHolder || run.assignee || run.taskId;
  return (
    <div
      style={{
        position: 'relative', overflow: 'hidden', background: 'var(--panel)', borderRadius: 'var(--r-2)', padding: 12,
        border: won ? '1px solid var(--done)' : '1px solid var(--border)',
        boxShadow: won ? '0 0 0 1px var(--done)' : undefined,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <span className="mono" style={{ fontSize: 12, color: 'var(--text-2)' }}>{run.taskId}</span>
        <Avatar id={agent} active={state === 'running'} />
        <span style={{ fontSize: 14, fontWeight: 500, color: 'var(--text)' }}>{run.leaseHolder || run.assignee || <span className="muted">unclaimed</span>}</span>
        {won && (
          <span className="mono" style={{ display: 'inline-flex', alignItems: 'center', gap: 3, fontSize: 10, letterSpacing: '0.05em', textTransform: 'uppercase', color: 'var(--done)', background: 'color-mix(in srgb, var(--done) 15%, transparent)', border: '1px solid color-mix(in srgb, var(--done) 25%, transparent)', borderRadius: 'var(--r-1)', padding: '1px 6px' }}>
            <Icon name="trophy" size={12} /> Win
          </span>
        )}
        <span style={{ marginLeft: 'auto' }}>
          <StatePill state={state} />
        </span>
      </div>

      {run.deliverable && (
        <div style={{ background: 'var(--surface-lowest)', border: '1px solid var(--border)', borderRadius: 'var(--r-2)', padding: 8, marginTop: 12 }}>
          <p style={{ margin: 0, fontSize: 12, lineHeight: 1.4, color: 'var(--text-2)', whiteSpace: 'pre-wrap' }}>{run.deliverable}</p>
        </div>
      )}

      {/* Pick action — only on submitted runs while the race is still open. A won run shows a disabled
          "Picked" affordance; a running run shows nothing (nothing to pick yet). */}
      {won ? (
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 12 }}>
          <button className="btn secondary" disabled style={{ display: 'inline-flex', alignItems: 'center', gap: 6, color: 'var(--done)', borderColor: 'color-mix(in srgb, var(--done) 30%, transparent)' }}>
            <Icon name="check" size={12} /> Picked
          </button>
        </div>
      ) : racing && state === 'submitted' ? (
        <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 12 }}>
          <button className="btn" style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }} onClick={onPick}>
            <Icon name="check" size={12} /> Pick
          </button>
        </div>
      ) : null}
    </div>
  );
}

export function Runs({ projectId, task }: { projectId: string; task: Task }) {
  const taskId = task.id;
  const qc = useQueryClient();
  const racing = !!task.racing;
  // Only fetch runs when this task is (or was) a parent of a race — a never-raced task has none, and the
  // query is cheap to keep mounted so it refreshes live once a race opens.
  // Live updates arrive via the board's SSE stream, which invalidates ['runs', projectId, …] on every
  // board change (the ui-backend keeps a stateful MCP session even under STATELESS_MCP=true, so resource
  // subscriptions fire) — this 30s poll is just a backstop. Only mounts while the drawer is open.
  const runsQ = useQuery({
    queryKey: ['runs', projectId, taskId],
    queryFn: () => runsApi.listRuns(projectId, taskId),
    refetchInterval: 30000,
  });
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  // The start-race form (count + Start) is shown inline once the human clicks "Race this task".
  const [startOpen, setStartOpen] = useState(false);
  // Max parallel runs a human can open at once. No project-level cap is modelled yet, so use 5.
  const RACE_CAP = 5;
  const [count, setCount] = useState(2);

  const runs = runsQ.data?.runs ?? [];

  const refresh = (): void => {
    void qc.invalidateQueries({ queryKey: ['runs', projectId, taskId] });
    void qc.invalidateQueries({ queryKey: ['task', projectId, taskId] });
    void qc.invalidateQueries({ queryKey: ['ctx', projectId, taskId] });
    void qc.invalidateQueries({ queryKey: ['tasks', projectId] });
    void qc.invalidateQueries({ queryKey: ['hist', projectId] });
  };

  /**
  * Run one action at a time, surfacing failures through `err`.
  *
  * Takes a THUNK, not a promise. `guard(runsApi.pickRun(…))` evaluates the call at the CALL SITE, so the
  * request was already in flight before `if (busy) return` could stop it: a second Pick reached the
  * server carrying a now-stale `task.version`, and because `guard` returned before its `await` that
  * rejection was unhandled — no `err`, nothing on screen, a pick that looked like it did nothing.
  */
  const guard = async (make: () => Promise<unknown>): Promise<void> => {
    if (busy) return;
    setBusy(true);
    setErr(null);
    try {
      await make();
      refresh();
    } catch (e) {
      setErr(String(e instanceof Error ? e.message : e));
    } finally {
      setBusy(false);
    }
  };

  const submittedStatuses: Status[] = ['REVIEW', 'DONE'];
  // After a pick, the parent is no longer racing and exactly one run keeps its deliverable in a
  // non-cancelled, submitted state — highlight that as the winner.
  const winnerId = !racing
    ? runs.find((r) => r.status !== 'CANCELLED' && (r.hasDeliverable || submittedStatuses.includes(r.status)))?.taskId
    : undefined;

  return (
    <section>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 10 }}>
        <h3 style={{ fontSize: 14, fontWeight: 600, color: 'var(--text)', margin: 0 }}>Runs</h3>
        {/* "+ Add run" opens one more run child into the live race; the count badge sits beside it. */}
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
          {racing && (
            <button className="btn secondary" style={{ padding: '2px 10px', fontSize: 12 }} disabled={busy} onClick={() => void guard(() => runsApi.openRun(projectId, taskId, {}))}>
              + Add run
            </button>
          )}
          {runs.length > 0 && (
            <span className="mono" style={{ fontSize: 12, color: 'var(--text-muted)', background: 'var(--panel)', border: '1px solid var(--border)', borderRadius: 'var(--r-2)', padding: '0 8px' }}>
              {runs.length} {runs.length === 1 ? 'run' : 'runs'}
            </span>
          )}
        </span>
      </div>

      {/* Start-race affordance — only when this task is NOT racing and has no runs yet. A human lead
          initiates the "race this task across N agents" flow here: fan the task out into N run children
          (each a real claimable task) that workers pick up and a lead later PICKS the winner from. */}
      {!racing && runs.length === 0 && (
        <div style={{ background: 'var(--panel)', border: '1px solid var(--border)', borderRadius: 'var(--r-2)', padding: 12 }}>
          {startOpen ? (
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
              <span style={{ fontSize: 12, color: 'var(--text-2)' }}>Race across</span>
              <input
                className="input"
                type="number"
                min={2}
                max={RACE_CAP}
                value={count}
                onChange={(e) => setCount(Math.max(2, Math.min(RACE_CAP, Number(e.target.value) || 2)))}
                aria-label="Number of runs"
                style={{ width: 64 }}
              />
              <span style={{ fontSize: 12, color: 'var(--text-2)' }}>agents</span>
              <span style={{ marginLeft: 'auto', display: 'inline-flex', gap: 6 }}>
                <button className="btn secondary" style={{ padding: '2px 10px', fontSize: 12 }} disabled={busy} onClick={() => setStartOpen(false)}>
                  Cancel
                </button>
                <button
                  className="btn"
                  style={{ padding: '2px 10px', fontSize: 12 }}
                  disabled={busy}
                  // Close the form only when the start actually succeeded — guard() swallows the rejection
                  // (surfacing it via err), so chaining after guard would close on failure too.
                  onClick={() => void guard(() => runsApi.startRuns(projectId, taskId, task.version, { count: Math.max(2, Math.min(RACE_CAP, count)) }).then((r) => { setStartOpen(false); return r; }))}
                >
                  Start
                </button>
              </span>
            </div>
          ) : (
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
              <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--text-2)' }}>
                <Icon name="finish" size={12} />
                Run this task across several agents in parallel, then pick the best result.
              </span>
              <button className="btn" style={{ marginLeft: 'auto', padding: '2px 10px', fontSize: 12 }} onClick={() => setStartOpen(true)}>
                Race this task
              </button>
            </div>
          )}
        </div>
      )}

      {/* Racing banner */}
      {racing && (
        <div style={{ position: 'relative', overflow: 'hidden', display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, padding: '8px 12px', marginBottom: 12, borderRadius: 'var(--r-2)', background: 'color-mix(in srgb, var(--progress) 10%, transparent)', border: '1px solid color-mix(in srgb, var(--progress) 25%, transparent)' }}>
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 12, color: 'var(--progress)' }}>
            <Icon name="finish" size={12} />
            Racing · {runs.filter((r) => r.status !== 'CANCELLED').length} runs open
            {task.raceOpenedAt && <span className="mono" style={{ fontSize: 11 }}>· started {timeAgo(task.raceOpenedAt)}</span>}
          </span>
          <button className="btn secondary" style={{ padding: '2px 10px', fontSize: 12 }} disabled={busy} onClick={() => void guard(() => runsApi.cancelRuns(projectId, taskId, task.version))}>
            Cancel race
          </button>
        </div>
      )}

      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        {runs.map((r) => (
          <RunCard
            key={r.taskId}
            run={r}
            racing={racing}
            won={r.taskId === winnerId}
            onPick={() => void guard(() => runsApi.pickRun(projectId, taskId, r.taskId, task.version))}
          />
        ))}
      </div>

      {err && <p className="error" style={{ fontSize: 11, margin: '10px 0 0' }}>{err}</p>}
      {racing && (
        <p className="muted" style={{ fontSize: 12, margin: '10px 0 0', lineHeight: 1.5 }}>
          Picking a run promotes its deliverable to {taskId} and cancels the others.
        </p>
      )}
    </section>
  );
}
