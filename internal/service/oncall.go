package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/oncall"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The on-call service (conductor src/tools/integrations.ts, deliveries.ts). An integration's
// secret is minted here, sealed with the keyring (oncall.Seal, AAD oncall.SecretAAD(id)) before it
// reaches the store, and answered once; every other answer redacts auth to its shape.

// The on-call defaults and bounds (conductor src/config/constants.ts:326-333).
const (
	DefaultRatePerMin   = 60
	DefaultPayloadCapKb = 256
	maxRatePerMin       = 6000
	maxPayloadCapKb     = 512
	maxIntegrationList  = 200
	maxDeliveriesPage   = 200
	// DeliveryRetention is how long a delivery row is kept; RawBodyRetention a refused delivery's
	// raw body (plan §Clarifications 5). R2.9 sweeps both.
	DeliveryRetention = 30 * 24 * time.Hour
	RawBodyRetention  = 7 * 24 * time.Hour
)

func (c *Core) Oncall() Oncall { return oncallSvc{c} }

type oncallSvc struct{ c *Core }

// IngestActor is the identity an integration's board writes are stamped with (conductor ingestActor).
func IngestActor(integrationID string) string { return "ingest:" + integrationID }

// newIntegrationID is 128 random bits: an integration id is a webhook URL path segment, so it must
// not be guessable.
func newIntegrationID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("service: crypto/rand: %v", err))
	}
	return "I-" + hex.EncodeToString(b)
}

// mintWebhookSecret is whsec_ and 256 random bits, base64url.
func mintWebhookSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("service: crypto/rand: %v", err))
	}
	return "whsec_" + base64.RawURLEncoding.EncodeToString(b)
}

// builtAuth is an auth block ready for the store, and the secret to show once ("" when none was
// minted: an external secret is never echoed).
type builtAuth struct {
	auth  oncall.Auth
	shown string
}

func secretBounds(field, v string) error {
	if n := len(v); n < 8 || n > 512 {
		return domain.Validation(field+" is 8 to 512 characters", map[string]any{"field": field})
	}
	return nil
}

// buildAuth turns a caller's auth input into the stored block: an hmac secret minted (or the
// source's own) and sealed, an api_key or basic key hashed, jwt's issuer kept, mtls refused.
func (s oncallSvc) buildAuth(in IntegrationAuthInput, integrationID string, now time.Time) (builtAuth, error) {
	switch in.Mode {
	case "mtls":
		return builtAuth{}, domain.Validation("auth mode 'mtls' is reserved and not yet enforceable; use hmac, api_key, basic or jwt", map[string]any{"mode": in.Mode})
	case "hmac":
		if s.c.sealer == nil {
			return builtAuth{}, domain.Validation("This server has no keyring, so webhook secrets cannot be stored; use api_key, basic or jwt", nil)
		}
		secret, shown := in.ExternalSecret, ""
		if secret == "" {
			secret = mintWebhookSecret()
			shown = secret
		} else if err := secretBounds("auth.externalSecret", secret); err != nil {
			return builtAuth{}, err
		}
		env, err := oncall.Seal(s.c.sealer, []byte(secret), oncall.SecretAAD(integrationID))
		if err != nil {
			return builtAuth{}, err
		}
		h := &oncall.HMACAuth{Current: env, RotatedAt: now.UnixMilli(), ExternallyIssued: in.ExternalSecret != ""}
		return builtAuth{auth: oncall.Auth{Mode: oncall.AuthHMAC, HMAC: h}, shown: shown}, nil
	case "api_key", "basic":
		if in.APIKey == "" {
			return builtAuth{}, domain.Validation("auth mode '"+string(in.Mode)+"' requires apiKey", map[string]any{"field": "auth.apiKey"})
		}
		if err := secretBounds("auth.apiKey", in.APIKey); err != nil {
			return builtAuth{}, err
		}
		return builtAuth{auth: oncall.Auth{Mode: oncall.AuthMode(in.Mode), APIKeyHash: oncall.HashAPIKey(in.APIKey)}}, nil
	case "jwt":
		j := in.JWT
		if j.Issuer == "" || j.Audience == "" || j.JWKSURL == "" {
			return builtAuth{}, domain.Validation("auth mode 'jwt' requires jwt {issuer, audience, jwksUrl}", map[string]any{"field": "auth.jwt"})
		}
		return builtAuth{auth: oncall.Auth{Mode: oncall.AuthJWT, JWT: &oncall.JWTAuth{Issuer: j.Issuer, Audience: j.Audience, JWKSURL: j.JWKSURL}}}, nil
	}
	return builtAuth{}, domain.Validation("unknown auth mode: "+string(in.Mode), map[string]any{"field": "auth.mode", "allowed": IntegrationAuthModeValues})
}

// requireExternalSecret: PagerDuty signs with a secret of its own, so minting one for it would
// make an integration no delivery can pass (conductor assertExternalSecretWhereRequired).
func requireExternalSecret(source string, mode IntegrationAuthMode, external string) error {
	if source != "pagerduty" || mode != "hmac" || external != "" {
		return nil
	}
	return domain.Validation("PagerDuty signs webhooks with its own secret: create the subscription in PagerDuty first, then pass the secret it shows as externalSecret",
		map[string]any{"source": source, "mode": mode})
}

