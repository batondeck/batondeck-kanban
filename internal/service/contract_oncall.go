package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Oncall is the on-call integrations and their delivery log (conductor src/tools/integrations.ts,
// deliveries.ts). Every method is workspace-admin only (ADR-0004: hidden from a non-admin's
// tools/list and FORBIDDEN by name) and then tiered on the project. A secret leaves the process
// once, unsealed, on CreateIntegration's, UpdateIntegration's (an auth switch to hmac) or
// RotateIntegrationSecret's answer, and never on a listing.
type Oncall interface {
	// CreateIntegration is create_integration.
	// Tier: structural. Event: integration.created (board). Audit: integration.created. Doorbell:
	// none. Replays on idempotencyKey.
	CreateIntegration(ctx context.Context, caller domain.Caller, req CreateIntegrationReq) (CreateIntegrationResp, error)

	// UpdateIntegration is update_integration.
	// Tier: structural. Event: integration.updated (board). Audit: integration.updated. Doorbell: none.
	UpdateIntegration(ctx context.Context, caller domain.Caller, req UpdateIntegrationReq) (UpdateIntegrationResp, error)

	// DeleteIntegration is delete_integration: a tombstone, its delivery log kept.
	// Tier: structural. Event: integration.deleted (board). Audit: integration.deleted. Doorbell: none.
	DeleteIntegration(ctx context.Context, caller domain.Caller, req DeleteIntegrationReq) (DeleteIntegrationResp, error)

	// ListIntegrations is list_integrations: auth redacted to its shape.
	// Tier: master. Writes nothing.
	ListIntegrations(ctx context.Context, caller domain.Caller, req ListIntegrationsReq) (ListIntegrationsResp, error)

	// RotateIntegrationSecret is rotate_integration_secret: the previous secret verifies for
	// oncall.RotationGrace unless revokePrevious.
	// Tier: structural. Event: integration.secret_rotated (board). Audit: integration.secret_rotated.
	// Doorbell: none.
	RotateIntegrationSecret(ctx context.Context, caller domain.Caller, req RotateIntegrationSecretReq) (RotateIntegrationSecretResp, error)

	// ListDeliveries is list_deliveries.
	// Tier: master. Writes nothing.
	ListDeliveries(ctx context.Context, caller domain.Caller, req ListDeliveriesReq) (ListDeliveriesResp, error)

	// ReadDeliveryRaw is read_delivery_raw: a kept raw body, base64.
	// Tier: master. Writes nothing.
	ReadDeliveryRaw(ctx context.Context, caller domain.Caller, req ReadDeliveryRawReq) (ReadDeliveryRawResp, error)

	// ReplayDelivery is replay_delivery: the stored normalized event, ingested again under a new
	// delivery id.
	// Tier: master. Event: incident.delivery, and task.created when it files a ticket. Audit: none.
	// Doorbell: the ticket's (assigned to the on-call identity, else broadcast).
	ReplayDelivery(ctx context.Context, caller domain.Caller, req ReplayDeliveryReq) (ReplayDeliveryResp, error)

	// SendTestEvent is send_test_event: a synthetic test event ingested, filing a ticket labelled test.
	// Tier: master. Event: task.created, incident.delivery. Audit: none. Doorbell: the ticket's.
	SendTestEvent(ctx context.Context, caller domain.Caller, req SendTestEventReq) (SendTestEventResp, error)
}

// CreateIntegrationReq is create_integration's input.
type CreateIntegrationReq struct {
	Auth           IntegrationAuthInput `json:"auth"`
	BoardID        string               `json:"boardId"`
	Name           string               `json:"name"`
	ProjectID      string               `json:"projectId"`
	Routing        IntegrationRouting   `json:"routing"`
	Source         IntegrationSource    `json:"source"`
	ColumnID       string               `json:"columnId,omitzero"`
	IdempotencyKey string               `json:"idempotencyKey,omitzero"`
	Lifecycle      IntegrationLifecycle `json:"lifecycle,omitzero"`
	Limits         IntegrationLimitsIn  `json:"limits,omitzero"`
	Mapping        IntegrationMapping   `json:"mapping,omitzero"`
}

// CreateIntegrationResp is create_integration's output: Secret, with ShownOnce, only when the
// server minted it.
type CreateIntegrationResp struct {
	Integration Integration `json:"integration"`
	Secret      string      `json:"secret,omitzero"`
	ShownOnce   True        `json:"shownOnce,omitzero"`
}

// UpdateIntegrationReq is update_integration's input.
type UpdateIntegrationReq struct {
	IntegrationID string           `json:"integrationId"`
	Patch         IntegrationPatch `json:"patch"`
	ProjectID     string           `json:"projectId"`
	Version       int64            `json:"version"`
}

// IntegrationPatch is update_integration's patch: each present field replaces the stored one.
type IntegrationPatch struct {
	Auth      IntegrationAuthInput `json:"auth,omitzero"`
	BoardID   string               `json:"boardId,omitzero"`
	ColumnID  Nullable[string]     `json:"columnId,omitzero"`
	Enabled   Optional[bool]       `json:"enabled,omitzero"`
	Lifecycle IntegrationLifecycle `json:"lifecycle,omitzero"`
	Limits    IntegrationLimitsIn  `json:"limits,omitzero"`
	Mapping   IntegrationMapping   `json:"mapping,omitzero"`
	Name      string               `json:"name,omitzero"`
	Routing   IntegrationRouting   `json:"routing,omitzero"`
}

// UpdateIntegrationResp is update_integration's output.
type UpdateIntegrationResp struct {
	Integration Integration `json:"integration"`
	Secret      string      `json:"secret,omitzero"`
	ShownOnce   True        `json:"shownOnce,omitzero"`
}

// DeleteIntegrationReq is delete_integration's input.
type DeleteIntegrationReq struct {
	IntegrationID string `json:"integrationId"`
	ProjectID     string `json:"projectId"`
	Version       int64  `json:"version"`
}

// DeleteIntegrationResp is delete_integration's output.
type DeleteIntegrationResp struct {
	Deleted     True        `json:"deleted"`
	Integration Integration `json:"integration"`
}

// ListIntegrationsReq is list_integrations's input.
type ListIntegrationsReq struct {
	ProjectID      string `json:"projectId"`
	IncludeDeleted bool   `json:"includeDeleted,omitzero"`
	Limit          int64  `json:"limit,omitzero"`
}

// ListIntegrationsResp is list_integrations's output.
type ListIntegrationsResp struct {
	Integrations []Integration `json:"integrations"`
}

// RotateIntegrationSecretReq is rotate_integration_secret's input.
type RotateIntegrationSecretReq struct {
	IntegrationID  string `json:"integrationId"`
	ProjectID      string `json:"projectId"`
	Version        int64  `json:"version"`
	ExternalSecret string `json:"externalSecret,omitzero"`
	RevokePrevious bool   `json:"revokePrevious,omitzero"`
}

// RotateIntegrationSecretResp is rotate_integration_secret's output.
type RotateIntegrationSecretResp struct {
	Integration Integration `json:"integration"`
	Secret      string      `json:"secret,omitzero"`
	ShownOnce   True        `json:"shownOnce,omitzero"`
}

// ListDeliveriesReq is list_deliveries's input.
type ListDeliveriesReq struct {
	IntegrationID string       `json:"integrationId"`
	ProjectID     string       `json:"projectId"`
	Cursor        string       `json:"cursor,omitzero"`
	Kind          DeliveryKind `json:"kind,omitzero"`
	Limit         int64        `json:"limit,omitzero"`
}

// ListDeliveriesResp is list_deliveries's output; NextCursor present means more rows exist.
type ListDeliveriesResp struct {
	Deliveries []Delivery `json:"deliveries"`
	NextCursor string     `json:"nextCursor,omitzero"`
}

// ReadDeliveryRawReq is read_delivery_raw's input.
type ReadDeliveryRawReq struct {
	DeliveryID    string `json:"deliveryId"`
	IntegrationID string `json:"integrationId"`
	ProjectID     string `json:"projectId"`
}

// ReadDeliveryRawResp is read_delivery_raw's output.
type ReadDeliveryRawResp struct {
	ContentType       RawContentType `json:"contentType"`
	DeliveryID        string         `json:"deliveryId"`
	PayloadBase64     *string        `json:"payloadBase64"`
	SuggestedFilename string         `json:"suggestedFilename"`
}

// ReplayDeliveryReq is replay_delivery's input.
type ReplayDeliveryReq struct {
	DeliveryID    string `json:"deliveryId"`
	IntegrationID string `json:"integrationId"`
	ProjectID     string `json:"projectId"`
}

// ReplayDeliveryResp is replay_delivery's output.
type ReplayDeliveryResp struct {
	NewDeliveryID string  `json:"newDeliveryId"`
	Replayed      bool    `json:"replayed"`
	Result        string  `json:"result"`
	TaskID        *string `json:"taskId,omitzero"`
}

// SendTestEventReq is send_test_event's input.
type SendTestEventReq struct {
	IntegrationID string `json:"integrationId"`
	ProjectID     string `json:"projectId"`
	Severity      string `json:"severity,omitzero"`
	Title         string `json:"title,omitzero"`
}

// SendTestEventResp is send_test_event's output: Doorbell is assigned, broadcast or none.
type SendTestEventResp struct {
	DeliveryID string  `json:"deliveryId"`
	Doorbell   string  `json:"doorbell"`
	Result     string  `json:"result"`
	TaskID     *string `json:"taskId,omitzero"`
}

// IntegrationAuthInput is the auth a caller may set: the mode, and the material that mode takes
// from the caller (an hmac secret is minted unless the source issued its own).
type IntegrationAuthInput struct {
	Mode           IntegrationAuthMode `json:"mode"`
	APIKey         string              `json:"apiKey,omitzero"`
	ExternalSecret string              `json:"externalSecret,omitzero"`
	JWT            IntegrationJWT      `json:"jwt,omitzero"`
}

// IntegrationJWT is a jwt integration's issuer, audience and key set, and the optional pins on
// who the token names: Subject is matched against sub exactly, Email against email exactly with
// email_verified true. On a shared issuer (Google, GitHub Actions) anyone can mint a token for a
// chosen audience, so only a pin identifies the sender.
type IntegrationJWT struct {
	Audience string `json:"audience"`
	Email    string `json:"email,omitzero"`
	Issuer   string `json:"issuer"`
	JWKSURL  string `json:"jwksUrl"`
	Subject  string `json:"subject,omitzero"`
}

// IntegrationRouting is who an incident is routed to. A stored routing that still carries the
// retired fallbackBroadcast key reads without it (P-310, owner decision 6: nothing read it).
type IntegrationRouting struct {
	Mode        RoutingMode       `json:"mode"`
	OnCallAgent Nullable[string]  `json:"onCallAgent,omitzero"`
	PDUserMap   map[string]string `json:"pdUserMap,omitzero"`
}

// IntegrationLimitsIn is the limits a caller may set; an absent one keeps its default (or, on an
// update, its stored value).
type IntegrationLimitsIn struct {
	PayloadCapKb int64 `json:"payloadCapKb,omitzero"`
	RatePerMin   int64 `json:"ratePerMin,omitzero"`
}

// IntegrationMapping is how an event becomes a ticket; extract, statusMap and defaults are the
// webhook source's alone.
type IntegrationMapping struct {
	AnnotationAllowlist []string                   `json:"annotationAllowlist,omitzero"`
	Defaults            MappingDefaults            `json:"defaults,omitzero"`
	Extract             MappingExtract             `json:"extract,omitzero"`
	Labels              []string                   `json:"labels,omitzero"`
	SeverityMap         map[string]domain.Priority `json:"severityMap,omitzero"`
	StatusMap           map[string]AlertStatus     `json:"statusMap,omitzero"`
	TitleTemplate       Nullable[string]           `json:"titleTemplate,omitzero"`
}

// MappingDefaults are the webhook source's fallbacks for a field its payload lacks.
type MappingDefaults struct {
	Severity string      `json:"severity,omitzero"`
	Status   AlertStatus `json:"status,omitzero"`
	Title    string      `json:"title,omitzero"`
}

// MappingExtract are the webhook source's dotted paths to each field.
type MappingExtract struct {
	Annotations string   `json:"annotations,omitzero"`
	DedupKey    []string `json:"dedupKey,omitzero"`
	Description []string `json:"description,omitzero"`
	Events      string   `json:"events,omitzero"`
	Fingerprint []string `json:"fingerprint,omitzero"`
	Labels      string   `json:"labels,omitzero"`
	Links       []string `json:"links,omitzero"`
	OccurredAt  []string `json:"occurredAt,omitzero"`
	Severity    []string `json:"severity,omitzero"`
	Status      []string `json:"status,omitzero"`
	Title       []string `json:"title,omitzero"`
}

// Integration is an integration as a caller sees it: auth redacted to its shape, never a secret,
// key or hash. Policy is the stored policy object, read by nothing: no input sets it since P-310
// (owner decision 6), so a new row's is {} and an older row's keeps the keys it was saved with.
type Integration struct {
	Auth         IntegrationAuthView  `json:"auth"`
	CreatedAt    string               `json:"createdAt"`
	CreatedBy    string               `json:"createdBy"`
	DeletedAt    string               `json:"deletedAt,omitzero"`
	Enabled      bool                 `json:"enabled"`
	EndpointPath string               `json:"endpointPath"`
	ID           string               `json:"id"`
	Lifecycle    IntegrationLifecycle `json:"lifecycle"`
	Limits       IntegrationLimits    `json:"limits"`
	Mapping      JSONObject           `json:"mapping"`
	Name         string               `json:"name"`
	Policy       JSONObject           `json:"policy"`
	ProjectID    string               `json:"projectId"`
	Routing      JSONObject           `json:"routing"`
	Source       IntegrationSource    `json:"source"`
	Target       IntegrationTarget    `json:"target"`
	UpdatedAt    string               `json:"updatedAt"`
	Version      float64              `json:"version"`
}

// IntegrationAuthView is auth's shape: the mode, when it rotated, whether the source issued the
// secret, and until when a rotated-out secret still verifies.
type IntegrationAuthView struct {
	Mode             IntegrationAuthMode `json:"mode"`
	DualAcceptUntil  *string             `json:"dualAcceptUntil,omitzero"`
	ExternallyIssued bool                `json:"externallyIssued,omitzero"`
	RotatedAt        string              `json:"rotatedAt,omitzero"`
}

// IntegrationLimits are an integration's effective limits.
type IntegrationLimits struct {
	PayloadCapKb float64 `json:"payloadCapKb"`
	RatePerMin   float64 `json:"ratePerMin"`
}

// IntegrationTarget is the board, and optionally the column, incidents are filed on.
type IntegrationTarget struct {
	BoardID  string  `json:"boardId"`
	ColumnID *string `json:"columnId,omitzero"`
}

// Delivery is one row of an integration's delivery log.
type Delivery struct {
	DeliveryID  string       `json:"deliveryId"`
	Kind        DeliveryKind `json:"kind"`
	Result      string       `json:"result"`
	Ts          string       `json:"ts"`
	Error       *string      `json:"error,omitzero"`
	Fingerprint *string      `json:"fingerprint,omitzero"`
	Severity    *string      `json:"severity,omitzero"`
	TaskID      *string      `json:"taskId,omitzero"`
	Title       *string      `json:"title,omitzero"`
}

// IntegrationSource is an integration's telemetry source (oncall.Sources).
type IntegrationSource string

// IntegrationSourceValues are IntegrationSource's values, in the schema's order.
var IntegrationSourceValues = []IntegrationSource{"alertmanager", "datadog", "dynatrace", "gcp", "generic", "grafana", "pagerduty", "webhook"}

// IntegrationAuthMode is an integration's auth mode.
type IntegrationAuthMode string

// IntegrationAuthModeValues are IntegrationAuthMode's values, in the schema's order.
var IntegrationAuthModeValues = []IntegrationAuthMode{"api_key", "basic", "hmac", "jwt", "mtls"}

// IntegrationLifecycle is what an integration does with a correlated event.
type IntegrationLifecycle string

// IntegrationLifecycleValues are IntegrationLifecycle's values, in the schema's order.
var IntegrationLifecycleValues = []IntegrationLifecycle{"autoclose", "correlate", "per_event"}

// RoutingMode is who an integration routes an incident to.
type RoutingMode string

// RoutingModeValues are RoutingMode's values, in the schema's order.
var RoutingModeValues = []RoutingMode{"broadcast", "oncall", "upstream"}

// AlertStatus is a normalized event's status.
type AlertStatus string

// AlertStatusValues are AlertStatus's values, in the schema's order.
var AlertStatusValues = []AlertStatus{"firing", "resolved", "test"}

// DeliveryKind is what a delivery carried.
type DeliveryKind string

// DeliveryKindValues are DeliveryKind's values, in the schema's order.
var DeliveryKindValues = []DeliveryKind{"firing", "rejected", "resolved", "test"}

// JSONObject is a JSON object of any members (conductor's z.record(z.string(), z.unknown())).
type JSONObject map[string]any

// RawContentType is read_delivery_raw's one content type.
type RawContentType string

// RawContentTypeValues are RawContentType's values.
var RawContentTypeValues = []RawContentType{"application/octet-stream"}
