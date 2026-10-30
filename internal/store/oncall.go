package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The on-call area of contract_oncall.go, on both engines. A create and an insert are one
// statement each, their missing parent the foreign key's refusal; an update is one compare-and-set
// UPDATE … RETURNING over the merged row, read first to merge and, when it matches no row, to tell
// a stale version from a missing row; a page is one SELECT newest first.

type oncallQueries interface {
	InsertIntegration(ctx context.Context, arg sqlitedb.InsertIntegrationParams) (sqlitedb.Integration, error)
	GetIntegration(ctx context.Context, arg sqlitedb.GetIntegrationParams) (sqlitedb.Integration, error)
	GetIntegrationByID(ctx context.Context, id string) (sqlitedb.Integration, error)
	GetAlertState(ctx context.Context, arg sqlitedb.GetAlertStateParams) (sqlitedb.AlertState, error)
	PutAlertState(ctx context.Context, arg sqlitedb.PutAlertStateParams) error
	ListIntegrations(ctx context.Context, arg sqlitedb.ListIntegrationsParams) ([]sqlitedb.Integration, error)
	UpdateIntegration(ctx context.Context, arg sqlitedb.UpdateIntegrationParams) (sqlitedb.Integration, error)
	InsertDelivery(ctx context.Context, arg sqlitedb.InsertDeliveryParams) (sqlitedb.Delivery, error)
	GetDelivery(ctx context.Context, arg sqlitedb.GetDeliveryParams) (sqlitedb.Delivery, error)
	ListDeliveries(ctx context.Context, arg sqlitedb.ListDeliveriesParams) ([]sqlitedb.Delivery, error)
	PutDeliveryRawBody(ctx context.Context, arg sqlitedb.PutDeliveryRawBodyParams) error
	GetDeliveryRawBody(ctx context.Context, arg sqlitedb.GetDeliveryRawBodyParams) (sqlitedb.DeliveryRawBody, error)
	DeleteExpiredDeliveries(ctx context.Context, arg sqlitedb.DeleteExpiredDeliveriesParams) (int64, error)
	DeleteExpiredDeliveryRawBodies(ctx context.Context, arg sqlitedb.DeleteExpiredDeliveryRawBodiesParams) (int64, error)
	DeleteExpiredAlertStates(ctx context.Context, arg sqlitedb.DeleteExpiredAlertStatesParams) (int64, error)
}

// DeliveryKey is a delivery's row key: the hex sha256 of its delivery id, so an id of any length
// and alphabet keys a row.
func DeliveryKey(deliveryID string) string {
	h := sha256.Sum256([]byte(deliveryID))
	return hex.EncodeToString(h[:])
}

var (
	lifecycles      = map[string]bool{"correlate": true, "autoclose": true, "per_event": true}
	deliveryKinds   = map[DeliveryKind]bool{DeliveryFiring: true, DeliveryResolved: true, DeliveryTest: true, DeliveryRejected: true}
	deliveryResults = map[DeliveryResult]bool{ResultCreated: true, ResultAppended: true, ResultAnnotated: true, ResultClosed: true,
		ResultRecorded: true, ResultRejected: true, ResultRateLimited: true, ResultCoalesced: true}
)

func (s *SQLite) CreateIntegration(ctx context.Context, in CreateIntegrationInput) (Integration, error) {
	return createIntegration(ctx, s, in)
}

func (s *Postgres) CreateIntegration(ctx context.Context, in CreateIntegrationInput) (Integration, error) {
	return createIntegration(ctx, s, in)
}

func (s *SQLite) GetIntegration(ctx context.Context, projectID, integrationID string) (Integration, error) {
	return getIntegration(ctx, s, projectID, integrationID)
}

func (s *Postgres) GetIntegration(ctx context.Context, projectID, integrationID string) (Integration, error) {
	return getIntegration(ctx, s, projectID, integrationID)
}