// checkMapping refuses a mapping that would do nothing: extract, statusMap and defaults are read by
// the webhook connector alone.
func checkMapping(source string, m IntegrationMapping) error {
	custom := !isZeroExtract(m.Extract) || len(m.StatusMap) > 0 || m.Defaults != (MappingDefaults{})
	if custom && source != "webhook" {
		return domain.Validation("mapping.extract, statusMap and defaults apply to the 'webhook' source only; '"+source+"' has a built-in connector",
			map[string]any{"source": source})
	}
	if err := checkLabelList("mapping.labels", m.Labels); err != nil {
		return err
	}
	for k, p := range m.SeverityMap {
		if err := checkPriority(p); err != nil {
			return domain.Validation("mapping.severityMap."+k+" is not a priority", map[string]any{"field": "mapping.severityMap"})
		}
	}
	return nil
}

func isZeroExtract(e MappingExtract) bool {
	b, _ := json.Marshal(e)
	return string(b) == "{}"
}

func checkIntegrationFields(name string, lifecycle IntegrationLifecycle, routing *IntegrationRouting, limits IntegrationLimitsIn) error {
	if err := maxChars("name", name, maxTitleChars); err != nil {
		return err
	}
	if lifecycle != "" && !inValues(lifecycle, IntegrationLifecycleValues) {
		return domain.Validation("unknown lifecycle: "+string(lifecycle), map[string]any{"field": "lifecycle", "allowed": IntegrationLifecycleValues})
	}
	if routing != nil && !inValues(routing.Mode, RoutingModeValues) {
		return domain.Validation("unknown routing mode: "+string(routing.Mode), map[string]any{"field": "routing.mode", "allowed": RoutingModeValues})
	}
	if limits.RatePerMin < 0 || limits.RatePerMin > maxRatePerMin {
		return domain.Validation("limits.ratePerMin is 1 to 6000", map[string]any{"field": "limits.ratePerMin"})
	}
	if limits.PayloadCapKb < 0 || limits.PayloadCapKb > maxPayloadCapKb {
		return domain.Validation("limits.payloadCapKb is 1 to 512", map[string]any{"field": "limits.payloadCapKb"})
	}
	return nil
}

func inValues[T comparable](v T, values []T) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

func marshalObject(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, internalf("oncall: %v", err)
	}
	return b, nil
}

// limitsJSON is the effective limits: in's where set, else base's.
func limitsJSON(in IntegrationLimitsIn, base IntegrationLimits) (json.RawMessage, error) {
	if in.RatePerMin > 0 {
		base.RatePerMin = float64(in.RatePerMin)
	}
	if in.PayloadCapKb > 0 {
		base.PayloadCapKb = float64(in.PayloadCapKb)
	}
	return marshalObject(map[string]int64{"ratePerMin": int64(base.RatePerMin), "payloadCapKb": int64(base.PayloadCapKb)})
}

// integrationOut is the row as a caller sees it: auth redacted to its shape.
func integrationOut(i store.Integration) (Integration, error) {
	var a oncall.Auth
	if err := json.Unmarshal(i.Auth, &a); err != nil {
		return Integration{}, internalf("integration %s auth: %v", i.ID, err)
	}
	view := IntegrationAuthView{Mode: IntegrationAuthMode(a.Mode)}
	if h := a.HMAC; h != nil {
		view.RotatedAt = isoMs(h.RotatedAt)
		view.ExternallyIssued = h.ExternallyIssued
		if h.Previous != "" {
			until := isoMs(h.PreviousExpiresAt)
			view.DualAcceptUntil = &until
		}
	}
	out := Integration{
		Auth: view, CreatedAt: isoMs(i.CreatedAt), CreatedBy: i.CreatedBy, Enabled: i.Enabled, EndpointPath: "/hooks/" + i.ID,
		ID: i.ID, Lifecycle: IntegrationLifecycle(i.Lifecycle), Name: i.Name, ProjectID: i.ProjectID,
		Source: IntegrationSource(i.Source), Target: IntegrationTarget{BoardID: i.BoardID, ColumnID: i.ColumnID},
		UpdatedAt: isoMs(i.UpdatedAt), Version: float64(i.Version),
	}
	if i.DeletedAt != nil {
		out.DeletedAt = isoMs(*i.DeletedAt)
	}
	for _, f := range []struct {
		dst *JSONObject
		src json.RawMessage
	}{{&out.Mapping, i.Mapping}, {&out.Routing, i.Routing}, {&out.Policy, i.Policy}} {
		*f.dst = JSONObject{}
		if err := json.Unmarshal(f.src, f.dst); err != nil {
			return Integration{}, internalf("integration %s: %v", i.ID, err)
		}
	}
	if err := json.Unmarshal(i.Limits, &out.Limits); err != nil {
		return Integration{}, internalf("integration %s limits: %v", i.ID, err)
	}
	return out, nil
}

// checkTarget is the board, which must exist, and the column, which must be on it.
func checkTarget(ctx context.Context, tx store.Store, projectID, boardID string, columnID *string) error {
	b, _, err := tx.GetBoard(ctx, projectID, boardID)
	if err != nil {
		return err
	}
	if columnID == nil {
		return nil
	}
	for _, c := range b.Columns {
		if c.ID == *columnID {
			return nil
		}
	}
	return domain.NotFound("Column", *columnID)
}

func (s oncallSvc) boardEvent(t *txn, typ domain.EventType, boardID string, data map[string]any) error {
	raw, err := marshalObject(data)
	if err != nil {
		return err
	}
	t.BoardEvent(store.EventInput{BoardID: boardID, Type: typ, Data: raw})
	return nil
}

