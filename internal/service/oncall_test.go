package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/oncall"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func oncallKeyring(t *testing.T) *core.Keyring {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
	k, err := core.OpenKeyring("", func(n string) (string, bool) { return key, n == core.MasterKeyEnv })
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// oncallFixture is the fixture with a keyring sealer, and alice a workspace admin as well as the
// project's admin: the on-call tools are workspace-admin only.
func oncallFixture(t *testing.T) (*fixture, *core.Keyring, domain.Caller) {
	t.Helper()
	k := oncallKeyring(t)
	f := newFixture(t, Options{Sealer: k})
	root := f.alice
	root.WorkspaceAdmin = true
	return f, k, root
}

func (f *fixture) integration(t *testing.T, root domain.Caller, req CreateIntegrationReq) CreateIntegrationResp {
	t.Helper()
	if req.ProjectID == "" {
		req.ProjectID = f.project.ID
	}
	if req.BoardID == "" {
		req.BoardID = f.board.ID
	}
	if req.Name == "" {
		req.Name = "Alerts"
	}
	if req.Source == "" {
		req.Source = "generic"
	}
	if req.Auth.Mode == "" {
		req.Auth.Mode = "hmac"
	}
	if req.Routing.Mode == "" {
		req.Routing.Mode = "broadcast"
	}
	out, err := f.c.Oncall().CreateIntegration(f.ctx, root, req)
	if err != nil {
		t.Fatalf("create_integration: %v", err)
	}
	return out
}

func signedV0(secret []byte, now time.Time, body []byte) (string, string) {
	ts := strconv.FormatInt(now.Unix(), 10)
	m := hmac.New(sha256.New, secret)
	m.Write(append([]byte(ts+"."), body...))
	return ts, "v0=" + hex.EncodeToString(m.Sum(nil))
}

