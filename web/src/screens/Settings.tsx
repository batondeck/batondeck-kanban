import { useEffect, useRef, useState } from 'react';
import { toolKey } from '../ToolLogo.js';
import { Icon } from '../icons.js';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import * as agentsApi from '../api/agents.js';
import * as boardsApi from '../api/boards.js';
import * as meApi from '../api/me.js';
import * as projectsApi from '../api/projects.js';
import * as tenantsApi from '../api/tenants.js';
import { WaitLoader } from '../brand.js';
import { Avatar, ConfirmButton, timeAgo } from '../components.js';
import type { Status } from '../types.js';
import { STATUSES } from '../types.js';
import { Cluster, Panel, Stack } from '../ui.js';
import { can } from '../permissions.js';
import { useAuth } from '../auth.js';

const STATUS_COLOR: Record<Status, string> = {
  TODO: 'var(--ready)',
  IN_PROGRESS: 'var(--progress)',
  BLOCKED: 'var(--blocked)',
  REVIEW: 'var(--review)',
  DONE: 'var(--done)',
};

/** A small bordered card used as a settings panel. */
/** Pill toggle switch matching the mockup. */
function Toggle({ checked, onChange }: { checked: boolean; onChange: (v: boolean) => void }) {
  return (
    <button
      role="switch"
      aria-checked={checked}
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
            <div style={{ fontSize: 14, fontWeight: 600 }}>Your ticket token limit</div>
            <p className="muted" style={{ margin: '2px 0 0', fontSize: 12 }}>
              Leave empty to use the project&apos;s limit. You can set a <strong>lower</strong> limit for
              yourself; a higher one has no effect, because a project&apos;s limit is a ceiling you cannot raise.
            </p>
            <Cluster gap={8} align="center" style={{ marginTop: 8 }}>
              <input
                className="input"
                type="number"
                min={100}
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
 * INVITE A TEAMMATE (T-389) — the first thing in the product that reads `batondeck:admin`.
 *
 * *** THE VISIBILITY CHECK HERE IS NOT THE SECURITY CONTROL. *** `can()` reads the session's
 * permissions, which come from the `cr` cookie and can be up to `ROLE_MAX_AGE_SEC` stale — and are
 * empty for any session minted before roles reached it. The real gate re-reads the caller's LIVE
 * membership role at the server on every call. This block only decides whether to show the form; the
 * failure mode of getting it wrong is a hidden control, never an unauthorized invite.
 *
 * It renders nothing rather than a disabled control for non-admins: an ordinary member has no action
 * to take here and a greyed-out box would just raise a question they cannot answer.
 */
function InviteTeammate({ permissions }: { permissions: readonly string[] | undefined }) {
  const [email, setEmail] = useState('');
  const [sent, setSent] = useState<string | null>(null);
  const qc = useQueryClient();
  const admin = can(permissions, 'batondeck:admin');
  /**
   * Who has been invited and has not accepted (T-396). `enabled: admin` spares a member a guaranteed
   * 403 — a courtesy, NOT the control: the gate is the server's live membership read, and `can()` here
   * is up to 8h stale.
   */
  const pending = useQuery({ queryKey: ['tenant-invitations'], queryFn: () => tenantsApi.listInvitations(), enabled: admin });
  const invite = useMutation({
    mutationFn: (e: string) => tenantsApi.inviteTeammate(e),
    onSuccess: (_r, e) => {
      setSent(e);
      setEmail('');
      // The new invitation must appear in the list below without a reload — the reported bug was
      // precisely that a successful send left no trace anywhere in the product.
      void qc.invalidateQueries({ queryKey: ['tenant-invitations'] });
    },
  });
  const act = useMutation({
    mutationFn: ({ id, action }: { id: string; action: 'resend' | 'revoke' }) =>
      action === 'resend' ? tenantsApi.resendInvitation(id) : tenantsApi.revokeInvitation(id),
    // Refetch on failure too: a 404 here means the invitation was accepted or revoked out from under
    // this tab, and the list is the thing that is wrong. Leaving a stale row would invite a retry loop.
    onSettled: () => qc.invalidateQueries({ queryKey: ['tenant-invitations'] }),
  });
  if (!admin) return null;
  return (
    // `pad` is REQUIRED here and its absence is what produced the reported glitch: `Panel`'s own
    // docblock says a body of prose or controls without it "sit[s] flush against the 1.5px rule, which
    // reads as a rendering fault rather than a design choice". That is precisely what shipped — the
    // input and button ran into the panel border and the confirmation line sat outside it.
    <Panel title="Invite a teammate" pad>
      <Stack gap={12}>
        <p className="muted" style={{ fontSize: 14, margin: 0 }}>
          They receive an email from WorkOS and join this organization as a member. Give them project
          access separately, from the project&rsquo;s Members list.
        </p>
        <Cluster gap={8}>
          <input
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="teammate@example.com"
            aria-label="Teammate email"
            // `minWidth: 0` — a flex child's default `min-width: auto` refuses to shrink below its
            // intrinsic width, which is how an input in a `Cluster` pushes the button past the edge.
            style={{ flex: 1, minWidth: 0, padding: '8px 10px', fontSize: 14 }}
          />
          <button
            type="button"
            className="btn primary"
            disabled={!email.includes('@') || invite.isPending}
            onClick={() => invite.mutate(email.trim())}
          >
            {invite.isPending ? 'Sending…' : 'Send invite'}
          </button>
        </Cluster>
        {invite.isError && (
          <p className="error" style={{ fontSize: 12, margin: 0 }}>
            {invite.error instanceof Error ? invite.error.message : 'Could not send the invitation.'}
          </p>
        )}
        {sent && !invite.isError && (
          <p className="muted" style={{ fontSize: 12, margin: 0 }}>Invitation sent to {sent}.</p>
        )}

        {/*
          THE PENDING LIST (T-396). An EMPTY list renders its own line rather than nothing: "no
          outstanding invitations" and "this panel is broken" look identical when the answer is blank,
          and telling them apart is the whole point of the ticket.
        */}
        <div style={{ borderTop: '1px solid var(--border)', paddingTop: 12 }}>
          <h4 style={{ margin: '0 0 8px', fontSize: 14 }}>Pending invitations</h4>
          {pending.isPending && <WaitLoader />}
          {pending.isError && (
            <p className="error" style={{ fontSize: 12, margin: 0 }}>
              {pending.error instanceof Error ? pending.error.message : 'Could not read the pending invitations.'}
            </p>
          )}
          {pending.data?.invitations.length === 0 && (
            <p className="muted" style={{ fontSize: 12, margin: 0 }}>Nobody is waiting to accept an invitation.</p>
          )}
          <Stack gap={8}>
            {pending.data?.invitations.map((i) => (
              <Cluster key={i.id} gap={8}>
                <span style={{ flex: 1, minWidth: 0, fontSize: 14, overflowWrap: 'anywhere' }}>{i.email}</span>
                <span className="muted" style={{ fontSize: 12 }}>
                  {/* `createdAt` is an ISO 8601 string from WorkOS; the browser's locale formats it. */}
                  Invited {new Date(i.createdAt).toLocaleDateString()}
                </span>
                <button
                  type="button"
                  className="btn secondary"
                  disabled={act.isPending}
                  onClick={() => act.mutate({ id: i.id, action: 'resend' })}
                >
                  Resend
                </button>
                <button
                  type="button"
                  className="btn secondary"
                  disabled={act.isPending}
                  onClick={() => act.mutate({ id: i.id, action: 'revoke' })}
                >
                  Revoke
                </button>
              </Cluster>
            ))}
          </Stack>
          {act.isError && (
            <p className="error" style={{ fontSize: 12, margin: '8px 0 0' }}>
              {act.error instanceof Error ? act.error.message : 'That invitation could not be updated.'}
            </p>
          )}
        </div>
      </Stack>
    </Panel>
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
 * The STABLE marketplace credential (owner spec 2026-09-06): one mk_… key per user per org, no
 * expiry, re-showable BY DESIGN — unlike a CLI token it is stored KEK-encrypted server-side so a
 * new laptop gets the same URL. Anonymous in the link, accountable on the record: the usage table
 * below is the leak story — every fetch names this user, with IP, client and what was downloaded.
 */
function MarketplaceAccess() {
  const { pullBase } = useAuth();
  const qc = useQueryClient();
  const invalidate = (): void => void qc.invalidateQueries({ queryKey: ['marketplaceKey'] });
  // The key is contractually STABLE, so refetch-on-focus buys nothing and costs the full usage
  // read every alt-tab: fetch once, refresh only through the mutations and the explicit button.
  const q = useQuery({ queryKey: ['marketplaceKey'], queryFn: () => meApi.getMarketplaceKey(), staleTime: Infinity });
  const create = useMutation({ mutationFn: () => meApi.createMarketplaceKey(), onSuccess: invalidate });
  const rotate = useMutation({ mutationFn: () => meApi.rotateMarketplaceKey(), onSuccess: invalidate });
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle');
  const copyTimer = useRef<number | null>(null);
  useEffect(() => () => { if (copyTimer.current !== null) window.clearTimeout(copyTimer.current); }, []);
  const flashCopy = (state: 'copied' | 'failed'): void => {
    setCopyState(state);
    if (copyTimer.current !== null) window.clearTimeout(copyTimer.current);
    copyTimer.current = window.setTimeout(() => setCopyState('idle'), 1500);
  };
  // Read defensively: the harness's stub fetch answers {} for unknown endpoints, and a screen that
  // throws on that blanks EVERY Settings panel, not just this one.
  const key = typeof q.data?.key === 'string' ? q.data.key : null;
  const uses = q.data?.recentUses ?? [];
  const url = key !== null && pullBase !== null ? `${pullBase}/pull/k/${key}` : null;
  const mutationErr = rotate.isError ? 'Rotate failed — the old URL is still live. Retry.' : create.isError ? 'Could not create the key. Retry.' : null;
  return (
    <section id="marketplace-access">
      <Panel
        title="Marketplace access"
        right={typeof q.data?.useCount === 'number' && key !== null ? <span className="muted" style={{ fontSize: 12 }}>{q.data.useCount} uses</span> : undefined}
      >
        <p className="muted" style={{ fontSize: 12, padding: '8px 12px', margin: 0 }}>
          Your personal plugin-marketplace URL. It never expires, stays the same on every device, and
          serves only the plugins your audiences receive. The link itself is the credential — anyone
          holding it pulls as you, and every use lands in the record below, so treat it like a key
          and rotate it if it leaks.
        </p>
        {q.isError && (
          /* A failed read must not read as "no key": the endpoint can fail while the key keeps
           * serving pulls, and Rotate is exactly the recovery that works without the read. */
          <Cluster gap={8} style={{ padding: '0 12px 12px' }}>
            <span style={{ fontSize: 12, color: 'var(--blocked)' }}>
              Could not load your marketplace key{q.error instanceof Error ? ` — ${q.error.message}` : ''}.
            </span>
            <button className="btn secondary" onClick={() => void q.refetch()}>Retry</button>
            <ConfirmButton className="btn secondary" label="Rotate anyway" confirmLabel="Rotate — old URL dies" onConfirm={() => rotate.mutate()} />
          </Cluster>
        )}
        {!q.isError && q.data !== undefined && key === null && (
          <Cluster gap={8} style={{ padding: '0 12px 12px' }}>
            <span className="muted" style={{ fontSize: 12 }}>No key yet — creating one is an explicit act, nothing is minted by just looking.</span>
            <button className="btn" disabled={create.isPending} onClick={() => create.mutate()}>
              {create.isPending ? 'Creating…' : 'Create marketplace URL'}
            </button>
          </Cluster>
        )}
        {url !== null && (
          <Cluster gap={8} style={{ padding: '0 12px 8px' }}>
            <input className="input mono" readOnly value={url} onFocus={(e) => e.currentTarget.select()} style={{ flex: 1, minWidth: 260, fontSize: 12 }} aria-label="Your marketplace URL" />
            <button
              className="btn"
              onClick={() => {
                if (!navigator.clipboard) { flashCopy('failed'); return; }
                navigator.clipboard.writeText(url).then(() => flashCopy('copied'), () => flashCopy('failed'));
              }}
            >
              {copyState === 'copied' ? 'Copied' : copyState === 'failed' ? 'Copy failed — select it' : 'Copy'}
            </button>
            <ConfirmButton className="btn secondary" label="Rotate" confirmLabel="Rotate — old URL dies" onConfirm={() => rotate.mutate()} />
          </Cluster>
        )}
        {mutationErr !== null && (
          <p style={{ fontSize: 12, color: 'var(--blocked)', padding: '0 12px 8px', margin: 0 }}>{mutationErr}</p>
        )}
        {uses.length > 0 && (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ whiteSpace: 'nowrap' }}>
              <thead><tr><th>When</th><th>What</th><th>From</th><th>Client</th></tr></thead>
              <tbody>
                {uses.map((u, i) => (
                  <tr key={i}>
                    <td className="muted">{timeAgo(u.ts)}</td>
                    <td>{u.kind === 'archive' ? `${u.pluginName ?? ''} ${u.semver ?? ''}` : 'catalogue'}</td>
                    <td className="mono">{u.ip ?? '—'}</td>
                    <td className="muted" style={{ maxWidth: 260, overflow: 'hidden', textOverflow: 'ellipsis' }}>{u.userAgent ?? '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {key !== null && uses.length === 0 && (
          <p className="muted" style={{ fontSize: 12, padding: '0 12px 12px', margin: 0 }}>Never used yet.</p>
        )}
      </Panel>
    </section>
  );
}

function CliTokens() {
  const qc = useQueryClient();
  const tokensQ = useQuery({ queryKey: ['cliTokens'], queryFn: () => meApi.listCliTokens() });
  const [label, setLabel] = useState('');
  const [minted, setMinted] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const tokens = tokensQ.data?.tokens ?? [];

  const refresh = (): void => void qc.invalidateQueries({ queryKey: ['cliTokens'] });
  const mint = useMutation({
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
    <section id="cli-tokens">
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
                    void navigator.clipboard?.writeText(minted).then(
                      () => setCopied(true),
                      () => setErr('Could not copy — select the value above and copy it manually.'),
                    );
                  }}
                >
                  Copy
                </button>
              )}
              <button
                className={copied ? 'btn' : 'btn secondary'}
                onClick={() => { setMinted(null); setCopied(false); }}
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
              onKeyDown={(e) => { if (e.key === 'Enter' && label.trim()) mint.mutate(); }}
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
  const { permissions } = useAuth();
  const qc = useQueryClient();
  const projects = useQuery({ queryKey: ['projects'], queryFn: () => projectsApi.listProjects() });
  const [selected, setSelected] = useState('');
  const projectId = selected || projects.data?.projects[0]?.projectId || '';

  const projQ = useQuery({ queryKey: ['project', projectId], queryFn: () => projectsApi.getProject(projectId), enabled: !!projectId });
  const boardsQ = useQuery({ queryKey: ['boards', projectId], queryFn: () => boardsApi.listBoards(projectId), enabled: !!projectId });
  const project = projQ.data?.project;
  // T-319: account id -> person. This roster carries role and Remove controls, so the id stays on
  // screen beneath the name — two accounts may legitimately render the same name.
  const memberDisplay = projQ.data?.memberDisplay ?? {};
  const [selBoard, setSelBoard] = useState('');
  const board = boardsQ.data?.boards.find((b) => b.id === selBoard) ?? boardsQ.data?.boards[0];

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
   * Honour `#sessions` in the URL. React Router does client-side navigation, so the browser never sees
   * a document load and never acts on the fragment — clicking "Manage your sessions" from the Agents
   * screen landed at the TOP of Settings, above Columns and Members, with the section the
   * link is named for far below the fold. Verified in the browser, which is the only place this is
   * observable at all.
   *
   * Deferred a frame: the section renders after the sessions query resolves, so scrolling on mount
   * would target an element that does not exist yet.
   */
  useEffect(() => {
    if (window.location.hash !== '#sessions') return;
    const t = setTimeout(() => document.getElementById('sessions')?.scrollIntoView({ behavior: 'smooth', block: 'start' }), 60);
    return () => clearTimeout(t);
  }, []);

  const sessionsQ = useQuery({ queryKey: ['agentSessions'], queryFn: () => agentsApi.agentSessions(100) });
  const mySessions = sessionsQ.data?.sessions ?? [];
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
    mutationFn: () => boardsApi.addColumn(projectId, board!.id, col.name, col.status, col.wip ? Number(col.wip) : null),
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
        <select className="input" value={projectId} onChange={(e) => { setSelected(e.target.value); setSelBoard(''); }}>
          {projects.data?.projects.map((p) => <option key={p.projectId} value={p.projectId}>{p.name}</option>)}
        </select>
      </div>
      <div className="page" style={{ maxWidth: 820 }}>
        {/* T-389 — ABOVE the board configuration because it is ORGANIZATION scope, not project scope.
            It renders nothing for anyone who is not a tenant admin. */}
        <InviteTeammate permissions={permissions} />
        <header style={{ marginBottom: 24 }}>
          <h1 style={{ fontSize: 20, margin: '0 0 4px', letterSpacing: '-0.01em' }}>Board Configuration</h1>
          <p className="muted" style={{ margin: 0, fontSize: 14 }}>
            Manage columns, workflows, members, and limits for your shared Kanban board.
          </p>
        </header>

        {err && <p className="error" style={{ fontSize: 12, marginBottom: 16 }}>{err}</p>}

        {/*
          YOUR settings — deliberately first, and deliberately OUTSIDE the project-scoped sections
          below. These follow the person, not the project, so they must render even when the account
          has no projects at all; nesting them under the project picker would hide them from exactly
          the user who has just signed up.
        */}
        <MyTokenBudget />

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
                      onChange={(e) => setDrafts({ ...drafts, [c.id]: { ...d, wip: e.target.value } })}
                    />
                    <Cluster gap={4} justify='flex-end'>
                      <button
                        className="btn secondary"
                        style={{ padding: '5px 9px' }}
                        disabled={!dirty}
                        // Clear the draft only on success — guard() swallows the rejection (surfacing it via
                        // err), so chaining after guard would wipe the user's edits on failure too.
                        onClick={() => void guard(
                          boardsApi.updateColumn(projectId, board.id, c.id, { name: d.name, wipLimit: d.wip ? Number(d.wip) : null })
                            .then((r) => { setDrafts((prev) => { const n = { ...prev }; delete n[c.id]; return n; }); return r; }),
                        )}
                      >
                        Save
                      </button>
                      <button
                        className="btn secondary"
                        style={{ padding: '5px 9px', color: 'var(--text-muted)' }}
                        title="Delete column"
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
                  value={col.name}
                  onChange={(e) => setCol({ ...col, name: e.target.value })}
                />
                <select className="input" style={{ width: 160 }} value={col.status} onChange={(e) => setCol({ ...col, status: e.target.value as Status })}>
                  {STATUSES.map((s) => <option key={s} value={s}>{s.replace('_', ' ')}</option>)}
                </select>
                <input
                  className="input mono"
                  style={{ width: 80, textAlign: 'right' }}
                  placeholder="∞"
                  value={col.wip}
                  onChange={(e) => setCol({ ...col, wip: e.target.value })}
                />
                <button className="btn" disabled={!col.name} onClick={() => addColumn.mutate()}>+ Add Column</button>
              </div>
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

        {/* Workflow / policy */}
        <section style={{ marginBottom: 36 }}>
          <h3 style={sectionTitle}>Workflow Settings</h3>
          {settings ? (
            <Stack gap={12}>
              <Panel pad>
                <Cluster gap={16} align='flex-start' justify='space-between'>
                  <div>
                    <div style={{ fontSize: 14, fontWeight: 600 }}>Require review step</div>
                    <p className="muted" style={{ margin: '2px 0 0', fontSize: 12 }}>
                      When off, completing a task sends it straight to Done instead of Review. Useful for simple pipelines.
                    </p>
                  </div>
                  <Toggle checked={settings.reviewEnabled} onChange={(v) => void guard(projectsApi.updateSettings(projectId, { reviewEnabled: v }))} />
                </Cluster>
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
                value={member}
                onChange={(e) => setMember(e.target.value)}
              />
              <select className="input" style={{ width: 130 }} value={role} onChange={(e) => setRole(e.target.value)}>
                {HUMAN_ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
              </select>
              <button className="btn" disabled={!member} onClick={() => addMember.mutate()}>+ Add Member</button>
            </div>
          </Panel>
        </section>

        {/* Anchored so `/settings#sessions` from the Agents screen lands here. */}
        <MarketplaceAccess />
      <CliTokens />

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
                              title="Revoke this session. The agent must reconnect (and rotate its id) to come back."
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