func (s oncallSvc) CreateIntegration(ctx context.Context, caller domain.Caller, req CreateIntegrationReq) (CreateIntegrationResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "create_integration", domain.GrantSubject{BoardID: req.BoardID}, false)
	if err != nil {
		return CreateIntegrationResp{}, err
	}
	for field, v := range map[string]string{"name": req.Name, "boardId": req.BoardID} {
		if err := required(field, v); err != nil {
			return CreateIntegrationResp{}, err
		}
	}
	if _, ok := oncall.LookupSource(string(req.Source)); !ok {
		return CreateIntegrationResp{}, domain.Validation("unknown source: "+string(req.Source), map[string]any{"field": "source", "allowed": IntegrationSourceValues})
	}
	if err := checkIntegrationFields(req.Name, req.Lifecycle, &req.Routing, req.Limits); err != nil {
		return CreateIntegrationResp{}, err
	}
	if err := checkMapping(string(req.Source), req.Mapping); err != nil {
		return CreateIntegrationResp{}, err
	}
	if err := requireExternalSecret(string(req.Source), req.Auth.Mode, req.Auth.ExternalSecret); err != nil {
		return CreateIntegrationResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return CreateIntegrationResp{}, err
	}
	lifecycle := req.Lifecycle
	if lifecycle == "" {
		lifecycle = "correlate"
	}
	var column *string
	if req.ColumnID != "" {
		column = &req.ColumnID
	}
	// The secret is minted inside the transaction, so a replay (fn not run) mints nothing and
	// shows nothing: it was shown once, by the call that created the row.
	var shown string
	out, err := mutate(ctx, s.c, caller, mutation{
		Tool: "create_integration", Args: map[string]any{"projectId": req.ProjectID, "name": req.Name, "source": string(req.Source), "boardId": req.BoardID},
		ProjectID: req.ProjectID, IdempotencyKey: req.IdempotencyKey, Access: a,
	}, func(t *txn) (Integration, error) {
		if err := checkTarget(ctx, t.Tx, req.ProjectID, req.BoardID, column); err != nil {
			return Integration{}, err
		}
		id := newIntegrationID()
		built, err := s.buildAuth(req.Auth, id, t.Now)
		if err != nil {
			return Integration{}, err
		}
		in := store.CreateIntegrationInput{ProjectID: req.ProjectID, ID: id, Name: req.Name, Source: string(req.Source),
			BoardID: req.BoardID, ColumnID: column, Lifecycle: string(lifecycle), CreatedBy: caller.Identity, CreatedAt: t.Now.UnixMilli()}
		if in.Auth, err = marshalObject(built.auth); err != nil {
			return Integration{}, err
		}
		if in.Mapping, err = marshalObject(req.Mapping); err != nil {
			return Integration{}, err
		}
		if in.Routing, err = marshalObject(req.Routing); err != nil {
			return Integration{}, err
		}
		if in.Policy, err = marshalObject(req.Policy); err != nil {
			return Integration{}, err
		}
		if in.Limits, err = limitsJSON(req.Limits, IntegrationLimits{RatePerMin: DefaultRatePerMin, PayloadCapKb: DefaultPayloadCapKb}); err != nil {
			return Integration{}, err
		}
		row, err := t.Tx.CreateIntegration(ctx, in)
		if err != nil {
			return Integration{}, err
		}
		if err := s.boardEvent(t, domain.EventIntegrationCreated, row.BoardID, map[string]any{"integrationId": row.ID, "source": row.Source}); err != nil {
			return Integration{}, err
		}
		shown = built.shown
		return integrationOut(row)
	})
	if err != nil {
		return CreateIntegrationResp{}, err
	}
	resp := CreateIntegrationResp{Integration: out}
	if shown != "" {
		resp.Secret, resp.ShownOnce = shown, true
	}
	return resp, nil
}