func (s *SQLite) GetIntegrationByID(ctx context.Context, integrationID string) (Integration, error) {
	return getIntegrationByID(ctx, s, integrationID)
}

func (s *Postgres) GetIntegrationByID(ctx context.Context, integrationID string) (Integration, error) {
	return getIntegrationByID(ctx, s, integrationID)
}

func (s *SQLite) GetAlertState(ctx context.Context, projectID, integrationID, fingerprint string) (AlertState, error) {
	return getAlertState(ctx, s, projectID, integrationID, fingerprint)
}

func (s *Postgres) GetAlertState(ctx context.Context, projectID, integrationID, fingerprint string) (AlertState, error) {
	return getAlertState(ctx, s, projectID, integrationID, fingerprint)
}

func (s *SQLite) PutAlertState(ctx context.Context, a AlertState) error {
	return putAlertState(ctx, s, a)
}

func (s *Postgres) PutAlertState(ctx context.Context, a AlertState) error {
	return putAlertState(ctx, s, a)
}

func (s *SQLite) ListIntegrations(ctx context.Context, projectID string, limit int, includeDeleted bool) ([]Integration, error) {
	return listIntegrations(ctx, s, projectID, limit, includeDeleted)
}

func (s *Postgres) ListIntegrations(ctx context.Context, projectID string, limit int, includeDeleted bool) ([]Integration, error) {
	return listIntegrations(ctx, s, projectID, limit, includeDeleted)
}

func (s *SQLite) UpdateIntegration(ctx context.Context, projectID, integrationID string, expectedVersion int64, p IntegrationPatch) (Integration, error) {
	return updateIntegration(ctx, s, projectID, integrationID, expectedVersion, p, 0)
}

func (s *Postgres) UpdateIntegration(ctx context.Context, projectID, integrationID string, expectedVersion int64, p IntegrationPatch) (Integration, error) {
	return updateIntegration(ctx, s, projectID, integrationID, expectedVersion, p, 0)
}

func (s *SQLite) DeleteIntegration(ctx context.Context, projectID, integrationID string, expectedVersion, at int64) (Integration, error) {
	return updateIntegration(ctx, s, projectID, integrationID, expectedVersion, IntegrationPatch{UpdatedAt: at}, at)
}

func (s *Postgres) DeleteIntegration(ctx context.Context, projectID, integrationID string, expectedVersion, at int64) (Integration, error) {
	return updateIntegration(ctx, s, projectID, integrationID, expectedVersion, IntegrationPatch{UpdatedAt: at}, at)
}

func (s *SQLite) InsertDelivery(ctx context.Context, d Delivery) (Delivery, bool, error) {
	return insertDelivery(ctx, s, d)
}

func (s *Postgres) InsertDelivery(ctx context.Context, d Delivery) (Delivery, bool, error) {
	return insertDelivery(ctx, s, d)
}

func (s *SQLite) GetDelivery(ctx context.Context, projectID, integrationID, deliveryID string) (Delivery, error) {
	return getDelivery(ctx, s, projectID, integrationID, deliveryID)
}

func (s *Postgres) GetDelivery(ctx context.Context, projectID, integrationID, deliveryID string) (Delivery, error) {
	return getDelivery(ctx, s, projectID, integrationID, deliveryID)
}

func (s *SQLite) ListDeliveries(ctx context.Context, projectID, integrationID string, f DeliveryFilter, cursor string, limit int) (DeliveryPage, error) {
	return listDeliveries(ctx, s, projectID, integrationID, f, cursor, limit)
}

func (s *Postgres) ListDeliveries(ctx context.Context, projectID, integrationID string, f DeliveryFilter, cursor string, limit int) (DeliveryPage, error) {
	return listDeliveries(ctx, s, projectID, integrationID, f, cursor, limit)
}

func (s *SQLite) PutDeliveryRawBody(ctx context.Context, projectID, integrationID, deliveryID string, body []byte, expiresAt int64) error {
	return putDeliveryRawBody(ctx, s, projectID, integrationID, deliveryID, body, expiresAt)
}

