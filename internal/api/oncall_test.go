package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/oncall"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// okJSON reads a 200's body as a JSON object: okJSON(t, what)(h.do(…)).
func okJSON(t *testing.T, what string) func(code int, body string) map[string]any {
	return func(code int, body string) map[string]any {
		t.Helper()
		if code != http.StatusOK {
			t.Fatalf("%s: %d %s, want 200", what, code, body)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("%s: %v in %s", what, err, body)
		}
		return out
	}
}

// errCode is a refusal's status and code.
func errCode(t *testing.T, what string, code int, body string, status int, want domain.Code) {
	t.Helper()
	var e struct{ Code domain.Code }
	_ = json.Unmarshal([]byte(body), &e)
	if code != status || e.Code != want {
		t.Fatalf("%s: %d %s, want %d %s", what, code, body, status, want)
	}
}

// Every on-call route on each engine, as a workspace admin who is the project's member: the
// catalog; create answers the minted secret once and the listing never; update, rotate (the new
// secret once), a test event, the delivery log paged by its cursor, replay, the raw body of a
// refused delivery as an octet-stream attachment, and the tombstoning delete with its version in
// the query. Then the cross-project refusals: a workspace admin who is a member of another project
// only is 403 FORBIDDEN on every route of this one, and this project's integration addressed under
// their own project is 404 NOT_FOUND (the positive control is root's 200 on the same routes).
func TestOncallRoutesOnTheStack(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := newStackHarness(t, s, Options{})
		root := h.asAdmin("root")
		ctx := context.Background()
		rootCaller := domain.Caller{Identity: root.userID, TokenScope: root.userID, WorkspaceAdmin: true}
		pid, bid := seedBoard(t, root, s.Core, "Oncall")
		on := "/api/oncall/" + pid + "/integrations"

		// The catalog: the eight sources in oncall.Sources' order, each with its modes and guide.
		cat := okJSON(t, "GET sources")(root.do("GET", "/api/oncall/sources", "", nil))
		var ids []string
		for _, e := range cat["sources"].([]any) {
			src := e.(map[string]any)
			ids = append(ids, src["id"].(string))
			if src["label"] == "" || src["authNote"] == "" || src["docsPage"] != "oncall-"+src["id"].(string) || len(src["authModes"].([]any)) == 0 {
				t.Fatalf("GET sources: entry %v lacks a field the setup form reads", src)
			}
		}
		if !slices.Equal(ids, oncall.SourceIDs()) {
			t.Fatalf("GET sources: %v, want %v", ids, oncall.SourceIDs())
		}

		code, body := root.do("POST", on, `{"name":"Alerts","source":"generic","boardId":"`+bid+`","auth":{"mode":"hmac"},"routing":{"mode":"broadcast"}}`, nil)
		created := okJSON(t, "POST integrations")(code, body)
		secret, _ := created["secret"].(string)
		if secret == "" || created["shownOnce"] != true {
			t.Fatalf("POST integrations: %s, want the minted secret shown once", body)
		}
		ig := created["integration"].(map[string]any)
		iid := ig["id"].(string)
		one := on + "/" + iid

		code, body = root.do("GET", on, "", nil)
		listed := okJSON(t, "GET integrations")(code, body)
		if n := len(listed["integrations"].([]any)); n != 1 {
			t.Fatalf("GET integrations: %d rows, want 1", n)
		}
		if strings.Contains(body, `"secret"`) || strings.Contains(body, secret) {
			t.Fatalf("GET integrations carries the secret: %s", body)
		}

		upd := okJSON(t, "POST integration")(root.do("POST", one, `{"version":1,"patch":{"name":"Renamed"}}`, nil))
		if g := upd["integration"].(map[string]any); g["name"] != "Renamed" || g["version"] != float64(2) || upd["secret"] != nil {
			t.Fatalf("POST integration: %v", upd)
		}

		code, body = root.do("POST", one+"/rotate-secret", `{"version":2}`, nil)
		rot := okJSON(t, "POST rotate-secret")(code, body)
		next, _ := rot["secret"].(string)
		if next == "" || next == secret || rot["shownOnce"] != true || strings.Count(body, next) != 1 {
			t.Fatalf("POST rotate-secret: %s, want a new secret shown once", body)
		}
		if a := rot["integration"].(map[string]any)["auth"].(map[string]any); a["dualAcceptUntil"] == nil {
			t.Fatalf("POST rotate-secret: auth %v, want the previous secret's window", a)
		}

		test := okJSON(t, "POST test")(root.do("POST", one+"/test", `{"title":"Wiring check"}`, nil))
		testID, _ := test["deliveryId"].(string)
		if testID == "" || test["taskId"] == nil || test["doorbell"] == nil {
			t.Fatalf("POST test: %v, want a delivery and its ticket", test)
		}

		// A refused delivery, through the ingest: its raw body is kept.
		refused := []byte(`{"title":"<script>alert(1)</script>"}`)
		out, err := s.Core.Hook(ctx, iid, service.HookRequest{Body: refused, Header: http.Header{
			service.HeaderTimestamp: {"1"}, service.HeaderSignature: {"v0=00"},
		}.Get})
		if err != nil || out.Refusal != service.HookUnauthorized {
			t.Fatalf("hook with a bad signature: %+v %v", out, err)
		}

		page := okJSON(t, "GET deliveries")(root.do("GET", one+"/deliveries?limit=1", "", nil))
		cursor, _ := page["nextCursor"].(string)
		if len(page["deliveries"].([]any)) != 1 || cursor == "" {
			t.Fatalf("GET deliveries?limit=1: %v, want one row and a cursor", page)
		}
		rest := okJSON(t, "GET deliveries (cursor)")(root.do("GET", one+"/deliveries?limit=5&cursor="+url.QueryEscape(cursor), "", nil))
		var rejectedID string
		for _, d := range append(page["deliveries"].([]any), rest["deliveries"].([]any)...) {
			if d := d.(map[string]any); d["kind"] == "rejected" {
				rejectedID = d["deliveryId"].(string)
			}
		}
		if rejectedID == "" || rest["nextCursor"] != nil {
			t.Fatalf("GET deliveries: pages %v %v, want the rejected row and an end", page, rest)
		}
		rejected := okJSON(t, "GET deliveries?kind=rejected")(root.do("GET", one+"/deliveries?kind=rejected", "", nil))
		if n := len(rejected["deliveries"].([]any)); n != 1 {
			t.Fatalf("GET deliveries?kind=rejected: %d rows, want 1", n)
		}

		rep := okJSON(t, "POST replay")(root.do("POST", one+"/deliveries/"+url.PathEscape(testID)+"/replay", "", nil))
		if id, _ := rep["newDeliveryId"].(string); !strings.HasPrefix(id, "replay:"+testID+":") || rep["result"] == "" {
			t.Fatalf("POST replay: %v", rep)
		}
		code, body = root.do("POST", one+"/deliveries/"+url.PathEscape(testID)+"/replay", `{"deliveryId":"other"}`, nil)
		errCode(t, "POST replay with a body field", code, body, http.StatusBadRequest, domain.CodeValidation)

		resp, err := http.DefaultClient.Do(root.request("GET", one+"/deliveries/"+url.PathEscape(rejectedID)+"/raw", "", nil))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		hd := resp.Header
		if resp.StatusCode != http.StatusOK || string(raw) != string(refused) || hd.Get("Content-Type") != "application/octet-stream" ||
			!strings.HasPrefix(hd.Get("Content-Disposition"), "attachment; filename=delivery-") || hd.Get("X-Content-Type-Options") != "nosniff" ||
			hd.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("GET raw: %d %v %q, want the refused bytes as an octet-stream attachment", resp.StatusCode, hd, raw)
		}
		code, body = root.do("GET", one+"/deliveries/"+url.PathEscape(testID)+"/raw", "", nil)
		errCode(t, "GET raw of a delivery whose body was never kept", code, body, http.StatusNotFound, domain.CodeNotFound)

		// The cross-project refusals, before the delete so every route still has a live target.
		bob := h.asAdmin("bob")
		bobCaller := domain.Caller{Identity: bob.userID, TokenScope: bob.userID, WorkspaceAdmin: true}
		other, err := s.Core.Projects().Create(ctx, bobCaller, service.CreateProjectReq{Name: "Bob's"})
		if err != nil {
			t.Fatal(err)
		}
		routes := func(base string) []struct{ method, path, body string } {
			b := base + "/" + iid
			return []struct{ method, path, body string }{
				{"GET", base, ""},
				{"POST", base, `{"name":"X","source":"generic","boardId":"` + bid + `","auth":{"mode":"hmac"},"routing":{"mode":"broadcast"}}`},
				{"POST", b, `{"version":3,"patch":{"name":"Stolen"}}`},
				{"DELETE", b + "?version=3", ""},
				{"POST", b + "/rotate-secret", `{"version":3}`},
				{"POST", b + "/test", `{}`},
				{"GET", b + "/deliveries", ""},
				{"POST", b + "/deliveries/" + url.PathEscape(testID) + "/replay", ""},
				{"GET", b + "/deliveries/" + url.PathEscape(rejectedID) + "/raw", ""},
			}
		}
		for _, r := range routes(on) {
			code, body := bob.do(r.method, r.path, r.body, nil)
			errCode(t, r.method+" "+r.path+" as another project's admin", code, body, http.StatusForbidden, domain.CodeForbidden)
		}
		// This project's integration under bob's own project: no such integration there. The log
		// reads by (project, integration), so it answers an empty page rather than NOT_FOUND (the
		// service's list_deliveries looks the integration up nowhere): no row crosses either way.
		for _, r := range routes("/api/oncall/" + other.ProjectID + "/integrations")[2:] {
			code, body := bob.do(r.method, r.path, r.body, nil)
			if strings.HasSuffix(r.path, "/deliveries") {
				if got := okJSON(t, r.path+" under another project")(code, body); len(got["deliveries"].([]any)) != 0 {
					t.Fatalf("GET %s under another project: %s, want no rows", r.path, body)
				}
				continue
			}
			errCode(t, r.method+" "+r.path+" under another project", code, body, http.StatusNotFound, domain.CodeNotFound)
		}
		if got := okJSON(t, "GET bob's integrations")(bob.do("GET", "/api/oncall/"+other.ProjectID+"/integrations?includeDeleted=true", "", nil)); len(got["integrations"].([]any)) != 0 {
			t.Fatalf("bob's project lists %v, want none", got)
		}
		// A project member who is not a workspace admin: the tools are admin-only.
		if _, err := s.Core.Projects().AddMember(ctx, rootCaller, service.AddMemberReq{ProjectID: pid, IdentityID: h.userID, Role: domain.RoleAdmin}); err != nil {
			t.Fatal(err)
		}
		code, body = h.do("GET", on, "", nil)
		errCode(t, "GET integrations as a project admin who is not a workspace admin", code, body, http.StatusForbidden, domain.CodeForbidden)

		// The delete: the version rides the query; a stale one is 409; the tombstone lists only
		// under includeDeleted.
		code, body = root.do("DELETE", one+"?version=2", "", nil)
		errCode(t, "DELETE with a stale version", code, body, http.StatusConflict, domain.CodeStale)
		code, body = root.do("DELETE", one+"?version=3", `{"version":3}`, nil)
		errCode(t, "DELETE with a body field", code, body, http.StatusBadRequest, domain.CodeValidation)
		del := okJSON(t, "DELETE integration")(root.do("DELETE", one+"?version=3", "", nil))
		if del["deleted"] != true || del["integration"].(map[string]any)["deletedAt"] == nil {
			t.Fatalf("DELETE integration: %v", del)
		}
		if n := len(okJSON(t, "GET integrations")(root.do("GET", on, "", nil))["integrations"].([]any)); n != 0 {
			t.Fatalf("GET integrations after the delete: %d rows, want 0", n)
		}
		if n := len(okJSON(t, "GET integrations?includeDeleted")(root.do("GET", on+"?includeDeleted=true", "", nil))["integrations"].([]any)); n != 1 {
			t.Fatalf("GET integrations?includeDeleted=true: %d rows, want the tombstone", n)
		}

		// No session: 401 before any service, the catalog included.
		for _, p := range []string{"/api/oncall/sources", on, one + "/deliveries/" + url.PathEscape(rejectedID) + "/raw"} {
			req := root.request("GET", p, "", nil)
			req.Header.Del("Cookie")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("GET %s with no session: %d, want 401", p, resp.StatusCode)
			}
		}
	})
}

