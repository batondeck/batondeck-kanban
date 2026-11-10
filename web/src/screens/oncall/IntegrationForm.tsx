/**
 * THE INTEGRATION CONFIG FORM — one component for create and for edit.
 *
 * What it replaces: a create form with THREE fields (name, source, board) whose own note admitted
 * the rest was unreachable — *"Change it, the lifecycle mode and the policy toggles from the MCP
 * surface until those controls land here"* — and NO edit form at all. Everything `create_integration`
 * and `update_integration` accept is here now, grouped the way an operator thinks about it rather
 * than the way the schema is ordered.
 *
 * TWO RULES THIS FILE FOLLOWS, both learned from the bugs already fixed in this directory:
 *
 *  1. **The auth mode is not a free choice, and the catalog decides it.** Most vendors cannot compute
 *     our HMAC and Cloud Monitoring cannot even set a header, so the mode select offers exactly
 *     `source.authModes` — served from `src/domain/oncall-sources.ts`, never hardcoded here. A form
 *     that offers a mode the vendor cannot send is how the commonest misconfiguration happens.
 *  2. **A credential the operator cannot supply is worse than no field.** `api_key`/`basic` mint the
 *     key in the browser and show it once, because the server only ever stores its hash; PagerDuty
 *     requires ITS OWN secret pasted in, because PagerDuty signs with it and the core now refuses the
 *     alternative outright.
 *
 * The form never guesses at authorization: tier refusals come back from the core and are shown
 * verbatim, rather than being predicted here as a second permission model to keep in sync.
 */
import { useState } from 'react';
import type {
  AlertStatus,
  CreateIntegrationBody,
  Integration,
  IntegrationAuthMode,
  IntegrationMappingBody,
  IntegrationPatchBody,
  LifecycleMode,
  OncallSource,
  Priority,
  RoutingMode,
} from '../../types.js';
import { Cluster, Note, Panel, Stack } from '../../ui.js';
import { ChainField, FieldGroup, ListField, MapEditor, RadioRow, TextField } from './fields.js';

const PRIORITIES: readonly Priority[] = ['low', 'normal', 'high', 'urgent'];
const ALERT_STATUSES: readonly AlertStatus[] = ['firing', 'resolved', 'test'];

/** Everything the form edits, flattened — assembled into the wire body on submit. */
export interface FormState {
  name: string;
  boardId: string;
  columnId: string;
  authMode: IntegrationAuthMode;
  apiKey: string;
  externalSecret: string;
  jwtIssuer: string;
  jwtAudience: string;
  jwtJwksUrl: string;
  jwtSubject: string;
  jwtEmail: string;
  routingMode: RoutingMode;
  onCallAgent: string;
  pdUserMap: Record<string, string>;
  lifecycle: LifecycleMode;
  severityMap: Record<string, string>;
  labels: string[] | undefined;
  annotationAllowlist: string[] | undefined;
  titleTemplate: string;
  ratePerMin: string;
  payloadCapKb: string;
  extract: NonNullable<IntegrationMappingBody['extract']>;
  statusMap: Record<string, string>;
  defaultTitle: string;
  defaultSeverity: string;
  defaultStatus: AlertStatus | '';
}

export function blankForm(source: OncallSource | undefined): FormState {
  return {
    name: '',
    boardId: '',
    columnId: '',
    // The catalog orders `authModes` best-first, so [0] is the recommended mode for this vendor.
    authMode: source?.authModes[0] ?? 'hmac',
    apiKey: '',
    externalSecret: '',
    jwtIssuer: '',
    jwtAudience: '',
    jwtJwksUrl: '',
    jwtSubject: '',
    jwtEmail: '',
    routingMode: 'broadcast',
    onCallAgent: '',
    pdUserMap: {},
    lifecycle: 'correlate',
    severityMap: {},
    labels: undefined,
    annotationAllowlist: undefined,
    titleTemplate: '',
    ratePerMin: '',
    payloadCapKb: '',
    extract: {},
    statusMap: {},
    defaultTitle: '',
    defaultSeverity: '',
    defaultStatus: '',
  };
}