func (s *Postgres) PutDeliveryRawBody(ctx context.Context, projectID, integrationID, deliveryID string, body []byte, expiresAt int64) error {
	return putDeliveryRawBody(ctx, s, projectID, integrationID, deliveryID, body, expiresAt)
}

func (s *SQLite) GetDeliveryRawBody(ctx context.Context, projectID, integrationID, deliveryID string) ([]byte, error) {
	return getDeliveryRawBody(ctx, s, projectID, integrationID, deliveryID)
}

func (s *Postgres) GetDeliveryRawBody(ctx context.Context, projectID, integrationID, deliveryID string) ([]byte, error) {
	return getDeliveryRawBody(ctx, s, projectID, integrationID, deliveryID)
}

func (s *SQLite) DeleteExpiredDeliveries(ctx context.Context, now int64, limit int) (int64, error) {
	return deleteExpired(ctx, s, "deliveries", now, limit)
}

func (s *Postgres) DeleteExpiredDeliveries(ctx context.Context, now int64, limit int) (int64, error) {
	return deleteExpired(ctx, s, "deliveries", now, limit)
}

func (s *SQLite) DeleteExpiredDeliveryRawBodies(ctx context.Context, now int64, limit int) (int64, error) {
	return deleteExpired(ctx, s, "raw bodies", now, limit)
}

func (s *Postgres) DeleteExpiredDeliveryRawBodies(ctx context.Context, now int64, limit int) (int64, error) {
	return deleteExpired(ctx, s, "raw bodies", now, limit)
}

func (s *SQLite) DeleteExpiredAlertStates(ctx context.Context, now int64, limit int) (int64, error) {
	return deleteExpired(ctx, s, "alert states", now, limit)
}

func (s *Postgres) DeleteExpiredAlertStates(ctx context.Context, now int64, limit int) (int64, error) {
	return deleteExpired(ctx, s, "alert states", now, limit)
}

func integrationFrom(r sqlitedb.Integration) Integration {
	return Integration{
		ProjectID: r.ProjectID, ID: r.ID, Name: r.Name, Source: r.Source, Enabled: r.Enabled == 1, BoardID: r.BoardID,
		ColumnID: strPtr(r.ColumnID), Auth: json.RawMessage(r.Auth), Mapping: json.RawMessage(r.Mapping),
		Routing: json.RawMessage(r.Routing), Lifecycle: r.Lifecycle, Policy: json.RawMessage(r.Policy),
		Limits: json.RawMessage(r.Limits), Version: r.Version, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt, DeletedAt: intPtr(r.DeletedAt),
	}
}

// integrationObjects checks each named JSON field is an object; nil takes def ({} or, for a
// required field, a refusal).
func integrationObjects(fields map[string]*json.RawMessage, required map[string]bool) error {
	for name, raw := range fields {
		if *raw == nil && required[name] {
			return domain.Validation("an integration needs "+name, map[string]any{"field": name})
		}
		v, err := object(name, *raw)
		if err != nil {
			return err
		}
		*raw = v
	}
	return nil
}

