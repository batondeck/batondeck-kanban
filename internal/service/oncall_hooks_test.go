package service

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/oncall"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// genericBody is a generic-connector delivery.
func genericBody(fingerprint, status, title string, extra ...string) []byte {
	m := map[string]any{"fingerprint": fingerprint, "status": status, "severity": "critical", "title": title}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i]] = extra[i+1]
	}
	b, _ := json.Marshal(m)
	return b
}

// signedHook is a delivery signed in the generic connector's HMAC form at now.
func signedHook(secret string, now time.Time, body []byte) HookRequest {
	ts, sig := signedV0([]byte(secret), now, body)
	h := http.Header{}
	h.Set(HeaderTimestamp, ts)
	h.Set(HeaderSignature, sig)
	return HookRequest{Header: h.Get, Body: body}
}

func (f *fixture) hook(t *testing.T, id string, r HookRequest) HookOutcome {
	t.Helper()
	out, err := f.c.Hook(f.ctx, id, r)
	if err != nil {
		t.Fatalf("Hook: %v", err)
	}
	return out
}

func (f *fixture) deliveries(t *testing.T, iid string) []store.Delivery {
	t.Helper()
	page, err := f.st.ListDeliveries(f.ctx, f.project.ID, iid, store.DeliveryFilter{}, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	return page.Deliveries
}

// AC1: a delivery signed in the generic form files an incident ticket with the alert's title; the
// same body with one byte changed is refused, logged naming the integration, and recorded as a
// rejected delivery whose raw body is kept.
func TestHookFilesAnIncidentFromASignedDelivery(t *testing.T) {
	f, _, root := oncallFixture(t)
	created := f.integration(t, root, CreateIntegrationReq{})
	iid := created.Integration.ID
	body := genericBody("disk-db-1", "firing", "Disk full on db-1")
	out := f.hook(t, iid, signedHook(created.Secret, f.clock.now(), body))
	if out.Refusal != "" || len(out.Results) != 1 || out.Results[0].Result != "created" || out.Results[0].TaskID == nil {
		t.Fatalf("a signed delivery: %+v", out)
	}
	task, err := f.st.GetTask(f.ctx, f.project.ID, *out.Results[0].TaskID)
	if err != nil || task.Title != "Disk full on db-1" || task.Status != domain.Todo || task.CreatedBy != IngestActor(iid) {
		t.Fatalf("the incident ticket = %+v, %v", task, err)
	}
	items, err := f.st.ListContextItems(f.ctx, f.project.ID, task.ID, store.ContextItemsQuery{})
	if err != nil || len(items.Items) != 1 || !strings.HasPrefix(items.Items[0].Body, "**FIRING** Disk full on db-1") {
		t.Fatalf("the ticket's snapshot = %+v, %v", items, err)
	}

	tampered := signedHook(created.Secret, f.clock.now(), body)
	tampered.Body = append([]byte(nil), body...)
	tampered.Body[len(tampered.Body)-3] ^= 0x01
	before := f.taskCount(t)
	if out := f.hook(t, iid, tampered); out.Refusal != HookUnauthorized {
		t.Fatalf("one byte changed: %+v, want unauthorized", out)
	}
	if want := "ingest: integration " + iid + " refused a delivery's signature: signature_mismatch"; !strings.Contains(f.logs.String(), want) {
		t.Fatalf("no refusal line %q in %q", want, f.logs.String())
	}
	if f.taskCount(t) != before {
		t.Fatal("a refused delivery filed a ticket")
	}
	var rejected *store.Delivery
	for _, d := range f.deliveries(t, iid) {
		if d.Kind == store.DeliveryRejected {
			rejected = &d
		}
	}
	if rejected == nil || rejected.Error == nil || *rejected.Error != "signature_mismatch" || rejected.Result != store.ResultRejected || rejected.NormalizedEvent != nil {
		t.Fatalf("the rejected delivery = %+v", rejected)
	}
	raw, err := f.st.GetDeliveryRawBody(f.ctx, f.project.ID, iid, rejected.DeliveryID)
	if err != nil || string(raw) != string(tampered.Body) {
		t.Fatalf("the kept raw body = %q, %v", raw, err)
	}
}

// AC2 and the not-found refusals: a body over the integration's cap is too_large and writes nothing;
// an unknown, tombstoned or disabled integration is one flat not_found (each beside its control).
func TestHookRefusesBeforeWriting(t *testing.T) {
	f, _, root := oncallFixture(t)
	created := f.integration(t, root, CreateIntegrationReq{Limits: IntegrationLimitsIn{PayloadCapKb: 1}})
	iid := created.Integration.ID
	big := genericBody("f", "firing", "t", "description", strings.Repeat("x", 1024))
	if len(big) <= 1024 {
		t.Fatal("the fixture body is not over 1 KiB")
	}
	before := f.count(t)
	if out := f.hook(t, iid, signedHook(created.Secret, f.clock.now(), big)); out.Refusal != HookTooLarge {
		t.Fatalf("a body over the cap: %+v", out)
	}
	if after := f.count(t); after != before || len(f.deliveries(t, iid)) != 0 || f.taskCount(t) != 0 {
		t.Fatalf("a too-large body wrote rows: %+v -> %+v", before, after)
	}
	small := genericBody("f", "firing", "t")
	if out := f.hook(t, iid, signedHook(created.Secret, f.clock.now(), small)); out.Refusal != "" {
		t.Fatalf("a body under the cap (control): %+v", out)
	}
	if out := f.hook(t, "I-00000000000000000000000000000000", signedHook(created.Secret, f.clock.now(), small)); out.Refusal != HookNotFound {
		t.Fatalf("an unknown integration: %+v", out)
	}
	row, _ := f.st.GetIntegration(f.ctx, f.project.ID, iid)
	off := false
	if _, err := f.st.UpdateIntegration(f.ctx, f.project.ID, iid, row.Version, store.IntegrationPatch{Enabled: &off, UpdatedAt: f.clock.now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	n := len(f.deliveries(t, iid))
	if out := f.hook(t, iid, signedHook(created.Secret, f.clock.now(), genericBody("g", "firing", "t"))); out.Refusal != HookNotFound || len(f.deliveries(t, iid)) != n {
		t.Fatalf("a disabled integration: %+v", out)
	}
	if !strings.Contains(f.logs.String(), "ingest: refused a delivery to integration "+iid+": not_found: the integration is disabled") {
		t.Fatalf("no disabled refusal line in %q", f.logs.String())
	}
	other := f.integration(t, root, CreateIntegrationReq{Name: "two"})
	if _, err := f.c.Oncall().DeleteIntegration(f.ctx, root, DeleteIntegrationReq{ProjectID: f.project.ID, IntegrationID: other.Integration.ID, Version: 1}); err != nil {
		t.Fatal(err)
	}
	if out := f.hook(t, other.Integration.ID, signedHook(other.Secret, f.clock.now(), small)); out.Refusal != HookNotFound {
		t.Fatalf("a tombstoned integration: %+v", out)
	}
}

// AC2: ratePerMin 40 is a burst of 10, so the 11th delivery in it is refused and one after the
// refill window passes; requirement 6a: send_test_event and replay_delivery draw on the same budget.
func TestHookRateLimit(t *testing.T) {
	f, _, root := oncallFixture(t)
	created := f.integration(t, root, CreateIntegrationReq{Limits: IntegrationLimitsIn{RatePerMin: 40}})
	iid := created.Integration.ID
	send := func(i int) HookOutcome {
		return f.hook(t, iid, signedHook(created.Secret, f.clock.now(), genericBody(fmt.Sprintf("fp-%d", i), "firing", "t")))
	}
	for i := range oncall.Capacity(40) {
		if out := send(i); out.Refusal != "" {
			t.Fatalf("delivery %d of %d: %+v", i+1, oncall.Capacity(40), out)
		}
	}
	tasks := f.taskCount(t)
	// P-233: the refusal says when a token is back: ceil(60/40) seconds.
	if out := send(10); out.Refusal != HookRateLimited || out.RetryAfter != 2 || f.taskCount(t) != tasks {
		t.Fatalf("the 11th delivery: %+v (tasks %d -> %d)", out, tasks, f.taskCount(t))
	}
	// 6a: the tools are refused on the spent budget too; another integration's is its own.
	_, err := f.c.Oncall().SendTestEvent(f.ctx, root, SendTestEventReq{ProjectID: f.project.ID, IntegrationID: iid})
	wantCode(t, err, domain.CodeRateLimited)
	d := f.deliveries(t, iid)[0]
	_, err = f.c.Oncall().ReplayDelivery(f.ctx, root, ReplayDeliveryReq{ProjectID: f.project.ID, IntegrationID: iid, DeliveryID: d.DeliveryID})
	wantCode(t, err, domain.CodeRateLimited)
	f.clock.advance(1500 * time.Millisecond)
	if out := send(11); out.Refusal != "" {
		t.Fatalf("after the refill window: %+v", out)
	}

	// 6a the other way round: ten send_test_events spend the budget the webhook then finds empty.
	second := f.integration(t, root, CreateIntegrationReq{Name: "second", Limits: IntegrationLimitsIn{RatePerMin: 40}})
	for i := range oncall.Capacity(40) {
		if _, err := f.c.Oncall().SendTestEvent(f.ctx, root, SendTestEventReq{ProjectID: f.project.ID, IntegrationID: second.Integration.ID}); err != nil {
			t.Fatalf("send_test_event %d (control): %v", i+1, err)
		}
		f.clock.advance(time.Millisecond)
	}
	if out := f.hook(t, second.Integration.ID, signedHook(second.Secret, f.clock.now(), genericBody("x", "firing", "t"))); out.Refusal != HookRateLimited {
		t.Fatalf("a webhook after ten send_test_events: %+v", out)
	}
}

// A delivery whose id was recorded answers that outcome and files no second ticket. It writes
// nothing either: no change row (which would drop the project's board snapshots and ring every
// master session for no change), no event, no audit row.
func TestHookRepeatedDeliveryWritesNoSecondTask(t *testing.T) {
	f, _, root := oncallFixture(t)
	created := f.integration(t, root, CreateIntegrationReq{Lifecycle: "per_event"})
	iid := created.Integration.ID
	body := genericBody("fp", "firing", "t", "deliveryId", "src-1")
	first := f.hook(t, iid, signedHook(created.Secret, f.clock.now(), body))
	before := f.count(t)
	again := f.hook(t, iid, signedHook(created.Secret, f.clock.now(), body))
	if first.Refusal != "" || again.Refusal != "" || again.Results[0].DeliveryID != "src-1#fp" || !again.Results[0].Replayed ||
		*again.Results[0].TaskID != *first.Results[0].TaskID {
		t.Fatalf("first %+v, again %+v", first, again)
	}
	if n := f.taskCount(t); n != 1 {
		t.Fatalf("%d tickets, want 1", n)
	}
	if after := f.count(t); after != before {
		t.Fatalf("a replayed delivery wrote rows (events, audit, changes): %+v -> %+v", before, after)
	}
	// control: a new delivery id under per_event files a second ticket.
	f.hook(t, iid, signedHook(created.Secret, f.clock.now(), genericBody("fp", "firing", "t", "deliveryId", "src-2")))
	if n := f.taskCount(t); n != 2 {
		t.Fatalf("%d tickets after a new delivery id, want 2", n)
	}
}

// The race half of a replay: an ingest that finds its delivery id recorded only inside its
// transaction (another took the project lock first) marks the mutation NoChange, and mutate writes
// nothing for it; NoChange beside a recorded write is INTERNAL.
func TestMutateNoChangeWritesNothing(t *testing.T) {
	f := newFixture(t, Options{})
	before := f.count(t)
	got, err := mutate(f.ctx, f.c, f.alice, mutation{Tool: "ingest", ProjectID: f.project.ID, LockProject: true}, func(x *txn) (int, error) {
		x.NoChange()
		return 7, nil
	})
	if err != nil || got != 7 {
		t.Fatalf("a NoChange mutation = %d, %v", got, err)
	}
	if after := f.count(t); after != before {
		t.Fatalf("a NoChange mutation wrote rows: %+v -> %+v", before, after)
	}
	_, err = mutate(f.ctx, f.c, f.alice, mutation{Tool: "ingest", ProjectID: f.project.ID}, func(x *txn) (int, error) {
		x.BoardEvent(store.EventInput{BoardID: f.board.ID, Type: domain.EventIncidentDelivery, Data: json.RawMessage(`{}`)})
		x.NoChange()
		return 0, nil
	})
	wantCode(t, err, domain.CodeInternal)
	if after := f.count(t); after != before {
		t.Fatalf("a refused NoChange mutation wrote rows: %+v -> %+v", before, after)
	}
}

// A delivery id the server derives from the body's hash (the source sent none of its own) answers
// as a retry only while the delivery is one: within the retry window, and while it is still the
// alert's latest delivery. An uptime check that goes down, up and down again posts the same body
// twice, and the second outage files again; so does the same body after the window. Without alert
// state (per_event) the window alone decides. A retry of each new delivery is still a replay.
func TestHookBodyHashIDIsARetryOnlyWhileItIsOne(t *testing.T) {
	f, _, root := oncallFixture(t)
	key := map[string]string{HeaderAPIKey: "key-0123456789"}
	hdr := func(name string) string { return key[name] }
	integration := func(name, lifecycle string) string {
		return f.integration(t, root, CreateIntegrationReq{Name: name, Source: "webhook", Lifecycle: IntegrationLifecycle(lifecycle),
			Auth: IntegrationAuthInput{Mode: "api_key", APIKey: "key-0123456789"},
			Mapping: IntegrationMapping{Extract: MappingExtract{Fingerprint: []string{"monitor"}, Title: []string{"monitor"}, Status: []string{"status"}},
				StatusMap: map[string]AlertStatus{"down": "firing", "up": "resolved"}}}).Integration.ID
	}
	send := func(iid, body string) HookResult {
		t.Helper()
		out := f.hook(t, iid, HookRequest{Header: hdr, Body: []byte(body)})
		if out.Refusal != "" || len(out.Results) != 1 {
			t.Fatalf("%s: %+v", body, out)
		}
		f.clock.advance(time.Second)
		return out.Results[0]
	}
	const down, up = `{"monitor":"api","status":"down"}`, `{"monitor":"api","status":"up"}`
	auto := integration("auto", "autoclose")
	first := send(auto, down)
	if first.Result != "created" || first.Replayed {
		t.Fatalf("the first outage: %+v", first)
	}
	if r := send(auto, down); !r.Replayed || r.DeliveryID != first.DeliveryID || *r.TaskID != *first.TaskID {
		t.Fatalf("its retry: %+v, want the first replayed", r)
	}
	if r := send(auto, up); r.Result != "closed" || r.Replayed {
		t.Fatalf("the recovery: %+v", r)
	}
	second := send(auto, down)
	if second.Result != "created" || second.Replayed || *second.TaskID == *first.TaskID || second.DeliveryID == first.DeliveryID {
		t.Fatalf("the second outage, the same body after a recovery: %+v, want a new ticket", second)
	}
	if r := send(auto, down); !r.Replayed || r.DeliveryID != second.DeliveryID || *r.TaskID != *second.TaskID {
		t.Fatalf("the second outage's retry: %+v, want it replayed", r)
	}
	// A sender that posts no recovery: the same body after the retry window is a new delivery.
	f.clock.advance(retryWindow)
	later := send(auto, down)
	if later.Result != "appended" || later.Replayed || *later.TaskID != *second.TaskID {
		t.Fatalf("the same body after the retry window: %+v, want it appended to the open ticket", later)
	}
	if r := send(auto, down); !r.Replayed || r.DeliveryID != later.DeliveryID {
		t.Fatalf("its retry: %+v, want it replayed", r)
	}

	each := integration("each", "per_event")
	one := send(each, down)
	if r := send(each, down); one.Result != "created" || !r.Replayed || *r.TaskID != *one.TaskID {
		t.Fatalf("per_event: %+v then %+v, want created then replayed", one, r)
	}
	f.clock.advance(retryWindow)
	two := send(each, down)
	if two.Result != "created" || two.Replayed || *two.TaskID == *one.TaskID {
		t.Fatalf("per_event, the same body after the retry window: %+v, want a second ticket", two)
	}
	if r := send(each, down); !r.Replayed || *r.TaskID != *two.TaskID {
		t.Fatalf("per_event, its retry: %+v, want it replayed", r)
	}
}

// AC4: under autoclose a repeat firing appends to the open ticket, and the resolve deletes it while
// it is untouched (get_task NOT_FOUND; the delivery row keeps its taskId); a ticket someone wrote is
// annotated instead — also when an ingest append followed the write.
func TestHookCorrelatesAndAutocloses(t *testing.T) {
	f, _, root := oncallFixture(t)
	created := f.integration(t, root, CreateIntegrationReq{Lifecycle: "autoclose"})
	iid := created.Integration.ID
	deliver := func(fp, status, id string) HookResult {
		t.Helper()
		out := f.hook(t, iid, signedHook(created.Secret, f.clock.now(), genericBody(fp, status, "API down", "deliveryId", id)))
		if out.Refusal != "" || len(out.Results) != 1 {
			t.Fatalf("%s %s: %+v", fp, status, out)
		}
		return out.Results[0]
	}
	opened := deliver("api", "firing", "1")
	appended := deliver("api", "firing", "2")
	if opened.Result != "created" || appended.Result != "appended" || *appended.TaskID != *opened.TaskID {
		t.Fatalf("open %+v, repeat %+v", opened, appended)
	}
	task, _ := f.st.GetTask(f.ctx, f.project.ID, *opened.TaskID)
	if !strings.Contains(string(task.CustomFields), `"incident.occurrences":2`) {
		t.Fatalf("custom fields after the append: %s", task.CustomFields)
	}
	closed := deliver("api", "resolved", "3")
	if closed.Result != "closed" || *closed.TaskID != *opened.TaskID {
		t.Fatalf("the resolve: %+v", closed)
	}
	_, err := f.c.Tasks().Get(f.ctx, f.alice, GetTaskReq{ProjectID: f.project.ID, TaskID: *opened.TaskID})
	wantCode(t, err, domain.CodeNotFound)
	list, err := f.c.Oncall().ListDeliveries(f.ctx, root, ListDeliveriesReq{ProjectID: f.project.ID, IntegrationID: iid})
	var closedRow *Delivery
	for i, d := range list.Deliveries {
		if d.Result == "closed" {
			closedRow = &list.Deliveries[i]
		}
	}
	if err != nil || len(list.Deliveries) != 3 || closedRow == nil || closedRow.TaskID == nil || *closedRow.TaskID != *opened.TaskID {
		t.Fatalf("list_deliveries after the close: %+v, %v", list, err)
	}

	// Touched: a context item (add_comment is R2.1's) by alice, then the resolve annotates.
	touched := deliver("db", "firing", "4")
	if _, err := f.c.Context().AddItem(f.ctx, f.alice, AddContextItemReq{ProjectID: f.project.ID, TaskID: *touched.TaskID, Kind: "note", Body: "looking"}); err != nil {
		t.Fatal(err)
	}
	if r := deliver("db", "resolved", "5"); r.Result != "annotated" {
		t.Fatalf("the resolve of a touched ticket: %+v, want annotated", r)
	}
	kept, err := f.st.GetTask(f.ctx, f.project.ID, *touched.TaskID)
	if err != nil || !strings.Contains(string(kept.CustomFields), `"incident.alertStatus":"resolved"`) {
		t.Fatalf("the annotated ticket = %+v, %v", kept, err)
	}
	items, _ := f.st.ListContextItems(f.ctx, f.project.ID, kept.ID, store.ContextItemsQuery{})
	annotated := 0
	for _, it := range items.Items {
		if strings.HasPrefix(it.Body, "Alert resolved upstream at ") && it.Author == IngestActor(iid) {
			annotated++
		}
	}
	if len(items.Items) != 3 || annotated != 1 {
		t.Fatalf("the annotation: %+v", items.Items)
	}

	// The stamp cannot be laundered: a write, then an ingest append, then the resolve annotates.
	laundered := deliver("cache", "firing", "6")
	if _, err := f.c.Context().AddItem(f.ctx, f.alice, AddContextItemReq{ProjectID: f.project.ID, TaskID: *laundered.TaskID, Kind: "note", Body: "mine"}); err != nil {
		t.Fatal(err)
	}
	if r := deliver("cache", "firing", "7"); r.Result != "appended" {
		t.Fatalf("the repeat: %+v", r)
	}
	if r := deliver("cache", "resolved", "8"); r.Result != "annotated" {
		t.Fatalf("a resolve after write-then-append: %+v, want annotated", r)
	}
	// correlate never closes: an untouched ticket is annotated.
	corr := f.integration(t, root, CreateIntegrationReq{Name: "corr"})
	f.hook(t, corr.Integration.ID, signedHook(corr.Secret, f.clock.now(), genericBody("x", "firing", "t")))
	if out := f.hook(t, corr.Integration.ID, signedHook(corr.Secret, f.clock.now(), genericBody("x", "resolved", "t"))); out.Results[0].Result != "annotated" {
		t.Fatalf("correlate's resolve: %+v", out)
	}
	// a resolve with no open ticket is recorded.
	if out := f.hook(t, corr.Integration.ID, signedHook(corr.Secret, f.clock.now(), genericBody("never", "resolved", "t"))); out.Results[0].Result != "recorded" {
		t.Fatalf("a resolve of nothing: %+v", out)
	}
}

// AC3's routing: an integration routed on-call to a member rings that identity assigned; one
// routed to nobody rings the project broadcast. Both are change-log rows the device dispatcher reads.
func TestHookRoutesTheDoorbell(t *testing.T) {
	f, _, root := oncallFixture(t)
	if _, err := f.st.AddMember(f.ctx, f.project.ID, "bob", domain.RoleMember, "alice"); err != nil {
		t.Fatal(err)
	}
	routed := f.integration(t, root, CreateIntegrationReq{Routing: IntegrationRouting{Mode: "oncall", OnCallAgent: Value("bob")}})
	nobody := f.integration(t, root, CreateIntegrationReq{Name: "nobody"})
	for _, c := range []struct {
		in          CreateIntegrationResp
		kind, scope string
	}{{routed, DoorbellAssigned, "bob"}, {nobody, DoorbellBroadcast, BroadcastScope(f.project.ID)}} {
		out := f.hook(t, c.in.Integration.ID, signedHook(c.in.Secret, f.clock.now(), genericBody("fp", "firing", "t")))
		ch := f.changes(t)
		last := ch[len(ch)-1]
		var p changePayload
		_ = json.Unmarshal(last.Payload, &p)
		if out.Refusal != "" || p.Kind != c.kind || last.Scope != c.scope || p.TaskID != *out.Results[0].TaskID || p.Tool != "ingest" {
			t.Fatalf("%s: outcome %+v, change %+v %+v; want %s on %s", c.in.Integration.Name, out, last, p, c.kind, c.scope)
		}
	}
}

// Each auth mode verifies before a byte is parsed, beside its control: an api key header, Basic
// (which challenges), PagerDuty's raw-body form with its own secret, and a JWT bearer whose JWKS
// fetch is stubbed and logged as the one outbound call.
func TestHookVerifiesEachMode(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	set, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k", "n": b64url(key.N.Bytes()), "e": b64url(big.NewInt(int64(key.E)).Bytes())}}})
	f := newFixture(t, Options{Sealer: oncallKeyring(t), JWKSFetch: func(context.Context, string) ([]byte, error) { return set, nil }})
	root := f.alice
	root.WorkspaceAdmin = true
	hdr := func(kv ...string) func(string) string {
		h := http.Header{}
		for i := 0; i+1 < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return h.Get
	}
	body := genericBody("fp", "firing", "t")
	check := func(name string, iid string, good, bad HookRequest, challenge bool) {
		t.Helper()
		if out := f.hook(t, iid, bad); out.Refusal != HookUnauthorized || out.Challenge != challenge {
			t.Errorf("%s, bad credential: %+v", name, out)
		}
		if out := f.hook(t, iid, good); out.Refusal != "" {
			t.Errorf("%s, good credential (control): %+v", name, out)
		}
	}
	apiKey := f.integration(t, root, CreateIntegrationReq{Name: "k", Auth: IntegrationAuthInput{Mode: "api_key", APIKey: "key-0123456789"}})
	check("api_key", apiKey.Integration.ID, HookRequest{Header: hdr(HeaderAPIKey, "key-0123456789"), Body: body}, HookRequest{Header: hdr(HeaderAPIKey, "key-wrong"), Body: body}, false)
	basic := f.integration(t, root, CreateIntegrationReq{Name: "b", Source: "gcp", Auth: IntegrationAuthInput{Mode: "basic", APIKey: "pass-0123456789"}})
	gcpBody := []byte(`{"version":"1.2","incident":{"incident_id":"i-1","state":"open","policy_name":"p","started_at":1729764000}}`)
	enc := func(s string) string { return "Basic " + base64.StdEncoding.EncodeToString([]byte(s)) }
	check("basic", basic.Integration.ID, HookRequest{Header: hdr(HeaderAuthorize, enc("u:pass-0123456789")), Body: gcpBody}, HookRequest{Header: hdr(), Body: gcpBody}, true)
	pd := f.integration(t, root, CreateIntegrationReq{Name: "pd", Source: "pagerduty", Auth: IntegrationAuthInput{Mode: "hmac", ExternalSecret: "pd-secret-0123"}})
	pdBody := []byte(`{"event":{"id":"E1","event_type":"incident.triggered","data":{"id":"P1","title":"pd"}}}`)
	m := hmac.New(sha256.New, []byte("pd-secret-0123"))
	m.Write(pdBody)
	check("pagerduty", pd.Integration.ID, HookRequest{Header: hdr(HeaderPagerDuty, "v1=00,v1="+hex.EncodeToString(m.Sum(nil))), Body: pdBody}, HookRequest{Header: hdr(HeaderPagerDuty, "v1=00"), Body: pdBody}, false)
	cfg := IntegrationJWT{Issuer: "https://accounts.google.com", Audience: "batondeck", JWKSURL: "https://www.googleapis.com/oauth2/v3/certs"}
	jwtInt := f.integration(t, root, CreateIntegrationReq{Name: "j", Source: "gcp", Auth: IntegrationAuthInput{Mode: "jwt", JWT: cfg}})
	tok := func(aud string) string {
		h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k"})
		c, _ := json.Marshal(map[string]any{"iss": cfg.Issuer, "aud": aud, "exp": f.clock.now().Add(time.Hour).Unix()})
		in := b64url(h) + "." + b64url(c)
		d := sha256.Sum256([]byte(in))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, d[:])
		return "Bearer " + in + "." + b64url(sig)
	}
	check("jwt", jwtInt.Integration.ID, HookRequest{Header: hdr(HeaderAuthorize, tok("batondeck")), Body: gcpBody}, HookRequest{Header: hdr(HeaderAuthorize, tok("other")), Body: gcpBody}, false)
	if want := "ingest: outbound JWKS fetch GET " + cfg.JWKSURL + " for integration " + jwtInt.Integration.ID; !strings.Contains(f.logs.String(), want) {
		t.Errorf("no outbound-call line %q", want)
	}
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// P-210: a rotated-out secret that will not open no longer refuses every delivery — the current one
// verifies alone (logged); a current secret that will not open refuses, recorded as secret_unavailable.
func TestHookToleratesAnUnopenablePreviousSecret(t *testing.T) {
	f, _, root := oncallFixture(t)
	created := f.integration(t, root, CreateIntegrationReq{})
	iid := created.Integration.ID
	rot, err := f.c.Oncall().RotateIntegrationSecret(f.ctx, root, RotateIntegrationSecretReq{ProjectID: f.project.ID, IntegrationID: iid, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	corrupt := func(field string) {
		row, _ := f.st.GetIntegration(f.ctx, f.project.ID, iid)
		var a map[string]any
		if err := json.Unmarshal(row.Auth, &a); err != nil {
			t.Fatal(err)
		}
		h := a["hmac"].(map[string]any)
		env := h[field].(string)
		h[field] = env[:len(env)-4] + "AAAA"
		raw, _ := json.Marshal(a)
		if _, err := f.st.UpdateIntegration(f.ctx, f.project.ID, iid, row.Version, store.IntegrationPatch{Auth: raw, UpdatedAt: f.clock.now().UnixMilli()}); err != nil {
			t.Fatal(err)
		}
	}
	corrupt("previousEncrypted")
	if out := f.hook(t, iid, signedHook(rot.Secret, f.clock.now(), genericBody("a", "firing", "t"))); out.Refusal != "" {
		t.Fatalf("the current secret with an unopenable previous one: %+v\n%s", out, f.logs.String())
	}
	if !strings.Contains(f.logs.String(), "verifying with the current secret alone") {
		t.Fatalf("the fallback is not logged: %q", f.logs.String())
	}
	corrupt("currentEncrypted")
	if out := f.hook(t, iid, signedHook(rot.Secret, f.clock.now(), genericBody("b", "firing", "t"))); out.Refusal != HookUnauthorized {
		t.Fatalf("an unopenable current secret: %+v", out)
	}
	// The accepted row and the refusal share a timestamp: find the refusal by its kind.
	var refused *store.Delivery
	for _, d := range f.deliveries(t, iid) {
		if d.Kind == store.DeliveryRejected {
			refused = &d
		}
	}
	if refused == nil || *refused.Error != "secret_unavailable" {
		t.Fatalf("the refusal's row: %+v", refused)
	}
}

// A verified body that is not JSON, or not the source's shape, is a bad request recorded on the
// delivery log naming the field; a webhook mapping whose path cannot be evaluated is refused on save.
func TestHookRecordsUnparsableDeliveries(t *testing.T) {
	f, _, root := oncallFixture(t)
	created := f.integration(t, root, CreateIntegrationReq{})
	iid := created.Integration.ID
	if out := f.hook(t, iid, signedHook(created.Secret, f.clock.now(), []byte(`{not json`))); out.Refusal != HookBadRequest || out.Detail != "invalid_json" {
		t.Fatalf("invalid json: %+v", out)
	}
	if out := f.hook(t, iid, signedHook(created.Secret, f.clock.now(), []byte(`{"fingerprint":"f","status":"firing","severity":"s","title":"t","titel":"x"}`))); out.Refusal != HookBadRequest || !strings.Contains(out.Detail, "unknown keys: titel") {
		t.Fatalf("an unknown key: %+v", out)
	}
	errs := map[string]bool{}
	for _, d := range f.deliveries(t, iid) {
		errs[*d.Error] = true
	}
	if !errs["invalid_json"] || !errs["unrecognized_payload: generic payload: unknown keys: titel"] {
		t.Fatalf("the rejected rows' errors: %v", errs)
	}
	_, err := f.c.Oncall().CreateIntegration(f.ctx, root, CreateIntegrationReq{ProjectID: f.project.ID, BoardID: f.board.ID, Name: "w", Source: "webhook",
		Auth: IntegrationAuthInput{Mode: "hmac"}, Routing: IntegrationRouting{Mode: "broadcast"}, Mapping: IntegrationMapping{Extract: MappingExtract{Fingerprint: []string{"a[x]"}}}})
	if e := wantCode(t, err, domain.CodeValidation); !strings.Contains(e.Message, "extract.fingerprint[0]") {
		t.Fatalf("a bad mapping path: %v", e)
	}
	if _, err := f.c.Oncall().CreateIntegration(f.ctx, root, CreateIntegrationReq{ProjectID: f.project.ID, BoardID: f.board.ID, Name: "w", Source: "webhook",
		Auth: IntegrationAuthInput{Mode: "hmac"}, Routing: IntegrationRouting{Mode: "broadcast"}, Mapping: IntegrationMapping{Extract: MappingExtract{Fingerprint: []string{"a[0].b"}}}}); err != nil {
		t.Fatalf("a good mapping path (control): %v", err)
	}
}

// The ingest on both engines: a signed firing files the ticket, a repeat appends, the resolve under
// autoclose deletes it and the delivery keeps its taskId. The Postgres leg skips without
// BATONDECK_TEST_POSTGRES_DSN.
func TestHookOverTheEngines(t *testing.T) {
	for _, e := range []struct {
		name string
		open func(t *testing.T) store.Store
	}{
		{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }},
		{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }},
	} {
		t.Run(e.name, func(t *testing.T) {
			ctx := context.Background()
			st := e.open(t)
			c := New(st, Options{ToolCallFlush: -1, Sealer: oncallKeyring(t)})
			t.Cleanup(func() { _ = c.Close(context.Background()) })
			carol := domain.Caller{Identity: "carol", Agent: "claude", TokenScope: "carol", WorkspaceAdmin: true}
			p, err := c.Projects().Create(ctx, carol, CreateProjectReq{Name: "Q"})
			if err != nil {
				t.Fatal(err)
			}
			b, err := c.Boards().Create(ctx, carol, CreateBoardReq{ProjectID: p.ProjectID, Name: "B"})
			if err != nil {
				t.Fatal(err)
			}
			in, err := c.Oncall().CreateIntegration(ctx, carol, CreateIntegrationReq{ProjectID: p.ProjectID, BoardID: b.Board.ID, Name: "n", Source: "generic",
				Lifecycle: "autoclose", Auth: IntegrationAuthInput{Mode: "hmac"}, Routing: IntegrationRouting{Mode: "broadcast"}})
			if err != nil {
				t.Fatal(err)
			}
			results := []string{}
			var taskID string
			for i, status := range []string{"firing", "firing", "resolved"} {
				out, err := c.Hook(ctx, in.Integration.ID, signedHook(in.Secret, time.Now(), genericBody("fp", status, "t", "deliveryId", fmt.Sprint(i))))
				if err != nil || out.Refusal != "" {
					t.Fatalf("delivery %d: %+v, %v", i, out, err)
				}
				results = append(results, out.Results[0].Result)
				taskID = *out.Results[0].TaskID
			}
			if strings.Join(results, ",") != "created,appended,closed" {
				t.Fatalf("results %v", results)
			}
			if _, err := st.GetTask(ctx, p.ProjectID, taskID); err == nil {
				t.Fatal("the closed incident is still there")
			}
			page, err := st.ListDeliveries(ctx, p.ProjectID, in.Integration.ID, store.DeliveryFilter{}, "", 10)
			if err != nil || len(page.Deliveries) != 3 || page.Deliveries[0].TaskID == nil || *page.Deliveries[0].TaskID != taskID {
				t.Fatalf("deliveries %+v, %v", page, err)
			}
		})
	}
}

// P-109: the delivery and raw-body windows come from Options. A delivery, accepted or refused,
// expires DeliveryRetention after it was written and a refused delivery's raw body RawBodyRetention
// after, so the sweep's expiry-keyed deletes take them then and not a millisecond before.
func TestHookStampsTheConfiguredWindows(t *testing.T) {
	const day = 24 * time.Hour
	f := newFixture(t, Options{Sealer: oncallKeyring(t), DeliveryRetention: 2 * day, RawBodyRetention: day})
	root := f.alice
	root.WorkspaceAdmin = true
	created := f.integration(t, root, CreateIntegrationReq{})
	iid := created.Integration.ID
	body := genericBody("disk-db-1", "firing", "Disk full on db-1")
	if out := f.hook(t, iid, signedHook(created.Secret, f.clock.now(), body)); out.Refusal != "" {
		t.Fatalf("a signed delivery: %+v", out)
	}
	tampered := signedHook(created.Secret, f.clock.now(), body)
	tampered.Body = append([]byte(nil), body...)
	tampered.Body[len(tampered.Body)-3] ^= 0x01
	if out := f.hook(t, iid, tampered); out.Refusal != HookUnauthorized {
		t.Fatalf("one byte changed: %+v, want unauthorized", out)
	}
	ds := f.deliveries(t, iid)
	if len(ds) != 2 {
		t.Fatalf("%d deliveries, want the accepted one and the refused one", len(ds))
	}
	for _, d := range ds {
		if got := time.Duration(d.ExpiresAt-d.TS) * time.Millisecond; got != 2*day {
			t.Errorf("the %s delivery expires %s after it was written, want 2 days", d.Kind, got)
		}
	}
	now := f.clock.now()
	if n, err := f.st.DeleteExpiredDeliveryRawBodies(f.ctx, now.Add(day-time.Millisecond).UnixMilli(), 100); err != nil || n != 0 {
		t.Fatalf("raw bodies past their expiry a millisecond before 1 day: %d, %v; want 0", n, err)
	}
	if n, err := f.st.DeleteExpiredDeliveryRawBodies(f.ctx, now.Add(day).UnixMilli(), 100); err != nil || n != 1 {
		t.Fatalf("raw bodies past their expiry at 1 day: %d, %v; want the refused one", n, err)
	}
}

// P-231: the rate budget is charged before a delivery is verified, and a failed JWKS fetch is
// remembered, so junk bearers buy no outbound fetch past the budget; on both engines, with a JWKS
// fetch that fails for …/bad and serves a real set for …/good, counted. (a) At 40 a minute (a burst
// of 10) 12 junk bearers, each with its own body: 10 unauthorized (one fetch, then the remembered
// failure), 2 rate_limited before verification, 10 rejected rows. (b) At 1 a minute one junk bearer
// fetches once; at +31 s (the failure forgotten, the bucket at 0.52) the next is rate_limited
// unverified, so still one fetch. Controls: an hmac integration's 10 bad signatures are 10 rejected
// rows and the 11th is rate_limited (503 over HTTP; a rejection is charged once); a valid bearer on …/good is accepted.
func TestHookChargesBeforeVerifying(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	set, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k", "n": b64url(key.N.Bytes()), "e": b64url(big.NewInt(int64(key.E)).Bytes())}}})
	const bad, good = "https://jwks.example.com/bad", "https://jwks.example.com/good"
	for _, e := range []struct {
		name string
		open func(t *testing.T) store.Store
	}{
		{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }},
		{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }},
	} {
		t.Run(e.name, func(t *testing.T) {
			ctx := context.Background()
			st := e.open(t)
			clk := &clock{t: time.Now()}
			var mu sync.Mutex
			fetches := map[string]int{}
			fetch := func(_ context.Context, u string) ([]byte, error) {
				mu.Lock()
				defer mu.Unlock()
				fetches[u]++
				if u == good {
					return set, nil
				}
				return nil, errors.New("HTTP 500 from the JWKS host")
			}
			count := func(u string) int {
				mu.Lock()
				defer mu.Unlock()
				return fetches[u]
			}
			logs := &logBuf{}
			c := New(st, Options{ToolCallFlush: -1, Sealer: oncallKeyring(t), Now: clk.now, JWKSFetch: fetch, Logger: log.New(logs, "", 0)})
			t.Cleanup(func() { _ = c.Close(context.Background()) })
			root := domain.Caller{Identity: "carol", Agent: "claude", TokenScope: "carol", WorkspaceAdmin: true}
			p, err := c.Projects().Create(ctx, root, CreateProjectReq{Name: "Q"})
			if err != nil {
				t.Fatal(err)
			}
			b, err := c.Boards().Create(ctx, root, CreateBoardReq{ProjectID: p.ProjectID, Name: "B"})
			if err != nil {
				t.Fatal(err)
			}
			integration := func(name string, rate int64, a IntegrationAuthInput) CreateIntegrationResp {
				t.Helper()
				source := IntegrationSource("gcp")
				if a.Mode == "hmac" {
					source = "generic"
				}
				out, err := c.Oncall().CreateIntegration(ctx, root, CreateIntegrationReq{ProjectID: p.ProjectID, BoardID: b.Board.ID, Name: name, Source: source,
					Auth: a, Routing: IntegrationRouting{Mode: "broadcast"}, Limits: IntegrationLimitsIn{RatePerMin: rate}})
				if err != nil {
					t.Fatal(err)
				}
				return out
			}
			jwtAt := func(u string) IntegrationAuthInput {
				return IntegrationAuthInput{Mode: "jwt", JWT: IntegrationJWT{Issuer: "https://accounts.google.com", Audience: "batondeck", JWKSURL: u}}
			}
			gcp := func(i int) []byte {
				return fmt.Appendf(nil, `{"version":"1.2","incident":{"incident_id":"i-%d","state":"open","policy_name":"p","started_at":1729764000}}`, i)
			}
			bearer := func(tok string) func(string) string {
				h := http.Header{}
				h.Set(HeaderAuthorize, "Bearer "+tok)
				return h.Get
			}
			hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "junk"})
			junk := bearer(b64url(hdr) + ".e30.AAAA")
			hook := func(iid string, r HookRequest) HookOutcome {
				t.Helper()
				out, err := c.Hook(ctx, iid, r)
				if err != nil {
					t.Fatal(err)
				}
				return out
			}
			rejected := func(iid string) int {
				t.Helper()
				page, err := st.ListDeliveries(ctx, p.ProjectID, iid, store.DeliveryFilter{}, "", 200)
				if err != nil {
					t.Fatal(err)
				}
				n := 0
				for _, d := range page.Deliveries {
					if d.Result == store.ResultRejected {
						n++
					}
				}
				return n
			}

			// (a) 12 junk bearers at 40 a minute.
			a := integration("a", 40, jwtAt(bad))
			got := map[HookRefusal]int{}
			for i := range 12 {
				got[hook(a.Integration.ID, HookRequest{Header: junk, Body: gcp(i)}).Refusal]++
			}
			if got[HookUnauthorized] != 10 || got[HookRateLimited] != 2 || count(bad) != 1 || rejected(a.Integration.ID) != 10 {
				t.Fatalf("(a) refusals %v, fetches %d, rejected rows %d; want 10 unauthorized, 2 rate_limited, 1 fetch, 10 rows",
					got, count(bad), rejected(a.Integration.ID))
			}
			if want := "ingest: integration " + a.Integration.ID + " over its rate limit (40 a minute): a delivery refused before verification"; !strings.Contains(logs.String(), want) {
				t.Fatalf("no line %q", want)
			}
			if n := strings.Count(logs.String(), "ingest: outbound JWKS fetch GET "+bad); n != 1 {
				t.Fatalf("%d outbound-fetch lines, want 1", n)
			}

			// (b) 1 a minute: the second junk bearer, past the remembered failure, is not verified.
			clk.advance(time.Minute) // (a)'s failure is forgotten
			before := count(bad)
			one := integration("b", 1, jwtAt(bad))
			if out := hook(one.Integration.ID, HookRequest{Header: junk, Body: gcp(100)}); out.Refusal != HookUnauthorized || count(bad) != before+1 {
				t.Fatalf("(b) the first junk bearer: %+v, fetches %d (was %d)", out, count(bad), before)
			}
			clk.advance(31 * time.Second)
			if out := hook(one.Integration.ID, HookRequest{Header: junk, Body: gcp(101)}); out.Refusal != HookRateLimited || out.RetryAfter != 60 || count(bad) != before+1 {
				t.Fatalf("(b) at +31 s: %+v, fetches %d; want rate_limited unverified, fetches %d", out, count(bad), before+1)
			}

			// Control: an hmac integration's bad signatures, each charged once.
			h := integration("h", 40, IntegrationAuthInput{Mode: "hmac"})
			for i := range 10 {
				r := signedHook("whsec_not-the-secret", clk.now(), genericBody(fmt.Sprint("bad-", i), "firing", "t"))
				if out := hook(h.Integration.ID, r); out.Refusal != HookUnauthorized {
					t.Fatalf("bad signature %d of 10: %+v", i+1, out)
				}
			}
			if out := hook(h.Integration.ID, signedHook("whsec_not-the-secret", clk.now(), genericBody("bad-10", "firing", "t"))); out.Refusal != HookRateLimited || rejected(h.Integration.ID) != 10 {
				t.Fatalf("the 11th bad signature: %+v, rejected rows %d", out, rejected(h.Integration.ID))
			}

			// Control: a valid bearer on the good set is accepted, twice, on one fetch.
			ok := integration("ok", 40, jwtAt(good))
			for i := range 2 {
				hd, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k"})
				cl, _ := json.Marshal(map[string]any{"iss": "https://accounts.google.com", "aud": "batondeck", "exp": clk.now().Add(time.Hour).Unix()})
				in := b64url(hd) + "." + b64url(cl)
				d := sha256.Sum256([]byte(in))
				sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, d[:])
				if out := hook(ok.Integration.ID, HookRequest{Header: bearer(in + "." + b64url(sig)), Body: gcp(200 + i)}); out.Refusal != "" || len(out.Results) != 1 {
					t.Fatalf("valid bearer %d: %+v", i+1, out)
				}
			}
			if count(good) != 1 {
				t.Fatalf("the good set was fetched %d times, want 1", count(good))
			}
		})
	}
}
