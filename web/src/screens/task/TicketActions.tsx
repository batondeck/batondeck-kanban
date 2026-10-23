/**
 * THE ONE STATUS + ACTIONS CONTROL, shared by the page and the drawer.
 *
 * There were two, and they did the same thing by different routes: `ActionsPanel` rendered a row of
 * "Move to" buttons in the page's main column, while the drawer's header carried a
 * `<select className="pill">` wired to the same `move.mutate`. Two call sites for one operation is
 * how they drift — and a reader had to learn a different control depending on which surface they
 * opened.
 *
 * PROPS ARE `{ d }` AND NOTHING ELSE, like every panel in `panels.tsx`. No `variant`, no `compact`:
 * a branch here would relocate the drift into this file rather than removing it. The page and the
 * drawer differ only in WHERE they mount this, and in CSS.
 *
 * The status control stays a `<select>` with the accessible name "Status" because that is what
 * `taskdrawer.test.tsx` already drives — it asserts the value, that unreachable targets drop out
 * once the board loads, and the exact `/move` body. Keeping the element and its label makes that an
 * unmodified regression guard on this refactor; changing them would mean rewriting the test to match
 * new code, which proves nothing.
 */
import { useState } from 'react';
import * as lifecycleApi from '../../api/lifecycle.js';
import { AgentSelect } from '../../AgentSelect.js';
import { Button, Cluster, Panel, Stack } from '../../ui.js';
import { Icon } from '../../icons.js';
import type { Status } from '../../types.js';
import { statusDot } from './panels.js';
import type { LoadedTaskDetail } from './useTaskDetail.js';

const label = {
  fontFamily: 'var(--mono)',
  fontSize: 10,
  letterSpacing: '0.04em',
  textTransform: 'uppercase' as const,
  color: 'var(--text-muted)',
};

export function TicketActions({ d }: { d: LoadedTaskDetail }) {
  const { task, projectId, taskId, run, move, myLease, heldByOther, setLease, colFor, moveTargets, agentList } = d;
  const [handoff, setHandoff] = useState<{ to: string; note: string } | null>(null);
  const pretty = (s: string): string => s.charAt(0) + s.slice(1).toLowerCase().replace('_', ' ');
  const readyName = colFor('READY')?.name ?? 'Ready';
  const stuck = moveTargets.length === 0;

  return (
    <Panel title="Actions" pad footnote="Claiming locks the ticket to you, so two tools never collide.">
      <Stack gap={16}>
        <div>
          <span style={{ ...label, display: 'block', marginBottom: 7 }}>Status</span>
          {/* The pill IS the select — picking a status runs the same optimistic move path the buttons
              used to (rollback + error toast on failure). Disabled rather than hidden when the ticket
              cannot move: a control that vanishes reads as a missing feature, not as a closed door. */}
          <Cluster gap={8} wrap>
            <select
              className="pill"
              aria-label="Status"
              title={stuck ? 'No moves available from here' : 'Change status'}
              value={task.status}
              disabled={stuck}
              onChange={(e) => {
                const to = e.target.value as Status;
                if (to !== task.status) move.mutate({ vars: to, task });
              }}
              style={{
                color: statusDot(task.status),
                background: `color-mix(in srgb, ${statusDot(task.status)} 14%, transparent)`,
                // A BORDER AND A CARET, because `appearance: none` on a filled pill reads as a LABEL.
                // Reported from staging: a BLOCKED ticket looked like a static red badge and the page
                // read as "cannot move this", while the select was enabled and offered both targets
                // the whole time. A control that works but does not look like one is still a defect.
                border: `1px solid color-mix(in srgb, ${statusDot(task.status)} 45%, transparent)`,
                appearance: 'none',
                WebkitAppearance: 'none',
                backgroundImage:
                  "url(\"data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 12 12'><path d='M2.5 4.5 6 8l3.5-3.5' fill='none' stroke='currentColor' stroke-width='1.6' stroke-linecap='round' stroke-linejoin='round'/></svg>\")",
                backgroundRepeat: 'no-repeat',
                backgroundPosition: 'right 8px center',
                paddingRight: 24,
                cursor: stuck ? 'default' : 'pointer',
                fontFamily: 'inherit',
                opacity: stuck ? 0.6 : 1,
              }}
            >
              <option value={task.status}>{task.status.replace('_', ' ')}</option>
              {moveTargets.map(({ to }) => (
                <option key={to} value={to}>
                  {to.replace('_', ' ')}
                </option>
              ))}
            </select>
            {stuck && (
              <span className="muted" style={{ fontSize: 12 }}>
                No moves available from {colFor(task.status)?.name ?? pretty(task.status)}.
              </span>
            )}
          </Cluster>
        </div>

        <div>
          <span style={{ ...label, display: 'block', marginBottom: 7 }}>Work the task</span>
          {task.status === 'READY' && !task.claim && (
            <Cluster gap={8} wrap>
              <Button
                variant="primary"
                onClick={() =>
                  void run(lifecycleApi.claimTask(projectId, taskId), (r) => {
                    if (r) setLease(r.leaseId);
                  })
                }
              >
                <Icon name="lock" />
                Claim to work
              </Button>
              <span className="muted" style={{ fontSize: 12 }}>Starts a lease only you hold.</span>
            </Cluster>
          )}
          {myLease && (
            <Cluster gap={4} wrap>
              <Button variant="primary" onClick={() => void run(lifecycleApi.completeTask(projectId, taskId, myLease), () => setLease(null))}><><Icon name="check" />Mark complete</></Button>
              <Button onClick={() => void run(lifecycleApi.blockTask(projectId, taskId, myLease, 'blocked from UI'), () => setLease(null))}>Block</Button>
              <Button onClick={() => setHandoff({ to: '', note: '' })}>Hand off…</Button>
              <Button onClick={() => void run(lifecycleApi.releaseTask(projectId, taskId, myLease), () => setLease(null))}>Release claim</Button>
            </Cluster>
          )}
          {heldByOther && (
            <span className="muted" style={{ fontSize: 12 }}>
              Held by <strong style={{ color: 'var(--text-2)' }}>{task.claim?.agent || task.claim?.owner}</strong> — wait for release to claim it.
            </span>
          )}
          {task.status !== 'READY' && !task.claim && (
            <span className="muted" style={{ fontSize: 12 }}>
              Move this task to <strong style={{ color: 'var(--text-2)' }}>{readyName}</strong> to claim it.
            </span>
          )}
        </div>

        {handoff && myLease && (
          <Cluster gap={4}>
            <AgentSelect agents={agentList} value={handoff.to} onChange={(v) => setHandoff({ ...handoff, to: v })} placeholder="to agent" style={{ flex: '0 0 180px' }} />
            <input className="input" style={{ flex: 1 }} placeholder="handoff note" value={handoff.note} onChange={(e) => setHandoff({ ...handoff, note: e.target.value })} />
            <Button
              variant="primary"
              disabled={!handoff.to}
              onClick={() =>
                void run(lifecycleApi.handoffTask(projectId, taskId, myLease, handoff.to, handoff.note), () => {
                  setHandoff(null);
                  setLease(null);
                })
              }
            >
              Send
            </Button>
            <Button onClick={() => setHandoff(null)}>Cancel</Button>
          </Cluster>
        )}
      </Stack>
    </Panel>
  );
}
