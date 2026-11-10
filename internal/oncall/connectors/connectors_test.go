package connectors_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/oncall"
	"github.com/batondeck/batondeck-kanban/internal/oncall/auth"
	"github.com/batondeck/batondeck-kanban/internal/oncall/connectors"
)

var now = time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC)

// run decodes testdata/<source>.json and runs its connector: the events and the source delivery id.
func run(t *testing.T, source string, m *connectors.Mapping) ([]oncall.Event, string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", source+".json"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := connectors.Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := connectors.For(source)
	if !ok {
		t.Fatalf("no connector for %s", source)
	}
	ctx := connectors.Context{Mapping: m, BodyHash: connectors.Sha256Hex(body)[:32], Now: now}
	evs, err := c.Events(p, ctx)
	if err != nil {
		t.Fatalf("%s fixture: %v", source, err)
	}
	for _, e := range evs {
		if e.Source != source || e.Fingerprint == "" || e.Title == "" || e.Severity == "" || e.OccurredAt == "" || e.Labels == nil || e.Annotations == nil || e.Links == nil {
			t.Fatalf("%s: an event misses a required field: %+v", source, e)
		}
	}
	return evs, c.DeliveryID(p, ctx)
}

func want(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", what, got, want)
	}
}

// The registry and the catalog name the same eight sources.
func TestEverySourceHasAConnector(t *testing.T) {
	for _, id := range oncall.SourceIDs() {
		if _, ok := connectors.For(id); !ok {
			t.Errorf("catalog source %s has no connector", id)
		}
	}
	if _, ok := connectors.For("nagios"); ok {
		t.Error("a source outside the catalog has a connector")
	}
	if n := len(oncall.SourceIDs()); n != 8 {
		t.Errorf("the catalog has %d sources, want 8", n)
	}
}

func TestAlertmanager(t *testing.T) {
	evs, id := run(t, "alertmanager", nil)
	want(t, "events", len(evs), 2)
	// groupKey names the group, the same on every notification of it: each event carries its own key.
	want(t, "delivery id (none: groupKey repeats)", id, "")
	want(t, "firing key", evs[0].DeliveryKey, "firing@2026-10-24T08:00:00.000Z")
	want(t, "resolved key", evs[1].DeliveryKey, "resolved@2026-10-24T07:00:00.000Z..2026-10-24T07:45:00.000Z")
	e := evs[0]
	want(t, "fingerprint", e.Fingerprint, "a1b2c3d4e5f60718")
	want(t, "status", e.Status, "firing")
	want(t, "severity", e.Severity, "critical")
	want(t, "title", e.Title, "p99 latency above 2s on api")
	want(t, "description", e.Description, "p99 has been over 2s for 10 minutes")
	want(t, "links", e.Links, []string{"http://prometheus:9090/graph?g0.expr=latency"})
	want(t, "occurredAt", e.OccurredAt, "2026-10-24T08:00:00.000Z")
	r := evs[1]
	want(t, "resolved status", r.Status, "resolved")
	want(t, "resolved title falls back to alertname", r.Title, "DiskFull")
	want(t, "resolved occurredAt is endsAt", r.OccurredAt, "2026-10-24T07:45:00.000Z")
}

func TestGrafana(t *testing.T) {
	evs, id := run(t, "grafana", nil)
	want(t, "events", len(evs), 1)
	want(t, "delivery id (none: groupKey repeats)", id, "")
	e := evs[0]
	want(t, "no per-event key (the body's hash, which changes with the values)", e.DeliveryKey, "")
	want(t, "fingerprint", e.Fingerprint, "5f0c2d1e9a8b7c6d")
	want(t, "title", e.Title, "CPU above 90% on web-1")
	want(t, "severity", e.Severity, "high")
	want(t, "links (empty panelURL dropped)", e.Links, []string{"https://grafana.example.com/alerting/grafana/abc/view", "https://grafana.example.com/alerting/silence/new", "https://grafana.example.com/d/xyz"})
}

