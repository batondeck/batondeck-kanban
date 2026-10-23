package memstore

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The on-call area (store.Oncall): integrations by (project, id), deliveries and raw bodies by
// (project, integration, store.DeliveryKey(delivery id)).

type rawBody struct {
	body      []byte
	expiresAt int64
}

var (
	lifecycles      = map[string]bool{"correlate": true, "autoclose": true, "per_event": true}
	deliveryKinds   = map[store.DeliveryKind]bool{store.DeliveryFiring: true, store.DeliveryResolved: true, store.DeliveryTest: true, store.DeliveryRejected: true}
	deliveryResults = map[store.DeliveryResult]bool{store.ResultCreated: true, store.ResultAppended: true, store.ResultAnnotated: true,
		store.ResultClosed: true, store.ResultRecorded: true, store.ResultRejected: true, store.ResultRateLimited: true, store.ResultCoalesced: true}
)

func isJSONObject(raw json.RawMessage) bool {
	var m map[string]json.RawMessage
	return json.Unmarshal(raw, &m) == nil && m != nil
}

func cloneRaw(r json.RawMessage) json.RawMessage {
	if r == nil {
		return nil
	}
	return append(json.RawMessage(nil), r...)
}

func cloneIntegration(i store.Integration) store.Integration {
	i.ColumnID = clonePtr(i.ColumnID)
	i.DeletedAt = clonePtr(i.DeletedAt)
	i.Auth, i.Mapping, i.Routing, i.Policy, i.Limits = cloneRaw(i.Auth), cloneRaw(i.Mapping), cloneRaw(i.Routing), cloneRaw(i.Policy), cloneRaw(i.Limits)
	return i
}

func cloneDelivery(d store.Delivery) store.Delivery {
	d.TaskID, d.Error = clonePtr(d.TaskID), clonePtr(d.Error)
	d.NormalizedEvent = cloneRaw(d.NormalizedEvent)
	return d
}

func (s *Store) CreateIntegration(ctx context.Context, in store.CreateIntegrationInput) (store.Integration, error) {
	switch {
	case in.ID == "" || in.Name == "" || in.Source == "" || in.BoardID == "" || in.CreatedBy == "":
		return store.Integration{}, domain.Validation("an integration needs an id, a name, a source, a board and a creator", nil)
	case !lifecycles[in.Lifecycle]:
		return store.Integration{}, domain.Validation("unknown lifecycle: "+in.Lifecycle, map[string]any{"lifecycle": in.Lifecycle})
	}
	fields := []struct {
		name     string
		raw      *json.RawMessage
		required bool
	}{{"auth", &in.Auth, true}, {"mapping", &in.Mapping, false}, {"routing", &in.Routing, true}, {"policy", &in.Policy, false}, {"limits", &in.Limits, true}}
	for _, f := range fields {
		switch {
		case *f.raw == nil && f.required:
			return store.Integration{}, domain.Validation("an integration needs "+f.name, map[string]any{"field": f.name})
		case *f.raw == nil:
			*f.raw = json.RawMessage(`{}`)
		case !isJSONObject(*f.raw):
			return store.Integration{}, domain.Validation(f.name+" must be a JSON object", map[string]any{"field": f.name})
		}
	}
	return get(ctx, s, func(st *state) (store.Integration, error) {
		if _, ok := st.projects[in.ProjectID]; !ok {
			return store.Integration{}, domain.NotFound("Project", in.ProjectID)
		}
		for k := range st.integrations {
			if k[1] == in.ID {
				return store.Integration{}, domain.Validation("integration id already exists", map[string]any{"id": in.ID})
			}
		}
		i := store.Integration{
			ProjectID: in.ProjectID, ID: in.ID, Name: in.Name, Source: in.Source, Enabled: true, BoardID: in.BoardID,
			ColumnID: in.ColumnID, Auth: in.Auth, Mapping: in.Mapping, Routing: in.Routing, Lifecycle: in.Lifecycle,
			Policy: in.Policy, Limits: in.Limits, Version: 1, CreatedBy: in.CreatedBy, CreatedAt: in.CreatedAt, UpdatedAt: in.CreatedAt,
		}
		st.integrations[[2]string{in.ProjectID, in.ID}] = cloneIntegration(i)
		return cloneIntegration(i), nil
	})
}