func createIntegration(ctx context.Context, e engine, in CreateIntegrationInput) (Integration, error) {
	switch {
	case in.ID == "" || in.Name == "" || in.Source == "" || in.BoardID == "" || in.CreatedBy == "":
		return Integration{}, domain.Validation("an integration needs an id, a name, a source, a board and a creator", nil)
	case !lifecycles[in.Lifecycle]:
		return Integration{}, domain.Validation("unknown lifecycle: "+in.Lifecycle, map[string]any{"lifecycle": in.Lifecycle})
	}
	if err := integrationObjects(map[string]*json.RawMessage{"auth": &in.Auth, "mapping": &in.Mapping, "routing": &in.Routing,
		"policy": &in.Policy, "limits": &in.Limits}, map[string]bool{"auth": true, "routing": true, "limits": true}); err != nil {
		return Integration{}, err
	}
	r, err := e.queries(true).InsertIntegration(ctx, sqlitedb.InsertIntegrationParams{
		ProjectID: in.ProjectID, ID: in.ID, Name: in.Name, Source: in.Source, Enabled: 1, BoardID: in.BoardID,
		ColumnID: nullStr(in.ColumnID), Auth: string(in.Auth), Mapping: string(in.Mapping), Routing: string(in.Routing),
		Lifecycle: in.Lifecycle, Policy: string(in.Policy), Limits: string(in.Limits), CreatedBy: in.CreatedBy,
		CreatedAt: in.CreatedAt, UpdatedAt: in.CreatedAt,
	})
	if isForeignKeyViolation(err) {
		return Integration{}, domain.NotFound("Project", in.ProjectID)
	}
	if err != nil {
		return Integration{}, fmt.Errorf("store: create integration: %w", err)
	}
	return integrationFrom(r), nil
}

func getIntegration(ctx context.Context, e engine, projectID, integrationID string) (Integration, error) {
	r, err := e.queries(false).GetIntegration(ctx, sqlitedb.GetIntegrationParams{ProjectID: projectID, ID: integrationID})
	if err := found(err, "Integration", integrationID); err != nil {
		return Integration{}, err
	}
	return integrationFrom(r), nil
}

func getIntegrationByID(ctx context.Context, e engine, integrationID string) (Integration, error) {
	r, err := e.queries(false).GetIntegrationByID(ctx, integrationID)
	if err := found(err, "Integration", integrationID); err != nil {
		return Integration{}, err
	}
	return integrationFrom(r), nil
}

func getAlertState(ctx context.Context, e engine, projectID, integrationID, fingerprint string) (AlertState, error) {
	r, err := e.queries(false).GetAlertState(ctx, sqlitedb.GetAlertStateParams{ProjectID: projectID, IntegrationID: integrationID, FingerprintHash: DeliveryKey(fingerprint)})
	if err := found(err, "AlertState", fingerprint); err != nil {
		return AlertState{}, err
	}
	return AlertState{ProjectID: r.ProjectID, IntegrationID: r.IntegrationID, Fingerprint: r.Fingerprint, TaskID: r.TaskID,
		Occurrences: r.Occurrences, FirstAt: r.FirstAt, LastAt: r.LastAt, AlertStatus: r.AlertStatus, ExpiresAt: r.ExpiresAt.Int64}, nil
}

func putAlertState(ctx context.Context, e engine, a AlertState) error {
	switch {
	case a.Fingerprint == "" || a.TaskID == "" || a.IntegrationID == "":
		return domain.Validation("an alert state needs an integration, a fingerprint and a task", nil)
	case a.AlertStatus != "firing" && a.AlertStatus != "resolved":
		return domain.Validation("unknown alert status: "+a.AlertStatus, map[string]any{"alertStatus": a.AlertStatus})
	case a.FirstAt <= 0 || a.LastAt <= 0 || a.Occurrences <= 0:
		return domain.Validation("an alert state needs its times and a positive occurrence count", nil)
	}
	err := e.queries(true).PutAlertState(ctx, sqlitedb.PutAlertStateParams{
		ProjectID: a.ProjectID, IntegrationID: a.IntegrationID, FingerprintHash: DeliveryKey(a.Fingerprint), Fingerprint: a.Fingerprint,
		TaskID: a.TaskID, Occurrences: a.Occurrences, FirstAt: a.FirstAt, LastAt: a.LastAt, AlertStatus: a.AlertStatus, ExpiresAt: nullMillis(a.ExpiresAt),
	})
	if isForeignKeyViolation(err) {
		return domain.NotFound("Integration", a.IntegrationID)
	}
	if err != nil {
		return fmt.Errorf("store: put alert state: %w", err)
	}
	return nil
}

