/**
 * WHERE A SENDER POSTS AND WHAT IT SENDS, for one integration.
 *
 * The route is this server's own `POST /hooks/<id>`, so the URL is the server's base (as this browser
 * reaches it) plus the integration's `endpointPath`. The headers each auth mode reads are the ones
 * `internal/service/oncall_hooks.go` verifies; the signature form follows the SOURCE (PagerDuty's
 * raw-body `v1=` for pagerduty alone, BatonDeck's timestamped `v0=` for every other hmac source,
 * whether its secret was minted or supplied). `docs/oncall.md` is the full guide; it is linked in
 * the repository because this binary serves no `/docs/` route (P-247).
 */
import type { Integration } from '../../types.js';
import { Note, Stack } from '../../ui.js';

export const ONCALL_GUIDE_URL = 'https://github.com/batondeck/batondeck-kanban/blob/main/docs/oncall.md';

/** The URL a sender posts to: this server's origin, as the browser reaches it, and the path. */
export const endpointUrl = (endpointPath: string): string => `${window.location.origin}${endpointPath}`;

const code = (s: string) => <code style={{ wordBreak: 'break-all' }}>{s}</code>;

/** The curl a sender of the BatonDeck v0 form can start from, with this integration's URL. */
function v0Example(url: string): string {
  return [
    `secret='whsec_…'   # the secret shown once, or the one you supplied`,
    `body='{"fingerprint":"disk-db-1","status":"firing","severity":"critical","title":"Disk full on db-1"}'`,
    `ts=$(date +%s)`,
    `sig=$(printf '%s.%s' "$ts" "$body" | openssl dgst -sha256 -hmac "$secret" -hex | sed 's/^.* //')`,
    `curl -sS '${url}' -H 'Content-Type: application/json' \\`,
    `  -H "X-Batondeck-Timestamp: $ts" -H "X-Batondeck-Signature: v0=$sig" --data-binary "$body"`,
  ].join('\n');
}

function HowToSend({ integration, url }: { integration: Integration; url: string }) {
  const mode = integration.auth.mode;
  if (mode === 'hmac' && integration.source === 'pagerduty') {
    return (
      <Note>
        PagerDuty signs each delivery itself with the secret it issued, in {code('X-PagerDuty-Signature: v1=…')} (the
        hex HMAC-SHA256 of the raw body): set this URL on the webhook subscription and nothing else.
      </Note>
    );
  }
  if (mode === 'hmac') {
    return (
      <Stack gap={8}>
        <Note>
          Sign every delivery with two headers: {code('X-Batondeck-Timestamp')}, the current Unix time in seconds
          (within 5 minutes of this server&apos;s clock), and {code('X-Batondeck-Signature')}, {code('v0=')} followed
          by the hex HMAC-SHA256 of {code('<timestamp>.<raw body>')} keyed with the secret.
        </Note>
        <pre
          aria-label="Signed delivery example"
          style={{ margin: 0, padding: 10, overflowX: 'auto', background: 'var(--panel-2)', border: '1px solid var(--border)', fontSize: 12 }}
        >
          {v0Example(url)}
        </pre>
      </Stack>
    );
  }
  if (mode === 'api_key') {
    return <Note>Send the key in the {code('X-Batondeck-Key')} header, and nothing else.</Note>;
  }
  if (mode === 'basic') {
    return <Note>Send HTTP Basic authentication: any username, the key as the password.</Note>;
  }
  if (mode === 'jwt') {
    return (
      <Note>
        Send {code('Authorization: Bearer <token>')}: an RS256 token from the configured issuer, for the configured
        audience, signed by a key in its JWKS (and naming the pinned subject or email, if set).
      </Note>
    );
  }
  return <Note>Auth mode {code(mode)} is not enforced by this server.</Note>;
}

export function EndpointHelp({ integration }: { integration: Integration }) {
  const url = endpointUrl(integration.endpointPath);
  return (
    <Stack gap={8}>
      <div>
        URL: {code(url)}
      </div>
      <Note>
        Paste this URL into the source&apos;s webhook settings; if the source reaches this server at another address
        (behind a reverse proxy), use that address with the same path. Auth mode: <b>{integration.auth.mode}</b>
        {integration.auth.externallyIssued ? ' (you supplied the secret)' : ''}.
      </Note>
      <HowToSend integration={integration} url={url} />
      <span className="muted" style={{ fontSize: 12 }}>
        Each source&apos;s payload, the answers a sender gets and the limits are in the{' '}
        <a href={ONCALL_GUIDE_URL} target="_blank" rel="noreferrer">
          on-call guide
        </a>
        .
      </span>
    </Stack>
  );
}
