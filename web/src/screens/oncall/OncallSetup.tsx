/**
 * The on-call setup drawer (spec §10): pick a source, configure an integration completely, manage
 * the ones that exist, and read each one's delivery log.
 *
 * TWO SAFETY PROPERTIES THIS FILE OWNS, both about bytes we did not write:
 *  1. A REJECTED delivery's payload is never rendered. The error string is shown as an escaped text
 *     node (React's default), and the payload itself is only ever reachable through a real
 *     navigation to the attachment route — never fetched and injected.
 *  2. A minted credential is displayed EXACTLY ONCE, in a panel the operator must dismiss. Nothing
 *     re-reads it, because nothing can: the server keeps only an encrypted or hashed copy.
 *
 * THE SOURCE LIST IS NO LONGER HERE. It was — a hand-mirrored copy of the core's catalog, including
 * each vendor's auth capability, kept honest by a lexical drift test. It now arrives from
 * `GET /api/oncall/sources`, so adding a telemetry source needs no edit in this package at all.
 */
import { useEffect, useMemo, useRef, useState } from 'react';
import { useMayReadIntegrations } from '../../displayNames.js';
import { Icon } from '../../icons.js';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import * as oncallApi from '../../api/oncall.js';
import type { Delivery, Integration, OncallSource } from '../../types.js';
import { Cluster, Empty, Note, Panel, Pill, Stack } from '../../ui.js';
import { ConfirmButton, timeAgo } from '../../components.js';
import { sourceLogo } from './logos.js';
import {
  IntegrationForm,
  blankForm,
  createBodyOf,
  formFrom,
  formProblems,
  patchBodyOf,
  type FormState,
} from './IntegrationForm.js';

/** 256 bits of browser-generated key. The server stores only its hash, so this is the one moment it
 *  can be known at all — held in a ref until the create is confirmed (see `mintedApiKey`). */
const mintKey = (): string => `${crypto.randomUUID()}${crypto.randomUUID()}`.replace(/-/g, '');

/**
 * THE `.html` IS LOAD-BEARING — every "Read the guide" link was dead without it.
 *
 * `docs-site` builds flat files (`oncall-webhook.html`), and the gateway serves `/docs/` with
 * `fastifyStatic` and no extension resolution. So `/docs/oncall-webhook` misses the static handler,
 * falls through to the SPA catch-all, and React Router — which has no such route — sends the reader
 * to `/projects`. Measured on staging 2026-08-29, by RESPONSE BODY because both URLs answer 200:
 *   /docs/oncall-webhook       -> <title>BatonDeck</title> + <div id="root">   (the SPA shell)
 *   /docs/oncall-webhook.html  -> <title>Custom webhook — any source</title>   (the actual guide)
 * It broke all EIGHT connectors, not just the custom one, because both call sites built the href the
 * same way. One function so a third call site cannot reintroduce it.
 */
export function docsHref(page: string): string {
  return `/docs/${page}.html`;
}

/**
 * The refused payload of one delivery, in a dialog.
 *
 * THIS REPLACED A DOWNLOAD LINK THAT COULD NOT WORK MOST OF THE TIME. The delivery ROW is kept for
 * `INGEST_DELIVERY_RETENTION_DAYS` (30); the raw body only for `INGEST_RAW_RETENTION_DAYS` (7). So
 * for 23 of those 30 days the button was offered, the payload was already TTL-deleted, and clicking
 * it navigated to a bare 404 with no explanation — reported from staging as "download doesn't work".
 * A dialog can say WHY, which a navigation cannot.
 *
 * RENDERED AS AN ESCAPED TEXT NODE, never as markup. These are unauthenticated attacker-controlled
 * bytes. The old route's defence was to refuse to render them at all (octet-stream + attachment +
 * nosniff) — that route still exists and still does that. What makes showing them here safe is a
 * different mechanism, not a weaker one: React escapes text children, so the payload cannot become
 * markup, and `<pre>` gives it no interpretation either. Do NOT reach for `dangerouslySetInnerHTML`
 * or a JSON viewer that renders HTML; the bytes are hostile by assumption.
 */