func liveIntegration(st *state, projectID, integrationID string) (store.Integration, error) {
	i, ok := st.integrations[[2]string{projectID, integrationID}]
	if !ok || i.DeletedAt != nil {
		return store.Integration{}, domain.NotFound("Integration", integrationID)
	}
	return i, nil
}

func (s *Store) GetIntegration(ctx context.Context, projectID, integrationID string) (store.Integration, error) {
	return get(ctx, s, func(st *state) (store.Integration, error) {
		i, err := liveIntegration(st, projectID, integrationID)
		return cloneIntegration(i), err
	})
}

func (s *Store) ListIntegrations(ctx context.Context, projectID string, limit int, includeDeleted bool) ([]store.Integration, error) {
	limit = clamp(limit, 100, 200)
	return get(ctx, s, func(st *state) ([]store.Integration, error) {
		var out []store.Integration
		for k, i := range st.integrations {
			if k[0] == projectID && (includeDeleted || i.DeletedAt == nil) {
				out = append(out, cloneIntegration(i))
			}
		}
		slices.SortFunc(out, func(a, b store.Integration) int {
			return cmp.Or(cmp.Compare(b.CreatedAt, a.CreatedAt), cmp.Compare(b.ID, a.ID))
		})
		if len(out) > limit {
			out = out[:limit]
		}
		return out, nil
	})
}

func (s *Store) UpdateIntegration(ctx context.Context, projectID, integrationID string, expectedVersion int64, p store.IntegrationPatch) (store.Integration, error) {
	return s.writeIntegration(ctx, projectID, integrationID, expectedVersion, p, 0)
}

func (s *Store) DeleteIntegration(ctx context.Context, projectID, integrationID string, expectedVersion, at int64) (store.Integration, error) {
	return s.writeIntegration(ctx, projectID, integrationID, expectedVersion, store.IntegrationPatch{UpdatedAt: at}, at)
}

func (s *Store) writeIntegration(ctx context.Context, projectID, integrationID string, expectedVersion int64, p store.IntegrationPatch, deletedAt int64) (store.Integration, error) {
	if p.UpdatedAt <= 0 {
		return store.Integration{}, domain.Validation("an integration update needs a time", nil)
	}
	if p.Name != nil && *p.Name == "" || p.BoardID != nil && *p.BoardID == "" {
		return store.Integration{}, domain.Validation("an integration's name and board are not empty", nil)
	}
	if p.Lifecycle != nil && !lifecycles[*p.Lifecycle] {
		return store.Integration{}, domain.Validation("unknown lifecycle: "+*p.Lifecycle, map[string]any{"lifecycle": *p.Lifecycle})
	}
	for name, raw := range map[string]json.RawMessage{"auth": p.Auth, "mapping": p.Mapping, "routing": p.Routing, "policy": p.Policy, "limits": p.Limits} {
		if raw != nil && !isJSONObject(raw) {
			return store.Integration{}, domain.Validation(name+" must be a JSON object", map[string]any{"field": name})
		}
	}
	return get(ctx, s, func(st *state) (store.Integration, error) {
		i, err := liveIntegration(st, projectID, integrationID)
		if err != nil {
			return store.Integration{}, err
		}
		if i.Version != expectedVersion {
			return store.Integration{}, domain.Stale(expectedVersion, i.Version)
		}
		if p.Name != nil {
			i.Name = *p.Name
		}
		if p.Enabled != nil {
			i.Enabled = *p.Enabled
		}
		if p.BoardID != nil {
			i.BoardID = *p.BoardID
		}
		if p.ColumnID != nil {
			i.ColumnID = clonePtr(p.ColumnID.Value)
		}
		if p.Lifecycle != nil {
			i.Lifecycle = *p.Lifecycle
		}
		for _, f := range []struct {
			dst *json.RawMessage
			src json.RawMessage
		}{{&i.Auth, p.Auth}, {&i.Mapping, p.Mapping}, {&i.Routing, p.Routing}, {&i.Policy, p.Policy}, {&i.Limits, p.Limits}} {
			if f.src != nil {
				*f.dst = cloneRaw(f.src)
			}
		}
		if deletedAt > 0 {
			i.DeletedAt = &deletedAt
		}
		i.Version++
		i.UpdatedAt = p.UpdatedAt
		st.integrations[[2]string{projectID, integrationID}] = cloneIntegration(i)
		return cloneIntegration(i), nil
	})
}

