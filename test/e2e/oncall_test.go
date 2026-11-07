//go:build e2e

package e2e

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

func init() {
	register(scenario{area: "integrations", name: "lifecycle", tools: []string{"create_integration", "list_integrations", "update_integration", "rotate_integration_secret", "delete_integration"}, run: integrationsLifecycle})
	register(scenario{area: "deliveries", name: "ingest-raw-replay", tools: []string{"list_deliveries", "read_delivery_raw", "replay_delivery"}, run: deliveriesIngest})
	register(scenario{area: "deliveries", name: "test-event", tools: []string{"send_test_event"}, run: deliveriesTestEvent})
}

// integration creates a generic hmac integration on p's board as the workspace admin and answers
// its id, endpoint path and secret.
func (e *env) integration(t *testing.T, p *project, name string) (id, path, secret string) {
	t.Helper()
	out := e.admin.Call(t, "create_integration", M{"projectId": p.id, "boardId": p.board, "name": name, "source": "generic",
		"auth": M{"mode": "hmac"}, "routing": M{"mode": "broadcast"}, "idempotencyKey": uniq("int")})
	return S(out, "integration.id"), S(out, "integration.endpointPath"), S(out, "secret")
}

// hook POSTs body to the integration's endpoint signed with secret (BatonDeck's v0 form) and
// answers the status and the decoded answer. An empty secret sends no signature.
func (e *env) hook(t *testing.T, path, secret string, body []byte) (int, M) {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	headers := map[string]string{"Content-Type": "application/json", "X-Batondeck-Timestamp": ts}
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(ts + "."))
		mac.Write(body)
		headers["X-Batondeck-Signature"] = "v0=" + hex.EncodeToString(mac.Sum(nil))
	}
	code, raw := fetch(t, "POST", e.srv.base+path, body, headers)
	var out M
	_ = json.Unmarshal(raw, &out)
	return code, out
}

// alert is a generic payload firing (or resolving) fingerprint.
func alert(fingerprint, status, title string) []byte {
	b, _ := json.Marshal(M{"fingerprint": fingerprint, "status": status, "severity": "critical", "title": title,
		"description": "e2e alert", "labels": M{"service": "feed"}, "occurredAt": time.Now().UTC().Format(time.RFC3339)})
	return b
}

