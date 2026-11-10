import { useEffect, useState } from 'react';
import { toolKey } from '../ToolLogo.js';
import { Icon } from '../icons.js';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import * as agentsApi from '../api/agents.js';
import * as boardsApi from '../api/boards.js';
import * as meApi from '../api/me.js';
import * as projectsApi from '../api/projects.js';
import * as usersApi from '../api/users.js';
import { WaitLoader } from '../brand.js';
import { Avatar } from '../components.js';
import type { AgentSession, Status } from '../types.js';
import { keepCachedDevices } from '../presenceMerge.js';
import { STATUSES } from '../types.js';
import { Cluster, Panel, Stack } from '../ui.js';
import { can, SUPERADMIN } from '../permissions.js';
import { useAuth } from '../auth.js';
import { useMyProjectRole } from '../displayNames.js';
import { closedWhy, mayCall } from '../toolFloors.js';
import { useSingleProject } from '../projectContext.js';

const STATUS_COLOR: Record<Status, string> = {
  TODO: 'var(--ready)',
  IN_PROGRESS: 'var(--progress)',
  BLOCKED: 'var(--blocked)',
  REVIEW: 'var(--review)',
  DONE: 'var(--done)',
};

/** A small bordered card used as a settings panel. */
/** Pill toggle switch matching the mockup. */
function Toggle({ checked, onChange, disabled, title, labelledBy, describedBy }: { checked: boolean; onChange: (v: boolean) => void; disabled?: boolean; title?: string; labelledBy: string; describedBy?: string }) {
  return (
    <button
      role="switch"
      aria-checked={checked}
      aria-labelledby={labelledBy}
      disabled={disabled}
      title={title}
      aria-describedby={describedBy}
      onClick={() => onChange(!checked)}
      style={{
        position: 'relative',
        width: 40,
        height: 22,
        flex: '0 0 auto',
        borderRadius: 999,
        border: 'none',
        background: checked ? 'var(--accent)' : 'var(--outline-variant)',
        transition: 'background 0.15s',
        padding: 0,
        opacity: disabled ? 0.5 : 1,
        cursor: disabled ? 'default' : 'pointer',
      }}
    >
      <span
        style={{
          position: 'absolute',
          top: 2,
          left: checked ? 20 : 2,
          width: 18,
          height: 18,
          borderRadius: '50%',
          background: '#fff',
          
          transition: 'left 0.15s',
        }}
      />
    </button>
  );
}

/**
 * Roles a PERSON can be given here. Deliberately not the full `Role` ladder.
 *
 * Two things this fixes. `agent` used to be in this list and was the DEFAULT selection — but that role
 * was RETIRED (`src/domain/types.ts`), so the core's Zod enum rejects it: clicking "+ Add Member"
 * without touching the dropdown always failed. And `worker` / `master` are agent PERSONAS — an agent
 * gets its reach through `add_member` over MCP, which is the documented onboarding path and is
 * unaffected by this list. This form is for sharing a board with a colleague by email; it is not where
 * you register an agent, so it does not offer to.
 */
const HUMAN_ROLES = ['viewer', 'member', 'admin'] as const;

/**
 * Your own preferences, rendered inside the Settings screen.
 *
 * This used to be a separate `/me` route. It was a page and a nav entry for two fields, and the app
 * already had a Settings screen — so it folded in here instead. What it must NOT do is sit inside the
 * project-scoped sections: these preferences follow the person, so they render regardless of which
 * project is picked, or whether any exists.
 */
function MyTokenBudget() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ['my-settings'], queryFn: () => meApi.getMySettings() });
  const [draft, setDraft] = useState('');
  const [saveErr, setSaveErr] = useState<string | null>(null);

  useEffect(() => {
    if (q.data) setDraft(q.data.maxTicketTokens == null ? '' : String(q.data.maxTicketTokens));
  }, [q.data]);

  const save = useMutation({
    mutationFn: (patch: Parameters<typeof meApi.setMySettings>[0]) => meApi.setMySettings(patch),
    onSuccess: (next) => { setSaveErr(null); qc.setQueryData(['my-settings'], next); },
    onError: (e: Error) => setSaveErr(e.message),
  });

  const s = q.data;
  // Tolerate a malformed/absent payload rather than taking the whole Settings screen down with it.
  // This section is a guest on somebody else's page: a failed or half-mocked /api/me/settings must
  // cost the user this card, not their columns, members and workflow settings.
  if (!s?.defaults) return null;

  return (
    <section style={{ marginBottom: 36 }}>
      <h3 style={{ fontSize: 12, letterSpacing: 0.6, textTransform: 'uppercase', color: 'var(--text-muted)', fontWeight: 600 }}>Your settings</h3>
      <p className="muted" style={{ margin: '0 0 12px', fontSize: 12 }}>
        These apply to you across every project — your teammates are unaffected.
      </p>
      <Panel pad>
        <Stack gap={16}>
          <div>
            <div id="my-token-limit-label" style={{ fontSize: 14, fontWeight: 600 }}>Your ticket token limit</div>
            <p className="muted" style={{ margin: '2px 0 0', fontSize: 12 }}>
              Leave empty to use the project&apos;s limit. You can set a <strong>lower</strong> limit for
              yourself; a higher one has no effect, because a project&apos;s limit is a ceiling you cannot raise.
            </p>
            <Cluster gap={8} align="center" style={{ marginTop: 8 }}>
              <input
                className="input"
                type="number"
                min={100}
                aria-labelledby="my-token-limit-label"
                placeholder={`Project default (${s.defaults.maxTicketTokens.toLocaleString()})`}
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                style={{ width: 230 }}
              />
              <button
                className="btn"
                onClick={() => save.mutate({ maxTicketTokens: draft.trim() === '' ? null : Number(draft) })}
              >
                Save
              </button>
              {s.maxTicketTokens != null && (
                <button className="btn secondary" onClick={() => save.mutate({ maxTicketTokens: null })}>
                  Use project default
                </button>
              )}
            </Cluster>
            {/*
              THE DISCLOSURE, and it is a requirement rather than polish. The number this limit is
              measured against is an ESTIMATE from text length; someone who sets it to a real model
              context window will be surprised in the wrong direction. Directly under the input is the
              only place that reliably lands.
            */}
            <p className="muted" style={{ margin: '10px 0 0', fontSize: 12 }}>
              <strong>Token counts are approximate.</strong> They are estimated from text length (about{' '}
              {s.defaults.charsPerToken} characters per token) and will not match a model&apos;s exact tokenizer —
              code and JSON pack denser than prose, so the real count runs higher. Leave yourself headroom.
            </p>
          </div>

          <div>
            <div style={{ fontSize: 14, fontWeight: 600 }}>When a ticket goes over budget</div>
            <p className="muted" style={{ margin: '2px 0 0', fontSize: 12 }}>
              You can be <strong>stricter</strong> than the project, never looser — if a project blocks
              over-budget writes, choosing Off here will not let yours through.
            </p>
            <Cluster gap={8} style={{ marginTop: 8 }}>
              {([null, 'off', 'warn', 'enforce'] as const).map((v) => (
                <button
                  key={String(v)}
                  className={`btn${s.tokenPolicy === v ? '' : ' secondary'}`}
                  onClick={() => save.mutate({ tokenPolicy: v })}
                >
                  {v === null ? 'Follow project' : v === 'enforce' ? 'Block' : v === 'warn' ? 'Warn' : 'Off'}
                </button>
              ))}
            </Cluster>
          </div>

          {saveErr && <p className="error" style={{ margin: 0, fontSize: 12 }}>{saveErr}</p>}
        </Stack>
      </Panel>
    </section>
  );
}