/** Hydrate the form from an existing integration, so Edit opens on what is actually configured. */
export function formFrom(i: Integration): FormState {
  const m = i.mapping ?? {};
  const r = i.routing ?? {};
  return {
    ...blankForm(undefined),
    name: i.name,
    boardId: i.target.boardId,
    columnId: i.target.columnId ?? '',
    authMode: i.auth.mode,
    routingMode: r.mode ?? 'broadcast',
    onCallAgent: r.onCallAgent ?? '',
    pdUserMap: r.pdUserMap ?? {},
    lifecycle: i.lifecycle,
    severityMap: (m.severityMap ?? {}) as Record<string, string>,
    labels: m.labels,
    annotationAllowlist: m.annotationAllowlist,
    titleTemplate: m.titleTemplate ?? '',
    ratePerMin: String(i.limits.ratePerMin),
    payloadCapKb: String(i.limits.payloadCapKb),
    extract: m.extract ?? {},
    statusMap: (m.statusMap ?? {}) as Record<string, string>,
    defaultTitle: m.defaults?.title ?? '',
    defaultSeverity: m.defaults?.severity ?? '',
    defaultStatus: m.defaults?.status ?? '',
  };
}

const trimmed = (v: string): string | undefined => (v.trim() ? v.trim() : undefined);
const someOf = <T,>(v: Record<string, T>): Record<string, T> | undefined =>
  Object.keys(v).length > 0 ? v : undefined;

function mappingOf(f: FormState, isWebhook: boolean): IntegrationMappingBody | undefined {
  const defaults = {
    ...(trimmed(f.defaultTitle) ? { title: f.defaultTitle.trim() } : {}),
    ...(trimmed(f.defaultSeverity) ? { severity: f.defaultSeverity.trim() } : {}),
    ...(f.defaultStatus ? { status: f.defaultStatus } : {}),
  };
  const m: IntegrationMappingBody = {
    ...(someOf(f.severityMap) ? { severityMap: f.severityMap as Record<string, Priority> } : {}),
    ...(f.labels ? { labels: f.labels } : {}),
    ...(f.annotationAllowlist ? { annotationAllowlist: f.annotationAllowlist } : {}),
    ...(trimmed(f.titleTemplate) ? { titleTemplate: f.titleTemplate.trim() } : {}),
    // The custom-mapping half is sent ONLY for the `webhook` source. The core refuses it on any
    // other, because a vendor connector never consults it — sending it would be a saved setting
    // that demonstrably does nothing.
    ...(isWebhook && Object.keys(f.extract).length > 0 ? { extract: f.extract } : {}),
    ...(isWebhook && someOf(f.statusMap)
      ? { statusMap: f.statusMap as Record<string, AlertStatus> }
      : {}),
    ...(isWebhook && Object.keys(defaults).length > 0 ? { defaults } : {}),
  };
  return Object.keys(m).length > 0 ? m : undefined;
}

function authOf(f: FormState): CreateIntegrationBody['auth'] {
  if (f.authMode === 'api_key' || f.authMode === 'basic') {
    return { mode: f.authMode, apiKey: f.apiKey };
  }
  if (f.authMode === 'jwt') {
    return {
      mode: 'jwt',
      jwt: {
        issuer: f.jwtIssuer.trim(),
        audience: f.jwtAudience.trim(),
        jwksUrl: f.jwtJwksUrl.trim(),
        // The pins are sent only when filled in: blank pins nothing.
        ...(trimmed(f.jwtSubject) ? { subject: f.jwtSubject.trim() } : {}),
        ...(trimmed(f.jwtEmail) ? { email: f.jwtEmail.trim() } : {}),
      },
    };
  }
  return {
    mode: f.authMode,
    ...(trimmed(f.externalSecret) ? { externalSecret: f.externalSecret.trim() } : {}),
  };
}