// TestOncallSecretIsSealedAndShownOnce (AC1): create_integration answers the minted secret once;
// the stored auth holds an enc1 envelope that opens to it under the keyring and never the
// plaintext; list_integrations carries no secret; an idempotent replay answers the same row and no
// secret (it was shown once).
func TestOncallSecretIsSealedAndShownOnce(t *testing.T) {
	f, k, root := oncallFixture(t)
	out := f.integration(t, root, CreateIntegrationReq{IdempotencyKey: "k-1"})
	if !strings.HasPrefix(out.Secret, "whsec_") || !bool(out.ShownOnce) || out.Integration.Auth.Mode != "hmac" || out.Integration.EndpointPath != "/hooks/"+out.Integration.ID {
		t.Fatalf("create_integration = %+v", out)
	}
	row, err := f.st.GetIntegration(f.ctx, f.project.ID, out.Integration.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(row.Auth, []byte(out.Secret)) {
		t.Fatalf("the stored auth carries the plaintext secret: %s", row.Auth)
	}
	var auth oncall.Auth
	if err := json.Unmarshal(row.Auth, &auth); err != nil {
		t.Fatal(err)
	}
	if auth.HMAC == nil || !strings.HasPrefix(auth.HMAC.Current, oncall.EnvelopePrefix) {
		t.Fatalf("the stored auth is not sealed: %s", row.Auth)
	}
	opened, err := oncall.Open(k, auth.HMAC.Current, oncall.SecretAAD(row.ID))
	if err != nil || string(opened) != out.Secret {
		t.Fatalf("the envelope opens to %q, %v; want the answered secret", opened, err)
	}
	t.Logf("stored auth: %.60s…", row.Auth)
	list, err := f.c.Oncall().ListIntegrations(f.ctx, root, ListIntegrationsReq{ProjectID: f.project.ID})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(list)
	if len(list.Integrations) != 1 || bytes.Contains(b, []byte(out.Secret)) || bytes.Contains(b, []byte("enc1:")) || bytes.Contains(b, []byte(`"secret"`)) {
		t.Fatalf("list_integrations carries secret material: %s", b)
	}
	again := f.integration(t, root, CreateIntegrationReq{IdempotencyKey: "k-1"})
	if again.Integration.ID != out.Integration.ID || again.Secret != "" || bool(again.ShownOnce) {
		t.Fatalf("the replay = %+v; want the same row and no secret", again)
	}
	other := f.integration(t, root, CreateIntegrationReq{Source: "grafana", Auth: IntegrationAuthInput{Mode: "api_key", APIKey: "fixture-key-123"}})
	r2, _ := f.st.GetIntegration(f.ctx, f.project.ID, other.Integration.ID)
	if other.Secret != "" || bytes.Contains(r2.Auth, []byte("fixture-key-123")) || !bytes.Contains(r2.Auth, []byte(oncall.HashAPIKey("fixture-key-123"))) {
		t.Fatalf("an api_key integration: answered %q, stored %s; want no secret answered and the key's hash stored", other.Secret, r2.Auth)
	}
}

// TestOncallRotationDualAccepts (AC2): after rotate_integration_secret, a delivery signed with the
// old secret verifies until RotationGrace after the rotation and is refused from then on; the new
// one verifies throughout; revokePrevious closes the window at once.
func TestOncallRotationDualAccepts(t *testing.T) {
	f, _, root := oncallFixture(t)
	created := f.integration(t, root, CreateIntegrationReq{})
	rotatedAt := f.clock.now()
	rot, err := f.c.Oncall().RotateIntegrationSecret(f.ctx, root, RotateIntegrationSecretReq{ProjectID: f.project.ID, IntegrationID: created.Integration.ID, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if rot.Secret == "" || rot.Secret == created.Secret || !bool(rot.ShownOnce) || rot.Integration.Auth.DualAcceptUntil == nil {
		t.Fatalf("rotate = %+v", rot)
	}
	row, _ := f.st.GetIntegration(f.ctx, f.project.ID, created.Integration.ID)
	body := []byte(`{"title":"disk full"}`)
	verify := func(secret string, at time.Time) bool {
		secrets, err := f.c.VerificationSecrets(row, at)
		if err != nil {
			t.Fatal(err)
		}
		ts, sig := signedV0([]byte(secret), at, body)
		_, ok := oncall.VerifyBatonDeck(body, ts, sig, secrets, at)
		return ok
	}
	for _, c := range []struct {
		name  string
		at    time.Time
		oldOK bool
	}{
		{"just after the rotation", rotatedAt.Add(time.Second), true},
		{"a minute before the window ends", rotatedAt.Add(oncall.RotationGrace - time.Minute), true},
		{"a second after the window ends", rotatedAt.Add(oncall.RotationGrace + time.Second), false},
	} {
		if got := verify(created.Secret, c.at); got != c.oldOK {
			t.Errorf("%s: the old secret verifies %v, want %v", c.name, got, c.oldOK)
		}
		if !verify(rot.Secret, c.at) {
			t.Errorf("%s: the new secret is refused", c.name)
		}
		t.Logf("%s: old secret %v, new secret true", c.name, c.oldOK)
	}
	revoked, err := f.c.Oncall().RotateIntegrationSecret(f.ctx, root, RotateIntegrationSecretReq{ProjectID: f.project.ID, IntegrationID: created.Integration.ID, Version: 2, RevokePrevious: true})
	if err != nil {
		t.Fatal(err)
	}
	row, _ = f.st.GetIntegration(f.ctx, f.project.ID, created.Integration.ID)
	if verify(rot.Secret, rotatedAt.Add(time.Second)) || !verify(revoked.Secret, rotatedAt.Add(time.Second)) || revoked.Integration.Auth.DualAcceptUntil != nil {
		t.Errorf("revokePrevious: the previous secret still verifies, or the new one does not (%+v)", revoked.Integration.Auth)
	}
}

// TestOncallTestEventAndReplay: send_test_event files a TODO ticket labelled incident and test and
// one delivery row; replay_delivery ingests it again under a new id; a delivery with no normalized
// event is refused; and a raw body is read until the retention sweep takes it, while the delivery
// is still listed (AC3).
func TestOncallTestEventAndReplay(t *testing.T) {
	f, _, root := oncallFixture(t)
	created := f.integration(t, root, CreateIntegrationReq{Routing: IntegrationRouting{Mode: "oncall", OnCallAgent: Value("bob")}})
	iid := created.Integration.ID
	sent, err := f.c.Oncall().SendTestEvent(f.ctx, root, SendTestEventReq{ProjectID: f.project.ID, IntegrationID: iid, Severity: "critical"})
	if err != nil {
		t.Fatal(err)
	}
	if sent.Result != "created" || sent.TaskID == nil || sent.Doorbell != DoorbellAssigned {
		t.Fatalf("send_test_event = %+v", sent)
	}
	task, err := f.st.GetTask(f.ctx, f.project.ID, *sent.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != domain.Todo || task.Priority != domain.PriorityUrgent || task.Assignee == nil || *task.Assignee != "bob" ||
		!strings.Contains(strings.Join(task.Labels, ","), "incident") || !strings.Contains(strings.Join(task.Labels, ","), "test") || task.CreatedBy != IngestActor(iid) {
		t.Fatalf("the incident ticket = %+v", task)
	}
	replay, err := f.c.Oncall().ReplayDelivery(f.ctx, root, ReplayDeliveryReq{ProjectID: f.project.ID, IntegrationID: iid, DeliveryID: sent.DeliveryID})
	if err != nil || replay.Result != "created" || replay.Replayed || !strings.HasPrefix(replay.NewDeliveryID, "replay:"+sent.DeliveryID+":") {
		t.Fatalf("replay_delivery = %+v, %v", replay, err)
	}
	refused := "refused-1"
	if _, _, err := f.st.InsertDelivery(f.ctx, store.Delivery{ProjectID: f.project.ID, IntegrationID: iid, DeliveryID: refused, TS: f.clock.now().UnixMilli(),
		Kind: store.DeliveryRejected, Result: store.ResultRejected, ExpiresAt: f.clock.now().Add(DeliveryRetention).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	_, err = f.c.Oncall().ReplayDelivery(f.ctx, root, ReplayDeliveryReq{ProjectID: f.project.ID, IntegrationID: iid, DeliveryID: refused})
	wantCode(t, err, domain.CodeValidation)

	now := f.clock.now()
	if err := f.st.PutDeliveryRawBody(f.ctx, f.project.ID, iid, refused, []byte(`{"forged":true}`), now.Add(RawBodyRetention).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	raw, err := f.c.Oncall().ReadDeliveryRaw(f.ctx, root, ReadDeliveryRawReq{ProjectID: f.project.ID, IntegrationID: iid, DeliveryID: refused})
	if err != nil || raw.PayloadBase64 == nil || *raw.PayloadBase64 != base64.StdEncoding.EncodeToString([]byte(`{"forged":true}`)) || raw.SuggestedFilename != "delivery-refused-1.txt" {
		t.Fatalf("read_delivery_raw = %+v, %v", raw, err)
	}
	if n, err := f.st.DeleteExpiredDeliveryRawBodies(f.ctx, now.Add(RawBodyRetention-time.Millisecond).UnixMilli(), 100); err != nil || n != 0 {
		t.Fatalf("a sweep before the retention took %d raw bodies (%v)", n, err)
	}
	if n, err := f.st.DeleteExpiredDeliveryRawBodies(f.ctx, now.Add(RawBodyRetention).UnixMilli(), 100); err != nil || n != 1 {
		t.Fatalf("the sweep at the retention took %d raw bodies (%v), want 1", n, err)
	}
	_, err = f.c.Oncall().ReadDeliveryRaw(f.ctx, root, ReadDeliveryRawReq{ProjectID: f.project.ID, IntegrationID: iid, DeliveryID: refused})
	wantCode(t, err, domain.CodeNotFound)
	list, err := f.c.Oncall().ListDeliveries(f.ctx, root, ListDeliveriesReq{ProjectID: f.project.ID, IntegrationID: iid})
	if err != nil || len(list.Deliveries) != 3 {
		t.Fatalf("list_deliveries = %+v, %v; want the test event, its replay and the refusal", list, err)
	}
	kinds := map[DeliveryKind]int{}
	for _, d := range list.Deliveries {
		kinds[d.Kind]++
	}
	if kinds["test"] != 2 || kinds["rejected"] != 1 {
		t.Fatalf("delivery kinds %v", kinds)
	}
}

// TestOncallRefusals: each declared refusal of the on-call service, beside its positive control.
func TestOncallRefusals(t *testing.T) {
	f, _, root := oncallFixture(t)
	o := f.c.Oncall()
	p, b := f.project.ID, f.board.ID
	created := f.integration(t, root, CreateIntegrationReq{})
	iid := created.Integration.ID
	base := func() CreateIntegrationReq {
		return CreateIntegrationReq{ProjectID: p, BoardID: b, Name: "n", Source: "generic", Auth: IntegrationAuthInput{Mode: "hmac"}, Routing: IntegrationRouting{Mode: "broadcast"}}
	}
	for _, c := range []struct {
		name string
		mut  func(*CreateIntegrationReq)
		code domain.Code
	}{
		{"mtls is reserved", func(r *CreateIntegrationReq) { r.Auth.Mode = "mtls" }, domain.CodeValidation},
		{"PagerDuty without its own secret", func(r *CreateIntegrationReq) { r.Source = "pagerduty" }, domain.CodeValidation},
		{"api_key without a key", func(r *CreateIntegrationReq) { r.Auth.Mode = "api_key" }, domain.CodeValidation},
		{"jwt without its issuer", func(r *CreateIntegrationReq) { r.Auth.Mode = "jwt" }, domain.CodeValidation},
		{"an extract mapping on a vendor source", func(r *CreateIntegrationReq) { r.Mapping.Extract.Title = []string{"a.b"} }, domain.CodeValidation},
		{"an unknown source", func(r *CreateIntegrationReq) { r.Source = "splunk" }, domain.CodeValidation},
		{"a rate over 6000", func(r *CreateIntegrationReq) { r.Limits.RatePerMin = 6001 }, domain.CodeValidation},
		{"a missing board", func(r *CreateIntegrationReq) { r.BoardID = "B-none" }, domain.CodeNotFound},
		{"a column off the board", func(r *CreateIntegrationReq) { r.ColumnID = "C-none" }, domain.CodeNotFound},
	} {
		req := base()
		c.mut(&req)
		_, err := o.CreateIntegration(f.ctx, root, req)
		if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != c.code {
			t.Errorf("%s: %v, want %s", c.name, err, c.code)
		}
	}
	// The controls: PagerDuty with its own secret (never echoed), and the webhook source's extract.
	pd := base()
	pd.Source, pd.Auth.ExternalSecret = "pagerduty", "pd-fixture-secret"
	if out, err := o.CreateIntegration(f.ctx, root, pd); err != nil || out.Secret != "" || !out.Integration.Auth.ExternallyIssued {
		t.Errorf("PagerDuty with externalSecret = %+v, %v; want created, the secret not echoed", out, err)
	}
	wh := base()
	wh.Source, wh.Mapping.Extract.Title = "webhook", []string{"alert.name"}
	if _, err := o.CreateIntegration(f.ctx, root, wh); err != nil {
		t.Errorf("a webhook source's extract mapping: %v", err)
	}

	// Workspace admin first, then membership, then the tier.
	alice := f.alice
	_, err := o.ListIntegrations(f.ctx, alice, ListIntegrationsReq{ProjectID: p})
	if e := wantCode(t, err, domain.CodeForbidden); e.Details["requiredCapability"] != "workspace-admin" {
		t.Errorf("a non-admin's list_integrations: %+v", e)
	}
	outsider := domain.Caller{Identity: "mallory", TokenScope: "mallory", WorkspaceAdmin: true}
	_, err = o.ListIntegrations(f.ctx, outsider, ListIntegrationsReq{ProjectID: p})
	wantCode(t, err, domain.CodeForbidden)
	worker := f.member(t, "wendy", domain.RoleMaster)
	worker.WorkspaceAdmin = true
	_, err = o.RotateIntegrationSecret(f.ctx, worker, RotateIntegrationSecretReq{ProjectID: p, IntegrationID: iid, Version: 1})
	wantCode(t, err, domain.CodeForbidden)
	if _, err := o.ListIntegrations(f.ctx, worker, ListIntegrationsReq{ProjectID: p}); err != nil {
		t.Errorf("a master workspace admin's list_integrations (master tier): %v", err)
	}

	_, err = o.UpdateIntegration(f.ctx, root, UpdateIntegrationReq{ProjectID: p, IntegrationID: iid, Version: 9, Patch: IntegrationPatch{Name: "x"}})
	wantCode(t, err, domain.CodeStale)
	_, err = o.UpdateIntegration(f.ctx, root, UpdateIntegrationReq{ProjectID: p, IntegrationID: "I-none", Version: 1, Patch: IntegrationPatch{Name: "x"}})
	wantCode(t, err, domain.CodeNotFound)
	up, err := o.UpdateIntegration(f.ctx, root, UpdateIntegrationReq{ProjectID: p, IntegrationID: iid, Version: 1, Patch: IntegrationPatch{Enabled: Some(false), Limits: IntegrationLimitsIn{RatePerMin: 5}}})
	if err != nil || up.Integration.Enabled || up.Integration.Limits.RatePerMin != 5 || up.Integration.Limits.PayloadCapKb != DefaultPayloadCapKb || up.Integration.Version != 2 {
		t.Fatalf("update_integration = %+v, %v", up, err)
	}
	key := f.integration(t, root, CreateIntegrationReq{Source: "grafana", Auth: IntegrationAuthInput{Mode: "api_key", APIKey: "fixture-key-123"}})
	_, err = o.RotateIntegrationSecret(f.ctx, root, RotateIntegrationSecretReq{ProjectID: p, IntegrationID: key.Integration.ID, Version: 1})
	wantCode(t, err, domain.CodeValidation)
	_, err = o.SendTestEvent(f.ctx, root, SendTestEventReq{ProjectID: p, IntegrationID: "I-none"})
	wantCode(t, err, domain.CodeNotFound)
	_, err = o.ReadDeliveryRaw(f.ctx, root, ReadDeliveryRawReq{ProjectID: p, IntegrationID: iid, DeliveryID: "d-none"})
	wantCode(t, err, domain.CodeNotFound)
	_, err = o.ListDeliveries(f.ctx, root, ListDeliveriesReq{ProjectID: p, IntegrationID: iid, Limit: 201})
	wantCode(t, err, domain.CodeValidation)

	_, err = o.DeleteIntegration(f.ctx, root, DeleteIntegrationReq{ProjectID: p, IntegrationID: iid, Version: 1})
	wantCode(t, err, domain.CodeStale)
	del, err := o.DeleteIntegration(f.ctx, root, DeleteIntegrationReq{ProjectID: p, IntegrationID: iid, Version: 2})
	if err != nil || !bool(del.Deleted) || del.Integration.DeletedAt == "" {
		t.Fatalf("delete_integration = %+v, %v", del, err)
	}
	_, err = o.DeleteIntegration(f.ctx, root, DeleteIntegrationReq{ProjectID: p, IntegrationID: iid, Version: 3})
	wantCode(t, err, domain.CodeNotFound)
	_, err = o.SendTestEvent(f.ctx, root, SendTestEventReq{ProjectID: p, IntegrationID: iid})
	wantCode(t, err, domain.CodeNotFound)

	audit, _ := f.st.ListAuditLog(f.ctx, store.AuditFilter{}, "", 50)
	actions := map[string]int{}
	for _, r := range audit.Rows {
		actions[r.Action]++
		if bytes.Contains(r.Data, []byte("whsec_")) || bytes.Contains(r.Data, []byte("fixture")) {
			t.Errorf("an audit row carries secret material: %s", r.Data)
		}
	}
	if actions["integration.created"] < 3 || actions["integration.updated"] != 1 || actions["integration.deleted"] != 1 {
		t.Errorf("audit actions %v", actions)
	}
}