/**
 * Settings → Users (R1.15): the workspace admin lists the local accounts and adds one, whose
 * generated password this panel shows once. Hidden from everyone else, and the BFF refuses them
 * regardless (403): hiding is not the control. Disable, password reset and role changes are the
 * host's `batondeck-kanban user disable|passwd`, so there is no control for them here.
 */
export function UsersSection() {
  const { permissions } = useAuth();
  const admin = can(permissions, SUPERADMIN);
  const qc = useQueryClient();
  const users = useQuery({ queryKey: ['local-users'], queryFn: () => usersApi.listUsers(), enabled: admin });
  const [login, setLogin] = useState('');
  const [name, setName] = useState('');
  // The one copy of the initial password: from the add answer, cleared by Done, never fetched.
  const [shown, setShown] = useState<{ login: string; password: string } | null>(null);
  const add = useMutation({
    mutationFn: () => usersApi.addUser(login.trim(), name.trim()),
    onSuccess: (r) => {
      setShown({ login: r.user.login, password: r.password });
      setLogin('');
      setName('');
      void qc.invalidateQueries({ queryKey: ['local-users'] });
    },
  });
  if (!admin) return null;
  return (
    <section id="users" style={{ marginBottom: 36 }}>
      <Panel title="Users" pad>
        <Stack gap={12}>
          <p className="muted" style={{ fontSize: 14, margin: 0 }}>
            The local accounts on this server. To disable one or reset a password, run{' '}
            <code>batondeck-kanban user disable|passwd LOGIN</code> on the host. Give a new user project
            access from the project&rsquo;s Members list.
          </p>
          {users.isPending && <WaitLoader />}
          {users.isError && (
            <p className="error" style={{ fontSize: 12, margin: 0 }}>
              {users.error instanceof Error ? users.error.message : 'Could not read the users.'}
            </p>
          )}
          {users.data && (
            <table aria-label="Users" style={{ width: '100%', fontSize: 14, borderCollapse: 'collapse' }}>
              <thead>
                <tr className="muted" style={{ textAlign: 'left', fontSize: 11, textTransform: 'uppercase' }}>
                  <th style={{ padding: '4px 8px 4px 0' }}>Login</th>
                  <th style={{ padding: '4px 8px' }}>Name</th>
                  <th style={{ padding: '4px 8px' }}>Role</th>
                  <th style={{ padding: '4px 0 4px 8px' }}>Status</th>
                </tr>
              </thead>
              <tbody>
                {users.data.users.map((u) => (
                  <tr key={u.id} style={{ borderTop: '1px solid var(--border)' }}>
                    <td style={{ padding: '6px 8px 6px 0', overflowWrap: 'anywhere' }}>{u.login}</td>
                    <td style={{ padding: '6px 8px' }}>{u.displayName}</td>
                    <td style={{ padding: '6px 8px' }}>{u.role}</td>
                    <td style={{ padding: '6px 0 6px 8px' }}>{u.disabled ? 'Disabled' : 'Active'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <form
            onSubmit={(e) => {
              e.preventDefault();
              add.mutate();
            }}
          >
            <Cluster gap={8}>
              <input
                className="input"
                value={login}
                onChange={(e) => setLogin(e.target.value)}
                placeholder="login, e.g. bob@example.com"
                aria-label="New user login"
                required
                style={{ flex: 1, minWidth: 0 }}
              />
              <input
                className="input"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Display name (optional)"
                aria-label="New user display name"
                style={{ flex: 1, minWidth: 0 }}
              />
              <button type="submit" className="btn primary" disabled={add.isPending}>
                {add.isPending ? 'Adding…' : 'Add user'}
              </button>
            </Cluster>
          </form>
          {add.isError && (
            <p className="error" style={{ fontSize: 12, margin: 0 }}>
              {add.error instanceof Error ? add.error.message : 'Could not add the user.'}
            </p>
          )}
          {shown && (
            <div role="status" style={{ borderTop: '1px solid var(--border)', paddingTop: 12 }}>
              <p style={{ fontSize: 14, margin: '0 0 8px' }}>
                Initial password for {shown.login}, shown once. Pass it on now:
              </p>
              <Cluster gap={8}>
                <code style={{ flex: 1, minWidth: 0, overflowWrap: 'anywhere' }}>{shown.password}</code>
                <button type="button" className="btn secondary" onClick={() => setShown(null)}>
                  Done
                </button>
              </Cluster>
            </div>
          )}
        </Stack>
      </Panel>
    </section>
  );
}

/**
 * CLI tokens (T-457) — mint a `bd_…` credential for a terminal, list what you hold, revoke one.
 *
 * *** THE VALUE IS RENDERED ONCE, FROM THE MINT RESPONSE, AND IS NEVER FETCHED. *** It lives in one
 * `useState` populated by the mutation and cleared by "Done". There is no API that could return it
 * again — `api` has no `getCliToken` — so this panel cannot re-display a token even if a future edit
 * tried to: a page that can re-show a credential turns any XSS or shoulder-surf into theft.
 *
 * Member-tier by design. `batondeck:settings` is reachable by every signed-in user, which is correct
 * for a SELF-service credential: the tools take no identity, so a user can only ever mint for
 * themselves, and one that could not would have no headless path at all (the gap this ticket closes).
 */
/**
 * The public half of a minted token — `bd_<id>…`. Used ONLY in the reveal, immediately after a copy,
 * so the panel can stop rendering the secret while still telling the holder which token they just
 * took. It is a transient acknowledgement, not a record.
 *
 * DERIVED HERE, CLIENT-SIDE, and that is now the only place this form exists. The server used to
 * return a `display` field and the table used to render it; both are gone — an identifier sitting in
 * a credentials table reads as a leaked credential no matter what the column is called. Nothing
 * persists this string and no read path can produce it again.
 *
 * Split on `_` rather than a fixed slice: the id length is a server detail, and a hardcoded offset
 * here would silently start cutting into the secret if it ever changed.
 */
const display = (token: string): string => `${token.split('_').slice(0, 2).join('_')}…`;

/**
 * A WIP limit field: blank is no limit (null), a whole number of 1 or more is that limit, anything
 * else is undefined and is refused before sending. `Number('five')` is NaN, which JSON writes as
 * null, and the server reads null as "no limit": a typo used to clear the column's limit silently.
 */
const parseWip = (raw: string): number | null | undefined => {
  const t = raw.trim();
  if (!t) return null;
  return /^[1-9]\d*$/.test(t) && Number.isSafeInteger(Number(t)) ? Number(t) : undefined;
};
const WIP_INVALID = 'A WIP limit is a whole number of 1 or more, or blank for no limit.';


function CliTokens() {
  const qc = useQueryClient();
  const tokensQ = useQuery({ queryKey: ['cliTokens'], queryFn: () => meApi.listCliTokens() });
  const [label, setLabel] = useState('');
  const [minted, setMinted] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const tokens = tokensQ.data?.tokens ?? [];

  const refresh = (): void => void qc.invalidateQueries({ queryKey: ['cliTokens'] });
  // gcTime 0: the answer carries the plaintext, so it must not outlive Done in the mutation cache.
  const mint = useMutation({
    gcTime: 0,
    mutationFn: () => meApi.createCliToken({ label: label.trim() }),
    onSuccess: (r) => { setMinted(r.token); setLabel(''); setErr(null); setCopied(false); refresh(); },
    onError: (e) => setErr(String(e instanceof Error ? e.message : e)),
  });
  const revoke = useMutation({
    mutationFn: (id: string) => meApi.revokeCliToken(id),
    onSuccess: () => { setErr(null); refresh(); },
    onError: (e) => setErr(String(e instanceof Error ? e.message : e)),
  });

  return (
    <section id="cli-tokens" style={{ marginBottom: 36 }}>
      <Panel
        title="CLI tokens"
        right={<span className="muted" style={{ fontSize: 12 }}>{tokens.filter((t) => t.status === 'live').length} live</span>}
      >
        <p className="muted" style={{ fontSize: 12, padding: '8px 12px', margin: 0 }}>
          Mint a token to use BatonDeck from a terminal. It carries your own access and nothing more,
          and it expires on its own.
        </p>
        {err && <p className="error" style={{ fontSize: 12, margin: '8px 12px' }}>{err}</p>}

        {minted ? (
          /*
           * THE ONE-TIME REVEAL, AND IT STOPS BEING SHOWN THE MOMENT IT IS TAKEN.
           *
           * The secret used to appear TWICE — once in the field and again in a plaintext
           * `export BATONDECK_TOKEN=…` line beneath it — and it stayed on screen after copying. Both
           * were needless exposure: a shoulder-surf or a screen-share window that lasts until someone
           * remembers to press Done, doubled. Now it is rendered once, and once `copied` flips, the
           * value is replaced by its public prefix, derived client-side (the table below shows no
           * identifier at all).
           *
           * `readOnly` rather than `disabled` so the value can still be selected and copied by
           * keyboard, which is how a lot of people will actually take it.
           */
          <div
            className="card"
            style={{ margin: '8px 12px', borderColor: 'var(--accent)', animation: 'none' }}
          >
            {copied ? (
              <>
                <p style={{ fontSize: 12, margin: 0, fontWeight: 600 }}>Copied to your clipboard.</p>
                <p className="muted" style={{ fontSize: 12, margin: '6px 0 0' }}>
                  It is not shown again. Paste it into your shell as{' '}
                  <code className="mono">BATONDECK_TOKEN</code>, then keep it somewhere safe —
                  <code className="mono"> {display(minted)}</code> is all you will see from here.
                </p>
              </>
            ) : (
              <>
                <p style={{ fontSize: 12, margin: 0, fontWeight: 600 }}>
                  Copy this now — it will not be shown again.
                </p>
                <input
                  className="input mono"
                  readOnly
                  aria-label="Your new CLI token"
                  value={minted}
                  onFocus={(e) => e.currentTarget.select()}
                  style={{ width: '100%', fontSize: 12, margin: '8px 0 0' }}
                />
                <p className="muted" style={{ fontSize: 12, margin: '6px 0 0' }}>
                  Your shell reads it as <code className="mono">BATONDECK_TOKEN</code>.
                </p>
              </>
            )}
            <Cluster style={{ marginTop: 10 }}>
              {!copied && (
                <button
                  className="btn"
                  onClick={() => {
                    // Clipboard access can be denied or absent (insecure origin, permission). Falling
                    // back to "select it yourself" beats a button that silently does nothing — and on
                    // that path the value must STAY on screen, so `copied` is not set.
                    // `?.` alone short-circuited to undefined with no clipboard at all (a plain-HTTP
                    // LAN origin), so neither callback ran and the button did nothing.
                    const fail = () => setErr('Could not copy — select the value above and copy it manually.');
                    if (!navigator.clipboard) { fail(); return; }
                    void navigator.clipboard.writeText(minted).then(() => setCopied(true), fail);
                  }}
                >
                  Copy
                </button>
              )}
              <button
                className={copied ? 'btn' : 'btn secondary'}
                onClick={() => { setMinted(null); setCopied(false); mint.reset(); }}
              >
                Done
              </button>
            </Cluster>
          </div>
        ) : (
          <Cluster style={{ padding: '0 12px 8px' }}>
            <input
              className="input"
              placeholder="What is it for? e.g. staging cutover probe"
              aria-label="Token label"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter' && label.trim() && !mint.isPending) mint.mutate(); }}
              style={{ minWidth: 260 }}
            />
            <button className="btn" disabled={!label.trim() || mint.isPending} onClick={() => mint.mutate()}>
              {mint.isPending ? 'Minting…' : 'Mint token'}
            </button>
          </Cluster>
        )}

        {tokens.length === 0 ? (
          <p className="muted" style={{ fontSize: 12, padding: 12, margin: 0 }}>You hold no CLI tokens.</p>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ whiteSpace: 'nowrap' }}>
              <thead>
                <tr><th>Label</th><th>Status</th><th>Expires</th><th>Last used</th><th /></tr>
              </thead>
              <tbody>
                {tokens.map((t) => (
                  <tr key={t.id}>
                    <td>{t.label}</td>
                    {/* NO IDENTIFIER COLUMN. It rendered `t.display` — the public half of the id,
                        never the secret (`src/domain/cli-token.ts` keeps `secretHash` only and states
                        there is deliberately no Store method that could return one). But a
                        `bd_bd7c064b…` in a credentials table is indistinguishable from a leaked
                        token BY LOOKING, and the label is not enough to fix that. The row is
                        identified by its label; `t.id` is still what Revoke sends, it is simply never
                        rendered. The `display` field is gone from the tool response too — with no
                        renderer left it was a field with no consumer, and two spellings of one id is
                        how they drift. */}
                    <td>{t.status}</td>
                    <td>{new Date(t.expiresAt).toLocaleString()}</td>
                    <td>{t.lastUsedAt ? new Date(t.lastUsedAt).toLocaleString() : 'never'}</td>
                    <td>
                      {t.status === 'live' && (
                        <button
                          className="btn danger sm"
                          disabled={revoke.isPending}
                          onClick={() => revoke.mutate(t.id)}
                        >
                          {revoke.isPending ? 'Revoking…' : 'Revoke'}
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
    </section>
  );
}

export function Settings() {
  const qc = useQueryClient();
  // The sidebar's selection, shared both ways (P-434). On "All projects" it is '' and the project
  // sections give way to "Pick a project to configure": nothing is loaded, edited or stored (P-442).
  const { projectId, setProjectId, projects } = useSingleProject();

  const projQ = useQuery({ queryKey: ['project', projectId], queryFn: () => projectsApi.getProject(projectId), enabled: !!projectId });
  const boardsQ = useQuery({ queryKey: ['boards', projectId], queryFn: () => boardsApi.listBoards(projectId), enabled: !!projectId });
  const project = projQ.data?.project;
  // T-319: account id -> person. This roster carries role and Remove controls, so the id stays on
  // screen beneath the name — two accounts may legitimately render the same name.
  const memberDisplay = projQ.data?.memberDisplay ?? {};
  const [selBoard, setSelBoard] = useState('');
  // Whichever picker changed the project, the board picked in the last one no longer applies.
  useEffect(() => setSelBoard(''), [projectId]);
  const board = boardsQ.data?.boards.find((b) => b.id === selBoard) ?? boardsQ.data?.boards[0];

  /**
   * WHAT THE CALLER'S ROLE IN THIS PROJECT LETS THEM CHANGE HERE (P-432), the tiers of
   * `internal/domain/grants.go`: `add_column` is write (a worker or above), `update_column` and
   * `remove_column` structural (a member or above), `add_member`, `remove_member` and
   * `update_settings` admin. A control below its tier is disabled and says why, as the task's Status
   * and Claim do, instead of offering a click the server refuses. While the role loads every one is
   * closed with no reason yet (fail closed). Hiding is not enforcing: the server still decides.
   */
  const myRole = useMyProjectRole(projectId);
  const roleKnown = myRole !== undefined;
  const canAddColumn = mayCall(myRole, 'add_column');
  const canEditColumns = mayCall(myRole, 'update_column') && mayCall(myRole, 'remove_column');
  const canManageMembers = mayCall(myRole, 'add_member') && mayCall(myRole, 'remove_member');
  const canChangeWorkflow = mayCall(myRole, 'update_settings');
  const columnsWhy = !roleKnown || canEditColumns
    ? null
    : canAddColumn
      ? closedWhy(myRole, 'update_column', 'rename, limit or delete columns')!
      : closedWhy(myRole, 'add_column', 'change columns')!;
  const membersWhy = closedWhy(myRole, 'add_member', 'add or remove members') ?? null;
  const workflowWhy = closedWhy(myRole, 'update_settings', 'change workflow settings') ?? null;

  /**
   * THE BOARD PICKER FOR EVERY BOARD-SCOPED SETTING, rendered in each section that one governs.
   *
   * `selBoard` already drove BOTH Columns and State transitions, but the control only appeared in the
   * Columns header and only when a project had MORE THAN ONE board. So the transition graph was
   * configured by a selector the reader could not see, and on a single-board project nothing on the
   * page said which board was being edited. Reported from staging as "board selector is not available
   * to set State transitions".
   *
   * It now renders whenever a board exists — with one board it is a LABEL that happens to be a
   * select, which is the point: the reader learns what these settings apply to.
   */
  // A VISIBLE caption, not just `aria-label`. Unlabelled, this control and the sidebar's project
  // switcher look identical — and a project named "Board 1" makes them read as the same dropdown
  // shown twice. They select different things; the screen has to say which is which.
  const boardPicker =
    (boardsQ.data?.boards.length ?? 0) > 0 ? (
      <label className="picker-labelled">
      <span className="picker-caption">Board</span>
      <select
        className="input"
        aria-label="Board to configure"
        value={board?.id ?? ''}
        onChange={(e) => setSelBoard(e.target.value)}
        disabled={(boardsQ.data?.boards.length ?? 0) < 2}
        title={(boardsQ.data?.boards.length ?? 0) < 2 ? 'The only board in this project' : 'Choose the board these settings apply to'}
      >
        {boardsQ.data?.boards.map((b) => <option key={b.id} value={b.id}>{b.name}</option>)}
      </select>
      </label>
    ) : null;

  /**
   * YOUR OWN MCP sessions. `list_agent_sessions` is CALLER-SCOPED — it never returns another user's
   * sessions — so this table is inherently personal and needs no admin gate.
   *
   * It exists because "Manage sessions" on the Agents screen pointed at /admin: admin-only, and
   * workspace-WIDE. The one person who could click it was sent somewhere that manages everyone's
   * sessions, and every other member had no way to stop their own agent at all. The gateway route for
   * self-service disconnect had been there the whole time; only the SPA was missing.
   */
  /**
   * Honour `#sessions` and `#cli-tokens` in the URL. React Router does client-side navigation, so the browser never sees
   * a document load and never acts on the fragment — clicking "Manage your sessions" from the Agents
   * screen landed at the TOP of Settings, above Columns and Members, with the section the
   * link is named for far below the fold. Verified in the browser, which is the only place this is
   * observable at all.
   *
   * Deferred a frame: the section renders after the sessions query resolves, so scrolling on mount
   * would target an element that does not exist yet.
   */
  useEffect(() => {
    const id = window.location.hash.slice(1);
    if (id !== 'sessions' && id !== 'cli-tokens') return;
    const t = setTimeout(() => document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' }), 60);
    return () => clearTimeout(t);
  }, []);

  // The entry Agents merges presence frames into: the list never carries `device:` rows, so the answer
  // keeps the ones the cache holds when it lands, or a visit here would drop them from Agents (P-394).
  const sessionsQ = useQuery({
    queryKey: ['agentSessions'],
    queryFn: async () => {
      const fresh = await agentsApi.agentSessions(100);
      return { sessions: keepCachedDevices(fresh.sessions, qc.getQueryData<{ sessions: AgentSession[] }>(['agentSessions'])?.sessions) };
    },
  });
  // MCP sessions only: the device rows a presence frame merges into the same cache (`device:<pid>`,
  // presenceMerge.ts) are processes on this machine, not sessions disconnect_agent can end.
  const mySessions = (sessionsQ.data?.sessions ?? []).filter((s) => !s.sessionKey.startsWith('device:'));
  const [endErr, setEndErr] = useState<string | null>(null);
  const endSession = useMutation({
    mutationFn: (v: { identityId: string; agentId: string }) => agentsApi.disconnectOwnSession(v.identityId, v.agentId),
    onSuccess: () => {
      setEndErr(null);
      void qc.invalidateQueries({ queryKey: ['agentSessions'] });
      void qc.invalidateQueries({ queryKey: ['agentsessions'] });
    },
    onError: (e) => setEndErr(String(e instanceof Error ? e.message : e)),
  });

  const [member, setMember] = useState('');
  const [role, setRole] = useState('member');
  const [col, setCol] = useState({ name: '', status: 'TODO' as Status, wip: '' });
  const [drafts, setDrafts] = useState<Record<string, { name: string; wip: string }>>({});
  const [err, setErr] = useState<string | null>(null);

  const refresh = (): void => {
    void qc.invalidateQueries({ queryKey: ['project', projectId] });
    void qc.invalidateQueries({ queryKey: ['boards', projectId] });
  };
  const guard = <T,>(p: Promise<T>): Promise<T | void> => {
    setErr(null);
    return p.then((r) => { refresh(); return r; }).catch((e) => setErr(String(e instanceof Error ? e.message : e)));
  };

  const addMember = useMutation({ mutationFn: () => projectsApi.addMember(projectId, member, role), onSuccess: () => { setMember(''); refresh(); }, onError: (e) => setErr(String(e)) });
  const removeMember = useMutation({ mutationFn: (id: string) => projectsApi.removeMember(projectId, id), onSuccess: refresh, onError: (e) => setErr(String(e)) });
  const addColumn = useMutation({
    mutationFn: (wipLimit: number | null) => boardsApi.addColumn(projectId, board!.id, col.name, col.status, wipLimit),
    onSuccess: () => { setCol({ name: '', status: 'TODO', wip: '' }); refresh(); },
    onError: (e) => setErr(String(e)),
  });

  const settings = project?.settings;
  const draftFor = (id: string, name: string, wip: number | null): { name: string; wip: string } =>
    drafts[id] ?? { name, wip: wip == null ? '' : String(wip) };

  const sectionTitle: React.CSSProperties = { fontSize: 14, fontWeight: 600, margin: '0 0 12px', color: 'var(--text)' };
  const headRow: React.CSSProperties = {
    display: 'grid',
    gridTemplateColumns: '1fr 200px 110px 80px',
    gap: 12,
    padding: '10px 14px',
    background: 'var(--surface-lowest)',
    borderBottom: '1px solid var(--border)',
    fontSize: 11,
    textTransform: 'uppercase',
    letterSpacing: '0.04em',
    color: 'var(--text-muted)',
    fontWeight: 600,
  };
  const dataRow: React.CSSProperties = {
    display: 'grid',
    gridTemplateColumns: '1fr 200px 110px 80px',
    gap: 12,
    padding: '10px 14px',
    alignItems: 'center',
    borderBottom: '1px solid var(--border)',
  };

  return (
    <>
      <div className="topbar">
        <h1 style={{ fontSize: 16, margin: 0 }}>Settings</h1>
        <div style={{ flex: 1 }} />
        {/* Captioned like the board picker, and named apart from the sidebar's "Project" (P-433). */}
        <label className="picker-labelled">
          <span className="picker-caption">Project</span>
          <select className="input" aria-label="Project to configure" value={projectId} onChange={(e) => setProjectId(e.target.value)}>
            {/* No project chosen (the sidebar's "All projects"): a placeholder, never a default pick. */}
            {!projectId && <option value="" disabled>Pick a project</option>}
            {projects.map((p) => <option key={p.projectId} value={p.projectId}>{p.name}</option>)}
          </select>
        </label>
      </div>
      <div className="page" style={{ maxWidth: 820 }}>
        <header style={{ marginBottom: 24 }}>
          <h1 style={{ fontSize: 20, margin: '0 0 4px', letterSpacing: '-0.01em' }}>Board Configuration</h1>
          <p className="muted" style={{ margin: 0, fontSize: 14 }}>
            Manage columns, workflows, members, and limits for your shared Kanban board.
          </p>
        </header>

        {err && <p className="error" style={{ fontSize: 12, marginBottom: 16 }}>{err}</p>}

        {/* The project sections need one project. With none chosen they are replaced by the prompt. */}
        {!projectId ? (
          <section style={{ marginBottom: 36 }}>
            <Panel pad>
              <h3 style={{ ...sectionTitle, margin: '0 0 4px' }}>Pick a project to configure</h3>
              <p className="muted" style={{ margin: 0, fontSize: 14 }}>
                {projects.length > 0
                  ? 'Columns, members and workflow settings belong to one project. Choose it in the Project picker above, or in the sidebar.'
                  : 'Columns, members and workflow settings belong to a project, and this account is not a member of one yet.'}
              </p>
            </Panel>
          </section>
        ) : (
        <>
        {/* Columns */}
        <section style={{ marginBottom: 36 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 12 }}>
            <h3 style={{ ...sectionTitle, margin: 0 }}>Columns</h3>
            <div style={{ flex: 1 }} />
            {boardPicker}
          </div>
          {board ? (
            <Panel pad>
              <div style={headRow}>
                <div>Column Name</div>
                <div>Mapped Status</div>
                <div style={{ textAlign: 'right' }}>WIP Limit</div>
                <div />
              </div>
              {board.columns.map((c) => {
                const d = draftFor(c.id, c.name, c.wipLimit);
                const dirty = d.name !== c.name || d.wip !== (c.wipLimit == null ? '' : String(c.wipLimit));
                return (
                  <div key={c.id} style={dataRow}>
                    <input
                      className="input"
                      style={{ width: '100%' }}
                      value={d.name}
                      aria-label={`Name of column ${c.name}`}
                      disabled={!canEditColumns}
                      aria-describedby={columnsWhy ? 'columns-closed' : undefined}
                      onChange={(e) => setDrafts({ ...drafts, [c.id]: { ...d, name: e.target.value } })}
                    />
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 14 }}>
                      <span className="dot" style={{ background: STATUS_COLOR[c.status] }} />
                      <span style={{ color: 'var(--text-2)' }}>{c.status.replace('_', ' ')}</span>
                    </div>
                    <input
                      className="input mono"
                      style={{ width: '100%', textAlign: 'right' }}
                      placeholder="∞"
                      value={d.wip}
                      aria-label={`WIP limit of column ${c.name}`}
                      disabled={!canEditColumns}
                      aria-describedby={columnsWhy ? 'columns-closed' : undefined}
                      onChange={(e) => setDrafts({ ...drafts, [c.id]: { ...d, wip: e.target.value } })}
                    />
                    <Cluster gap={4} justify='flex-end'>
                      <button
                        className="btn secondary"
                        style={{ padding: '5px 9px' }}
                        aria-label={`Save column ${c.name}`}
                        disabled={!dirty || !canEditColumns}
                        // Clear the draft only on success — guard() swallows the rejection (surfacing it via
                        // err), so chaining after guard would wipe the user's edits on failure too.
                        onClick={() => {
                          const wip = parseWip(d.wip);
                          if (wip === undefined) { setErr(WIP_INVALID); return; }
                          void guard(
                            boardsApi.updateColumn(projectId, board.id, c.id, { name: d.name, wipLimit: wip })
                              .then((r) => { setDrafts((prev) => { const n = { ...prev }; delete n[c.id]; return n; }); return r; }),
                          );
                        }}
                      >
                        Save
                      </button>
                      <button
                        className="btn secondary"
                        style={{ padding: '5px 9px', color: 'var(--text-muted)' }}
                        aria-label={`Delete column ${c.name}`}
                        title={columnsWhy ?? 'Delete column'}
                        disabled={!canEditColumns}
                        aria-describedby={columnsWhy ? 'columns-closed' : undefined}
                        onClick={() => void guard(boardsApi.removeColumn(projectId, board.id, c.id))}
                      >
                        <Icon name="close" size={12} />
                      </button>
                    </Cluster>
                  </div>
                );
              })}
              {/* Add column footer */}
              <div style={{ padding: 12, background: 'var(--surface-lowest)', display: 'flex', gap: 6, alignItems: 'center' }}>
                <input
                  className="input"
                  style={{ flex: 1 }}
                  placeholder="New column name"
                  aria-label="New column name"
                  value={col.name}
                  disabled={!canAddColumn}
                  onChange={(e) => setCol({ ...col, name: e.target.value })}
                />
                <select className="input" style={{ width: 160 }} aria-label="New column status" value={col.status} disabled={!canAddColumn} onChange={(e) => setCol({ ...col, status: e.target.value as Status })}>
                  {STATUSES.map((s) => <option key={s} value={s}>{s.replace('_', ' ')}</option>)}
                </select>
                <input
                  className="input mono"
                  style={{ width: 80, textAlign: 'right' }}
                  placeholder="∞"
                  value={col.wip}
                  aria-label="New column WIP limit"
                  disabled={!canAddColumn}
                  onChange={(e) => setCol({ ...col, wip: e.target.value })}
                />
                <button
                  className="btn"
                  disabled={!canAddColumn || !col.name || addColumn.isPending}
                  title={!canAddColumn && columnsWhy ? columnsWhy : undefined}
                  aria-describedby={!canAddColumn && columnsWhy ? 'columns-closed' : undefined}
                  onClick={() => {
                    const wip = parseWip(col.wip);
                    if (wip === undefined) { setErr(WIP_INVALID); return; }
                    setErr(null);
                    addColumn.mutate(wip);
                  }}
                >
                  + Add Column
                </button>
              </div>
              {columnsWhy && (
                <p id="columns-closed" className="muted" style={{ margin: 0, padding: '8px 12px', fontSize: 12 }}>{columnsWhy}</p>
              )}
            </Panel>
          ) : (
            <Panel pad>
              <p className="muted" style={{ margin: 0 }}>
                This project has no board yet — transitions are configured per board. Create one from
                the Projects screen and it will appear here.
              </p>
            </Panel>
          )}
        </section>

        {/* Members */}
        <section style={{ marginBottom: 24 }}>
          <h3 style={sectionTitle}>Members</h3>
          <Panel>
            <table>
              <thead>
                <tr>
                  <th>Identity</th>
                  <th>Role</th>
                  <th style={{ textAlign: 'right' }} />
                </tr>
              </thead>
              <tbody>
                {project && Object.entries(project.members).map(([id, r]) => {
                  const name = memberDisplay[id];
                  return (
                  <tr key={id}>
                    <td>
                      <Cluster gap={8}>
                        <Avatar id={id} label={name} />
                        {name ? (
                          <span style={{ display: 'inline-flex', flexDirection: 'column', lineHeight: 1.3 }}>
                            <span style={{ fontSize: 14, fontWeight: 600 }}>{name}</span>
                            <span className="mono" style={{ fontSize: 11, color: 'var(--text-muted)' }}>{id}</span>
                          </span>
                        ) : (
                          <span className="mono" style={{ fontSize: 12 }}>{id}</span>
                        )}
                      </Cluster>
                    </td>
                    <td>
                      <span className={`role-badge${r === 'admin' ? ' admin' : ''}`}>{r}</span>
                    </td>
                    <td style={{ textAlign: 'right' }}>
                      <button
                        className="btn secondary"
                        style={{ padding: '5px 9px', color: 'var(--text-muted)' }}
                        aria-label={`Remove ${name ?? id}`}
                        disabled={!canManageMembers}
                        title={membersWhy ?? undefined}
                        aria-describedby={membersWhy ? 'members-closed' : undefined}
                        onClick={() => removeMember.mutate(id)}
                      >
                        Remove
                      </button>
                    </td>
                  </tr>
                  );
                })}
              </tbody>
            </table>
            <div style={{ padding: 12, background: 'var(--surface-lowest)', display: 'flex', gap: 6, alignItems: 'center' }}>
              <input
                className="input"
                style={{ flex: 1 }}
                type="email"
                placeholder="teammate@example.com"
                aria-label="New member login"
                value={member}
                disabled={!canManageMembers}
                onChange={(e) => setMember(e.target.value)}
              />
              <select className="input" style={{ width: 130 }} aria-label="New member role" value={role} disabled={!canManageMembers} onChange={(e) => setRole(e.target.value)}>
                {HUMAN_ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
              </select>
              <button
                className="btn"
                disabled={!canManageMembers || !member}
                title={membersWhy ?? undefined}
                aria-describedby={membersWhy ? 'members-closed' : undefined}
                onClick={() => addMember.mutate()}
              >
                + Add Member
              </button>
            </div>
            {membersWhy && (
              <p id="members-closed" className="muted" style={{ margin: 0, padding: '8px 12px', fontSize: 12 }}>{membersWhy}</p>
            )}
          </Panel>
        </section>
        </>
        )}

        <CliTokens />
        {/* Workspace scope, not project scope: it renders nothing for anyone but the workspace admin. */}
        <UsersSection />
        {/*
          YOUR settings — deliberately OUTSIDE the project-scoped sections. These follow the person,
          not the project, so they must render even when the account has no projects at all; nesting them under the project picker would hide them from exactly
          the user who has just signed up. Columns, Members and CLI tokens come first (usability #9).
        */}
        <MyTokenBudget />

        {/* Workflow / policy */}
        {projectId && (
        <section style={{ marginBottom: 36 }}>
          <h3 style={sectionTitle}>Workflow Settings</h3>
          {settings ? (
            <Stack gap={12}>
              <Panel pad>
                <Cluster gap={16} align='flex-start' justify='space-between'>
                  <div>
                    <div id="review-step-label" style={{ fontSize: 14, fontWeight: 600 }}>Require review step</div>
                    <p className="muted" style={{ margin: '2px 0 0', fontSize: 12 }}>
                      When off, completing a task sends it straight to Done instead of Review. Useful for simple pipelines.
                    </p>
                  </div>
                  <Toggle
                    labelledBy="review-step-label"
                    checked={settings.reviewEnabled}
                    disabled={!canChangeWorkflow}
                    title={workflowWhy ?? undefined}
                    describedBy={workflowWhy ? 'workflow-closed' : undefined}
                    onChange={(v) => void guard(projectsApi.updateSettings(projectId, { reviewEnabled: v }))}
                  />
                </Cluster>
                {workflowWhy && (
                  <p id="workflow-closed" className="muted" style={{ margin: '8px 0 0', fontSize: 12 }}>{workflowWhy}</p>
                )}
              </Panel>
              {/* T-43: "Enforce transaction quota" + "Quota cap" used to sit here. They are the OPERATOR's
                  cost control over the tenant, not a workflow choice, and the core refuses both fields
                  from anyone who is not a workspace admin (src/tools/projects.ts, update_settings) — so
                  rendering them here only offered a project admin a guaranteed FORBIDDEN. They now live
                  in the /admin portal (web/src/screens/Admin.tsx, ProjectQuotaCard). */}
            </Stack>
          ) : (
            <Panel pad>
              <WaitLoader />
            </Panel>
          )}
        </section>
        )}


        {/* Anchored so `/settings#sessions` from the Agents screen lands here. */}

        <section id="sessions">
          <Panel
            title="Your MCP sessions"
            right={<span className="muted" style={{ fontSize: 12 }}>{mySessions.length} logged</span>}
          >
            {endErr && <p className="error" style={{ fontSize: 12, margin: '8px 12px' }}>{endErr}</p>}
            {mySessions.length === 0 ? (
              <p className="muted" style={{ fontSize: 12, padding: 12, margin: 0 }}>
                No agent has connected under your identity yet.
              </p>
            ) : (
              <div style={{ overflowX: 'auto' }}>
                <table style={{ whiteSpace: 'nowrap' }}>
                  <thead>
                    <tr>
                      <th>Agent</th>
                      <th>Client</th>
                      <th>Last seen</th>
                      <th />
                    </tr>
                  </thead>
                  <tbody>
                    {mySessions.map((s) => (
                      <tr key={s.sessionKey}>
                        <td>
                          <Cluster gap={8}>
                            {/* `tool` from the CLIENT as well as the name: `toolKey` matches a
                                `claude-*` NAME prefix first, but an agent called `zygomorphic-emu`
                                over `claude-code` has no such prefix, so without the client it fell
                                back to initials. The client is the more reliable signal of the two. */}
                            <Avatar id={s.agentName} tool={toolKey(s.agentName, s.clientName)} />
                            <span>{s.agentName}</span>
                          </Cluster>
                        </td>
                        <td className="mono">{s.clientName}</td>
                        <td className="muted">{new Date(s.lastSeenAt).toLocaleString()}</td>
                        <td style={{ textAlign: 'right' }}>
                          {s.revokedAt ? (
                            <span className="muted" style={{ fontSize: 12 }}>disconnected</span>
                          ) : (
                            <button
                              className="btn secondary"
                              disabled={endSession.isPending}
                              onClick={() => endSession.mutate({ identityId: s.identityId, agentId: s.agentId })}
                              title="End this session's MCP connection. The agent's next request is refused and it initialises again."
                            >
                              Disconnect
                            </button>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Panel>
        </section>
      </div>
    </>
  );
}
