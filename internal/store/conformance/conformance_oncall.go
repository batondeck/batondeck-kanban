package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("oncall", oncall) }

// oncall is the on-call area: an integration created, read, merged under its version, tombstoned
// and listed newest first; a delivery recorded once, paged newest first by kind; a raw body kept
// and swept apart from its delivery; a project delete taking all of it; and the refusals.
func oncall(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.ListIntegrations(ctx, "P-probe", 0, false)
		return err
	}
	area(t, "Oncall", newStore, probe, func(t *testing.T) {
		auth := json.RawMessage(`{"mode":"hmac","hmac":{"currentEncrypted":"enc1:a:b:c:d:e:f","rotatedAt":1}}`)
		create := func(t *testing.T, s store.Store, p, b, id string, at int64) store.Integration {
			t.Helper()
			i, err := s.CreateIntegration(ctx, store.CreateIntegrationInput{
				ProjectID: p, ID: id, Name: "alerts " + id, Source: "generic", BoardID: b, Auth: auth,
				Routing: json.RawMessage(`{"mode":"broadcast"}`), Lifecycle: "correlate",
				Limits: json.RawMessage(`{"ratePerMin":60,"payloadCapKb":256}`), CreatedBy: "alice", CreatedAt: at,
			})
			noErr(t, err)
			return i
		}
		delivery := func(p, integration, id string, ts int64, kind store.DeliveryKind) store.Delivery {
			return store.Delivery{ProjectID: p, IntegrationID: integration, DeliveryID: id, TS: ts, Kind: kind,
				Result: store.ResultRecorded, NormalizedEvent: json.RawMessage(`{"title":"t"}`), ExpiresAt: ts + 1000}
		}

		t.Run("CreatedEnabledAtVersionOneAndReadBack", func(t *testing.T) {
			s, _, p, b := seeded(t, newStore)
			i := create(t, s, p, b, "I-a", 100)
			col := "C-1"
			if !i.Enabled || i.Version != 1 || i.Name != "alerts I-a" || i.BoardID != b || i.ColumnID != nil || i.CreatedAt != 100 ||
				i.UpdatedAt != 100 || i.DeletedAt != nil || string(i.Mapping) != "{}" || string(i.Policy) != "{}" || !bytes.Equal(i.Auth, auth) {
				t.Fatalf("CreateIntegration = %+v", i)
			}
			got, err := s.GetIntegration(ctx, p, "I-a")
			noErr(t, err)
			if got.ID != "I-a" || got.Version != 1 || !bytes.Equal(got.Auth, auth) || string(got.Limits) != `{"ratePerMin":60,"payloadCapKb":256}` {
				t.Fatalf("GetIntegration = %+v", got)
			}
			_, err = s.CreateIntegration(ctx, store.CreateIntegrationInput{ProjectID: p, ID: "I-c", Name: "n", Source: "webhook", BoardID: b,
				ColumnID: &col, Auth: auth, Routing: json.RawMessage(`{}`), Lifecycle: "per_event", Limits: json.RawMessage(`{}`), CreatedBy: "alice", CreatedAt: 1})
			noErr(t, err)
			if c, _ := s.GetIntegration(ctx, p, "I-c"); c.ColumnID == nil || *c.ColumnID != col || c.Lifecycle != "per_event" {
				t.Fatalf("columnId and lifecycle did not round-trip: %+v", c)
			}
		})

		t.Run("UpdateMergesUnderItsVersion", func(t *testing.T) {
			s, _, p, b := seeded(t, newStore)
			create(t, s, p, b, "I-a", 100)
			name, off, col := "renamed", false, "C-9"
			newAuth := json.RawMessage(`{"mode":"api_key","apiKeyHash":"ab"}`)
			u, err := s.UpdateIntegration(ctx, p, "I-a", 1, store.IntegrationPatch{Name: &name, Enabled: &off,
				ColumnID: &store.Nullable[string]{Value: &col}, Auth: newAuth, UpdatedAt: 200})
			noErr(t, err)
			if u.Version != 2 || u.Name != name || u.Enabled || u.ColumnID == nil || *u.ColumnID != col || !bytes.Equal(u.Auth, newAuth) ||
				u.UpdatedAt != 200 || u.CreatedAt != 100 || string(u.Routing) != `{"mode":"broadcast"}` {
				t.Fatalf("UpdateIntegration = %+v", u)
			}
			u, err = s.UpdateIntegration(ctx, p, "I-a", 2, store.IntegrationPatch{ColumnID: &store.Nullable[string]{}, UpdatedAt: 300})
			noErr(t, err)
			if u.ColumnID != nil || u.Version != 3 || u.Name != name {
				t.Fatalf("a null columnId: %+v", u)
			}
			_, err = s.UpdateIntegration(ctx, p, "I-a", 2, store.IntegrationPatch{Name: &name, UpdatedAt: 400})
			wantErr(t, err, domain.CodeStale, "")
			_, err = s.UpdateIntegration(ctx, p, "I-none", 1, store.IntegrationPatch{UpdatedAt: 400})
			wantErr(t, err, domain.CodeNotFound, "Integration")
			for name, patch := range map[string]store.IntegrationPatch{
				"no time":           {},
				"an empty name":     {Name: new(string), UpdatedAt: 1},
				"a lifecycle":       {Lifecycle: &name, UpdatedAt: 1},
				"a non-object auth": {Auth: json.RawMessage(`[1]`), UpdatedAt: 1},
			} {
				_, err := s.UpdateIntegration(ctx, p, "I-a", 3, patch)
				if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeValidation {
					t.Errorf("%s: %v, want VALIDATION", name, err)
				}
			}
		})

		t.Run("DeleteTombstonesAndListHidesTombstones", func(t *testing.T) {
			s, _, p, b := seeded(t, newStore)
			create(t, s, p, b, "I-old", 100)
			create(t, s, p, b, "I-new", 200)
			_, err := s.DeleteIntegration(ctx, p, "I-old", 7, 300)
			wantErr(t, err, domain.CodeStale, "")
			d, err := s.DeleteIntegration(ctx, p, "I-old", 1, 300)
			noErr(t, err)
			if d.DeletedAt == nil || *d.DeletedAt != 300 || d.Version != 2 {
				t.Fatalf("DeleteIntegration = %+v", d)
			}
			_, err = s.GetIntegration(ctx, p, "I-old")
			wantErr(t, err, domain.CodeNotFound, "Integration")
			_, err = s.DeleteIntegration(ctx, p, "I-old", 2, 400)
			wantErr(t, err, domain.CodeNotFound, "Integration")
			_, err = s.UpdateIntegration(ctx, p, "I-old", 2, store.IntegrationPatch{UpdatedAt: 400})
			wantErr(t, err, domain.CodeNotFound, "Integration")
			live, err := s.ListIntegrations(ctx, p, 0, false)
			noErr(t, err)
			all, err := s.ListIntegrations(ctx, p, 0, true)
			noErr(t, err)
			if len(live) != 1 || live[0].ID != "I-new" || len(all) != 2 || all[0].ID != "I-new" || all[1].ID != "I-old" || all[1].DeletedAt == nil {
				t.Fatalf("live %+v, all %+v", live, all)
			}
			// A tombstone keeps its deliveries writable and readable (its delivery log).
			_, ins, err := s.InsertDelivery(ctx, delivery(p, "I-old", "late", 500, store.DeliveryFiring))
			noErr(t, err)
			if !ins {
				t.Fatal("a delivery to a tombstoned integration was not recorded")
			}
		})

		t.Run("ListIsNewestFirstLimitedAndPerProject", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			for i := range 3 {
				create(t, s, p, b, fmt.Sprintf("I-%d", i), int64(100+i))
			}
			p2, b2, err := sd.seedProject(ctx)
			noErr(t, err)
			create(t, s, p2, b2, "I-other", 999)
			got, err := s.ListIntegrations(ctx, p, 2, false)
			noErr(t, err)
			if len(got) != 2 || got[0].ID != "I-2" || got[1].ID != "I-1" {
				t.Fatalf("ListIntegrations limit 2 = %+v", got)
			}
			_, err = s.GetIntegration(ctx, p, "I-other")
			wantErr(t, err, domain.CodeNotFound, "Integration")
		})

		t.Run("CreateRefusals", func(t *testing.T) {
			s, _, p, b := seeded(t, newStore)
			ok := store.CreateIntegrationInput{ProjectID: p, ID: "I-x", Name: "n", Source: "generic", BoardID: b, Auth: auth,
				Routing: json.RawMessage(`{}`), Lifecycle: "correlate", Limits: json.RawMessage(`{}`), CreatedBy: "alice", CreatedAt: 1}
			for name, mut := range map[string]func(*store.CreateIntegrationInput){
				"no id":                func(in *store.CreateIntegrationInput) { in.ID = "" },
				"no name":              func(in *store.CreateIntegrationInput) { in.Name = "" },
				"no board":             func(in *store.CreateIntegrationInput) { in.BoardID = "" },
				"an unknown lifecycle": func(in *store.CreateIntegrationInput) { in.Lifecycle = "forever" },
				"no auth":              func(in *store.CreateIntegrationInput) { in.Auth = nil },
				"a non-object routing": func(in *store.CreateIntegrationInput) { in.Routing = json.RawMessage(`"x"`) },
			} {
				in := ok
				mut(&in)
				_, err := s.CreateIntegration(ctx, in)
				if e, isErr := domain.AsBatonDeckError(err); !isErr || e.Code != domain.CodeValidation {
					t.Errorf("%s: %v, want VALIDATION", name, err)
				}
			}
			in := ok
			in.ProjectID = "P-none"
			_, err := s.CreateIntegration(ctx, in)
			wantErr(t, err, domain.CodeNotFound, "Project")
			_, err = s.CreateIntegration(ctx, ok)
			noErr(t, err)
		})

		t.Run("ADeliveryIsRecordedOnce", func(t *testing.T) {
			s, _, p, b := seeded(t, newStore)
			create(t, s, p, b, "I-a", 1)
			task := "T-7"
			d := delivery(p, "I-a", "dlv:1", 100, store.DeliveryTest)
			d.Result, d.TaskID = store.ResultCreated, &task
			row, ins, err := s.InsertDelivery(ctx, d)
			noErr(t, err)
			if !ins || row.DeliveryID != "dlv:1" || row.Result != store.ResultCreated || row.TaskID == nil || *row.TaskID != task || row.Kind != store.DeliveryTest || string(row.NormalizedEvent) != `{"title":"t"}` {
				t.Fatalf("InsertDelivery = %+v, %v", row, ins)
			}
			again := delivery(p, "I-a", "dlv:1", 200, store.DeliveryFiring)
			row, ins, err = s.InsertDelivery(ctx, again)
			noErr(t, err)
			if ins || row.Result != store.ResultCreated || row.TS != 100 {
				t.Fatalf("a second insert of dlv:1 = %+v, %v; want the first row, not inserted", row, ins)
			}
			got, err := s.GetDelivery(ctx, p, "I-a", "dlv:1")
			noErr(t, err)
			if got.TS != 100 || got.Error != nil {
				t.Fatalf("GetDelivery = %+v", got)
			}
			_, err = s.GetDelivery(ctx, p, "I-a", "dlv:2")
			wantErr(t, err, domain.CodeNotFound, "Delivery")
			_, _, err = s.InsertDelivery(ctx, delivery(p, "I-none", "dlv:3", 1, store.DeliveryFiring))
			wantErr(t, err, domain.CodeNotFound, "Integration")
			for name, mut := range map[string]func(*store.Delivery){
				"an unknown kind":    func(d *store.Delivery) { d.Kind = "maybe" },
				"an unknown result":  func(d *store.Delivery) { d.Result = "kept" },
				"no time":            func(d *store.Delivery) { d.TS = 0 },
				"a non-object event": func(d *store.Delivery) { d.NormalizedEvent = json.RawMessage(`[]`) },
			} {
				bad := delivery(p, "I-a", "dlv:bad", 1, store.DeliveryFiring)
				mut(&bad)
				_, _, err := s.InsertDelivery(ctx, bad)
				if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeValidation {
					t.Errorf("%s: %v, want VALIDATION", name, err)
				}
			}
		})

		t.Run("DeliveriesPageNewestFirstByKind", func(t *testing.T) {
			s, _, p, b := seeded(t, newStore)
			create(t, s, p, b, "I-a", 1)
			create(t, s, p, b, "I-b", 2)
			for i, kind := range []store.DeliveryKind{store.DeliveryFiring, store.DeliveryResolved, store.DeliveryFiring, store.DeliveryFiring, store.DeliveryTest} {
				_, _, err := s.InsertDelivery(ctx, delivery(p, "I-a", fmt.Sprintf("d%d", i), int64(100+i/2), kind))
				noErr(t, err)
			}
			_, _, err := s.InsertDelivery(ctx, delivery(p, "I-b", "other", 999, store.DeliveryFiring))
			noErr(t, err)
			var seen []string
			cursor := ""
			for range 5 {
				page, err := s.ListDeliveries(ctx, p, "I-a", store.DeliveryFilter{}, cursor, 2)
				noErr(t, err)
				for _, d := range page.Deliveries {
					seen = append(seen, d.DeliveryID)
				}
				if cursor = page.NextCursor; cursor == "" {
					break
				}
			}
			if len(seen) != 5 || seen[0] != "d4" {
				t.Fatalf("paged %v, want 5 rows newest first from d4", seen)
			}
			for i := 1; i < len(seen); i++ {
				a, _ := s.GetDelivery(ctx, p, "I-a", seen[i-1])
				c, _ := s.GetDelivery(ctx, p, "I-a", seen[i])
				if a.TS < c.TS {
					t.Fatalf("paged %v out of ts order", seen)
				}
			}
			firing, err := s.ListDeliveries(ctx, p, "I-a", store.DeliveryFilter{Kind: store.DeliveryFiring}, "", 0)
			noErr(t, err)
			if len(firing.Deliveries) != 3 || firing.NextCursor != "" {
				t.Fatalf("kind firing = %+v", firing)
			}
			_, err = s.ListDeliveries(ctx, p, "I-a", store.DeliveryFilter{}, "not a cursor!", 0)
			wantErr(t, err, domain.CodeValidation, "")
			_, err = s.ListDeliveries(ctx, p, "I-a", store.DeliveryFilter{Kind: "sometimes"}, "", 0)
			wantErr(t, err, domain.CodeValidation, "")
		})

		t.Run("RawBodiesAreKeptAndSweptApartFromTheirDelivery", func(t *testing.T) {
			s, _, p, b := seeded(t, newStore)
			create(t, s, p, b, "I-a", 1)
			for _, id := range []string{"r1", "r2", "r3"} {
				_, _, err := s.InsertDelivery(ctx, delivery(p, "I-a", id, 100, store.DeliveryRejected))
				noErr(t, err)
			}
			body := []byte("{\"x\":\x00\"attacker bytes\"}")
			noErr(t, s.PutDeliveryRawBody(ctx, p, "I-a", "r1", body, 50))
			noErr(t, s.PutDeliveryRawBody(ctx, p, "I-a", "r2", []byte("b2"), 60))
			noErr(t, s.PutDeliveryRawBody(ctx, p, "I-a", "r3", []byte("b3"), 5000))
			got, err := s.GetDeliveryRawBody(ctx, p, "I-a", "r1")
			noErr(t, err)
			if !bytes.Equal(got, body) {
				t.Fatalf("GetDeliveryRawBody = %q", got)
			}
			wantErr(t, s.PutDeliveryRawBody(ctx, p, "I-a", "r-none", body, 50), domain.CodeNotFound, "Delivery")
			_, err = s.DeleteExpiredDeliveryRawBodies(ctx, 100, 0)
			wantErr(t, err, domain.CodeValidation, "")
			n, err := s.DeleteExpiredDeliveryRawBodies(ctx, 100, 1)
			noErr(t, err)
			if n != 1 {
				t.Fatalf("a sweep of limit 1 deleted %d", n)
			}
			_, err = s.GetDeliveryRawBody(ctx, p, "I-a", "r1")
			wantErr(t, err, domain.CodeNotFound, "DeliveryRawBody")
			n, err = s.DeleteExpiredDeliveryRawBodies(ctx, 100, 10)
			noErr(t, err)
			if n != 1 {
				t.Fatalf("the second sweep deleted %d, want r2 alone", n)
			}
			if _, err := s.GetDelivery(ctx, p, "I-a", "r1"); err != nil {
				t.Fatalf("the raw-body sweep took its delivery: %v", err)
			}
			if _, err := s.GetDeliveryRawBody(ctx, p, "I-a", "r3"); err != nil {
				t.Fatalf("an unexpired raw body went: %v", err)
			}
			// The deliveries' sweep takes r1..r3 (expired at 1100) and r3's raw body with r3.
			n, err = s.DeleteExpiredDeliveries(ctx, 1100, 10)
			noErr(t, err)
			if n != 3 {
				t.Fatalf("the delivery sweep deleted %d, want 3", n)
			}
			_, err = s.GetDeliveryRawBody(ctx, p, "I-a", "r3")
			wantErr(t, err, domain.CodeNotFound, "DeliveryRawBody")
		})

		t.Run("AProjectDeleteTakesItsOncallRows", func(t *testing.T) {
			s, _, p, b := seeded(t, newStore)
			create(t, s, p, b, "I-a", 1)
			_, _, err := s.InsertDelivery(ctx, delivery(p, "I-a", "d", 100, store.DeliveryRejected))
			noErr(t, err)
			noErr(t, s.PutDeliveryRawBody(ctx, p, "I-a", "d", []byte("b"), 500))
			noErr(t, s.DeleteProjectRecursive(ctx, p))
			all, err := s.ListIntegrations(ctx, p, 0, true)
			noErr(t, err)
			_, derr := s.GetDelivery(ctx, p, "I-a", "d")
			_, rerr := s.GetDeliveryRawBody(ctx, p, "I-a", "d")
			if len(all) != 0 {
				t.Fatalf("integrations outlived their project: %+v", all)
			}
			wantErr(t, derr, domain.CodeNotFound, "Delivery")
			wantErr(t, rerr, domain.CodeNotFound, "DeliveryRawBody")
		})
	})
}