func listIntegrations(ctx context.Context, e engine, projectID string, limit int, includeDeleted bool) ([]Integration, error) {
	rows, err := e.queries(false).ListIntegrations(ctx, sqlitedb.ListIntegrationsParams{
		ProjectID: projectID, IncludeDeleted: boolInt(includeDeleted), PageLimit: int64(clamp(limit, 100, 200)),
	})
	if err != nil {
		return nil, fmt.Errorf("store: list integrations: %w", err)
	}
	return convert(rows, integrationFrom), nil
}

// updateIntegration merges p into the live row and writes it at expectedVersion; deletedAt > 0
// tombstones it in the same write.
func updateIntegration(ctx context.Context, e engine, projectID, integrationID string, expectedVersion int64, p IntegrationPatch, deletedAt int64) (Integration, error) {
	if p.UpdatedAt <= 0 {
		return Integration{}, domain.Validation("an integration update needs a time", nil)
	}
	if p.Name != nil && *p.Name == "" || p.BoardID != nil && *p.BoardID == "" {
		return Integration{}, domain.Validation("an integration's name and board are not empty", nil)
	}
	if p.Lifecycle != nil && !lifecycles[*p.Lifecycle] {
		return Integration{}, domain.Validation("unknown lifecycle: "+*p.Lifecycle, map[string]any{"lifecycle": *p.Lifecycle})
	}
	for name, raw := range map[string]json.RawMessage{"auth": p.Auth, "mapping": p.Mapping, "routing": p.Routing, "policy": p.Policy, "limits": p.Limits} {
		if raw != nil && !isObject(raw) {
			return Integration{}, domain.Validation(name+" must be a JSON object", map[string]any{"field": name})
		}
	}
	cur, err := getIntegration(ctx, e, projectID, integrationID)
	if err != nil {
		return Integration{}, err
	}
	if cur.Version != expectedVersion {
		return Integration{}, domain.Stale(expectedVersion, cur.Version)
	}
	next := cur
	if p.Name != nil {
		next.Name = *p.Name
	}
	if p.Enabled != nil {
		next.Enabled = *p.Enabled
	}
	if p.BoardID != nil {
		next.BoardID = *p.BoardID
	}
	if p.ColumnID != nil {
		next.ColumnID = p.ColumnID.Value
	}
	if p.Lifecycle != nil {
		next.Lifecycle = *p.Lifecycle
	}
	for _, f := range []struct {
		dst *json.RawMessage
		src json.RawMessage
	}{
		{&next.Auth, p.Auth}, {&next.Mapping, p.Mapping}, {&next.Routing, p.Routing}, {&next.Policy, p.Policy}, {&next.Limits, p.Limits},
	} {
		if f.src != nil {
			*f.dst = f.src
		}
	}
	del := nullMillis(deletedAt)
	r, err := e.queries(true).UpdateIntegration(ctx, sqlitedb.UpdateIntegrationParams{
		Name: next.Name, Enabled: boolInt(next.Enabled), BoardID: next.BoardID, ColumnID: nullStr(next.ColumnID),
		Auth: string(next.Auth), Mapping: string(next.Mapping), Routing: string(next.Routing), Lifecycle: next.Lifecycle,
		Policy: string(next.Policy), Limits: string(next.Limits), DeletedAt: del, UpdatedAt: p.UpdatedAt,
		ProjectID: projectID, ID: integrationID, Version: expectedVersion,
	})
	if isNoRows(err) {
		// Another writer moved it between the read and the write: say which way.
		now, gerr := getIntegration(ctx, e, projectID, integrationID)
		if gerr != nil {
			return Integration{}, gerr
		}
		return Integration{}, domain.Stale(expectedVersion, now.Version)
	}
	if err != nil {
		return Integration{}, fmt.Errorf("store: update integration: %w", err)
	}
	return integrationFrom(r), nil
}