// TestOncallRetiredSettingsRefusedOverREST (P-310, owner decision 6): the REST routes validate
// against the same input schemas as the tools, so a body carrying policy or routing's
// fallbackBroadcast is 400 VALIDATION on create and on update; the same bodies without them answer
// 200 (the positive control).
func TestOncallRetiredSettingsRefusedOverREST(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := newStackHarness(t, s, Options{})
		root := h.asAdmin("root")
		pid, bid := seedBoard(t, root, s.Core, "Oncall")
		on := "/api/oncall/" + pid + "/integrations"
		base := `"name":"Alerts","source":"generic","boardId":"` + bid + `","auth":{"mode":"hmac"}`
		for what, body := range map[string]string{
			"policy":            `{` + base + `,"routing":{"mode":"broadcast"},"policy":{"autoReviewCycle":true}}`,
			"fallbackBroadcast": `{` + base + `,"routing":{"mode":"oncall","onCallAgent":"claude-oncall","fallbackBroadcast":true}}`,
		} {
			code, got := root.do("POST", on, body, nil)
			errCode(t, "POST integrations with "+what, code, got, http.StatusBadRequest, domain.CodeValidation)
			if !strings.Contains(got, what) {
				t.Fatalf("POST integrations with %s refused for another reason: %s", what, got)
			}
		}
		ig := okJSON(t, "POST integrations")(root.do("POST", on, `{`+base+`,"routing":{"mode":"broadcast"}}`, nil))["integration"].(map[string]any)
		one := on + "/" + ig["id"].(string)
		for what, body := range map[string]string{
			"policy":            `{"version":1,"patch":{"policy":{"openPrForReview":true}}}`,
			"fallbackBroadcast": `{"version":1,"patch":{"routing":{"mode":"broadcast","fallbackBroadcast":true}}}`,
		} {
			code, got := root.do("POST", one, body, nil)
			errCode(t, "POST integration with "+what, code, got, http.StatusBadRequest, domain.CodeValidation)
		}
		okJSON(t, "POST integration")(root.do("POST", one, `{"version":1,"patch":{"routing":{"mode":"broadcast"}}}`, nil))
	})
}