func (s oncallSvc) UpdateIntegration(ctx context.Context, caller domain.Caller, req UpdateIntegrationReq) (UpdateIntegrationResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "update_integration", domain.GrantSubject{BoardID: req.Patch.BoardID}, false)
	if err != nil {
		return UpdateIntegrationResp{}, err
	}
	if err := required("integrationId", req.IntegrationID); err != nil {
		return UpdateIntegrationResp{}, err
	}
	p := req.Patch
	var routing *IntegrationRouting
	if p.Routing.Mode != "" || p.Routing.OnCallAgent.Set || p.Routing.FallbackBroadcast || p.Routing.PDUserMap != nil {
		routing = &p.Routing
	}
	if err := checkIntegrationFields(p.Name, p.Lifecycle, routing, p.Limits); err != nil {
		return UpdateIntegrationResp{}, err
	}
	var shown string
	out, err := mutate(ctx, s.c, caller, mutation{
		Tool: "update_integration", Args: map[string]any{"projectId": req.ProjectID, "integrationId": req.IntegrationID},
		ProjectID: req.ProjectID, Access: a,
	}, func(t *txn) (Integration, error) {
		cur, err := t.Tx.GetIntegration(ctx, req.ProjectID, req.IntegrationID)
		if err != nil {
			return Integration{}, err
		}
		if cur.Version != req.Version {
			return Integration{}, domain.Stale(req.Version, cur.Version)
		}
		// Against the stored source: a source is fixed once its endpoint is pasted somewhere.
		if err := checkMapping(cur.Source, p.Mapping); err != nil {
			return Integration{}, err
		}
		patch := store.IntegrationPatch{UpdatedAt: t.Now.UnixMilli()}
		if p.Name != "" {
			patch.Name = &p.Name
		}
		if p.Enabled.Set {
			patch.Enabled = &p.Enabled.V
		}
		if p.Lifecycle != "" {
			l := string(p.Lifecycle)
			patch.Lifecycle = &l
		}
		board, column := cur.BoardID, cur.ColumnID
		if p.BoardID != "" {
			board = p.BoardID
			patch.BoardID = &p.BoardID
		}
		if p.ColumnID.Set {
			column = p.ColumnID.Ptr()
			patch.ColumnID = &store.Nullable[string]{Value: column}
		}
		if patch.BoardID != nil || patch.ColumnID != nil {
			if err := checkTarget(ctx, t.Tx, req.ProjectID, board, column); err != nil {
				return Integration{}, err
			}
		}
		if p.Auth.Mode != "" {
			if err := requireExternalSecret(cur.Source, p.Auth.Mode, p.Auth.ExternalSecret); err != nil {
				return Integration{}, err
			}
			built, err := s.buildAuth(p.Auth, cur.ID, t.Now)
			if err != nil {
				return Integration{}, err
			}
			if patch.Auth, err = marshalObject(built.auth); err != nil {
				return Integration{}, err
			}
			shown = built.shown
		}
		if !isZeroMapping(p.Mapping) {
			if patch.Mapping, err = marshalObject(p.Mapping); err != nil {
				return Integration{}, err
			}
		}
		if routing != nil {
			if patch.Routing, err = marshalObject(p.Routing); err != nil {
				return Integration{}, err
			}
		}
		if p.Policy != (IntegrationPolicy{}) {
			if patch.Policy, err = marshalObject(p.Policy); err != nil {
				return Integration{}, err
			}
		}
		if p.Limits != (IntegrationLimitsIn{}) {
			var base IntegrationLimits
			if err := json.Unmarshal(cur.Limits, &base); err != nil {
				return Integration{}, internalf("integration %s limits: %v", cur.ID, err)
			}
			if patch.Limits, err = limitsJSON(p.Limits, base); err != nil {
				return Integration{}, err
			}
		}
		row, err := t.Tx.UpdateIntegration(ctx, req.ProjectID, req.IntegrationID, req.Version, patch)
		if err != nil {
			return Integration{}, err
		}
		if err := s.boardEvent(t, domain.EventIntegrationUpdated, row.BoardID, map[string]any{"integrationId": row.ID}); err != nil {
			return Integration{}, err
		}
		return integrationOut(row)
	})
	if err != nil {
		return UpdateIntegrationResp{}, err
	}
	resp := UpdateIntegrationResp{Integration: out}
	if shown != "" {
		resp.Secret, resp.ShownOnce = shown, true
	}
	return resp, nil
}

func isZeroMapping(m IntegrationMapping) bool {
	b, _ := json.Marshal(m)
	return string(b) == "{}"
}

func (s oncallSvc) DeleteIntegration(ctx context.Context, caller domain.Caller, req DeleteIntegrationReq) (DeleteIntegrationResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "delete_integration", domain.GrantSubject{}, false)
	if err != nil {
		return DeleteIntegrationResp{}, err
	}
	if err := required("integrationId", req.IntegrationID); err != nil {
		return DeleteIntegrationResp{}, err
	}
	out, err := mutate(ctx, s.c, caller, mutation{
		Tool: "delete_integration", Args: map[string]any{"projectId": req.ProjectID, "integrationId": req.IntegrationID},
		ProjectID: req.ProjectID, Access: a,
	}, func(t *txn) (Integration, error) {
		row, err := t.Tx.DeleteIntegration(ctx, req.ProjectID, req.IntegrationID, req.Version, t.Now.UnixMilli())
		if err != nil {
			return Integration{}, err
		}
		if err := s.boardEvent(t, domain.EventIntegrationDeleted, row.BoardID, map[string]any{"integrationId": row.ID}); err != nil {
			return Integration{}, err
		}
		return integrationOut(row)
	})
	if err != nil {
		return DeleteIntegrationResp{}, err
	}
	return DeleteIntegrationResp{Deleted: true, Integration: out}, nil
}

func (s oncallSvc) ListIntegrations(ctx context.Context, caller domain.Caller, req ListIntegrationsReq) (ListIntegrationsResp, error) {
	if _, err := s.c.authorize(ctx, caller, req.ProjectID, "list_integrations", domain.GrantSubject{}, false); err != nil {
		return ListIntegrationsResp{}, err
	}
	if req.Limit < 0 || req.Limit > maxIntegrationList {
		return ListIntegrationsResp{}, domain.Validation("limit is 1 to 200", map[string]any{"field": "limit"})
	}
	rows, err := s.c.store.ListIntegrations(ctx, req.ProjectID, int(req.Limit), req.IncludeDeleted)
	if err != nil {
		return ListIntegrationsResp{}, s.c.mapErr("list_integrations", err)
	}
	out := ListIntegrationsResp{Integrations: make([]Integration, 0, len(rows))}
	for _, r := range rows {
		i, err := integrationOut(r)
		if err != nil {
			return ListIntegrationsResp{}, s.c.mapErr("list_integrations", err)
		}
		out.Integrations = append(out.Integrations, i)
	}
	return out, nil
}