func deliveryFrom(r sqlitedb.Delivery) Delivery {
	d := Delivery{
		ProjectID: r.ProjectID, IntegrationID: r.IntegrationID, DeliveryID: r.DeliveryID, TS: r.Ts,
		Kind: DeliveryKind(r.Kind), Result: DeliveryResult(r.Result), TaskID: strPtr(r.TaskID), Error: strPtr(r.Error),
		ExpiresAt: r.ExpiresAt,
	}
	if r.NormalizedEvent.Valid {
		d.NormalizedEvent = json.RawMessage(r.NormalizedEvent.String)
	}
	return d
}

func insertDelivery(ctx context.Context, e engine, d Delivery) (Delivery, bool, error) {
	switch {
	case d.DeliveryID == "" || d.IntegrationID == "" || d.TS <= 0 || d.ExpiresAt <= 0:
		return Delivery{}, false, domain.Validation("a delivery needs an id, an integration, a time and an expiry", nil)
	case !deliveryKinds[d.Kind]:
		return Delivery{}, false, domain.Validation("unknown delivery kind: "+string(d.Kind), map[string]any{"kind": d.Kind})
	case !deliveryResults[d.Result]:
		return Delivery{}, false, domain.Validation("unknown delivery result: "+string(d.Result), map[string]any{"result": d.Result})
	case d.NormalizedEvent != nil && !isObject(d.NormalizedEvent):
		return Delivery{}, false, domain.Validation("normalizedEvent must be a JSON object", map[string]any{"field": "normalizedEvent"})
	}
	var event = nullText(string(d.NormalizedEvent))
	r, err := e.queries(true).InsertDelivery(ctx, sqlitedb.InsertDeliveryParams{
		ProjectID: d.ProjectID, IntegrationID: d.IntegrationID, ID: DeliveryKey(d.DeliveryID), DeliveryID: d.DeliveryID,
		Ts: d.TS, Kind: string(d.Kind), Result: string(d.Result), TaskID: nullStr(d.TaskID), Error: nullStr(d.Error),
		NormalizedEvent: event, ExpiresAt: d.ExpiresAt,
	})
	switch {
	case isNoRows(err):
		prior, err := getDelivery(ctx, e, d.ProjectID, d.IntegrationID, d.DeliveryID)
		return prior, false, err
	case isForeignKeyViolation(err):
		return Delivery{}, false, domain.NotFound("Integration", d.IntegrationID)
	case err != nil:
		return Delivery{}, false, fmt.Errorf("store: insert delivery: %w", err)
	}
	return deliveryFrom(r), true, nil
}

func getDelivery(ctx context.Context, e engine, projectID, integrationID, deliveryID string) (Delivery, error) {
	r, err := e.queries(false).GetDelivery(ctx, sqlitedb.GetDeliveryParams{ProjectID: projectID, IntegrationID: integrationID, ID: DeliveryKey(deliveryID)})
	if err := found(err, "Delivery", deliveryID); err != nil {
		return Delivery{}, err
	}
	return deliveryFrom(r), nil
}

// deliveryCursor is a page cursor, base64url {order: the decimal ts, id: the row key}; "" is the
// first page, read as the row after (MaxInt64, "~"), above every hex key.
func deliveryCursor(cursor string) (int64, string, error) {
	if cursor == "" {
		return math.MaxInt64, "~", nil
	}
	order, id, err := DecodeCursor(cursor)
	if err != nil {
		return 0, "", err
	}
	ts, err := strconv.ParseInt(order, 10, 64)
	if err != nil || id == "" {
		return 0, "", domain.Validation("Invalid cursor", nil)
	}
	return ts, id, nil
}