const commonBody = (f: FormState, isWebhook: boolean) => ({
  name: f.name.trim(),
  boardId: f.boardId.trim(),
  ...(trimmed(f.columnId) ? { columnId: f.columnId.trim() } : {}),
  routing: {
    mode: f.routingMode,
    ...(trimmed(f.onCallAgent) ? { onCallAgent: f.onCallAgent.trim() } : {}),
    ...(someOf(f.pdUserMap) ? { pdUserMap: f.pdUserMap } : {}),
  },
  ...(mappingOf(f, isWebhook) ? { mapping: mappingOf(f, isWebhook) } : {}),
  lifecycle: f.lifecycle,
  ...(trimmed(f.ratePerMin) || trimmed(f.payloadCapKb)
    ? {
        limits: {
          ...(trimmed(f.ratePerMin) ? { ratePerMin: Number(f.ratePerMin) } : {}),
          ...(trimmed(f.payloadCapKb) ? { payloadCapKb: Number(f.payloadCapKb) } : {}),
        },
      }
    : {}),
});

export function createBodyOf(f: FormState, source: OncallSource): CreateIntegrationBody {
  return {
    ...commonBody(f, source.id === 'webhook'),
    source: source.id,
    auth: authOf(f),
  };
}

/**
 * The edit body. `auth` is OMITTED unless the operator actually supplied new material: sending it
 * unchanged would re-run `buildAuth`, which MINTS a fresh hmac secret — silently rotating the
 * integration on every save and breaking the live sender.
 */
export function patchBodyOf(f: FormState, source: OncallSource, current: Integration): IntegrationPatchBody {
  const modeChanged = f.authMode !== current.auth.mode;
  const suppliedMaterial =
    (f.authMode === 'api_key' || f.authMode === 'basic') && f.apiKey.trim().length > 0;
  const suppliedJwt = f.authMode === 'jwt' && f.jwtIssuer.trim().length > 0;
  const suppliedExternal = f.authMode === 'hmac' && f.externalSecret.trim().length > 0;
  const sendAuth = modeChanged || suppliedMaterial || suppliedJwt || suppliedExternal;
  return {
    ...commonBody(f, source.id === 'webhook'),
    ...(sendAuth ? { auth: authOf(f) } : {}),
  };
}

/** True when this form can be submitted — the fields the core would refuse without. */
export function formProblems(f: FormState, source: OncallSource, isCreate: boolean): string[] {
  const out: string[] = [];
  if (!f.name.trim()) out.push('Name is required.');
  if (!f.boardId.trim()) out.push('Target board id is required.');
  if (isCreate && (f.authMode === 'api_key' || f.authMode === 'basic') && !f.apiKey.trim()) {
    out.push('This mode needs a key — generate one.');
  }
  if (f.authMode === 'jwt' && (!f.jwtIssuer.trim() || !f.jwtAudience.trim() || !f.jwtJwksUrl.trim())) {
    out.push('JWT mode needs an issuer, an audience and a JWKS URL.');
  }
  // PagerDuty signs with its OWN secret; the core refuses a mint. Saying so here turns a server
  // refusal into a field the operator can see is empty.
  if (source.id === 'pagerduty' && f.authMode === 'hmac' && isCreate && !f.externalSecret.trim()) {
    out.push("PagerDuty issues its own secret — paste it in; BatonDeck cannot mint one it will use.");
  }
  if (f.routingMode === 'oncall' && !f.onCallAgent.trim()) {
    out.push("On-call routing needs an agent name (or use broadcast).");
  }
  return out;
}