func (s *Store) InsertDelivery(ctx context.Context, d store.Delivery) (store.Delivery, bool, error) {
	switch {
	case d.DeliveryID == "" || d.IntegrationID == "" || d.TS <= 0 || d.ExpiresAt <= 0:
		return store.Delivery{}, false, domain.Validation("a delivery needs an id, an integration, a time and an expiry", nil)
	case !deliveryKinds[d.Kind]:
		return store.Delivery{}, false, domain.Validation("unknown delivery kind: "+string(d.Kind), map[string]any{"kind": d.Kind})
	case !deliveryResults[d.Result]:
		return store.Delivery{}, false, domain.Validation("unknown delivery result: "+string(d.Result), map[string]any{"result": d.Result})
	case d.NormalizedEvent != nil && !isJSONObject(d.NormalizedEvent):
		return store.Delivery{}, false, domain.Validation("normalizedEvent must be a JSON object", map[string]any{"field": "normalizedEvent"})
	}
	var inserted bool
	row, err := get(ctx, s, func(st *state) (store.Delivery, error) {
		if _, ok := st.integrations[[2]string{d.ProjectID, d.IntegrationID}]; !ok {
			return store.Delivery{}, domain.NotFound("Integration", d.IntegrationID)
		}
		k := [3]string{d.ProjectID, d.IntegrationID, store.DeliveryKey(d.DeliveryID)}
		if prior, ok := st.deliveries[k]; ok {
			return cloneDelivery(prior), nil
		}
		inserted = true
		st.deliveries[k] = cloneDelivery(d)
		return cloneDelivery(d), nil
	})
	return row, inserted, err
}

func (s *Store) GetDelivery(ctx context.Context, projectID, integrationID, deliveryID string) (store.Delivery, error) {
	return get(ctx, s, func(st *state) (store.Delivery, error) {
		d, ok := st.deliveries[[3]string{projectID, integrationID, store.DeliveryKey(deliveryID)}]
		if !ok {
			return store.Delivery{}, domain.NotFound("Delivery", deliveryID)
		}
		return cloneDelivery(d), nil
	})
}

func (s *Store) ListDeliveries(ctx context.Context, projectID, integrationID string, f store.DeliveryFilter, cursor string, limit int) (store.DeliveryPage, error) {
	if f.Kind != "" && !deliveryKinds[f.Kind] {
		return store.DeliveryPage{}, domain.Validation("unknown delivery kind: "+string(f.Kind), map[string]any{"kind": f.Kind})
	}
	afterTs, afterKey := int64(1<<63-1), "~"
	if cursor != "" {
		order, id, err := store.DecodeCursor(cursor)
		if err != nil {
			return store.DeliveryPage{}, err
		}
		ts, perr := strconv.ParseInt(order, 10, 64)
		if perr != nil || id == "" {
			return store.DeliveryPage{}, domain.Validation("Invalid cursor", nil)
		}
		afterTs, afterKey = ts, id
	}
	limit = clamp(limit, 50, 200)
	return get(ctx, s, func(st *state) (store.DeliveryPage, error) {
		type keyed struct {
			key string
			d   store.Delivery
		}
		var rows []keyed
		for k, d := range st.deliveries {
			if k[0] != projectID || k[1] != integrationID || f.Kind != "" && d.Kind != f.Kind {
				continue
			}
			if d.TS < afterTs || d.TS == afterTs && k[2] < afterKey {
				rows = append(rows, keyed{k[2], d})
			}
		}
		slices.SortFunc(rows, func(a, b keyed) int { return cmp.Or(cmp.Compare(b.d.TS, a.d.TS), cmp.Compare(b.key, a.key)) })
		page := store.DeliveryPage{Deliveries: []store.Delivery{}}
		for i, r := range rows {
			if i == limit {
				last := rows[limit-1]
				page.NextCursor = store.EncodeCursor(strconv.FormatInt(last.d.TS, 10), last.key)
				break
			}
			page.Deliveries = append(page.Deliveries, cloneDelivery(r.d))
		}
		return page, nil
	})
}