// The workspace admin creates an hmac integration (the secret shown once, never listed), a signed
// delivery files a ticket, a disabled integration answers 404, a rotated secret is accepted beside
// the previous one until revokePrevious, and a deleted one stops answering and lists only as a
// tombstone. The master may not manage integrations; a stale version and a second delete are
// refused.
func integrationsLifecycle(t *testing.T, e *env) {
	p := e.project(t, "Oncall")
	e.master.Refuse(t, "create_integration", M{"projectId": p.id, "boardId": p.board, "name": "x", "source": "generic", "auth": M{"mode": "hmac"}, "routing": M{"mode": "broadcast"}}, "FORBIDDEN")
	e.admin.Refuse(t, "create_integration", M{"projectId": p.id, "boardId": p.board, "name": "x", "source": "carrier-pigeon", "auth": M{"mode": "hmac"}, "routing": M{"mode": "broadcast"}}, "VALIDATION")
	id, path, secret := e.integration(t, p, "Feed alerts")
	want(t, id != "" && path == "/hooks/"+id && strings.HasPrefix(secret, "whsec_"), "create_integration answered id %q path %q secret %q", id, path, secret[:min(6, len(secret))])

	listed := e.admin.Call(t, "list_integrations", M{"projectId": p.id})
	want(t, has(ids(A(listed, "integrations"), "id"), id) && S(listed, "integrations.0.auth.mode") == "hmac", "list_integrations: %s", js(listed))
	want(t, !strings.Contains(js(listed), secret), "list_integrations shows the secret")
	e.master.Refuse(t, "list_integrations", M{"projectId": p.id}, "FORBIDDEN")

	code, out := e.hook(t, path, secret, alert("disk-full", "firing", "Disk full on feed-1"))
	want(t, code == 202 && N(out, "accepted") == 1 && S(out, "results.0.result") == "created", "a signed delivery answered %d %s", code, js(out))
	ticket := e.get(t, p, S(out, "results.0.taskId"))
	want(t, strings.Contains(S(ticket, "title"), "Disk full on feed-1") && S(ticket, "boardId") == p.board, "the filed ticket: %s", js(ticket))

	v := -1.0
	for _, in := range A(listed, "integrations") {
		if S(in, "id") == id {
			v = N(in, "version")
		}
	}
	off := O(e.admin.Call(t, "update_integration", M{"projectId": p.id, "integrationId": id, "version": v, "patch": M{"enabled": false, "name": "Feed alerts (paused)"}}), "integration")
	want(t, !B(off, "enabled") && S(off, "name") == "Feed alerts (paused)" && N(off, "version") > v, "update_integration answered %s", js(off))
	code, _ = e.hook(t, path, secret, alert("disk-full-2", "firing", "while paused"))
	want(t, code == 404, "a delivery to a disabled integration answered %d, want 404", code)
	e.admin.Refuse(t, "update_integration", M{"projectId": p.id, "integrationId": id, "version": v, "patch": M{"enabled": true}}, "STALE")
	e.master.Refuse(t, "update_integration", M{"projectId": p.id, "integrationId": id, "version": N(off, "version"), "patch": M{"enabled": true}}, "FORBIDDEN")
	on := O(e.admin.Call(t, "update_integration", M{"projectId": p.id, "integrationId": id, "version": N(off, "version"), "patch": M{"enabled": true}}), "integration")

	rot := e.admin.Call(t, "rotate_integration_secret", M{"projectId": p.id, "integrationId": id, "version": N(on, "version")})
	fresh := S(rot, "secret")
	want(t, strings.HasPrefix(fresh, "whsec_") && fresh != secret && B(rot, "shownOnce"), "rotate_integration_secret answered a secret %v", fresh != "")
	code, _ = e.hook(t, path, fresh, alert("cpu-1", "firing", "CPU high"))
	want(t, code == 202, "a delivery signed with the new secret answered %d", code)
	code, _ = e.hook(t, path, secret, alert("cpu-2", "firing", "CPU high again"))
	want(t, code == 202, "inside the dual-accept window the previous secret answered %d", code)
	e.master.Refuse(t, "rotate_integration_secret", M{"projectId": p.id, "integrationId": id, "version": N(rot, "integration.version")}, "FORBIDDEN")
	rev := e.admin.Call(t, "rotate_integration_secret", M{"projectId": p.id, "integrationId": id, "version": N(rot, "integration.version"), "revokePrevious": true})
	code, _ = e.hook(t, path, fresh, alert("cpu-3", "firing", "revoked"))
	want(t, code == 401, "after revokePrevious the previous secret answered %d, want 401", code)
	code, _ = e.hook(t, path, S(rev, "secret"), alert("cpu-4", "firing", "current"))
	want(t, code == 202, "the current secret after revokePrevious answered %d", code)

	e.master.Refuse(t, "delete_integration", M{"projectId": p.id, "integrationId": id, "version": N(rev, "integration.version")}, "FORBIDDEN")
	del := e.admin.Call(t, "delete_integration", M{"projectId": p.id, "integrationId": id, "version": N(rev, "integration.version")})
	want(t, B(del, "deleted") && S(del, "integration.deletedAt") != "", "delete_integration answered %s", js(del))
	code, _ = e.hook(t, path, S(rev, "secret"), alert("cpu-5", "firing", "after delete"))
	want(t, code == 404, "a deleted integration's endpoint answered %d, want 404", code)
	want(t, !has(ids(A(e.admin.Call(t, "list_integrations", M{"projectId": p.id}), "integrations"), "id"), id), "list_integrations still lists the deleted integration")
	want(t, has(ids(A(e.admin.Call(t, "list_integrations", M{"projectId": p.id, "includeDeleted": true}), "integrations"), "id"), id), "includeDeleted does not list the tombstone")
	e.admin.Refuse(t, "delete_integration", M{"projectId": p.id, "integrationId": id, "version": N(del, "integration.version")}, "NOT_FOUND")
}