func TestDatadog(t *testing.T) {
	evs, id := run(t, "datadog", nil)
	want(t, "delivery id", id, "")
	e := evs[0]
	want(t, "fingerprint keeps a numeric id's digits", e.Fingerprint, "7001234567")
	want(t, "status", e.Status, "firing")
	want(t, "severity", e.Severity, "P1")
	want(t, "labels", e.Labels, map[string]string{"env": "prod", "service": "checkout", "team": "payments"})
	want(t, "annotations", e.Annotations, map[string]string{"datadog_transition": "triggered", "datadog_event_type": "query_alert_monitor"})
	want(t, "description", e.Description, "Error rate is 7.2% (threshold 5%)")
	want(t, "occurredAt", e.OccurredAt, "2026-10-24T10:00:00.000Z")
}

func TestDynatrace(t *testing.T) {
	evs, _ := run(t, "dynatrace", nil)
	e := evs[0]
	want(t, "fingerprint", e.Fingerprint, "-1234567890123456789_1729760000000V2")
	want(t, "status", e.Status, "firing")
	want(t, "severity", e.Severity, "performance")
	want(t, "title", e.Title, "Response time degradation")
	want(t, "impacted", e.Annotations["impacted_entities"], "checkout, cart")
	want(t, "state", e.Annotations["dynatrace_state"], "OPEN")
	want(t, "occurredAt is now when absent", e.OccurredAt, "2026-10-24T13:00:00.000Z")
}

func TestGeneric(t *testing.T) {
	evs, id := run(t, "generic", nil)
	want(t, "delivery id", id, "gen-0001")
	e := evs[0]
	want(t, "event", e, oncall.Event{Source: "generic", Fingerprint: "checkout-latency", Status: "firing", Severity: "critical",
		Title: "Checkout latency over budget", Description: "p95 above 800ms for 5 minutes", Labels: map[string]string{"env": "prod", "service": "checkout"},
		Annotations: map[string]string{"runbook": "https://runbooks.example.com/checkout"}, Links: []string{"https://grafana.example.com/d/checkout"},
		OccurredAt: "2026-10-24T11:00:00.000Z"})
	// The one strict connector: an unknown key is refused by name.
	c, _ := connectors.For("generic")
	_, gerr := c.Events(map[string]any{"fingerprint": "f", "status": "firing", "severity": "s", "title": "t", "titel": "x"}, connectors.Context{Now: now})
	if gerr == nil || !strings.Contains(gerr.Error(), "unknown keys: titel") {
		t.Errorf("an unknown key: %v, want it refused by name", gerr)
	}
	_, gerr = c.Events(map[string]any{"fingerprint": "f", "status": "maybe", "severity": "s", "title": "t"}, connectors.Context{Now: now})
	if gerr == nil {
		t.Error("an unknown status was accepted")
	}
}

func TestPagerDuty(t *testing.T) {
	evs, id := run(t, "pagerduty", nil)
	want(t, "delivery id", id, "01DEN1HNLBC1VITYLBI7MKHAYW")
	e := evs[0]
	want(t, "fingerprint", e.Fingerprint, "PGR0VU2")
	want(t, "status", e.Status, "firing")
	want(t, "severity (urgency high)", e.Severity, "critical")
	want(t, "upstream assignee", e.UpstreamAssignee, "PTUXL6G")
	want(t, "labels", e.Labels, map[string]string{"urgency": "high", "pagerduty_service": "API Service", "pagerduty_priority": "P1"})
	want(t, "links", e.Links, []string{"https://acme.pagerduty.com/incidents/PGR0VU2"})

	// A subscription may carry every event type: only an incident event whose data is the incident
	// is an alert. A note (its own id, the incident's in a reference), a service event and the
	// subscription's test ping are accepted as no event, never filed as a new incident nor refused.
	c, _ := connectors.For("pagerduty")
	for _, name := range []string{"pagerduty_annotated", "pagerduty_ping", "service.updated"} {
		body := []byte(`{"event":{"id":"01DEN1HNLBC1VITYLBI7MKHB03","event_type":"service.updated","resource_type":"service","data":{"id":"PF9KMXH","type":"service","name":"API Service"}}}`)
		if name != "service.updated" {
			var err error
			if body, err = os.ReadFile(filepath.Join("testdata", name+".json")); err != nil {
				t.Fatal(err)
			}
		}
		p, err := connectors.Decode(body)
		if err != nil {
			t.Fatal(err)
		}
		evs, err := c.Events(p, connectors.Context{Now: now})
		if err != nil || len(evs) != 0 {
			t.Errorf("%s: %d events, %v; want none and no error", name, len(evs), err)
		}
	}
	// control: an incident event with no data.type (an older payload) still maps by data.id.
	p, _ := connectors.Decode([]byte(`{"event":{"id":"E2","event_type":"incident.acknowledged","data":{"id":"PGR0VU2","title":"t"}}}`))
	if evs, err := c.Events(p, connectors.Context{Now: now}); err != nil || len(evs) != 1 || evs[0].Fingerprint != "PGR0VU2" || evs[0].Status != "firing" {
		t.Errorf("an incident event without data.type: %+v, %v", evs, err)
	}
}