func (s *Store) PutDeliveryRawBody(ctx context.Context, projectID, integrationID, deliveryID string, body []byte, expiresAt int64) error {
	if deliveryID == "" || expiresAt <= 0 || body == nil {
		return domain.Validation("a raw body needs a delivery, a body and an expiry", nil)
	}
	return s.do(ctx, func(st *state) error {
		k := [3]string{projectID, integrationID, store.DeliveryKey(deliveryID)}
		if _, ok := st.deliveries[k]; !ok {
			return domain.NotFound("Delivery", deliveryID)
		}
		st.rawBodies[k] = rawBody{body: bytes.Clone(body), expiresAt: expiresAt}
		return nil
	})
}

func (s *Store) GetDeliveryRawBody(ctx context.Context, projectID, integrationID, deliveryID string) ([]byte, error) {
	return get(ctx, s, func(st *state) ([]byte, error) {
		r, ok := st.rawBodies[[3]string{projectID, integrationID, store.DeliveryKey(deliveryID)}]
		if !ok {
			return nil, domain.NotFound("DeliveryRawBody", deliveryID)
		}
		return bytes.Clone(r.body), nil
	})
}

// sweepOldest deletes at most limit keys whose expiry is at or before now, oldest expiry first.
func sweepOldest[V any](m map[[3]string]V, expiry func(V) int64, now int64, limit int) [][3]string {
	var keys [][3]string
	for k, v := range m {
		if expiry(v) <= now {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b [3]string) int {
		return cmp.Or(cmp.Compare(expiry(m[a]), expiry(m[b])), cmp.Compare(a[2], b[2]))
	})
	if len(keys) > limit {
		keys = keys[:limit]
	}
	for _, k := range keys {
		delete(m, k)
	}
	return keys
}

func (s *Store) DeleteExpiredDeliveries(ctx context.Context, now int64, limit int) (int64, error) {
	if limit <= 0 {
		return 0, domain.Validation("a sweep needs a positive limit", map[string]any{"limit": limit})
	}
	return get(ctx, s, func(st *state) (int64, error) {
		keys := sweepOldest(st.deliveries, func(d store.Delivery) int64 { return d.ExpiresAt }, now, limit)
		for _, k := range keys {
			delete(st.rawBodies, k)
		}
		return int64(len(keys)), nil
	})
}

func (s *Store) DeleteExpiredDeliveryRawBodies(ctx context.Context, now int64, limit int) (int64, error) {
	if limit <= 0 {
		return 0, domain.Validation("a sweep needs a positive limit", map[string]any{"limit": limit})
	}
	return get(ctx, s, func(st *state) (int64, error) {
		return int64(len(sweepOldest(st.rawBodies, func(r rawBody) int64 { return r.expiresAt }, now, limit))), nil
	})
}

// deleteProjectOncall is a project delete's cascade: its integrations, their deliveries and raw
// bodies (the engines' ON DELETE CASCADE).
func deleteProjectOncall(st *state, projectID string) {
	maps.DeleteFunc(st.integrations, func(k [2]string, _ store.Integration) bool { return k[0] == projectID })
	maps.DeleteFunc(st.deliveries, func(k [3]string, _ store.Delivery) bool { return k[0] == projectID })
	maps.DeleteFunc(st.rawBodies, func(k [3]string, _ rawBody) bool { return k[0] == projectID })
}