function RawPayloadDialog({
  projectId,
  integrationId,
  deliveryId,
  onClose,
}: {
  projectId: string;
  integrationId: string;
  deliveryId: string;
  onClose: () => void;
}) {
  const q = useQuery({
    queryKey: ['oncall-raw', projectId, integrationId, deliveryId],
    queryFn: () => oncallApi.readDeliveryRaw(projectId, integrationId, deliveryId),
    retry: false,
  });
  // Escape closes, and focus starts on the dialog, so a keyboard user is not stranded behind it.
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    ref.current?.focus();
    const onKey = (e: KeyboardEvent): void => { if (e.key === 'Escape') onClose(); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const expired = q.isError;
  return (
    <div className="raw-dialog-scrim" onClick={onClose}>
      <div
        className="raw-dialog"
        role="dialog"
        aria-modal="true"
        aria-label={`Refused payload for ${deliveryId}`}
        tabIndex={-1}
        ref={ref}
        onClick={(e) => e.stopPropagation()}
      >
        <Cluster gap={8} justify="space-between">
          <b>Refused payload</b>
          <button className="btn secondary icon" onClick={onClose} aria-label="Close">
            <Icon name="close" />
          </button>
        </Cluster>
        <div className="mono" style={{ fontSize: 11, color: 'var(--text-muted)', wordBreak: 'break-all' }}>
          {deliveryId}
        </div>
        {q.isLoading && <p className="muted">Loading…</p>}
        {expired && (
          <Note>
            This payload is no longer stored. A delivery’s log entry is kept for 30 days, but the raw
            body it refused is kept for 7 — so the record of the refusal outlives the bytes that
            caused it. Nothing went wrong; there is simply nothing left to show.
          </Note>
        )}
        {q.data && (
          // Escaped text, never markup — see the docblock above.
          <pre className="raw-dialog-body">{q.data.payload}</pre>
        )}
      </div>
    </div>
  );
}

function DeliveryLog({ projectId, integration }: { projectId: string; integration: Integration }) {
  const qc = useQueryClient();
  /** The delivery whose refused payload is open, or null. */
  const [rawFor, setRawFor] = useState<string | null>(null);
  const q = useInfiniteQuery({
    queryKey: ['oncall-deliveries', projectId, integration.id],
    initialPageParam: '',
    queryFn: ({ pageParam }) =>
      oncallApi.listDeliveries(projectId, integration.id, (pageParam as string) || undefined),
    getNextPageParam: (last) => last.nextCursor ?? null,
    refetchInterval: 20_000,
    retry: false,
  });
  const rows: Delivery[] = q.data?.pages.flatMap((p) => (Array.isArray(p.deliveries) ? p.deliveries : [])) ?? [];
  const replay = useMutation({
    mutationFn: (deliveryId: string) => oncallApi.replayDelivery(projectId, integration.id, deliveryId),
    onSuccess: () =>
      void qc.invalidateQueries({ queryKey: ['oncall-deliveries', projectId, integration.id] }),
  });

  if (rows.length === 0) {
    return <Empty>No deliveries yet. Send a test event, or wait for the source to fire.</Empty>;
  }
  return (
    <Stack gap={8}>
      {/*
        THE DRAWER IS A FIXED 520px WITH `overflow-y` ONLY (index.css:441), so a table wider than
        that does not scroll — it is CLIPPED, and the column that loses is the last one: the Replay
        and Download-payload buttons were half-visible against the drawer edge.

        `overflow-x` on a wrapper is the in-repo answer (`.chr-table-wrap`, and index.css:603 states
        the reason: "block+overflow keeps a wide table scrolling inside itself instead of the whole
        drawer body"). `nowrap` moved off the table and onto the two columns that must not wrap — a
        relative timestamp and a status word — so `Event` and `Result` can wrap instead of forcing
        the whole row wide. A fingerprint is long and arbitrary; wrapping it is what usually keeps
        the row inside 520px in the first place, and the scroll is the fallback when it does not.
      */}
      <div style={{ overflowX: 'auto' }}>
        <table>
          <thead>
            <tr>
              <th style={{ whiteSpace: 'nowrap' }}>When</th>
              <th>Event</th>
              <th>Result</th>
              <th style={{ whiteSpace: 'nowrap' }}>Ticket</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {rows.map((d) => (
              <tr key={d.deliveryId}>
                <td style={{ whiteSpace: 'nowrap' }}>{timeAgo(d.ts)}</td>
                <td style={{ wordBreak: 'break-word' }}>
                  {d.kind}
                  {d.fingerprint ? ` · ${d.fingerprint}` : ''}
                </td>
                <td style={{ wordBreak: 'break-word' }}>
                  {/* The refusal reason is server-authored, but it can quote source-supplied text —
                      rendered as a text node so it is escaped whatever it contains. */}
                  {d.result}
                  {d.error ? ` — ${d.error}` : ''}
                </td>
                <td style={{ whiteSpace: 'nowrap' }}>{d.taskId ?? '—'}</td>
                <td>
                  <Cluster gap={8}>
                    {d.kind !== 'rejected' && !integration.deletedAt && (
                      <button
                        className="btn secondary"
                        disabled={replay.isPending}
                        onClick={() => replay.mutate(d.deliveryId)}
                      >
                        Replay
                      </button>
                    )}
                    {d.kind === 'rejected' && (
                      <button className="btn secondary" onClick={() => setRawFor(d.deliveryId)}>
                        View payload
                      </button>
                    )}
                  </Cluster>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {rawFor && (
        <RawPayloadDialog
          projectId={projectId}
          integrationId={integration.id}
          deliveryId={rawFor}
          onClose={() => setRawFor(null)}
        />
      )}
      {q.hasNextPage && (
        <Note>
          <Cluster gap={8} wrap>
            <span role="status">
              Showing {rows.length} deliver{rows.length === 1 ? 'y' : 'ies'} — this list is partial,
              more exist.
            </span>
            <button
              className="btn secondary"
              onClick={() => void q.fetchNextPage()}
              disabled={q.isFetchingNextPage}
            >
              {q.isFetchingNextPage ? 'Loading…' : 'Load more'}
            </button>
          </Cluster>
        </Note>
      )}
    </Stack>
  );
}

/**
 * A source's brand mark, sized for a list row.
 *
 * `aria-hidden` and no `title`: the button already carries the product name as text, and a second
 * accessible name for the same control makes a screen reader announce the vendor twice.
 *
 * The brand colour is applied as `fill`, so these are the one place in the app that does NOT use a
 * theme token — a vendor's mark is their colour in both themes, and re-tinting it per theme is how
 * you end up shipping a green PagerDuty. The neutral tile behind it is what keeps a dark mark legible
 * on a dark background.
 */
function SourceMark({ id }: { id: string }) {
  const logo = sourceLogo(id);
  if (!logo) return null;
  return (
    <span
      aria-hidden="true"
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        width: 28,
        height: 28,
        flex: '0 0 28px',
        borderRadius: 'var(--r-2)',
        background: 'var(--panel-2)',
        border: '1px solid var(--border)',
      }}
    >
      <svg width="16" height="16" viewBox="0 0 24 24" role="img">
        <path d={logo.path} fill={`#${logo.hex}`} />
      </svg>
    </span>
  );
}

/**
 * THE SOURCE PICKER — the step that used to be a bare `<select>` of seven names.
 *
 * Each card states what the vendor can actually authenticate with and why, because that is the
 * decision being made: picking Cloud Monitoring means accepting HTTP Basic, and picking Grafana
 * means a header rather than a signature. Burying that in a dropdown option label is how an
 * operator ends up configuring a mode their tool cannot send.
 */
function SourcePicker({
  sources,
  onPick,
  onCancel,
}: {
  sources: OncallSource[];
  onPick: (s: OncallSource) => void;
  /**
   * `null` when there is nothing to cancel BACK to — the operator has no integrations, so the source
   * list is the resting state rather than a detour from it. A Cancel that returns you to an empty
   * panel is the dead end this drawer was reported for.
   */
  onCancel: (() => void) | null;
}) {
  const [q, setQ] = useState('');
  const needle = q.trim().toLowerCase();
  const shown = needle
    ? sources.filter((s) => `${s.label} ${s.id}`.toLowerCase().includes(needle))
    : sources;
  // The escape hatch goes last and is called out: it is the answer for every tool NOT in the list,
  // and an operator who does not find their vendor needs to be told it exists rather than
  // concluding BatonDeck cannot ingest from them.
  const custom = shown.find((s) => s.id === 'webhook');
  const vendors = shown.filter((s) => s.id !== 'webhook');

  return (
    <Panel title="Choose a source" pad>
      {/* A stable hook for "the source list is showing". Tests used to infer it from a vendor name
          being findable anywhere on screen, which stopped being true the moment the integration rows
          began showing their own source — two buttons then matched /Grafana/i. */}
      <Stack gap={12} className="source-picker">
        <input
          className="input"
          aria-label="Filter sources"
          placeholder="Filter…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        {shown.length === 0 &&
          (needle ? (
            <Empty>No source matches “{q}”. The custom webhook takes any tool.</Empty>
          ) : (
            // The catalog is a network read, so this is the loading/unavailable state — never a
            // claim that BatonDeck supports nothing.
            <Empty>Loading the source catalog…</Empty>
          ))}
        <Stack gap={8}>
          {vendors.map((s) => (
            <button
              key={s.id}
              className="btn secondary"
              style={{ textAlign: 'left', display: 'block', width: '100%' }}
              onClick={() => onPick(s)}
            >
              <Cluster gap={8}>
                <SourceMark id={s.id} />
                <Stack gap={2}>
                  <Cluster gap={8} wrap>
                    <b>{s.label}</b>
                    {s.authModes.map((m) => (
                      <Pill key={m}>{m}</Pill>
                    ))}
                  </Cluster>
                  <span className="muted">{s.authNote}</span>
                </Stack>
              </Cluster>
            </button>
          ))}
        </Stack>
        {custom && (
          <Panel title="Not listed?" pad>
            <Stack gap={8}>
              <Note>
                Any tool that can POST JSON works. You describe where the fields live in its payload
                — no code from us, no release to wait for.
              </Note>
              <Cluster gap={8} wrap>
                {/* The webhook gets its mark too — it is the source most operators end up on, and
                    it sits in its own panel rather than the vendor list, which is exactly how it got
                    left as bare text. */}
                <button className="btn" onClick={() => onPick(custom)}>
                  <Cluster gap={8}>
                    <SourceMark id={custom.id} />
                    <span>Use a custom webhook</span>
                  </Cluster>
                </button>
                <a className="btn secondary" href={docsHref(custom.docsPage)} target="_blank" rel="noreferrer">
                  Read the guide
                </a>
              </Cluster>
            </Stack>
          </Panel>
        )}
        {onCancel && (
          <Cluster gap={8}>
            <button className="btn secondary" onClick={onCancel}>
              Cancel
            </button>
          </Cluster>
        )}
      </Stack>
    </Panel>
  );
}

export function OncallSetup({
  projectId,
  integrations,
  onClose,
}: {
  projectId: string;
  integrations: Integration[];
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const dialogRef = useRef<HTMLDivElement>(null);
  const [selected, setSelected] = useState<string | null>(integrations[0]?.id ?? null);
  /** null = not creating · a source = configuring a new one · 'picking' = choosing the source. */
  const [creating, setCreating] = useState<'picking' | OncallSource | null>(
    integrations.length === 0 ? 'picking' : null,
  );
  const [editing, setEditing] = useState(false);
  const [form, setForm] = useState<FormState>(blankForm(undefined));
  const [showDeleted, setShowDeleted] = useState(false);
  const [shownSecret, setShownSecret] = useState<{
    secret: string;
    path: string;
    kind: 'hmac' | 'api_key';
  } | null>(null);
  /**
   * The api_key we minted for THIS create, held until the server confirms it.
   *
   * An API key is hashed server-side and never returned, so if it is not held here it cannot be
   * shown and the integration is unusable. This bug shipped once already — four sources produced
   * integrations whose key nobody could know, and with no delete tool there was no repair.
   */
  const mintedApiKey = useRef<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  /** Rotating a source-issued secret needs the source's NEW value; see the Rotate control below. */
  const [rotating, setRotating] = useState(false);
  const [rotateSecret, setRotateSecret] = useState('');

  const sourcesQ = useQuery({ queryKey: ['oncall-sources'], queryFn: () => oncallApi.oncallSources(), staleTime: 300_000 });
  const sources = useMemo(() => sourcesQ.data?.sources ?? [], [sourcesQ.data]);
  const sourceOf = (id: string): OncallSource | undefined => sources.find((s) => s.id === id);

  const mayReadIntegrations = useMayReadIntegrations(projectId);
  const deletedQ = useQuery({
    queryKey: ['oncall-integrations-all', projectId],
    queryFn: () => oncallApi.listIntegrations(projectId, true),
    // Same master-tier gate as every other list_integrations read — see useMayReadIntegrations.
    enabled: showDeleted && mayReadIntegrations,
  });
  const tombstones = (deletedQ.data?.integrations ?? []).filter((i) => i.deletedAt);

  // TaskDrawer's conventions: focus the dialog once, Escape blurs a focused field first so a typing
  // operator is never one keystroke from losing the form, and only a second Escape closes.
  useEffect(() => {
    dialogRef.current?.focus();
  }, []);
  useEffect(() => {
    const onKey = (e: KeyboardEvent): void => {
      if (e.key !== 'Escape') return;
      const el = document.activeElement as HTMLElement | null;
      if (el && (el.tagName === 'INPUT' || el.tagName === 'SELECT' || el.tagName === 'TEXTAREA')) {
        el.blur();
        return;
      }
      onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const invalidate = (): void => {
    void qc.invalidateQueries({ queryKey: ['oncall-integrations', projectId] });
    void qc.invalidateQueries({ queryKey: ['oncall-integrations-all', projectId] });
  };

  const create = useMutation({
    gcTime: 0, // a secret in the response or the variables leaves memory with the drawer
    mutationFn: (body: Parameters<typeof oncallApi.createIntegration>[1]) =>
      oncallApi.createIntegration(projectId, body),
    onSuccess: (res) => {
      setError(null);
      setCreating(null);
      setSelected(res.integration.id);
      // hmac: the server mints and echoes it once. api_key/basic: WE minted it and the server only
      // ever stored its hash, so this is the single moment it can be shown at all.
      if (res.secret) {
        setShownSecret({ secret: res.secret, path: res.integration.endpointPath, kind: 'hmac' });
      } else if (mintedApiKey.current) {
        setShownSecret({
          secret: mintedApiKey.current,
          path: res.integration.endpointPath,
          kind: 'api_key',
        });
      }
      mintedApiKey.current = null;
      invalidate();
    },
    // Tier refusals come from the CORE and are shown verbatim — the drawer never guesses at a
    // caller's role, which would be a second authorization model to keep in sync.
    onError: (e: Error) => setError(e.message),
  });
  const save = useMutation({
    gcTime: 0, // a secret in the response or the variables leaves memory with the drawer
    mutationFn: ({ i, patch }: { i: Integration; patch: Parameters<typeof oncallApi.updateIntegration>[3] }) =>
      oncallApi.updateIntegration(projectId, i.id, i.version, patch),
    onSuccess: (res) => {
      setError(null);
      setEditing(false);
      if (res.integration && 'secret' in res && typeof res.secret === 'string') {
        setShownSecret({ secret: res.secret, path: res.integration.endpointPath, kind: 'hmac' });
      }
      invalidate();
    },
    onError: (e: Error) => setError(e.message),
  });
  const rotate = useMutation({
    gcTime: 0, // a secret in the response or the variables leaves memory with the drawer
    mutationFn: ({ i, externalSecret }: { i: Integration; externalSecret?: string }) =>
      oncallApi.rotateIntegrationSecret(projectId, i.id, i.version, {
        ...(externalSecret ? { externalSecret } : {}),
      }),
    onSuccess: (res) => {
      // A minted secret is echoed once. An externally-issued one is NOT — the operator already has
      // it, and echoing a third-party credential back would be a second place it lives.
      if (res.secret) {
        setShownSecret({ secret: res.secret, path: res.integration.endpointPath, kind: 'hmac' });
      }
      setRotateSecret('');
      setRotating(false);
      invalidate();
    },
    onError: (e: Error) => setError(e.message),
  });
  const toggle = useMutation({
    mutationFn: (i: Integration) =>
      oncallApi.updateIntegration(projectId, i.id, i.version, { enabled: !i.enabled }),
    onSuccess: invalidate,
    onError: (e: Error) => setError(e.message),
  });
  const remove = useMutation({
    mutationFn: (i: Integration) => oncallApi.deleteIntegration(projectId, i.id, i.version),
    onSuccess: () => {
      setError(null);
      setSelected(null);
      setEditing(false);
      invalidate();
    },
    onError: (e: Error) => setError(e.message),
  });
  const test = useMutation({
    mutationFn: (i: Integration) => oncallApi.sendTestEvent(projectId, i.id, 'Wiring check'),
    onSuccess: () => {
      setError(null);
      void qc.invalidateQueries({ queryKey: ['oncall-deliveries', projectId] });
    },
    onError: (e: Error) => setError(e.message),
  });

  const active = integrations.find((i) => i.id === selected) ?? null;
  const activeSource = active ? sourceOf(active.source) : undefined;
  const formSource = creating instanceof Object ? creating : activeSource;
  const problems = formSource ? formProblems(form, formSource, creating !== null) : [];
  const set = (patch: Partial<FormState>): void => setForm((f) => ({ ...f, ...patch }));

  /**
   * THE SOURCE LIST IS THE RESTING STATE WHEN THERE IS NOTHING ELSE TO SHOW.
   *
   * It was not. `creating` went to `null` on every cancel, and with zero integrations `null` renders
   * "None yet." beside a `+ New` button whose only job is to put back the list you were just looking
   * at. Reported from staging: *"on cancel it doesn't show the list of options by default, it needs
   * new button to show it, basically new button there on top is useless"*. Correct — a dead end plus
   * a button to escape it is not a state, it is a missing default.
   *
   * So the picker shows whenever the operator is choosing OR has nothing to look at, and the two
   * controls that used to fight over that are now derived from it rather than set by hand.
   */
  const showPicker = creating === 'picking' || (creating === null && integrations.length === 0);
  /** Cancelling a half-filled form returns to the SOURCE LIST, never to an empty panel. */
  const cancelToList = (): void => setCreating('picking');

  return (
    <div className="drawer-overlay" onClick={onClose}>
      <div
        className="drawer"
        role="dialog"
        aria-modal="true"
        aria-label="On-call setup"
        tabIndex={-1}
        ref={dialogRef}
        onClick={(e) => e.stopPropagation()}
      >
        <Stack gap={12}>
          {/* space-between, so the close control sits at the drawer's right edge instead of
              trailing the title like a word after it. */}
          <Cluster gap={8} justify="space-between">
            <h2 style={{ margin: 0 }}>On-call setup</h2>
            <button className="btn secondary icon" onClick={onClose} aria-label="Close setup">
              <Icon name="close" size={12} />
            </button>
          </Cluster>

          {error && <Note>{error}</Note>}

          {shownSecret && (
            <Panel
              title={
                shownSecret.kind === 'hmac'
                  ? 'Your webhook secret — shown once'
                  : 'Your API key — shown once'
              }
              pad
            >
              <Stack gap={8}>
                <Note>
                  Copy this now. It <b>cannot be shown again</b>.{' '}
                  {shownSecret.kind === 'hmac'
                    ? 'It is stored encrypted; if you lose it, rotate to mint a new one.'
                    : 'Only its hash is stored. Rotate does not apply to API keys — if you lose it, set a new one from Edit.'}
                </Note>
                <code style={{ wordBreak: 'break-all' }}>{shownSecret.secret}</code>
                <div>
                  Endpoint path: <code>{shownSecret.path}</code>
                </div>
                <button className="btn" onClick={() => setShownSecret(null)}>
                  I have stored it
                </button>
              </Stack>
            </Panel>
          )}

          {/* `pad` because this panel's content is buttons and pills, not the self-padding rows
              (`KV`, `LineRow`, `table`) Panel's default assumes. Without it every integration row sat
              flush against the panel edge and "Show deleted" touched two of them — reported twice
              from staging. The four sibling panels in this drawer already pass it.
              The comment sits HERE, above the element, because a JSX comment in ATTRIBUTE position is
              a syntax error that tsc catches and esbuild does not — the suite stays green while
              typecheck fails. */}
          <Panel
            title="Integrations"
            right={
              // Hidden while the picker is already open — there it is a no-op that re-selects the
              // state you are in, which is what made it read as useless.
              showPicker ? null : (
                <button
                  className="btn secondary"
                  onClick={() => {
                    setEditing(false);
                    setCreating('picking');
                  }}
                >
                  + New
                </button>
              )
            }
            pad
          >
            {integrations.length === 0 ? (
              // No "None yet." dead end: with nothing configured the source list below IS the panel's
              // content, so this only says which step the operator is on.
              <Empty>Nothing configured yet — pick a source below.</Empty>
            ) : (
              /* A LIST, NOT A ROW OF BUTTONS AND WRAPPING CHIPS.
                 Each row was `Cluster … wrap`, so the source and status pills broke to a second line
                 at a different point on EVERY row depending on how long the name was, and nothing
                 lined up down the column. The selected row was `btn` (solid primary), which reads as
                 the page's main call to action rather than "this one is selected" — on a list where
                 every row is equally clickable. Reported from staging as inconsistent and not well
                 thought through, and both halves of that are fair.
                 Now: one grid, so name / source / status align across rows; the whole row is the
                 target; selection is a tint plus a left rule. */
              <div className="int-list">
                {integrations.map((i) => (
                  <button
                    key={i.id}
                    type="button"
                    className={`int-row${i.id === selected ? ' is-selected' : ''}`}
                    {...(i.id === selected ? { 'aria-current': 'true' as const } : {})}
                    onClick={() => {
                      setSelected(i.id);
                      setEditing(false);
                      setCreating(null);
                    }}
                  >
                    <SourceMark id={i.source} />
                    <span className="int-name">{i.name}</span>
                    <span className="int-source">{sourceOf(i.source)?.label ?? i.source}</span>
                    <span className="int-state">
                      <Pill tone={i.enabled ? 'ok' : 'warn'}>{i.enabled ? 'enabled' : 'disabled'}</Pill>
                      {i.auth.dualAcceptUntil && (
                        <span title={`Previous secret accepted until ${i.auth.dualAcceptUntil}`}>
                          <Pill>rotating</Pill>
                        </span>
                      )}
                    </span>
                  </button>
                ))}
              </div>
            )}
            <Stack gap={8}>
              {/* A SECONDARY DISCLOSURE, not a primary action. As a `.btn secondary` it was a full
                  bordered button pinned to the panel's bottom-left corner, competing with the source
                  list for attention and touching two edges. `.btn-quiet` is borderless until hovered. */}
              {/* BREATHING ROOM ABOVE AND BELOW. The toggle sat flush between the last integration
                  row and the explanatory note under it, so it read as part of both and as a boundary
                  for neither. It is a divider between two regions; it has to look like one. */}
              <Cluster gap={8} justify="center" className="deleted-toggle">
                <button className="btn-quiet" onClick={() => setShowDeleted((v) => !v)}>
                  {showDeleted ? 'Hide deleted' : 'Show deleted'}
                </button>
              </Cluster>
              {showDeleted &&
                (tombstones.length === 0 ? (
                  <Empty>None deleted.</Empty>
                ) : (
                  <Stack gap={4}>
                    <Note>
                      A deleted integration keeps its delivery log so past incidents stay
                      explainable, but its endpoint no longer answers and it cannot be revived.
                    </Note>
                    {tombstones.map((i) => (
                      <Cluster key={i.id} gap={8} wrap>
                        <span>{i.name}</span>
                        <Pill>{sourceOf(i.source)?.label ?? i.source}</Pill>
                        <Pill tone="warn">deleted {timeAgo(i.deletedAt as string)}</Pill>
                      </Cluster>
                    ))}
                  </Stack>
                ))}
            </Stack>
          </Panel>

          {showPicker && (
            <SourcePicker
              sources={sources}
              onCancel={integrations.length ? () => setCreating(null) : null}
              onPick={(s) => {
                setForm(blankForm(s));
                setCreating(s);
              }}
            />
          )}

          {creating instanceof Object && (
            <>
              <Panel title={`New ${creating.label} integration`} pad>
                <Cluster gap={8} wrap>
                  <SourceMark id={creating.id} />
                  <span className="muted">{creating.authNote}</span>
                  <a className="btn secondary" href={docsHref(creating.docsPage)} target="_blank" rel="noreferrer">
                    Wiring guide
                  </a>
                </Cluster>
              </Panel>
              <IntegrationForm
                source={creating}
                form={form}
                set={set}
                isCreate
                onGenerateKey={() => set({ apiKey: mintKey() })}
              />
              {problems.length > 0 && (
                <Note>
                  <Stack gap={2}>
                    {problems.map((p) => (
                      <span key={p}>{p}</span>
                    ))}
                  </Stack>
                </Note>
              )}
              <Cluster gap={8}>
                <button
                  className="btn"
                  disabled={create.isPending || problems.length > 0}
                  onClick={() => {
                    // Held in a ref BEFORE the call: the server never returns it, so if it is not
                    // captured here it can never be shown.
                    mintedApiKey.current =
                      form.authMode === 'api_key' || form.authMode === 'basic' ? form.apiKey : null;
                    create.mutate(createBodyOf(form, creating));
                  }}
                >
                  {create.isPending ? 'Creating…' : 'Create'}
                </button>
                <button className="btn secondary" onClick={cancelToList}>
                  Cancel
                </button>
              </Cluster>
            </>
          )}

          {active && creating === null && (
            <>
              <Panel title={`${active.name} — endpoint`} pad>
                <Stack gap={8}>
                  <div>
                    Path: <code>{active.endpointPath}</code>
                  </div>
                  <Note>
                    Paste this path onto the ingest function&apos;s host in the source&apos;s webhook
                    settings. Auth mode: <b>{active.auth.mode}</b>
                    {active.auth.externallyIssued ? ' (the source issued the secret)' : ''}.
                  </Note>
                  <Cluster gap={8} wrap>
                    <button
                      className="btn secondary"
                      onClick={() => {
                        setForm(formFrom(active));
                        setEditing((v) => !v);
                      }}
                    >
                      {editing ? 'Close editor' : 'Edit configuration'}
                    </button>
                    <button className="btn secondary" onClick={() => toggle.mutate(active)}>
                      {active.enabled ? 'Disable' : 'Enable'}
                    </button>
                    {active.auth.mode === 'hmac' &&
                      // *** A SOURCE THAT ISSUES ITS OWN SECRET CANNOT BE ROTATED BY MINTING ONE. ***
                      // PagerDuty signs with its own value, so a minted whsec_ is a secret it will
                      // never send — the core refuses that rotation, and offering a one-click button
                      // for it put the operator most likely to click it (whose PD integration is
                      // already misbehaving) one click from a dead integration.
                      (active.auth.externallyIssued ? (
                        <button className="btn secondary" onClick={() => setRotating((v) => !v)}>
                          {rotating ? 'Cancel rotation' : 'Rotate secret'}
                        </button>
                      ) : (
                        <button className="btn secondary" onClick={() => rotate.mutate({ i: active })}>
                          Rotate secret
                        </button>
                      ))}
                    <button
                      className="btn secondary"
                      disabled={test.isPending}
                      onClick={() => test.mutate(active)}
                    >
                      {test.isPending ? 'Sending…' : 'Send test event'}
                    </button>
                    {/* Two-step, and the note says what survives — because unlike Disable this one
                        cannot be undone, and an operator reaching for it may only want to pause. */}
                    <ConfirmButton
                      label="Delete"
                      confirmLabel="Delete for good"
                      className="btn secondary"
                      onConfirm={() => remove.mutate(active)}
                    />
                  </Cluster>
                  {rotating && active.auth.externallyIssued && (
                    <Stack gap={4}>
                      <Note>
                        This source issued its own signing secret. Rotate it in the source&apos;s own
                        console first, then paste the NEW value here — BatonDeck cannot mint one it
                        would ever send.
                      </Note>
                      <Cluster gap={8} wrap>
                        <input
                          className="input"
                          style={{ minWidth: 260 }}
                          aria-label="New signing secret from the source"
                          value={rotateSecret}
                          onChange={(e) => setRotateSecret(e.target.value)}
                          placeholder="paste the source's new secret"
                        />
                        <button
                          className="btn"
                          disabled={rotate.isPending || rotateSecret.trim().length < 8}
                          onClick={() => rotate.mutate({ i: active, externalSecret: rotateSecret.trim() })}
                        >
                          {rotate.isPending ? 'Rotating…' : 'Rotate'}
                        </button>
                      </Cluster>
                    </Stack>
                  )}
                  <Note>
                    <b>Disable</b> pauses deliveries and is reversible. <b>Delete</b> retires the
                    endpoint for good — the delivery log and the incident tickets it already filed
                    survive, but the integration cannot be brought back.
                  </Note>
                </Stack>
              </Panel>

              {editing && activeSource && (
                <>
                  <IntegrationForm
                    source={activeSource}
                    form={form}
                    set={set}
                    isCreate={false}
                    onGenerateKey={() => set({ apiKey: mintKey() })}
                  />
                  {problems.length > 0 && (
                    <Note>
                      <Stack gap={2}>
                        {problems.map((p) => (
                          <span key={p}>{p}</span>
                        ))}
                      </Stack>
                    </Note>
                  )}
                  <Cluster gap={8}>
                    <button
                      className="btn"
                      disabled={save.isPending || problems.length > 0}
                      onClick={() => {
                        mintedApiKey.current = null;
                        save.mutate({
                          i: active,
                          patch: patchBodyOf(form, activeSource, active),
                        });
                      }}
                    >
                      {save.isPending ? 'Saving…' : 'Save changes'}
                    </button>
                    <button className="btn secondary" onClick={() => setEditing(false)}>
                      Cancel
                    </button>
                  </Cluster>
                </>
              )}

              <Panel title="Delivery log">
                <DeliveryLog projectId={projectId} integration={active} />
              </Panel>
            </>
          )}
        </Stack>
      </div>
    </div>
  );
}
