package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/oncall"
	"github.com/batondeck/batondeck-kanban/internal/oncall/auth"
	"github.com/batondeck/batondeck-kanban/internal/oncall/connectors"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The webhook ingest (plan §Architecture "Oncall ingest is in-binary"; conductor
// functions/src/oncall.ts and src/server/ingest.ts, the function → core hop collapsed into one
// process). POST /hooks/{integrationId} reaches Hook outside every session: the integration's id is
// the address, its signature (or key, or bearer) the credential, so there is no caller to authorize.
// Every refusal is one stderr line naming the integration.

// The headers a delivery is verified by.
const (
	HeaderAPIKey      = "X-Batondeck-Key"
	HeaderSignature   = "X-Batondeck-Signature"
	HeaderTimestamp   = "X-Batondeck-Timestamp"
	HeaderPagerDuty   = "X-Pagerduty-Signature"
	HeaderAuthorize   = "Authorization"
	rawBodyKeptMax    = 64 << 10
	rejectionIDSuffix = "#rejected"
)

// HookRequest is one delivery as the route read it: its headers (Header is http.Header.Get) and
// its whole body, already under the route's outer cap.
type HookRequest struct {
	Header func(name string) string
	Body   []byte
}

// HookRefusal is why a delivery was refused, "" when it was accepted.
type HookRefusal string

const (
	// HookNotFound is an unknown, tombstoned or disabled integration: one flat answer, so an
	// unguessable URL is not an oracle telling a scanner which ids exist.
	HookNotFound HookRefusal = "not_found"
	// HookTooLarge is a body over the integration's payloadCapKb: nothing is written.
	HookTooLarge HookRefusal = "too_large"
	// HookRateLimited is over the integration's rate budget, or the pre-check's burst: the route
	// answers it 503 RATE_LIMITED with Retry-After, which Alertmanager and PagerDuty both retry (P-353).
	HookRateLimited HookRefusal = "rate_limited"
	// HookUnauthorized is a signature, key or bearer that does not verify: a rejected delivery row.
	HookUnauthorized HookRefusal = "unauthorized"
	// HookBadRequest is a verified body that is not JSON or not the source's shape: a rejected
	// delivery row whose error names the field.
	HookBadRequest HookRefusal = "bad_request"
	// HookUnavailable is a verified delivery whose ingest failed retryably (a lost race, STALE, or
	// a store fault, INTERNAL): nothing of the failed event landed, and the sender is told to retry,
	// since Alertmanager and PagerDuty retry a 5xx, while Alertmanager drops every 4xx and PagerDuty
	// every 4xx other than 429 (P-233).
	HookUnavailable HookRefusal = "unavailable"
)

// HookOutcome is what Hook did: Refusal "" accepted, with one result per event the body carried.
// Challenge asks the route for a Basic WWW-Authenticate (an RFC 7617 sender authenticates only
// after one); Detail is a bad request's or an unavailable ingest's reason, the sender's to read;
// Code is an unavailable ingest's error code; RetryAfter is the seconds a rate-limited or
// unavailable sender should wait before it retries.
type HookOutcome struct {
	Refusal    HookRefusal
	Detail     string
	Code       string
	RetryAfter int
	Challenge  bool
	Results    []HookResult
}

// rateRetryAfter is the whole seconds the rate budget takes to refill one token at ratePerMin
// (the limiter refills ratePerMin a minute, at least 1): ceil(60/ratePerMin), at least 1.
func rateRetryAfter(ratePerMin int) int {
	if ratePerMin < 1 {
		ratePerMin = 1
	}
	return max(1, (60+ratePerMin-1)/ratePerMin)
}

// HookResult is one event's delivery: its id, what it did and the ticket it touched.
type HookResult struct {
	DeliveryID string  `json:"deliveryId"`
	Result     string  `json:"result"`
	TaskID     *string `json:"taskId,omitempty"`
	Replayed   bool    `json:"replayed,omitempty"`
}

// Hook ingests one webhook delivery to integrationID, in order: the coarse pre-check on the id as
// presented (before any read), the integration by its id alone (unknown, tombstoned or disabled is
// one not_found), the per-integration body cap, verification by the integration's auth mode before
// a byte is parsed, then the source's connector and one Core.ingest per event under the delivery id
// <source id or the body's hash>#<fingerprint or index> (a part over idPartMax bytes hashed). The delivery is charged to the
// integration's rate budget before it is verified, so a refused sender cannot buy an outbound JWKS
// fetch past the budget (P-231); a verification or parse failure is paid by that charge and is a
// rejected delivery row (its raw body kept for the raw-body window while the integration's hourly
// budget lasts). A delivery that parses gets the charge back before its events are ingested, since
// ingest charges each new delivery id itself. The error is an internal fault only.
func (c *Core) Hook(ctx context.Context, integrationID string, r HookRequest) (HookOutcome, error) {
	now := c.now()
	refuse := func(why HookRefusal, format string, a ...any) (HookOutcome, error) {
		c.log.Printf("ingest: refused a delivery to integration %s: %s: %s", integrationID, why, fmt.Sprintf(format, a...))
		return HookOutcome{Refusal: why}, nil
	}
	if !c.limiter.PreCheck(integrationID, now) {
		out, err := refuse(HookRateLimited, "over the pre-check burst")
		out.RetryAfter = 1 // the pre-check refills prePerSec tokens a second
		return out, err
	}
	integration, err := c.store.GetIntegrationByID(ctx, integrationID)
	if isNotFound(err) {
		return refuse(HookNotFound, "no such integration")
	}
	if err != nil {
		return HookOutcome{}, c.mapErr("ingest", err)
	}
	if !integration.Enabled {
		return refuse(HookNotFound, "the integration is disabled")
	}
	var limits IntegrationLimits
	if err := json.Unmarshal(integration.Limits, &limits); err != nil {
		return HookOutcome{}, c.mapErr("ingest", fmt.Errorf("integration %s limits: %w", integration.ID, err))
	}
	capKb := int(limits.PayloadCapKb)
	if capKb <= 0 {
		capKb = DefaultPayloadCapKb
	}
	if len(r.Body) > capKb<<10 {
		return refuse(HookTooLarge, "a %d-byte body is over the %d KiB cap", len(r.Body), capKb)
	}
	// The charge comes before verification: a jwt integration's verification may fetch its JWKS,
	// the only outbound call in ingest (P-231).
	if !c.limiter.Check(integration.ID, int(limits.RatePerMin), now, "") {
		c.log.Printf("ingest: integration %s over its rate limit (%d a minute): a delivery refused before verification", integration.ID, int(limits.RatePerMin))
		return HookOutcome{Refusal: HookRateLimited, RetryAfter: rateRetryAfter(int(limits.RatePerMin))}, nil
	}
	if reason, challenge := c.verifyHook(ctx, integration, r, now); reason != "" {
		c.log.Printf("ingest: integration %s refused a delivery's signature: %q", integration.ID, reason)
		if err := c.reject(ctx, integration, r.Body, reason, now); err != nil {
			return HookOutcome{}, err
		}
		return HookOutcome{Refusal: HookUnauthorized, Challenge: challenge}, nil
	}
	badRequest := func(reason string) (HookOutcome, error) {
		reason = capReason(reason)
		c.log.Printf("ingest: integration %s refused a delivery: %q", integration.ID, reason)
		if err := c.reject(ctx, integration, r.Body, reason, now); err != nil {
			return HookOutcome{}, err
		}
		return HookOutcome{Refusal: HookBadRequest, Detail: reason}, nil
	}
	payload, err := connectors.Decode(r.Body)
	if err != nil {
		return badRequest("invalid_json")
	}
	conn, ok := connectors.For(integration.Source)
	if !ok {
		return HookOutcome{}, c.mapErr("ingest", fmt.Errorf("integration %s: no connector for source %q", integration.ID, integration.Source))
	}
	var mapping connectors.Mapping
	if err := json.Unmarshal(integration.Mapping, &mapping); err != nil {
		return HookOutcome{}, c.mapErr("ingest", fmt.Errorf("integration %s mapping: %w", integration.ID, err))
	}
	bodyHash := connectors.Sha256Hex(r.Body)[:32]
	cc := connectors.Context{Mapping: &mapping, BodyHash: bodyHash, Now: now}
	events, err := conn.Events(payload, cc)
	if err != nil {
		return badRequest("unrecognized_payload: " + err.Error())
	}
	base := idPart(conn.DeliveryID(payload, cc))
	if base == "" {
		base = bodyHash
	}
	// The delivery parsed: its events pay as they did before the charge moved ahead of verification,
	// one token per new delivery id and none for a recorded one, so the charge is given back.
	c.limiter.Refund(integration.ID, int(limits.RatePerMin))
	caller := domain.Caller{Identity: IngestActor(integration.ID)}
	out := HookOutcome{Results: []HookResult{}}
	for i, ev := range events {
		// The fingerprint qualifies the id, so a grouped delivery's alerts never collapse into one.
		key := idPart(ev.Fingerprint)
		if key == "" {
			key = strconv.Itoa(i)
		}
		deliveryID := base + "#" + key
		got, err := c.ingest(ctx, caller, "ingest", access{}, integration, ev, deliveryID)
		if e, ok := domain.AsBatonDeckError(err); ok {
			if e.Code == domain.CodeRateLimited {
				out.Refusal, out.RetryAfter = HookRateLimited, rateRetryAfter(int(limits.RatePerMin))
				return out, nil
			}
			c.log.Printf("ingest: integration %s delivery %q refused: %s %q", integration.ID, deliveryID, e.Code, e.Message)
			if e.Retryable {
				// Nothing of this event landed (its transaction rolled back), and the events before it
				// are recorded, so the sender's retry replays them and lands this one once.
				out.Refusal, out.Code, out.Detail, out.RetryAfter = HookUnavailable, string(e.Code), e.Message, 1
				return out, nil
			}
			out.Refusal, out.Detail = HookBadRequest, e.Message
			return out, nil
		}
		if err != nil {
			return HookOutcome{}, err
		}
		task := "none"
		if got.TaskID != nil {
			task = *got.TaskID
		}
		c.log.Printf("ingest: integration %s delivery %q %s task %s doorbell %s replayed %t", integration.ID, deliveryID, got.Result, task, got.Doorbell, got.Replayed)
		out.Results = append(out.Results, HookResult{DeliveryID: deliveryID, Result: string(got.Result), TaskID: got.TaskID, Replayed: got.Replayed})
	}
	return out, nil
}

// idPartMax is the most bytes of a sender's delivery id or fingerprint a delivery id carries as
// sent; a longer one is replaced by idPart's hash.
const idPartMax = 200

// idPart is s as one part of a delivery id: as sent when it is at most idPartMax bytes, else
// "sha256:" and 32 hex of its hash, so the id stays within replay_delivery's and read_delivery_raw's
// 1024 characters and the same long id still names the same delivery.
func idPart(s string) string {
	if len(s) <= idPartMax {
		return s
	}
	return "sha256:" + connectors.Sha256Hex([]byte(s))[:32]
}

// verifyHook verifies r by integration's auth mode: "" when it passes, else the reason, and
// whether the refusal should challenge for Basic.
func (c *Core) verifyHook(ctx context.Context, integration store.Integration, r HookRequest, now time.Time) (reason string, challenge bool) {
	var a oncall.Auth
	if err := json.Unmarshal(integration.Auth, &a); err != nil {
		c.log.Printf("ingest: integration %s auth: %v", integration.ID, err)
		return "auth_unreadable", false
	}
	switch a.Mode {
	case oncall.AuthAPIKey:
		if !auth.VerifyAPIKey(r.Header(HeaderAPIKey), a.APIKeyHash) {
			return auth.ReasonAPIKeyMismatch, false
		}
		return "", false
	case oncall.AuthBasic:
		why, ok := auth.VerifyBasic(r.Header(HeaderAuthorize), a.APIKeyHash)
		if !ok {
			return why, true
		}
		return "", false
	case oncall.AuthJWT:
		if a.JWT == nil {
			return "jwt_not_configured", false
		}
		cfg := auth.JWT{Issuer: a.JWT.Issuer, Audience: a.JWT.Audience, JWKSURL: a.JWT.JWKSURL, Subject: a.JWT.Subject, Email: a.JWT.Email}
		why, ok := c.jwt.Verify(ctx, r.Header(HeaderAuthorize), cfg, integration.ID)
		if !ok {
			return why, false
		}
		return "", false
	case oncall.AuthHMAC:
		secrets, why := c.hookSecrets(integration, a, now)
		if why != "" {
			return why, false
		}
		// PagerDuty signs the raw body with its own secret and no timestamp; every other source signs
		// BatonDeck's timestamped form, whether its secret was minted or pasted (externallyIssued
		// only means it was never shown back), so the catalog's "replay-bounded" holds for it.
		var ok bool
		if integration.Source == "pagerduty" {
			sig := r.Header(HeaderPagerDuty)
			if sig == "" {
				sig = r.Header(HeaderSignature)
			}
			why, ok = oncall.VerifyRawBody(r.Body, sig, secrets)
		} else {
			why, ok = oncall.VerifyBatonDeck(r.Body, r.Header(HeaderTimestamp), r.Header(HeaderSignature), secrets, now)
		}
		if !ok {
			return why, false
		}
		return "", false
	}
	return "mode_not_enforceable: " + string(a.Mode), false
}

// hookSecrets opens the secrets an hmac delivery may be signed with. A previous secret that will
// not open (a keyring rotated since it was sealed, P-210) does not refuse the delivery: the current
// one is tried alone, as conductor's decryptSecretAny let a rotation survive, and the failure is
// logged. A current secret that will not open refuses it (reason secret_unavailable), recorded on
// the delivery log where the operator will look.
func (c *Core) hookSecrets(integration store.Integration, a oncall.Auth, now time.Time) ([][]byte, string) {
	if a.HMAC == nil {
		return nil, oncall.ReasonNoSecrets
	}
	secrets, err := c.VerificationSecrets(integration, now)
	if err == nil {
		return secrets, ""
	}
	if a.HMAC.DualAcceptOpen(now) && c.sealer != nil {
		c.log.Printf("ingest: integration %s: opening its secrets: %v; verifying with the current secret alone", integration.ID, err)
		current, cerr := oncall.HMACAuth{Current: a.HMAC.Current}.Secrets(c.sealer, oncall.SecretAAD(integration.ID), now)
		if cerr == nil {
			return current, ""
		}
		err = cerr
	}
	c.log.Printf("ingest: integration %s: its current secret will not open: %v", integration.ID, err)
	return nil, "secret_unavailable"
}

// rejectionReasonMax is the most runes of a refusal reason a rejected delivery's error keeps: a
// reason can carry the sender's text (a connector's error naming a field, a token header's alg),
// and the row is written for a sender who may hold no credential at all.
const rejectionReasonMax = 500

// capReason is reason cut to rejectionReasonMax runes.
func capReason(reason string) string {
	if rs := []rune(reason); len(rs) > rejectionReasonMax {
		return string(rs[:rejectionReasonMax])
	}
	return reason
}

// reject records a refused delivery (kind and result rejected, the reason its error), already paid
// by Hook's charge ahead of verification (P-231), so it charges nothing itself. The raw body (its
// first 64 KiB) is kept for the raw-body window while the integration's hourly budget of
// oncall.RejectionsPerHour lasts. Its id is the body's hash, so a sender repeating one body is
// recorded once; its error is the reason cut to rejectionReasonMax runes.
func (c *Core) reject(ctx context.Context, integration store.Integration, body []byte, reason string, now time.Time) error {
	reason = capReason(reason)
	deliveryID := connectors.Sha256Hex(body)[:32] + rejectionIDSuffix
	err := c.store.Atomically(ctx, func(tx store.Store) error {
		_, inserted, err := tx.InsertDelivery(ctx, store.Delivery{
			ProjectID: integration.ProjectID, IntegrationID: integration.ID, DeliveryID: deliveryID, TS: now.UnixMilli(),
			Kind: store.DeliveryRejected, Result: store.ResultRejected, Error: &reason, ExpiresAt: now.Add(c.deliveryTTL).UnixMilli(),
		})
		if err != nil || !inserted || len(body) == 0 || !c.limiter.RejectionBudget(integration.ID, now) {
			return err
		}
		kept := body[:min(len(body), rawBodyKeptMax)]
		return tx.PutDeliveryRawBody(ctx, integration.ProjectID, integration.ID, deliveryID, kept, now.Add(c.rawBodyTTL).UnixMilli())
	})
	return c.mapErr("ingest", err)
}