func (s oncallSvc) RotateIntegrationSecret(ctx context.Context, caller domain.Caller, req RotateIntegrationSecretReq) (RotateIntegrationSecretResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "rotate_integration_secret", domain.GrantSubject{}, false)
	if err != nil {
		return RotateIntegrationSecretResp{}, err
	}
	if err := required("integrationId", req.IntegrationID); err != nil {
		return RotateIntegrationSecretResp{}, err
	}
	if req.ExternalSecret != "" {
		if err := secretBounds("externalSecret", req.ExternalSecret); err != nil {
			return RotateIntegrationSecretResp{}, err
		}
	}
	if s.c.sealer == nil {
		return RotateIntegrationSecretResp{}, domain.Validation("This server has no keyring, so webhook secrets cannot be stored", nil)
	}
	var shown string
	out, err := mutate(ctx, s.c, caller, mutation{
		Tool:      "rotate_integration_secret",
		Args:      map[string]any{"projectId": req.ProjectID, "integrationId": req.IntegrationID, "revokePrevious": req.RevokePrevious},
		ProjectID: req.ProjectID, Access: a,
	}, func(t *txn) (Integration, error) {
		cur, err := t.Tx.GetIntegration(ctx, req.ProjectID, req.IntegrationID)
		if err != nil {
			return Integration{}, err
		}
		if cur.Version != req.Version {
			return Integration{}, domain.Stale(req.Version, cur.Version)
		}
		var auth oncall.Auth
		if err := json.Unmarshal(cur.Auth, &auth); err != nil {
			return Integration{}, internalf("integration %s auth: %v", cur.ID, err)
		}
		if auth.Mode != oncall.AuthHMAC || auth.HMAC == nil {
			return Integration{}, domain.Validation("Integration auth mode is '"+string(auth.Mode)+"'; rotation applies to 'hmac' only", map[string]any{"mode": auth.Mode})
		}
		if err := requireExternalSecret(cur.Source, "hmac", req.ExternalSecret); err != nil {
			return Integration{}, err
		}
		secret := req.ExternalSecret
		if secret == "" {
			secret = mintWebhookSecret()
			shown = secret
		}
		env, err := oncall.Seal(s.c.sealer, []byte(secret), oncall.SecretAAD(cur.ID))
		if err != nil {
			return Integration{}, err
		}
		next := &oncall.HMACAuth{Current: env, RotatedAt: t.Now.UnixMilli(), ExternallyIssued: req.ExternalSecret != ""}
		if !req.RevokePrevious {
			next.Previous = auth.HMAC.Current
			next.PreviousExpiresAt = t.Now.Add(oncall.RotationGrace).UnixMilli()
		}
		auth.HMAC = next
		raw, err := marshalObject(auth)
		if err != nil {
			return Integration{}, err
		}
		row, err := t.Tx.UpdateIntegration(ctx, req.ProjectID, req.IntegrationID, req.Version, store.IntegrationPatch{Auth: raw, UpdatedAt: t.Now.UnixMilli()})
		if err != nil {
			return Integration{}, err
		}
		if err := s.boardEvent(t, domain.EventIntegrationSecretRotated, row.BoardID, map[string]any{"integrationId": row.ID, "revokedPrevious": req.RevokePrevious}); err != nil {
			return Integration{}, err
		}
		return integrationOut(row)
	})
	if err != nil {
		return RotateIntegrationSecretResp{}, err
	}
	resp := RotateIntegrationSecretResp{Integration: out}
	if shown != "" {
		resp.Secret, resp.ShownOnce = shown, true
	}
	return resp, nil
}

// VerificationSecrets is the secrets a delivery to i at now may be signed with: the current one and,
// while its window is open, the rotated-out one (oncall.HMACAuth.Secrets). For R2.9's ingest route.
func (c *Core) VerificationSecrets(i store.Integration, now time.Time) ([][]byte, error) {
	var auth oncall.Auth
	if err := json.Unmarshal(i.Auth, &auth); err != nil {
		return nil, internalf("integration %s auth: %v", i.ID, err)
	}
	if auth.HMAC == nil {
		return nil, nil
	}
	if c.sealer == nil {
		return nil, internalf("oncall: no keyring to open integration %s's secrets", i.ID)
	}
	return auth.HMAC.Secrets(c.sealer, oncall.SecretAAD(i.ID), now)
}

func deliveryOut(d store.Delivery) Delivery {
	out := Delivery{DeliveryID: d.DeliveryID, Kind: DeliveryKind(d.Kind), Result: string(d.Result), Ts: isoMs(d.TS), Error: d.Error, TaskID: d.TaskID}
	if d.NormalizedEvent != nil {
		var ev oncall.Event
		if json.Unmarshal(d.NormalizedEvent, &ev) == nil {
			out.Fingerprint, out.Title, out.Severity = &ev.Fingerprint, &ev.Title, &ev.Severity
		}
	}
	return out
}