func listDeliveries(ctx context.Context, e engine, projectID, integrationID string, f DeliveryFilter, cursor string, limit int) (DeliveryPage, error) {
	if f.Kind != "" && !deliveryKinds[f.Kind] {
		return DeliveryPage{}, domain.Validation("unknown delivery kind: "+string(f.Kind), map[string]any{"kind": f.Kind})
	}
	afterTs, afterID, err := deliveryCursor(cursor)
	if err != nil {
		return DeliveryPage{}, err
	}
	limit = clamp(limit, 50, 200)
	rows, err := e.queries(false).ListDeliveries(ctx, sqlitedb.ListDeliveriesParams{
		ProjectID: projectID, IntegrationID: integrationID, Kind: string(f.Kind), AfterTs: afterTs, AfterID: afterID,
		PageLimit: int64(limit + 1),
	})
	if err != nil {
		return DeliveryPage{}, fmt.Errorf("store: list deliveries: %w", err)
	}
	page := DeliveryPage{Deliveries: make([]Delivery, 0, min(len(rows), limit))}
	for i, r := range rows {
		if i == limit {
			last := rows[limit-1]
			page.NextCursor = EncodeCursor(strconv.FormatInt(last.Ts, 10), last.ID)
			break
		}
		page.Deliveries = append(page.Deliveries, deliveryFrom(r))
	}
	return page, nil
}

func putDeliveryRawBody(ctx context.Context, e engine, projectID, integrationID, deliveryID string, body []byte, expiresAt int64) error {
	if deliveryID == "" || expiresAt <= 0 || body == nil {
		return domain.Validation("a raw body needs a delivery, a body and an expiry", nil)
	}
	err := e.queries(true).PutDeliveryRawBody(ctx, sqlitedb.PutDeliveryRawBodyParams{
		ProjectID: projectID, IntegrationID: integrationID, DeliveryID: DeliveryKey(deliveryID), Body: body, ExpiresAt: expiresAt,
	})
	if isForeignKeyViolation(err) {
		return domain.NotFound("Delivery", deliveryID)
	}
	if err != nil {
		return fmt.Errorf("store: put raw body: %w", err)
	}
	return nil
}

func getDeliveryRawBody(ctx context.Context, e engine, projectID, integrationID, deliveryID string) ([]byte, error) {
	r, err := e.queries(false).GetDeliveryRawBody(ctx, sqlitedb.GetDeliveryRawBodyParams{ProjectID: projectID, IntegrationID: integrationID, DeliveryID: DeliveryKey(deliveryID)})
	if err := found(err, "DeliveryRawBody", deliveryID); err != nil {
		return nil, err
	}
	return r.Body, nil
}

func deleteExpired(ctx context.Context, e engine, what string, now int64, limit int) (int64, error) {
	if limit <= 0 {
		return 0, domain.Validation("a sweep needs a positive limit", map[string]any{"limit": limit})
	}
	q := e.queries(true)
	var n int64
	var err error
	switch what {
	case "deliveries":
		n, err = q.DeleteExpiredDeliveries(ctx, sqlitedb.DeleteExpiredDeliveriesParams{Now: now, PageLimit: int64(limit)})
	case "alert states":
		n, err = q.DeleteExpiredAlertStates(ctx, sqlitedb.DeleteExpiredAlertStatesParams{Now: now, PageLimit: int64(limit)})
	default:
		n, err = q.DeleteExpiredDeliveryRawBodies(ctx, sqlitedb.DeleteExpiredDeliveryRawBodiesParams{Now: now, PageLimit: int64(limit)})
	}
	if err != nil {
		return 0, fmt.Errorf("store: sweep %s: %w", what, err)
	}
	return n, nil
}

func (p pgQueries) InsertIntegration(ctx context.Context, arg sqlitedb.InsertIntegrationParams) (sqlitedb.Integration, error) {
	r, err := p.q.InsertIntegration(ctx, pgdb.InsertIntegrationParams(arg))
	return sqlitedb.Integration(r), err
}

func (p pgQueries) GetIntegration(ctx context.Context, arg sqlitedb.GetIntegrationParams) (sqlitedb.Integration, error) {
	r, err := p.q.GetIntegration(ctx, pgdb.GetIntegrationParams(arg))
	return sqlitedb.Integration(r), err
}

