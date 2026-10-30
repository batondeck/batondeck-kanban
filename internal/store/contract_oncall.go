package store

import (
	"context"
	"encoding/json"
)

// Integration is one on-call integrations row. Auth is the oncall.Auth block as JSON, its secrets
// already sealed or hashed (the service seals before it gets here; the store never sees a
// plaintext). Mapping, Routing, Policy and Limits are JSON objects. Times are Unix milliseconds;
// DeletedAt is set on a tombstone.
type Integration struct {
	ProjectID string
	ID        string
	Name      string
	Source    string
	Enabled   bool
	BoardID   string
	ColumnID  *string
	Auth      json.RawMessage
	Mapping   json.RawMessage
	Routing   json.RawMessage
	Lifecycle string
	Policy    json.RawMessage
	Limits    json.RawMessage
	Version   int64
	CreatedBy string
	CreatedAt int64
	UpdatedAt int64
	DeletedAt *int64
}

// CreateIntegrationInput is an integration to create, enabled, at version 1. ID is the caller's:
// unguessable and unique on its own (POST /hooks/{id} resolves the project from it), and the AAD
// its sealed secrets are bound to, so it exists before the row does. Mapping and Policy nil are {}.
type CreateIntegrationInput struct {
	ProjectID string
	ID        string
	Name      string
	Source    string
	BoardID   string
	ColumnID  *string
	Auth      json.RawMessage
	Mapping   json.RawMessage
	Routing   json.RawMessage
	Lifecycle string
	Policy    json.RawMessage
	Limits    json.RawMessage
	CreatedBy string
	CreatedAt int64
}

// IntegrationPatch changes the named fields; a nil field is unchanged. UpdatedAt is required.
type IntegrationPatch struct {
	Name      *string
	Enabled   *bool
	BoardID   *string
	ColumnID  *Nullable[string]
	Auth      json.RawMessage
	Mapping   json.RawMessage
	Routing   json.RawMessage
	Lifecycle *string
	Policy    json.RawMessage
	Limits    json.RawMessage
	UpdatedAt int64
}

// DeliveryKind is what a delivery carried.
type DeliveryKind string

const (
	DeliveryFiring   DeliveryKind = "firing"
	DeliveryResolved DeliveryKind = "resolved"
	DeliveryTest     DeliveryKind = "test"
	DeliveryRejected DeliveryKind = "rejected"
)

// DeliveryResult is what a delivery did: the result vocabulary of conductor's ingest.
type DeliveryResult string

const (
	ResultCreated     DeliveryResult = "created"
	ResultAppended    DeliveryResult = "appended"
	ResultAnnotated   DeliveryResult = "annotated"
	ResultClosed      DeliveryResult = "closed"
	ResultRecorded    DeliveryResult = "recorded"
	ResultRejected    DeliveryResult = "rejected"
	ResultRateLimited DeliveryResult = "rate_limited"
	ResultCoalesced   DeliveryResult = "coalesced"
)

// Delivery is one deliveries row. DeliveryID is the source's (or the server's) delivery id, the
// row's key its hex sha256 (DeliveryKey). TaskID may name a task since deleted (autoclose).
// NormalizedEvent is the verified, normalized event as JSON, nil for a refusal parsed no further.
type Delivery struct {
	ProjectID       string
	IntegrationID   string
	DeliveryID      string
	TS              int64
	Kind            DeliveryKind
	Result          DeliveryResult
	TaskID          *string
	Error           *string
	NormalizedEvent json.RawMessage
	ExpiresAt       int64
}

// AlertState is a fingerprint's correlation to its incident ticket (an alert_states row): the
// ticket TaskID may since have been deleted (autoclose) or moved to DONE, so a reader checks it.
// AlertStatus is firing or resolved. Times are Unix milliseconds; ExpiresAt 0 is none.
type AlertState struct {
	ProjectID     string
	IntegrationID string
	Fingerprint   string
	TaskID        string
	Occurrences   int64
	FirstAt       int64
	LastAt        int64
	AlertStatus   string
	ExpiresAt     int64
}

// DeliveryFilter narrows ListDeliveries: Kind "" is every kind.
type DeliveryFilter struct {
	Kind DeliveryKind
}

// DeliveryPage is one page, newest first; NextCursor is "" on the last page.
type DeliveryPage struct {
	Deliveries []Delivery
	NextCursor string
}