func (s oncallSvc) ListDeliveries(ctx context.Context, caller domain.Caller, req ListDeliveriesReq) (ListDeliveriesResp, error) {
	if _, err := s.c.authorize(ctx, caller, req.ProjectID, "list_deliveries", domain.GrantSubject{}, false); err != nil {
		return ListDeliveriesResp{}, err
	}
	if err := required("integrationId", req.IntegrationID); err != nil {
		return ListDeliveriesResp{}, err
	}
	if req.Limit < 0 || req.Limit > maxDeliveriesPage {
		return ListDeliveriesResp{}, domain.Validation("limit is 1 to 200", map[string]any{"field": "limit"})
	}
	if req.Kind != "" && !inValues(req.Kind, DeliveryKindValues) {
		return ListDeliveriesResp{}, domain.Validation("unknown kind: "+string(req.Kind), map[string]any{"field": "kind", "allowed": DeliveryKindValues})
	}
	page, err := s.c.store.ListDeliveries(ctx, req.ProjectID, req.IntegrationID, store.DeliveryFilter{Kind: store.DeliveryKind(req.Kind)}, req.Cursor, int(req.Limit))
	if err != nil {
		return ListDeliveriesResp{}, s.c.mapErr("list_deliveries", err)
	}
	out := ListDeliveriesResp{Deliveries: make([]Delivery, 0, len(page.Deliveries)), NextCursor: page.NextCursor}
	for _, d := range page.Deliveries {
		out.Deliveries = append(out.Deliveries, deliveryOut(d))
	}
	return out, nil
}

var unsafeFilename = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// ReadDeliveryRaw answers NOT_FOUND for a delivery with no kept body (never kept, or swept after
// RawBodyRetention): the task's AC3, where conductor answers payloadBase64 null.
func (s oncallSvc) ReadDeliveryRaw(ctx context.Context, caller domain.Caller, req ReadDeliveryRawReq) (ReadDeliveryRawResp, error) {
	if _, err := s.c.authorize(ctx, caller, req.ProjectID, "read_delivery_raw", domain.GrantSubject{}, false); err != nil {
		return ReadDeliveryRawResp{}, err
	}
	for field, v := range map[string]string{"integrationId": req.IntegrationID, "deliveryId": req.DeliveryID} {
		if err := required(field, v); err != nil {
			return ReadDeliveryRawResp{}, err
		}
	}
	if err := maxChars("deliveryId", req.DeliveryID, 1024); err != nil {
		return ReadDeliveryRawResp{}, err
	}
	if _, err := s.c.store.GetDelivery(ctx, req.ProjectID, req.IntegrationID, req.DeliveryID); err != nil {
		return ReadDeliveryRawResp{}, s.c.mapErr("read_delivery_raw", err)
	}
	body, err := s.c.store.GetDeliveryRawBody(ctx, req.ProjectID, req.IntegrationID, req.DeliveryID)
	if err != nil {
		return ReadDeliveryRawResp{}, s.c.mapErr("read_delivery_raw", err)
	}
	b64 := base64.StdEncoding.EncodeToString(body)
	return ReadDeliveryRawResp{
		ContentType: "application/octet-stream", DeliveryID: req.DeliveryID, PayloadBase64: &b64,
		SuggestedFilename: "delivery-" + unsafeFilename.ReplaceAllString(req.DeliveryID, "_") + ".txt",
	}, nil
}

func (s oncallSvc) ReplayDelivery(ctx context.Context, caller domain.Caller, req ReplayDeliveryReq) (ReplayDeliveryResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "replay_delivery", domain.GrantSubject{}, false)
	if err != nil {
		return ReplayDeliveryResp{}, err
	}
	for field, v := range map[string]string{"integrationId": req.IntegrationID, "deliveryId": req.DeliveryID} {
		if err := required(field, v); err != nil {
			return ReplayDeliveryResp{}, err
		}
	}
	if err := maxChars("deliveryId", req.DeliveryID, 1024); err != nil {
		return ReplayDeliveryResp{}, err
	}
	integration, err := s.c.store.GetIntegration(ctx, req.ProjectID, req.IntegrationID)
	if err != nil {
		return ReplayDeliveryResp{}, s.c.mapErr("replay_delivery", err)
	}
	original, err := s.c.store.GetDelivery(ctx, req.ProjectID, req.IntegrationID, req.DeliveryID)
	if err != nil {
		return ReplayDeliveryResp{}, s.c.mapErr("replay_delivery", err)
	}
	if original.NormalizedEvent == nil {
		return ReplayDeliveryResp{}, domain.Validation("This delivery has no normalized event to replay: it was refused before parsing. Its raw payload is in read_delivery_raw; unverified bytes are never replayed",
			map[string]any{"deliveryId": req.DeliveryID})
	}
	var ev oncall.Event
	if err := json.Unmarshal(original.NormalizedEvent, &ev); err != nil {
		return ReplayDeliveryResp{}, s.c.mapErr("replay_delivery", fmt.Errorf("delivery %s event: %w", req.DeliveryID, err))
	}
	// A new delivery id per replay: the original's would replay its own recorded outcome.
	newID := "replay:" + req.DeliveryID + ":" + strconv.FormatInt(s.c.nowMs(), 10)
	got, err := s.c.ingest(ctx, caller, "replay_delivery", a, integration, ev, newID)
	if err != nil {
		return ReplayDeliveryResp{}, err
	}
	return ReplayDeliveryResp{NewDeliveryID: newID, Replayed: got.Replayed, Result: string(got.Result), TaskID: got.TaskID}, nil
}

