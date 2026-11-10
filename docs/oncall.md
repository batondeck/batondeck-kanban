# On-call webhooks

An on-call integration turns alerts from a monitoring tool into incident tickets on a board. Each
integration has its own webhook URL; a delivery to it is verified by the integration's auth mode
before a byte of it is parsed, then mapped by the source's connector into one ticket per alert.

A workspace admin creates integrations in the web UI (**On-call** in the sidebar, then **Setup**) or
with the `create_integration` tool. This page is what a sender needs: the URL, how
to authenticate each delivery, the payload each source sends, and the answers it gets back.

## The URL

```
POST <server base URL>/hooks/<integration id>
```

The setup drawer shows the full URL as your browser reaches the server, and every integration
answer carries the path as `endpointPath` (`/hooks/I-…`). If the sender reaches the server at
another address (the `public_url` behind a reverse proxy, say), use that base with the same path.
The route sits outside the sign-in: the integration id is the address and the delivery's signature,
key or bearer is the credential. An unknown, disabled or deleted integration answers one flat 404.

The integration id is not a secret on its own: project members see it on the incident tickets it
files (`createdBy: ingest:I-…`). The credential is what keeps a stranger from filing alerts; keep it
secret, and see [Limits](#limits) for what someone holding only the URL can still do.

## Authenticating a delivery

The source decides which modes are offered (the setup form shows only those). Each mode reads one
or two request headers; header names are case-insensitive.

### `hmac`: Generic signed JSON and Custom webhook

The server mints a `whsec_…` secret and shows it once (or uses the `externalSecret` you supply, 8
to 512 characters, which is never shown back). Each delivery carries two headers:

| Header | Value |
|---|---|
| `X-Batondeck-Timestamp` | the current time in Unix seconds, within 300 seconds of the server's clock |
| `X-Batondeck-Signature` | `v0=` and the hex HMAC-SHA256 of `<timestamp>.<raw body>`, keyed with the secret's text as is |

The signed string is the timestamp, a full stop, and the exact bytes of the request body. Several
signatures may be sent comma-separated (`v0=…,v0=…`); one match is enough. With `curl` and
`openssl`:

```bash
url='https://board.example.com/hooks/I-0123456789abcdef0123456789abcdef'
secret='whsec_…'
body='{"fingerprint":"disk-db-1","status":"firing","severity":"critical","title":"Disk full on db-1"}'
ts=$(date +%s)
sig=$(printf '%s.%s' "$ts" "$body" | openssl dgst -sha256 -hmac "$secret" -hex | sed 's/^.* //')
curl -sS "$url" -H 'Content-Type: application/json' \
  -H "X-Batondeck-Timestamp: $ts" -H "X-Batondeck-Signature: v0=$sig" \
  --data-binary "$body"
```

Send the body with `--data-binary` (or your client's raw-bytes option) so it reaches the server
exactly as signed. The form is the same whether the secret was minted or supplied.

**Rotation.** `rotate_integration_secret` (or **Rotate secret** in the drawer) mints a new secret,
shown once, or uses the `externalSecret` you pass it; for an integration whose secret you
supplied, the drawer asks you to paste the new one. Either way the previous secret keeps verifying
for 24 hours so the sender can switch over, unless `revokePrevious` ends it at once.

### `hmac`: PagerDuty

PagerDuty signs with the secret it issues when you create the webhook subscription, so paste that
secret as `externalSecret`; the server cannot mint one PagerDuty would use. PagerDuty sends
`X-PagerDuty-Signature: v1=<hex HMAC-SHA256 of the raw body>` (possibly several, comma-separated)
and no timestamp; a replay is bounded by the delivery id instead (see [Deduplication](#deduplication)).
To rotate, rotate in PagerDuty first, then paste the new value.

### `api_key`: Grafana, Alertmanager, Datadog, Dynatrace and Custom webhook

Send the key in one header, and nothing else:

```
X-Batondeck-Key: <key>
```

The setup form generates a key (or takes yours, 8 to 512 characters) and shows it once; only its
SHA-256 is stored. To change it, set a new one from **Edit configuration**.

### `basic`: Google Cloud Monitoring and Custom webhook

HTTP Basic authentication with any username and the key as the password. A request without
credentials is answered 401 with a `WWW-Authenticate: Basic` challenge, which is what Cloud
Monitoring waits for before it sends them.

### `jwt`: Custom webhook

```
Authorization: Bearer <JWT>
```

The token must be RS256, with `iss` equal to the integration's issuer, the integration's audience
in `aud`, an `exp` in the future, and (if present) an `nbf` in the past. The signing key is looked
up by `kid` in the key set at the integration's JWKS URL, which must be https on a public address;
the set is cached for 10 minutes, and a token with an unknown `kid` fetches it again at most every
30 seconds.

On a shared issuer (Google, GitHub Actions and the like) anyone with an account there can mint a
correctly signed token for an audience they choose, so issuer and audience alone do not identify
the sender. Pin the sender with `subject` (the token's `sub` must equal it exactly, for example a
service account's unique id or a GitHub Actions `repo:owner/name:ref:refs/heads/main` subject)
and/or `email` (the token's `email` must equal it exactly, letter case included, and
`email_verified` must be true).

## Payloads

Every connector caps what it keeps: a title at 300 characters, a description at 4,000, a
fingerprint at 512, a severity at 64, at most 64 labels and 64 annotations, and 10 links. Longer
values are truncated, not refused. A body is refused 413 over the integration's `payloadCapKb`
(256 KiB by default, at most 512 KiB).

### Generic signed JSON (`generic`)

One alert per delivery, as a JSON object. Unknown top-level keys are refused by name, so a typo is
an error you see rather than a field silently dropped.

| Key | Type | |
|---|---|---|
| `fingerprint` | string | required: the alert's identity; deliveries with the same fingerprint update one ticket |
| `status` | `"firing"`, `"resolved"` or `"test"` | required |
| `severity` | string | required: mapped to a priority by the integration's `severityMap` |
| `title` | string | required |
| `description` | string | |
| `labels` | object of strings | |
| `annotations` | object of strings | |
| `links` | array of strings | |
| `upstreamAssignee` | string | |
| `occurredAt` | ISO 8601 timestamp | defaults to the time of receipt |
| `deliveryId` | string | the sender's id for this delivery, used to deduplicate retries |
| `raw` | any | accepted and ignored |

```json
{"fingerprint":"checkout-latency","status":"firing","severity":"critical",
 "title":"Checkout latency over budget","description":"p95 above 800ms for 5 minutes",
 "labels":{"env":"prod","service":"checkout"},"links":["https://grafana.example.com/d/checkout"],
 "occurredAt":"2026-10-24T11:00:00Z","deliveryId":"gen-0001"}
```

### Grafana and Alertmanager

Their standard webhook payload, unchanged: point the contact point (Grafana) or `webhook_config`
(Alertmanager) at the URL and add the `X-Batondeck-Key` header. A grouped notification files one
ticket per alert, by the alert's fingerprint. Alertmanager's `groupKey` is its delivery id.

### Datadog

A webhook whose payload is a JSON template with these keys (Datadog's `$` variables fill them):

```json
{"alert_id":"$ALERT_ID","event_type":"$EVENT_TYPE","transition":"$ALERT_TRANSITION",
 "title":"$EVENT_TITLE","body":"$EVENT_MSG","severity":"$ALERT_PRIORITY","link":"$LINK","tags":"$TAGS"}
```

`alert_id` is required and is the fingerprint; a `transition` of `Recovered` resolves the ticket.
`tags` (`k:v,k:v`) become labels; `date`, if you add it, is read as an ISO timestamp.

### Dynatrace

A custom integration whose payload carries the problem's placeholders, in Dynatrace's default names
or camel case (`problemId`, `state`, `problemTitle`, `severityLevel`, …):

```json
{"PID":"{PID}","State":"{State}","ProblemTitle":"{ProblemTitle}","ProblemSeverity":"{ProblemSeverity}",
 "ProblemURL":"{ProblemURL}","ProblemDetailsText":"{ProblemDetailsText}","ProblemImpact":"{ProblemImpact}",
 "ImpactedEntities":{ImpactedEntities}}
```

`PID` is required and is the fingerprint; a `State` of `RESOLVED` or `CLOSED` resolves the ticket.

### Google Cloud Monitoring (`gcp`)

The notification channel's standard webhook (incident payload version 1.2). `incident_id` is the
fingerprint, `state` `closed` resolves, and `<incident_id>:<state>` is the delivery id.

### PagerDuty

A v3 webhook subscription's payload. The event's `id` is the delivery id; `incident.*` events
open, update and resolve the ticket.

### Custom webhook (`webhook`)

Any JSON. The integration's field mapping (`mapping.extract`) names, as dotted paths with numeric
indices and fallbacks, where each field lives; at minimum `extract.fingerprint` and
`extract.title`. The setup form's **How paths work** panel has a worked example. A path the mapper
cannot evaluate is refused when the mapping is saved.

## Answers

| Status | Body | Meaning | Retry? |
|---|---|---|---|
| 202 | `{"accepted":n,"results":[{"deliveryId","result","taskId","replayed"}]}` | each alert's outcome: `created`, `appended`, `annotated`, `closed` or `recorded` | no |
| 400 | `{"code":"VALIDATION","message":…}` | verified, but not JSON or not the source's shape; the message names the field | no: fix the payload |
| 401 | `{"code":"UNAUTHENTICATED"}` | the signature, key or bearer did not verify; the reason is on the integration's delivery log, never in the answer | no: fix the credential |
| 404 | `{"code":"NOT_FOUND"}` | no such integration, or it is disabled or deleted | no |
| 413 | `{"code":"too_large"}` | the body is over the integration's cap | no |
| 503 | `{"code":"RATE_LIMITED","retryable":true,"results":[…]}` | over the integration's rate budget; `Retry-After` says when a token is back, and the alerts of a grouped delivery that landed before the budget ran out are in `results` | yes, after `Retry-After`: a retry replays them free and lands the rest |
| 503 | `{"code":"STALE" or "INTERNAL","retryable":true,"results":[…]}` | a retryable failure; the alerts before it in `results` landed | yes: a retry replays them and lands the rest |
| 500 | `{"code":"INTERNAL","retryable":true}` | a server fault before any alert landed | yes |

Alertmanager and PagerDuty both retry a 5xx and drop a 4xx, which is why every answer that is
worth retrying is a 503.

## Deduplication

Each alert in a delivery gets the delivery id `<source id>#<fingerprint>`: the source id is the
sender's own (`deliveryId`, Alertmanager's `groupKey`, PagerDuty's event id, Cloud Monitoring's
`<incident_id>:<state>`, the first event's value at a Custom webhook mapping's
`extract.dedupKey`), or the body's SHA-256 when the source has none; the fingerprint is the
alert's, or its position in the delivery. A part longer than 200 bytes is replaced by
`sha256:<32 hex>` of it. A delivery id that was already recorded answers its recorded outcome with
`replayed: true` and files nothing, so a sender's retry is safe.

What a new delivery id does to the ticket depends on the integration's lifecycle: `correlate` (the
default) keeps one open ticket per fingerprint and appends repeats; `autoclose` does the same and,
on a resolve, deletes the ticket if nobody has touched it (annotating it otherwise); `per_event`
files a new ticket for every firing.

## Delivery log and retention

Every delivery that reaches verification, accepted or refused, is a row on the integration's
delivery log (`list_deliveries`, or **Delivery log** in the drawer), kept 30 days
(`retention.deliveries_days`); a 404, 413 or rate-limited answer writes nothing. A refused
delivery's row carries the refusal reason, cut to 500 characters. Its raw body (the first 64 KiB)
is kept 7 days (`retention.raw_bodies_days`) for at most 20 refused deliveries an hour per
integration, so you can see what a misconfigured sender sent; `replay_delivery` replays only
deliveries that verified and parsed.

## Limits

- **Rate budget.** Each integration has a token bucket of `ceil(ratePerMin / 4)` deliveries,
  refilled at `ratePerMin` a minute (`limits.ratePerMin`, 60 by default, at most 6,000). A delivery
  is charged to it *before* it is verified, so that unverified bearers cannot make the server
  fetch a `jwt` integration's JWKS past the budget. The consequence: anyone who holds the URL,
  with no credential, can spend the budget and make the real sender's deliveries answer 503 until
  they stop. If that happens, delete the integration and create a new one (a new URL), and give
  the new id only to the sender. The relief lasts only while the new id stays private: it is
  named again on every ticket the integration files (`createdBy` is `ingest:<id>`), so every
  project member who can read those tickets can see it. A coarser pre-check of 30 requests
  (refilled at 120 a second) per URL bounds a flood before the integration is even looked up.
- **The JWKS fetch** is the one outbound call ingest makes. It is https only, to a host whose every
  address is public (private, loopback, link-local, carrier-grade NAT and other reserved ranges are
  refused), with no redirects, at most 64 KiB and 5 seconds, and is logged as
  `ingest: outbound JWKS fetch GET <url> for integration <id>`.