export function IntegrationForm({
  source,
  form,
  set,
  isCreate,
  onGenerateKey,
}: {
  source: OncallSource;
  form: FormState;
  set: (patch: Partial<FormState>) => void;
  isCreate: boolean;
  onGenerateKey: () => void;
}) {
  const [showAdvanced, setShowAdvanced] = useState(false);
  const isWebhook = source.id === 'webhook';
  const setExtract = (patch: Partial<FormState['extract']>): void =>
    set({ extract: { ...form.extract, ...patch } });

  return (
    <Stack gap={12}>
      <Panel title="Basics" pad>
        <Stack gap={12}>
          <TextField
            id="oc-name"
            label="Name"
            required
            value={form.name}
            onChange={(name) => set({ name })}
            placeholder="prod-grafana"
            hint="Yours — it appears on the delivery log and in the audit trail."
          />
          <TextField
            id="oc-board"
            label="Target board id"
            required
            value={form.boardId}
            onChange={(boardId) => set({ boardId })}
            placeholder="B-…"
            hint="Incident tickets land here."
          />
          {/* Optional-ness is carried by the ABSENCE of the required marker and by the placeholder
              stating the fallback — not by "(optional)" typed into the label, which styled the two
              words identically to the field name and was applied inconsistently across the form. */}
          <TextField
            id="oc-column"
            label="Target column id"
            value={form.columnId}
            onChange={(columnId) => set({ columnId })}
            placeholder="leave blank for the board's first column"
          />
        </Stack>
      </Panel>

      <Panel title="Authentication" pad>
        <Stack gap={12}>
          <Note>{source.authNote}</Note>
          {source.authModes.length > 1 ? (
            <RadioRow
              name="oc-auth"
              label="Mode"
              value={form.authMode}
              onChange={(authMode) => set({ authMode })}
              options={source.authModes.map((m) => ({ value: m, label: m }))}
              hint="Only the modes this source can physically present are offered."
            />
          ) : (
            <Note>
              Mode: <b>{form.authMode}</b> — the only one this source can send.
            </Note>
          )}

          {(form.authMode === 'api_key' || form.authMode === 'basic') && (
            <Field2
              label={isCreate ? 'Key' : 'New key (leave blank to keep the current one)'}
              hint={
                form.authMode === 'basic'
                  ? 'Sent as the PASSWORD of HTTP Basic; the username is ignored. Stored as a hash — shown once.'
                  : 'Sent in the `x-batondeck-key` header and NOTHING else. Stored as a hash — shown once.'
              }
            >
              <Cluster gap={8} wrap>
                <input
                  id="oc-apikey"
                  className="input"
                  style={{ minWidth: 260 }}
                  aria-label="API key"
                  value={form.apiKey}
                  onChange={(e) => set({ apiKey: e.target.value })}
                  placeholder={isCreate ? 'generate, or paste your own' : 'unchanged'}
                />
                <button className="btn secondary" type="button" onClick={onGenerateKey}>
                  Generate
                </button>
              </Cluster>
            </Field2>
          )}

          {form.authMode === 'hmac' && (
            <TextField
              id="oc-external"
              label={
                source.id === 'pagerduty'
                  ? "PagerDuty's signing secret (required)"
                  : 'External secret (optional)'
              }
              value={form.externalSecret}
              onChange={(externalSecret) => set({ externalSecret })}
              placeholder={source.id === 'pagerduty' ? 'paste from PagerDuty' : 'blank → BatonDeck mints one'}
              hint={
                source.id === 'pagerduty'
                  ? 'PagerDuty signs with a secret IT issues when you create the webhook subscription. A secret minted here would never be used, so every delivery would be refused.'
                  : 'Leave blank and BatonDeck mints a whsec_… and shows it once. Fill it in to use a secret your sender already holds; either way the sender signs the same timestamped v0 form, shown on the endpoint panel.'
              }
            />
          )}

          {form.authMode === 'jwt' && (
            <Stack gap={12}>
              {/* The stored jwt block is never shown back, so an edit cannot prefill it: what is
                  saved here replaces it whole, pins included. */}
              {!isCreate && (
                <Note>
                  The JWT settings are not shown back. Filling them in replaces all of them, so
                  re-enter the pins too: a blank pin removes the one stored.
                </Note>
              )}
              <TextField
                id="oc-jwt-iss"
                label="Issuer"
                value={form.jwtIssuer}
                onChange={(jwtIssuer) => set({ jwtIssuer })}
                placeholder="https://idp.example.com/"
                hint="On a shared issuer (Google, GitHub Actions) anyone with an account there can mint a token for your audience, so issuer and audience alone do not identify the sender: pin the subject or email below."
              />
              <TextField
                id="oc-jwt-aud"
                label="Audience"
                value={form.jwtAudience}
                onChange={(jwtAudience) => set({ jwtAudience })}
              />
              <TextField
                id="oc-jwt-jwks"
                label="JWKS URL"
                value={form.jwtJwksUrl}
                onChange={(jwtJwksUrl) => set({ jwtJwksUrl })}
                placeholder="https://idp.example.com/.well-known/jwks.json"
                hint="RS256 only. Must be https, on a public address — private ranges are refused."
              />
              <TextField
                id="oc-jwt-sub"
                label="Subject pin (sub)"
                value={form.jwtSubject}
                onChange={(jwtSubject) => set({ jwtSubject })}
                placeholder="blank → any subject"
                hint="The token's sub must equal this exactly: a service account's unique id, or a GitHub Actions repo:owner/name:ref:… subject."
              />
              <TextField
                id="oc-jwt-email"
                label="Email pin"
                value={form.jwtEmail}
                onChange={(jwtEmail) => set({ jwtEmail })}
                placeholder="blank → any email"
                hint="The token's email must equal this exactly, and the token must say email_verified."
              />
            </Stack>
          )}
        </Stack>
      </Panel>

      <Panel title="Routing" pad>
        <Stack gap={12}>
          <RadioRow
            name="oc-routing"
            label="Who gets woken"
            value={form.routingMode}
            onChange={(routingMode) => set({ routingMode })}
            options={[
              { value: 'broadcast', label: 'Broadcast', note: 'every live agent; first free one claims' },
              { value: 'oncall', label: 'One named agent', note: 'assigned in the same transaction as the ticket' },
              { value: 'upstream', label: 'Upstream', note: "PagerDuty's own assignment" },
            ]}
          />
          {form.routingMode === 'oncall' && (
            <TextField
              id="oc-agent"
              label="On-call agent"
              value={form.onCallAgent}
              onChange={(onCallAgent) => set({ onCallAgent })}
              placeholder="claude-oncall"
              hint="The agent's display name — the same namespace as a ticket assignee."
            />
          )}
          {form.routingMode === 'upstream' && (
            <MapEditor
              id="oc-pdmap"
              label="PagerDuty user → agent"
              value={form.pdUserMap}
              onChange={(pdUserMap) => set({ pdUserMap })}
              keyPlaceholder="PXXXXXX"
              valuePlaceholder="claude-oncall"
              hint="A miss falls back to broadcast."
            />
          )}
        </Stack>
      </Panel>

      <Panel title="Incident lifecycle" pad>
        <RadioRow
          name="oc-lifecycle"
          label="When the same alert fires again"
          value={form.lifecycle}
          onChange={(lifecycle) => set({ lifecycle })}
          options={[
            { value: 'correlate', label: 'Correlate', note: 'one ticket, occurrence count bumps' },
            { value: 'autoclose', label: 'Auto-close', note: 'cancels the ticket if UNTOUCHED when the alert clears' },
            { value: 'per_event', label: 'Per event', note: 'a new ticket every firing' },
          ]}
        />
      </Panel>

      {isWebhook && (
        <Panel title="Field mapping" pad>
          <Stack gap={12}>
            {/*
              A WORKED EXAMPLE BEATS A PARAGRAPH ABOUT PATHS. The rules were prose above the inputs,
              so the reader had to hold "dots, numeric indices, comma-separated fallbacks" in their
              head while typing. Showing one payload beside the paths that read it makes all three
              rules self-evident, and it is what a reader with their own payload open actually needs.
            */}
            <details className="record">
              <summary className="record-head" style={{ cursor: 'pointer' }}>
                <span className="record-title">How paths work — a worked example</span>
              </summary>
              <div style={{ padding: 12 }}>
                <Stack gap={8}>
                  <span className="muted" style={{ fontSize: 12 }}>
                    Given this payload:
                  </span>
                  <pre
                    style={{
                      margin: 0,
                      padding: 10,
                      overflowX: 'auto',
                      background: 'var(--panel-2)',
                      border: '1px solid var(--border)',
                      borderRadius: 'var(--r-2)',
                      fontFamily: 'var(--mono)',
                      fontSize: 12,
                    }}
                  >
{`{
  "alert": { "id": "cpu-91", "state": "ALARM",
             "rules": [{ "name": "CPU > 90%" }] },
  "level": "P2",
  "url": "https://grafana.example/d/abc"
}`}
                  </pre>
                  <table>
                    <thead>
                      <tr>
                        <th>Field</th>
                        <th>Path to type</th>
                        <th>Reads</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr>
                        <td>Fingerprint</td>
                        <td><code>alert.id</code></td>
                        <td><code>cpu-91</code></td>
                      </tr>
                      <tr>
                        <td>Title</td>
                        <td><code>alert.rules[0].name</code></td>
                        <td><code>CPU &gt; 90%</code></td>
                      </tr>
                      <tr>
                        <td>Severity</td>
                        <td><code>alert.severity, level</code></td>
                        <td><code>P2</code> — first path that resolves wins</td>
                      </tr>
                    </tbody>
                  </table>
                  <span className="muted" style={{ fontSize: 12 }}>
                    Dots descend, <code>[0]</code> indexes an array, a comma is a fallback chain, and{' '}
                    <code>$body_hash</code> derives a stable id when your tool sends none.
                  </span>
                </Stack>
              </div>
            </details>

            <FieldGroup
              title="Correlation"
              hint="The one field with no sensible default — everything else can be guessed or left blank."
            >
              <ChainField
                id="oc-x-fp"
                label="Fingerprint"
                required
                value={form.extract.fingerprint}
                onChange={(fingerprint) => setExtract({ fingerprint })}
                placeholder="alert.id, $body_hash"
                hint="THE correlation key: two alerts sharing it land on one ticket. Point it at the alert's identity, never at a timestamp."
              />
            </FieldGroup>

            <FieldGroup
              title="What the ticket says"
              hint="Left blank, each falls back to a default below."
            >
              <ChainField
                id="oc-x-title"
                label="Title"
                value={form.extract.title}
                onChange={(title) => setExtract({ title })}
                placeholder="alert.name, summary"
              />
              <ChainField
                id="oc-x-sev"
                label="Severity"
                value={form.extract.severity}
                onChange={(severity) => setExtract({ severity })}
                placeholder="alert.level"
                hint="The raw word. The severity map below turns it into a ticket priority."
              />
              <ChainField
                id="oc-x-status"
                label="Status"
                value={form.extract.status}
                onChange={(status) => setExtract({ status })}
                placeholder="alert.state"
              />
              <MapEditor
                id="oc-statusmap"
                label="Status vocabulary"
                value={form.statusMap}
                onChange={(statusMap) => set({ statusMap })}
                keyPlaceholder="ALARM"
                allowed={ALERT_STATUSES}
                hint="Only for words the built-in list gets wrong — ok/closed/recovered and alarm/triggered/problem are already understood."
              />
            </FieldGroup>

            <FieldGroup
              title="Optional — most integrations leave these blank"
              hint="Set them only if your payload carries the data and you want it on the ticket."
            >
              <TextField
                id="oc-x-events"
                label="Batch array path"
                value={form.extract.events ?? ''}
                onChange={(v) => setExtract({ events: v.trim() || undefined })}
                placeholder="alerts"
                hint="If your tool batches, point this at the array: one ticket per alert instead of one per batch. Inside it, prefix a path with $. to reach the whole body."
              />
              <TextField
                id="oc-x-labels"
                label="Labels path"
                value={form.extract.labels ?? ''}
                onChange={(v) => setExtract({ labels: v.trim() || undefined })}
                placeholder="alert.tags"
                hint={'An object of scalars, or an array of "k:v" / "k=v" strings.'}
              />
              <TextField
                id="oc-x-annotations"
                label="Annotations path"
                value={form.extract.annotations ?? ''}
                onChange={(v) => setExtract({ annotations: v.trim() || undefined })}
                placeholder="alert.annotations"
              />
              <ChainField
                id="oc-x-links"
                label="Links"
                value={form.extract.links}
                onChange={(links) => setExtract({ links })}
                placeholder="alert.url"
                hint="Only http(s) values are kept."
              />
              <ChainField
                id="oc-x-occurred"
                label="Timestamp"
                value={form.extract.occurredAt}
                onChange={(occurredAt) => setExtract({ occurredAt })}
                placeholder="alert.startsAt"
                hint="ISO, epoch seconds or epoch milliseconds."
              />
              <ChainField
                id="oc-x-dedup"
                label="Delivery id"
                value={form.extract.dedupKey}
                onChange={(dedupKey) => setExtract({ dedupKey })}
                placeholder="notification.id"
                hint="Your tool's own per-notification id, if it has one. Blank → the body hash, so an identical redelivery is treated as a retry."
              />
            </FieldGroup>
            <Cluster gap={8} wrap>
              <TextField
                id="oc-def-title"
                label="Default title"
                value={form.defaultTitle}
                onChange={(defaultTitle) => set({ defaultTitle })}
              />
              <TextField
                id="oc-def-sev"
                label="Default severity"
                value={form.defaultSeverity}
                onChange={(defaultSeverity) => set({ defaultSeverity })}
                placeholder="warning"
              />
            </Cluster>
          </Stack>
        </Panel>
      )}

      <Panel
        title="Ticket shaping and limits"
        pad
        right={
          <button className="btn secondary" type="button" onClick={() => setShowAdvanced((v) => !v)}>
            {showAdvanced ? 'Hide' : 'Show'}
          </button>
        }
      >
        {!showAdvanced ? (
          <Note>
            Severity → priority, stamped labels, the annotation allowlist and the rate limits. Defaults are sensible; open this when you need to change one.
          </Note>
        ) : (
          <Stack gap={12}>
            <MapEditor
              id="oc-sevmap"
              label="Severity → priority"
              value={form.severityMap}
              onChange={(severityMap) => set({ severityMap })}
              keyPlaceholder="P1"
              allowed={PRIORITIES}
              hint="Vendor severity vocabularies rarely match ours. An unmapped word becomes 'normal'."
            />
            <ListField
              id="oc-labels"
              label="Labels stamped on every ticket"
              value={form.labels}
              onChange={(labels) => set({ labels })}
              placeholder="oncall, prod"
            />
            <ListField
              id="oc-allowlist"
              label="Annotation allowlist"
              value={form.annotationAllowlist}
              onChange={(annotationAllowlist) => set({ annotationAllowlist })}
              placeholder="summary, runbook_url"
              hint="Which annotation keys reach a TICKET. Ticket data outlives the delivery log's retention, so this is the boundary for anything sensitive. Blank → all of them."
            />
            <TextField
              id="oc-title-tpl"
              label="Title template"
              value={form.titleTemplate}
              onChange={(titleTemplate) => set({ titleTemplate })}
              hint="Blank → the alert's own title."
            />
            <Cluster gap={8} wrap>
              <TextField
                id="oc-rate"
                label="Deliveries per minute"
                type="number"
                value={form.ratePerMin}
                onChange={(ratePerMin) => set({ ratePerMin })}
                placeholder="60"
                hint="Over it, deliveries are refused 503 RATE_LIMITED with Retry-After; a source that retries a 5xx retries them."
              />
              <TextField
                id="oc-cap"
                label="Body cap (KB)"
                type="number"
                value={form.payloadCapKb}
                onChange={(payloadCapKb) => set({ payloadCapKb })}
                placeholder="256"
                hint="Max 512 — the core's own request limit is 1 MB and the forwarded envelope carries the body twice."
              />
            </Cluster>
          </Stack>
        )}
      </Panel>
    </Stack>
  );
}

/** A local label+hint wrapper for the one control that is a Cluster rather than a single input. */
function Field2({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <Stack gap={4}>
      <span>{label}</span>
      {children}
      {hint && <Note>{hint}</Note>}
    </Stack>
  );
}