func (s oncallSvc) SendTestEvent(ctx context.Context, caller domain.Caller, req SendTestEventReq) (SendTestEventResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "send_test_event", domain.GrantSubject{}, false)
	if err != nil {
		return SendTestEventResp{}, err
	}
	if err := required("integrationId", req.IntegrationID); err != nil {
		return SendTestEventResp{}, err
	}
	if err := maxChars("title", req.Title, maxTitleChars); err != nil {
		return SendTestEventResp{}, err
	}
	if req.Severity != "" {
		if err := checkLabel("severity", req.Severity); err != nil {
			return SendTestEventResp{}, err
		}
	}
	integration, err := s.c.store.GetIntegration(ctx, req.ProjectID, req.IntegrationID)
	if err != nil {
		return SendTestEventResp{}, s.c.mapErr("send_test_event", err)
	}
	now := s.c.now()
	ev := oncall.Event{
		Source: integration.Source, Fingerprint: "test-" + strconv.FormatInt(now.UnixMilli(), 10), Status: oncall.StatusTest,
		Severity: req.Severity, Title: req.Title,
		Description: "Synthetic event from send_test_event: it exercises the ingest path end to end. Close this ticket once the integration is confirmed.",
		Labels:      map[string]string{"test": "true", "integration": integration.Name}, Annotations: map[string]string{}, Links: []string{},
		OccurredAt: isoMs(now.UnixMilli()),
	}
	if ev.Severity == "" {
		ev.Severity = "info"
	}
	if ev.Title == "" {
		ev.Title = "Test event from " + integration.Name
	}
	deliveryID := "test:" + integration.ID + ":" + strconv.FormatInt(now.UnixMilli(), 10)
	got, err := s.c.ingest(ctx, caller, "send_test_event", a, integration, ev, deliveryID)
	if err != nil {
		return SendTestEventResp{}, err
	}
	return SendTestEventResp{DeliveryID: deliveryID, Doorbell: got.Doorbell, Result: string(got.Result), TaskID: got.TaskID}, nil
}

// Ingested is what ingesting one event did: the delivery row's result and ticket, whether the
// delivery id was already recorded (nothing re-ran), and the doorbell rung (assigned, broadcast or
// none).
type Ingested struct {
	Result   store.DeliveryResult
	TaskID   *string
	Replayed bool
	Doorbell string
}

// defaultSeverityMap is conductor's DEFAULT_SEVERITY_MAP (incident.ts:24-39).
var defaultSeverityMap = map[string]domain.Priority{
	"critical": domain.PriorityUrgent, "fatal": domain.PriorityUrgent, "disaster": domain.PriorityUrgent, "emergency": domain.PriorityUrgent,
	"error": domain.PriorityHigh, "high": domain.PriorityHigh, "major": domain.PriorityHigh,
	"warning": domain.PriorityNormal, "warn": domain.PriorityNormal, "normal": domain.PriorityNormal, "minor": domain.PriorityNormal,
	"info": domain.PriorityLow, "low": domain.PriorityLow, "none": domain.PriorityLow,
}

func severityPriority(severity string, m map[string]domain.Priority) domain.Priority {
	key := strings.ToLower(strings.TrimSpace(severity))
	for _, k := range []string{key, severity} {
		if p, ok := m[k]; ok {
			return p
		}
	}
	if p, ok := defaultSeverityMap[key]; ok {
		return p
	}
	return domain.PriorityNormal
}

// incidentAssignee is who routing hands a new incident to (conductor resolveIncidentAssignee):
// oncall's on-call agent, upstream's mapped source assignee, broadcast nobody.
func incidentAssignee(r IntegrationRouting, ev oncall.Event) string {
	switch r.Mode {
	case "oncall":
		if r.OnCallAgent.Set && !r.OnCallAgent.Null {
			return r.OnCallAgent.V
		}
	case "upstream":
		if ev.UpstreamAssignee != "" {
			return r.PDUserMap[ev.UpstreamAssignee]
		}
	}
	return ""
}

func incidentTitle(ev oncall.Event, m IntegrationMapping) string {
	title := ev.Title
	if m.TitleTemplate.Set && !m.TitleTemplate.Null && m.TitleTemplate.V != "" {
		title = strings.NewReplacer("{title}", ev.Title, "{severity}", ev.Severity, "{source}", ev.Source, "{fingerprint}", ev.Fingerprint).Replace(m.TitleTemplate.V)
	}
	if r := []rune(title); len(r) > maxTitleChars {
		title = string(r[:maxTitleChars])
	}
	return title
}