func (p pgQueries) GetIntegrationByID(ctx context.Context, id string) (sqlitedb.Integration, error) {
	r, err := p.q.GetIntegrationByID(ctx, id)
	return sqlitedb.Integration(r), err
}

func (p pgQueries) GetAlertState(ctx context.Context, arg sqlitedb.GetAlertStateParams) (sqlitedb.AlertState, error) {
	r, err := p.q.GetAlertState(ctx, pgdb.GetAlertStateParams(arg))
	return sqlitedb.AlertState(r), err
}

func (p pgQueries) PutAlertState(ctx context.Context, arg sqlitedb.PutAlertStateParams) error {
	return p.q.PutAlertState(ctx, pgdb.PutAlertStateParams(arg))
}

func (p pgQueries) ListIntegrations(ctx context.Context, arg sqlitedb.ListIntegrationsParams) ([]sqlitedb.Integration, error) {
	rows, err := p.q.ListIntegrations(ctx, pgdb.ListIntegrationsParams(arg))
	return convert(rows, func(r pgdb.Integration) sqlitedb.Integration { return sqlitedb.Integration(r) }), err
}

func (p pgQueries) UpdateIntegration(ctx context.Context, arg sqlitedb.UpdateIntegrationParams) (sqlitedb.Integration, error) {
	r, err := p.q.UpdateIntegration(ctx, pgdb.UpdateIntegrationParams(arg))
	return sqlitedb.Integration(r), err
}

func (p pgQueries) InsertDelivery(ctx context.Context, arg sqlitedb.InsertDeliveryParams) (sqlitedb.Delivery, error) {
	r, err := p.q.InsertDelivery(ctx, pgdb.InsertDeliveryParams(arg))
	return sqlitedb.Delivery(r), err
}

func (p pgQueries) GetDelivery(ctx context.Context, arg sqlitedb.GetDeliveryParams) (sqlitedb.Delivery, error) {
	r, err := p.q.GetDelivery(ctx, pgdb.GetDeliveryParams(arg))
	return sqlitedb.Delivery(r), err
}

func (p pgQueries) ListDeliveries(ctx context.Context, arg sqlitedb.ListDeliveriesParams) ([]sqlitedb.Delivery, error) {
	rows, err := p.q.ListDeliveries(ctx, pgdb.ListDeliveriesParams(arg))
	return convert(rows, func(r pgdb.Delivery) sqlitedb.Delivery { return sqlitedb.Delivery(r) }), err
}

func (p pgQueries) PutDeliveryRawBody(ctx context.Context, arg sqlitedb.PutDeliveryRawBodyParams) error {
	return p.q.PutDeliveryRawBody(ctx, pgdb.PutDeliveryRawBodyParams(arg))
}

func (p pgQueries) GetDeliveryRawBody(ctx context.Context, arg sqlitedb.GetDeliveryRawBodyParams) (sqlitedb.DeliveryRawBody, error) {
	r, err := p.q.GetDeliveryRawBody(ctx, pgdb.GetDeliveryRawBodyParams(arg))
	return sqlitedb.DeliveryRawBody(r), err
}

func (p pgQueries) DeleteExpiredDeliveries(ctx context.Context, arg sqlitedb.DeleteExpiredDeliveriesParams) (int64, error) {
	return p.q.DeleteExpiredDeliveries(ctx, pgdb.DeleteExpiredDeliveriesParams(arg))
}

func (p pgQueries) DeleteExpiredDeliveryRawBodies(ctx context.Context, arg sqlitedb.DeleteExpiredDeliveryRawBodiesParams) (int64, error) {
	return p.q.DeleteExpiredDeliveryRawBodies(ctx, pgdb.DeleteExpiredDeliveryRawBodiesParams(arg))
}

func (p pgQueries) DeleteExpiredAlertStates(ctx context.Context, arg sqlitedb.DeleteExpiredAlertStatesParams) (int64, error) {
	return p.q.DeleteExpiredAlertStates(ctx, pgdb.DeleteExpiredAlertStatesParams(arg))
}