// gcp: the fixture normalized, and the bearer it arrives with verified against a JWKS whose fetch is
// stubbed — the one outbound call ingest makes, whose log line is asserted.
func TestGCP(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const jwksURL = "https://www.googleapis.com/oauth2/v3/certs"
	var fetched []string
	var logged []string
	v := &auth.Verifier{
		Fetch: func(_ context.Context, u string) ([]byte, error) {
			fetched = append(fetched, u)
			return jwks(key, "k1"), nil
		},
		Logf: func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) },
		Now:  func() time.Time { return now },
	}
	cfg := auth.JWT{Issuer: "https://accounts.google.com", Audience: "https://kanban.example.com/hooks/I-gcp", JWKSURL: jwksURL}
	tok := signRS256(t, key, "k1", map[string]any{"iss": cfg.Issuer, "aud": cfg.Audience, "exp": now.Add(time.Hour).Unix(), "sub": "monitoring"})
	if reason, ok := v.Verify(context.Background(), "Bearer "+tok, cfg, "I-gcp"); !ok {
		t.Fatalf("a Google-signed bearer was refused: %s", reason)
	}
	want(t, "fetches", fetched, []string{jwksURL})
	if !slices.Contains(logged, "ingest: outbound JWKS fetch GET "+jwksURL+" for integration I-gcp") {
		t.Fatalf("the outbound call was not logged: %q", logged)
	}
	if reason, ok := v.Verify(context.Background(), "Bearer "+tok, cfg, "I-gcp"); !ok || len(fetched) != 1 {
		t.Fatalf("a second verify: %s, %d fetches; want ok from the cached set", reason, len(fetched))
	}

	evs, id := run(t, "gcp", nil)
	want(t, "delivery id", id, "0.mx1abcdefghi:open")
	e := evs[0]
	want(t, "fingerprint is the incident id", e.Fingerprint, "0.mx1abcdefghi")
	want(t, "status", e.Status, "firing")
	want(t, "title is the policy name", e.Title, "api — 5xx responses")
	want(t, "'No severity' is absent", e.Severity, "warning")
	want(t, "occurredAt from started_at", e.OccurredAt, "2024-10-24T10:00:00.000Z")
	want(t, "labels", e.Labels["resource.service_name"]+"|"+e.Labels["metric.response_code_class"]+"|"+e.Labels["user.team"], "api|5xx|platform")
	want(t, "annotations", e.Annotations["observed_value"]+"|"+e.Annotations["threshold_value"], "30.000|3")
}