// Oncall is the on-call area: integrations, their deliveries and the raw bodies kept for
// diagnosis. Keys: integrations (project_id, id), deliveries (project_id, integration_id,
// sha256(delivery_id)), raw bodies (project_id, integration_id, sha256(delivery_id)). Errors: a
// missing project is domain.NotFound("Project", id); a missing or tombstoned integration
// domain.NotFound("Integration", id); a missing alert state domain.NotFound("AlertState",
// fingerprint); a missing delivery domain.NotFound("Delivery", id); a missing
// raw body domain.NotFound("DeliveryRawBody", id); an expected version that is not the row's
// domain.Stale; an unknown lifecycle, kind or result, an empty name, board, creator or id,
// a field that is not a JSON object, a malformed cursor and a non-positive sweep limit
// domain.Validation. The store checks shapes, not meaning: what a source, a mapping or an auth
// block may hold is the service's.
type Oncall interface {
	// CreateIntegration inserts the integration, enabled, at version 1.
	CreateIntegration(ctx context.Context, in CreateIntegrationInput) (Integration, error)
	// GetIntegration is the live integration (not a tombstone).
	GetIntegration(ctx context.Context, projectID, integrationID string) (Integration, error)
	// GetIntegrationByID is the live integration by its id alone: the one lookup not scoped by
	// project, for POST /hooks/{id}, whose id is unguessable and unique on its own.
	GetIntegrationByID(ctx context.Context, integrationID string) (Integration, error)
	// ListIntegrations is the project's integrations newest first (created_at, then id), tombstones
	// only when includeDeleted. limit <= 0 is 100 and a limit above 200 is 200.
	ListIntegrations(ctx context.Context, projectID string, limit int, includeDeleted bool) ([]Integration, error)
	// UpdateIntegration applies p to the live integration at expectedVersion, bumps its version and
	// returns it after.
	UpdateIntegration(ctx context.Context, projectID, integrationID string, expectedVersion int64, p IntegrationPatch) (Integration, error)
	// DeleteIntegration tombstones the live integration at expectedVersion (deleted_at = at, the
	// version bumped) and returns it after; its deliveries stay. A second delete is NotFound.
	DeleteIntegration(ctx context.Context, projectID, integrationID string, expectedVersion, at int64) (Integration, error)

	// GetAlertState is the fingerprint's correlation row (keyed by its sha256, DeliveryKey).
	GetAlertState(ctx context.Context, projectID, integrationID, fingerprint string) (AlertState, error)
	// PutAlertState writes a's row whole, inserting or replacing it. The integration must exist (a
	// tombstone counts); an empty fingerprint or task, a status that is not firing or resolved and a
	// non-positive time are VALIDATION.
	PutAlertState(ctx context.Context, a AlertState) error

	// InsertDelivery records d; inserted is false, and the row the one already recorded, when its
	// delivery id was recorded before (a replayed delivery changes nothing). The integration must
	// exist (a tombstone counts).
	InsertDelivery(ctx context.Context, d Delivery) (row Delivery, inserted bool, err error)
	// GetDelivery is one delivery by its delivery id.
	GetDelivery(ctx context.Context, projectID, integrationID, deliveryID string) (Delivery, error)
	// ListDeliveries is one page of the integration's deliveries matching f, newest first (ts, then
	// key); a cursor is base64url JSON {order, id}. limit <= 0 is 50 and a limit above 200 is 200.
	ListDeliveries(ctx context.Context, projectID, integrationID string, f DeliveryFilter, cursor string, limit int) (DeliveryPage, error)
	// PutDeliveryRawBody keeps body for the recorded delivery until expiresAt, replacing one kept.
	PutDeliveryRawBody(ctx context.Context, projectID, integrationID, deliveryID string, body []byte, expiresAt int64) error
	// GetDeliveryRawBody is the delivery's kept raw body.
	GetDeliveryRawBody(ctx context.Context, projectID, integrationID, deliveryID string) ([]byte, error)
	// DeleteExpiredDeliveries deletes at most limit deliveries expired at or before now, oldest
	// expiry first, each with its raw body, and returns how many (the retention sweep's).
	DeleteExpiredDeliveries(ctx context.Context, now int64, limit int) (int64, error)
	// DeleteExpiredDeliveryRawBodies deletes at most limit raw bodies expired at or before now,
	// oldest expiry first, leaving their deliveries, and returns how many.
	DeleteExpiredDeliveryRawBodies(ctx context.Context, now int64, limit int) (int64, error)
	// DeleteExpiredAlertStates deletes at most limit alert states expired at or before now whose
	// ticket is gone or DONE, oldest expiry first, and returns how many (the retention sweep's). An
	// expired row of an open ticket stays, since the next firing appends to that ticket through it,
	// and so does a row with no expiry. A DONE ticket reopened after its row went is no longer
	// correlated: the next firing files a new ticket, and a resolved event is recorded.
	DeleteExpiredAlertStates(ctx context.Context, now int64, limit int) (int64, error)
}

var (
	_ Oncall = (*SQLite)(nil)
	_ Oncall = (*Postgres)(nil)
)