// A signed firing alert files a ticket and is logged as a firing delivery; a forged signature is
// refused (401) and logged as rejected with its raw body kept, which read_delivery_raw answers; a
// rejected delivery cannot be replayed, the firing one can. Only the workspace admin reads the
// delivery log.
func deliveriesIngest(t *testing.T, e *env) {
	p := e.project(t, "Deliveries")
	id, path, secret := e.integration(t, p, "Pager")
	code, out := e.hook(t, path, secret, alert("db-down", "firing", "Database down"))
	want(t, code == 202 && S(out, "results.0.result") == "created", "the signed delivery answered %d %s", code, js(out))
	firing, task := S(out, "results.0.deliveryId"), S(out, "results.0.taskId")

	forged := alert("forged", "firing", "Forged alert")
	code, _ = e.hook(t, path, "whsec_not-the-secret", forged)
	want(t, code == 401, "a forged signature answered %d, want 401", code)
	// A rejected delivery's id is the body's hash, so the unsigned one sends another body.
	code, _ = e.hook(t, path, "", alert("unsigned", "firing", "Unsigned alert"))
	want(t, code == 401, "an unsigned delivery answered %d, want 401", code)

	list := func(kind string) []any {
		args := M{"projectId": p.id, "integrationId": id}
		if kind != "" {
			args["kind"] = kind
		}
		return A(e.admin.Call(t, "list_deliveries", args), "deliveries")
	}
	fires := list("firing")
	want(t, len(fires) == 1 && S(fires[0], "deliveryId") == firing && S(fires[0], "taskId") == task && S(fires[0], "title") == "Database down", "list_deliveries firing: %s", js(fires))
	rejected := list("rejected")
	reasons := map[string]string{}
	for _, r := range rejected {
		reasons[S(r, "error")] = S(r, "deliveryId")
	}
	want(t, len(rejected) == 2 && reasons["signature_mismatch"] != "" && reasons["missing_signature"] != "", "list_deliveries rejected: %s", js(rejected))
	want(t, len(list("")) == 3, "list_deliveries holds %d rows, want 3", len(list("")))
	page := e.admin.Call(t, "list_deliveries", M{"projectId": p.id, "integrationId": id, "limit": 1})
	want(t, len(A(page, "deliveries")) == 1 && S(page, "nextCursor") != "", "list_deliveries limit 1: %s", js(page))

	raw := e.admin.Call(t, "read_delivery_raw", M{"projectId": p.id, "integrationId": id, "deliveryId": reasons["signature_mismatch"]})
	body, err := base64.StdEncoding.DecodeString(S(raw, "payloadBase64"))
	want(t, err == nil && string(body) == string(forged) && S(raw, "contentType") == "application/octet-stream", "read_delivery_raw answered %s (decoded %q, %v)", js(raw), body, err)
	e.admin.Refuse(t, "read_delivery_raw", M{"projectId": p.id, "integrationId": id, "deliveryId": "no-such-delivery"}, "NOT_FOUND")
	e.master.Refuse(t, "read_delivery_raw", M{"projectId": p.id, "integrationId": id, "deliveryId": S(rejected[0], "deliveryId")}, "FORBIDDEN")

	e.admin.Refuse(t, "replay_delivery", M{"projectId": p.id, "integrationId": id, "deliveryId": S(rejected[0], "deliveryId")}, "VALIDATION")
	e.master.Refuse(t, "replay_delivery", M{"projectId": p.id, "integrationId": id, "deliveryId": firing}, "FORBIDDEN")
	rep := e.admin.Call(t, "replay_delivery", M{"projectId": p.id, "integrationId": id, "deliveryId": firing})
	want(t, S(rep, "newDeliveryId") != "" && S(rep, "newDeliveryId") != firing && S(rep, "result") != "", "replay_delivery answered %s", js(rep))
	want(t, len(list("")) == 4, "the replay is not in the delivery log")
	e.master.Refuse(t, "list_deliveries", M{"projectId": p.id, "integrationId": id}, "FORBIDDEN")
}

// send_test_event files a real ticket labelled test through the integration and rings the doorbell;
// it is logged as a test delivery. The master may not send one, nor to an unknown integration.
func deliveriesTestEvent(t *testing.T, e *env) {
	p := e.project(t, "Test events")
	id, _, _ := e.integration(t, p, "Wiring check")
	got := e.admin.Call(t, "send_test_event", M{"projectId": p.id, "integrationId": id, "title": "Wiring check", "severity": "warning"})
	want(t, S(got, "result") == "created" && S(got, "doorbell") == "broadcast" && S(got, "taskId") != "", "send_test_event answered %s", js(got))
	ticket := e.get(t, p, S(got, "taskId"))
	want(t, has(strs(A(ticket, "labels")), "test") && S(ticket, "status") == "TODO", "the test ticket: %s", js(ticket))
	tests := A(e.admin.Call(t, "list_deliveries", M{"projectId": p.id, "integrationId": id, "kind": "test"}), "deliveries")
	want(t, len(tests) == 1 && S(tests[0], "deliveryId") == S(got, "deliveryId"), "list_deliveries test: %s", js(tests))
	e.master.Refuse(t, "send_test_event", M{"projectId": p.id, "integrationId": id}, "FORBIDDEN")
	e.admin.Refuse(t, "send_test_event", M{"projectId": p.id, "integrationId": "I-nosuch"}, "NOT_FOUND")
}