// ingest files one verified, normalized event against integration under deliveryID, in one
// transaction: a delivery id already recorded answers its recorded outcome and changes nothing;
// otherwise oncall.Decide picks the action, the action's writes land, and the delivery row and the
// incident.delivery event record it. Correlation is not built yet: every event is decided as if no
// incident is open, so only the create and record arms run here — R2.9 adds append, annotate and
// close over alert_states, and the rate check. A created incident lands TODO on the integration's
// column (or the board's first TODO column), labelled incident (and test for a test event), and
// rings its routed assignee assigned, else the project broadcast.
func (c *Core) ingest(ctx context.Context, caller domain.Caller, tool string, a access, integration store.Integration, ev oncall.Event, deliveryID string) (Ingested, error) {
	var mapping IntegrationMapping
	var routing IntegrationRouting
	if err := json.Unmarshal(integration.Mapping, &mapping); err != nil {
		return Ingested{}, c.mapErr(tool, fmt.Errorf("integration %s mapping: %w", integration.ID, err))
	}
	if err := json.Unmarshal(integration.Routing, &routing); err != nil {
		return Ingested{}, c.mapErr(tool, fmt.Errorf("integration %s routing: %w", integration.ID, err))
	}
	event, err := json.Marshal(ev)
	if err != nil {
		return Ingested{}, c.mapErr(tool, err)
	}
	kind := store.DeliveryFiring
	switch ev.Status {
	case oncall.StatusTest:
		kind = store.DeliveryTest
	case oncall.StatusResolved:
		kind = store.DeliveryResolved
	}
	actor := IngestActor(integration.ID)
	return mutate(ctx, c, caller, mutation{
		Tool: tool, Args: map[string]any{"projectId": integration.ProjectID, "integrationId": integration.ID},
		ProjectID: integration.ProjectID, Access: a,
	}, func(t *txn) (Ingested, error) {
		if prior, err := t.Tx.GetDelivery(ctx, integration.ProjectID, integration.ID, deliveryID); err == nil {
			return Ingested{Result: prior.Result, TaskID: prior.TaskID, Replayed: true, Doorbell: "none"}, nil
		} else if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeNotFound {
			return Ingested{}, err
		}
		out := Ingested{Result: store.ResultRecorded, Doorbell: "none"}
		if oncall.Decide(integration.Lifecycle, ev.Status, false, false) == oncall.ActionCreate {
			task, err := c.fileIncident(ctx, t, integration, mapping, routing, ev, actor)
			if err != nil {
				return Ingested{}, err
			}
			out.Result, out.TaskID = store.ResultCreated, &task.ID
			for _, b := range t.bells {
				out.Doorbell = b.Kind
			}
		}
		ts := t.Now.UnixMilli()
		if _, _, err := t.Tx.InsertDelivery(ctx, store.Delivery{
			ProjectID: integration.ProjectID, IntegrationID: integration.ID, DeliveryID: deliveryID, TS: ts, Kind: kind,
			Result: out.Result, TaskID: out.TaskID, NormalizedEvent: event, ExpiresAt: t.Now.Add(DeliveryRetention).UnixMilli(),
		}); err != nil {
			return Ingested{}, err
		}
		data, err := marshalObject(map[string]any{"integrationId": integration.ID, "deliveryId": deliveryID, "result": out.Result, "kind": kind})
		if err != nil {
			return Ingested{}, err
		}
		if out.TaskID != nil {
			t.Event(store.EventInput{TaskID: *out.TaskID, BoardID: integration.BoardID, Actor: actor, Type: domain.EventIncidentDelivery, Data: data})
		} else {
			t.BoardEvent(store.EventInput{BoardID: integration.BoardID, Actor: actor, Type: domain.EventIncidentDelivery, Data: data})
		}
		return out, nil
	})
}

// fileIncident creates the incident ticket for ev on the integration's target and rings it.
func (c *Core) fileIncident(ctx context.Context, t *txn, integration store.Integration, mapping IntegrationMapping, routing IntegrationRouting, ev oncall.Event, actor string) (store.Task, error) {
	board, _, err := t.Tx.GetBoard(ctx, integration.ProjectID, integration.BoardID)
	if err != nil {
		return store.Task{}, err
	}
	var col *store.Column
	for i := range board.Columns {
		if integration.ColumnID == nil && board.Columns[i].Status == domain.Todo || integration.ColumnID != nil && board.Columns[i].ID == *integration.ColumnID {
			col = &board.Columns[i]
			break
		}
	}
	if col == nil {
		return store.Task{}, domain.Validation("The integration's board has no TODO column / its column is gone", map[string]any{"boardId": board.ID})
	}
	if col.Status != domain.Todo {
		return store.Task{}, domain.Validation(fmt.Sprintf("An incident is filed in a TODO column; column %s is %s", col.ID, col.Status), map[string]any{"columnId": col.ID, "status": col.Status})
	}
	order, err := headOfColumn(ctx, t.Tx, integration.ProjectID, board.ID, col.ID)
	if err != nil {
		return store.Task{}, err
	}
	labels := []string{"incident"}
	for _, l := range append(append([]string{}, mapping.Labels...), map[bool]string{true: "test"}[ev.Status == oncall.StatusTest]) {
		if l != "" && !inValues(l, labels) && len(labels) < 32 {
			labels = append(labels, l)
		}
	}
	fields, err := marshalObject(map[string]any{
		"incident.fingerprint": ev.Fingerprint, "incident.source": ev.Source, "incident.integrationId": integration.ID,
		"incident.occurrences": 1, "incident.alertStatus": map[bool]string{true: "resolved", false: "firing"}[ev.Status == oncall.StatusResolved],
	})
	if err != nil {
		return store.Task{}, err
	}
	var assignee *string
	if who := incidentAssignee(routing, ev); who != "" {
		assignee = &who
	}
	task, err := t.Tx.CreateTask(ctx, store.CreateTaskInput{
		ProjectID: integration.ProjectID, BoardID: board.ID, ColumnID: col.ID, Title: incidentTitle(ev, mapping),
		Description: ev.Description, Status: col.Status, Priority: severityPriority(ev.Severity, mapping.SeverityMap),
		Assignee: assignee, Labels: labels, OrderKey: order, CustomFields: fields, CreatedBy: actor, CreatedAt: t.Now.UnixMilli(),
	})
	if err != nil {
		return store.Task{}, err
	}
	data, err := marshalObject(map[string]any{"title": task.Title, "incident": true, "integrationId": integration.ID})
	if err != nil {
		return store.Task{}, err
	}
	t.Event(store.EventInput{TaskID: task.ID, BoardID: task.BoardID, Actor: actor, Type: domain.EventTaskCreated, Data: data})
	t.Wrote(task)
	if assignee != nil {
		t.RingAssigned(*assignee, task)
	} else {
		t.RingFor(nil, task, false)
	}
	return task, nil
}