func TestWebhook(t *testing.T) {
	m := &connectors.Mapping{
		Extract: &connectors.Extract{Events: "checks", Fingerprint: []string{"id"}, Title: []string{"name"}, Severity: []string{"level"},
			Status: []string{"state"}, Links: []string{"url"}, Labels: "tags", OccurredAt: []string{"at"}, DedupKey: []string{"$.source"}},
	}
	evs, id := run(t, "webhook", m)
	want(t, "events", len(evs), 2)
	want(t, "delivery id from dedupKey", id, "homegrown-monitor")
	want(t, "first", evs[0], oncall.Event{Source: "webhook", Fingerprint: "chk-db", Status: "firing", Severity: "critical", Title: "Database reachable",
		Labels: map[string]string{"env": "prod", "tier": "data"}, Annotations: map[string]string{}, Links: []string{"https://status.example.com/chk-db"},
		OccurredAt: "2024-10-24T12:00:00.000Z"})
	want(t, "second status (ok is resolved)", evs[1].Status, "resolved")
	want(t, "a javascript: link is dropped", evs[1].Links, []string{})
	want(t, "epoch milliseconds", evs[1].OccurredAt, "2024-10-24T12:00:00.000Z")

	c, _ := connectors.For("webhook")
	if _, err := c.Events(map[string]any{}, connectors.Context{Now: now}); err == nil || !strings.Contains(err.Error(), "no field mapping") {
		t.Errorf("no mapping: %v, want the setup refusal", err)
	}
	// Only colliding fingerprints are numbered: one alert keeps one fingerprint in any batch.
	same := &connectors.Mapping{Extract: &connectors.Extract{Events: "a", Fingerprint: []string{"k"}}}
	got, err := c.Events(map[string]any{"a": []any{map[string]any{"k": "x"}, map[string]any{"k": "y"}, map[string]any{"k": "x"}}}, connectors.Context{Mapping: same, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	want(t, "fingerprints", []string{got[0].Fingerprint, got[1].Fingerprint, got[2].Fingerprint}, []string{"x#0", "y", "x#1"})
}

// Cap drops every NUL (U+0000) from what an event keeps, which Postgres cannot store in text: a key
// that was nothing but NULs names nothing and goes.
func TestCapDropsNUL(t *testing.T) {
	e := connectors.Cap(oncall.Event{Source: "s", Fingerprint: "f\x00p", Status: "firing", Severity: "hi\x00gh", Title: "a\x00b",
		Description: "\x00d", Labels: map[string]string{"k\x00": "v\x00", "\x00": "gone"}, Annotations: map[string]string{"a": "\x00"},
		Links: []string{"https://x.example/\x00"}, UpstreamAssignee: "P\x001", OccurredAt: "2026-10-24T13:00:00.000Z", DeliveryKey: "k\x00"})
	want(t, "capped", e, oncall.Event{Source: "s", Fingerprint: "fp", Status: "firing", Severity: "high", Title: "ab", Description: "d",
		Labels: map[string]string{"k": "v"}, Annotations: map[string]string{"a": ""}, Links: []string{"https://x.example/"}, UpstreamAssignee: "P1",
		OccurredAt: "2026-10-24T13:00:00.000Z", DeliveryKey: "k"})
}

func TestValidateMapping(t *testing.T) {
	ok := connectors.Mapping{Extract: &connectors.Extract{Fingerprint: []string{"a.b[0].c", "$body_hash"}, Events: "$.items"}}
	if p := connectors.ValidateMapping(ok); len(p) != 0 {
		t.Fatalf("a valid mapping: %v", p)
	}
	for path, wantWhy := range map[string]string{
		"a[x]":                         "malformed path",
		"a[0]b":                        "malformed path",
		"$env.HOME":                    "unknown sigil",
		"$.":                           "must be followed",
		strings.Repeat("a.", 13) + "a": "deeper than 12",
		strings.Repeat("a", 201):       "exceeds 200",
	} {
		p := connectors.ValidateMapping(connectors.Mapping{Extract: &connectors.Extract{Title: []string{path}}})
		if len(p) != 1 || !strings.Contains(p[0], wantWhy) || !strings.Contains(p[0], "extract.title[0]") {
			t.Errorf("path %q: %v, want one problem naming extract.title[0] and %q", path, p, wantWhy)
		}
	}
	if p := connectors.ValidateMapping(connectors.Mapping{Extract: &connectors.Extract{Labels: "$now"}}); len(p) != 1 || !strings.Contains(p[0], "not a container") {
		t.Errorf("labels at a sigil: %v", p)
	}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func jwks(k *rsa.PrivateKey, kid string) []byte {
	b, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
		"n": b64(k.N.Bytes()), "e": b64(big.NewInt(int64(k.E)).Bytes())}}})
	return b
}

func signRS256(t *testing.T, k *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	c, _ := json.Marshal(claims)
	in := b64(h) + "." + b64(c)
	d := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, d[:])
	if err != nil {
		t.Fatal(err)
	}
	return in + "." + b64(sig)
}
