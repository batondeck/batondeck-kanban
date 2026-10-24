package api

import (
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
	"github.com/batondeck/batondeck-kanban/internal/store/memstore"
)

func fakeServices(f *servicefake.Fake) func(*memstore.Store) Options {
	return func(*memstore.Store) Options { return Options{Services: f.Services()} }
}

type routeRow struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Source string `json:"source"`
}

func loadRoutes(t *testing.T) []routeRow {
	t.Helper()
	b, err := os.ReadFile("routes.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Routes []routeRow `json:"routes"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Routes
}

// AC1: the mounted table is routes.json, route for route, and every row is served by the mux (not
// its 404 or 405): a route left out of the table, or out of the file, fails here.
func TestRouteTable(t *testing.T) {
	f := servicefake.New()
	h := newHarness(t, fakeServices(f))
	want := loadRoutes(t)
	key := func(m, p string) string { return m + " " + p }
	var wantKeys, gotKeys []string
	for _, r := range want {
		wantKeys = append(wantKeys, key(r.Method, r.Path))
	}
	o := Options{Services: f.Services(), Auth: h.auth}
	for _, r := range table(&o) {
		gotKeys = append(gotKeys, key(r.Method, r.Path))
	}
	sort.Strings(wantKeys)
	sort.Strings(gotKeys)
	for _, k := range wantKeys {
		if !slices.Contains(gotKeys, k) {
			t.Errorf("routes.json lists %s, which is not mounted", k)
		}
	}
	for _, k := range gotKeys {
		if !slices.Contains(wantKeys, k) {
			t.Errorf("%s is mounted but not in routes.json", k)
		}
	}
	t.Logf("route table: %d routes in routes.json, %d mounted", len(wantKeys), len(gotKeys))
	for _, r := range want {
		if r.Path == "/api/stream" || r.Path == "/api/login" || r.Path == "/api/logout" {
			continue // the stream stays open (stream_test.go); login is the harness's own sign-in; logout would end it for every later row (users_test.go)
		}
		path := strings.NewReplacer("{projectId}", "P-1", "{boardId}", "B-1", "{taskId}", "T-1", "{columnId}", "C-1",
			"{identityId}", "U-2", "{edgeId}", "E-1", "{sprintId}", "S-1", "{followUpId}", "F-1", "{integrationId}", "I-1",
			"{deliveryId}", "D-1", "{pageId}", "PG-1", "{versionId}", "V-1", "{id}", "K-1").Replace(r.Path)
		body := ""
		if r.Method == "POST" || r.Method == "PATCH" {
			body = "{}"
		}
		code, got := h.do(r.Method, path, body, nil)
		if code == http.StatusNotFound || code == http.StatusMethodNotAllowed {
			if !strings.Contains(got, `"code"`) {
				t.Errorf("%s %s: the mux answered %d %q, so the route is not mounted", r.Method, path, code, got)
			}
		}
	}
}

// Requirement 2: a route's arguments are the body under the path parameters, validated by the tool's
// own schema: an unknown body key is VALIDATION, the URL's ids win over the body's, a DELETE takes no
// body field, and a GET's query is typed by the schema.
func TestRouteArguments(t *testing.T) {
	f := servicefake.New()
	h := newHarness(t, fakeServices(f))
	last := func() any {
		calls := f.Calls()
		if len(calls) == 0 {
			t.Fatal("no service call")
		}
		return calls[len(calls)-1].Req
	}
	if code, body := h.do("POST", "/api/projects/P-1/tasks/T-1/move", `{"toStatus":"DONE","version":3,"projectId":"P-evil","taskId":"T-evil"}`, nil); code != 200 {
		t.Fatalf("move: %d %s", code, body)
	}
	if got := last().(service.MoveTaskReq); got.ProjectID != "P-1" || got.TaskID != "T-1" || got.ToStatus != domain.Done {
		t.Fatalf("move reached the service as %+v; the URL's ids must win", got)
	}
	n := len(f.Calls())
	if code, body := h.do("POST", "/api/projects/P-1/tasks/T-1/move", `{"toStatus":"DONE","version":3,"bogus":1}`, nil); code != 400 || !strings.Contains(body, `"VALIDATION"`) {
		t.Fatalf("an unknown body key: %d %s, want 400 VALIDATION", code, body)
	}
	if code, body := h.do("POST", "/api/projects/P-1/tasks/T-1/move", `{"toStatus":"SIDEWAYS","version":3}`, nil); code != 400 {
		t.Fatalf("a status outside the enum: %d %s, want 400", code, body)
	}
	if code, body := h.do("DELETE", "/api/projects/P-1/dependencies/E-1", `{"edgeId":"E-2"}`, nil); code != 400 {
		t.Fatalf("a DELETE with a body field: %d %s, want 400", code, body)
	}
	if len(f.Calls()) != n {
		t.Fatalf("a refused request reached the service: %+v", f.Calls()[n:])
	}
	if code, body := h.do("GET", "/api/projects/P-1/boards/B-1/tasks?limit=5&unblockedOnly=true&status=TODO&ignored=x", "", nil); code != 200 {
		t.Fatalf("list tasks: %d %s", code, body)
	}
	if got := last().(service.ListTasksReq); got.Limit != 5 || !got.UnblockedOnly || got.Status != domain.Todo || got.BoardID != "B-1" {
		t.Fatalf("list tasks reached the service as %+v", got)
	}
	if code, body := h.do("GET", "/api/projects/P-1/boards/B-1/tasks?limit=five", "", nil); code != 400 {
		t.Fatalf("a non-numeric limit: %d %s, want 400", code, body)
	}
	if code, _ := h.do("GET", "/api/projects/P-1/tasks/T-1/subtasks", "", nil); code != 200 {
		t.Fatalf("subtasks: %d", code)
	}
	if got := last().(service.ListSubtasksReq); got.ParentTaskID != "T-1" {
		t.Fatalf("subtasks reached the service as %+v, want parentTaskId T-1", got)
	}
}

// Requirement 4: a service error is ui-backend's status for its code and {code, message, details}.
func TestRouteErrorShape(t *testing.T) {
	f := servicefake.New()
	h := newHarness(t, fakeServices(f))
	want := map[domain.Code]int{
		domain.CodeValidation: 400, domain.CodeForbidden: 403, domain.CodeNotFound: 404, domain.CodeStale: 409,
		domain.CodeConflictLocked: 409, domain.CodeLeaseExpired: 409, domain.CodeInvalidTransition: 409,
		domain.CodeWIPExceeded: 409, domain.CodeCycleDetected: 409, domain.CodeQuotaExceeded: 429,
		domain.CodeRateLimited: 429, domain.CodeInternal: 500, domain.CodeUnauthenticated: 401,
	}
	for code, status := range want {
		got, body := h.do("GET", "/api/projects/"+servicefake.Trigger(code), "", nil)
		var e struct {
			Code    domain.Code `json:"code"`
			Message string      `json:"message"`
		}
		if err := json.Unmarshal([]byte(body), &e); err != nil || got != status || e.Code != code || e.Message == "" {
			t.Errorf("%s: %d %s, want %d with the code", code, got, body, status)
		}
	}
	f.Set("Tasks.Move", service.MoveTaskResp{}, domain.NewError(domain.CodeStale, "version 2 is not 3", map[string]any{"version": 3}))
	if code, body := h.do("POST", "/api/projects/P-1/tasks/T-1/move", `{"toStatus":"DONE","version":2}`, nil); code != 409 || !strings.Contains(body, `"details":{"version":3}`) {
		t.Fatalf("details: %d %s", code, body)
	}
}

// AC3: a state change from a page of another site, which sends Origin and no Fetch Metadata, is
// refused 403 and audited; so is a same-origin one without the session's CSRF token; the same
// request from this origin with the token reaches the service.
func TestCSRFOnATaskRoute(t *testing.T) {
	f := servicefake.New()
	h := newHarness(t, fakeServices(f))
	const path, body = "/api/projects/P-1/tasks/T-1/move", `{"toStatus":"DONE","version":1}`
	code, got := h.do("POST", path, body, map[string]string{"Sec-Fetch-Site": "", "Origin": "https://evil.example"})
	if code != http.StatusForbidden {
		t.Fatalf("cross-site, no Fetch Metadata: %d %s, want 403", code, got)
	}
	code, got = h.do("POST", path, body, map[string]string{"Sec-Fetch-Site": "cross-site"})
	if code != http.StatusForbidden {
		t.Fatalf("Sec-Fetch-Site cross-site: %d %s, want 403", code, got)
	}
	code, got = h.do("POST", path, body, map[string]string{"X-CSRF-Token": ""})
	if code != http.StatusForbidden {
		t.Fatalf("same-origin without the CSRF token: %d %s, want 403", code, got)
	}
	if n := len(f.Calls()); n != 0 {
		t.Fatalf("a refused request reached the service: %+v", f.Calls())
	}
	var reasons []string
	for _, e := range h.audits() {
		if e.Action == "request.refused" && e.Subject == "path:"+path && e.Actor == h.userID {
			reasons = append(reasons, e.Data["reason"].(string))
		}
	}
	if !slices.Equal(reasons, []string{"cross_site", "cross_site", "csrf_token"}) {
		t.Fatalf("audited refusals %v, want cross_site, cross_site, csrf_token", reasons)
	}
	code, got = h.do("POST", path, body, nil)
	if code != http.StatusOK || len(f.Calls()) != 1 || f.Calls()[0].Method != "Tasks.Move" {
		t.Fatalf("same-origin with the token: %d %s %+v, want 200 and one Tasks.Move", code, got, f.Calls())
	}
}

// Every /api route but the public config and the sign-in wants a principal.
func TestRoutesWantASession(t *testing.T) {
	f := servicefake.New()
	h := newHarness(t, fakeServices(f))
	for _, r := range loadRoutes(t) {
		if r.Path == "/api/public-config" || r.Path == "/api/login" {
			continue
		}
		req := h.request(r.Method, r.Path, "", nil)
		req.Header.Del("Cookie")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d, want 401", r.Method, r.Path, resp.StatusCode)
		}
	}
	if len(f.Calls()) != 0 {
		t.Fatalf("an unauthenticated request reached the service: %+v", f.Calls())
	}
}

// /api/session and /api/public-config carry ui-backend's keys.
func TestSessionAndPublicConfig(t *testing.T) {
	h := newHarness(t, fakeServices(servicefake.New()))
	code, body := h.do("GET", "/api/session", "", nil)
	var s map[string]any
	if err := json.Unmarshal([]byte(body), &s); err != nil || code != 200 {
		t.Fatalf("session: %d %s", code, body)
	}
	if s["identity"] != h.userID || s["isAdmin"] != false || s["csrfToken"] != h.csrf || s["subscriptionsEnabled"] != false {
		t.Fatalf("session: %s", body)
	}
	for _, k := range []string{"permissions", "uiSettings"} {
		if _, ok := s[k]; !ok {
			t.Fatalf("session has no %s: %s", k, body)
		}
	}
	req := h.request("GET", "/api/public-config", "", nil)
	req.Header.Del("Cookie")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var pc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&pc); err != nil || resp.StatusCode != 200 || pc["demoLogin"] != false || pc["workosLogin"] != false || len(pc) != 2 {
		t.Fatalf("public-config: %d %v", resp.StatusCode, pc)
	}
}

// AC4: GET /api/projects/:p/boards/:b over the real service, on every engine, answers the keys conductor's get_board
// answered (testdata/get_board.conductor.json, recorded from conductor's test harness at 5e2baafa),
// with board.transitions as the one addition: an optional key of conductor's BoardSchema
// (src/tools/schemas.ts:257) that this server always fills (the fixed table).
func TestGetBoardShapeMatchesConductor(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := newStackHarness(t, s, Options{})
		pid, bid := seedBoard(t, h, s.Core, "Shape")
		code, body := h.do("GET", "/api/projects/"+pid+"/boards/"+bid, "", nil)
		if code != 200 {
			t.Fatalf("get board: %d %s", code, body)
		}
		fixture, err := os.ReadFile("testdata/get_board.conductor.json")
		if err != nil {
			t.Fatal(err)
		}
		var want, got any
		if err := json.Unmarshal(fixture, &want); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatal(err)
		}
		// taskCounts is keyed by column id, which differs per run: its values are compared by type.
		diffs := shapeDiff("", want, got, map[string]bool{".board.transitions": true}, map[string]bool{".taskCounts": true})
		if len(diffs) > 0 {
			t.Fatalf("GET board differs in shape from conductor's get_board:\n%s\ngot %s", strings.Join(diffs, "\n"), body)
		}
	})
}

// shapeDiff lists where got's keys and JSON types differ from want's: a key of want missing from got,
// a key of got not in want (unless optional names its path), or a type that differs. An array is
// compared by its first element; a map under byValue is compared by its values' types only.
func shapeDiff(path string, want, got any, optional, byValue map[string]bool) []string {
	var out []string
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return []string{path + ": want an object, got " + typeName(got)}
		}
		if byValue[path] {
			for k, v := range g {
				if typeName(v) != "number" {
					out = append(out, path+"."+k+": want a number, got "+typeName(v))
				}
			}
			return out
		}
		for k, wv := range w {
			gv, ok := g[k]
			if !ok {
				out = append(out, path+"."+k+": missing")
				continue
			}
			out = append(out, shapeDiff(path+"."+k, wv, gv, optional, byValue)...)
		}
		for k := range g {
			if _, ok := w[k]; !ok && !optional[path+"."+k] {
				out = append(out, path+"."+k+": not in conductor's answer")
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok {
			return []string{path + ": want an array, got " + typeName(got)}
		}
		if len(w) > 0 && len(g) > 0 {
			out = append(out, shapeDiff(path+"[]", w[0], g[0], optional, byValue)...)
		}
	default:
		// conductor's wipLimit null against ours: null is the value conductor recorded for an
		// unlimited column, and this server's default columns are unlimited too.
		if typeName(want) != typeName(got) {
			out = append(out, path+": want "+typeName(want)+", got "+typeName(got))
		}
	}
	return out
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "?"
}

// outOfScope is what the table must never hold: the groups the owner dropped (plan "Dropped as the
// owner ordered") by prefix, and the in-scope-group routes left out by decision, by method and path.
var outOfScope = struct {
	prefixes []string
	routes   map[string]string
}{
	prefixes: []string{"/api/studio", "/api/admin", "/api/tenants", "/api/billing", "/api/onboarding"},
	routes: map[string]string{
		"GET " + ppb + "/transitions":         "the transition table is fixed",
		"PUT " + ppb + "/transitions":         "the transition table is fixed",
		"GET /api/me/marketplace-key":         "the plan dropped the marketplace key",
		"POST /api/me/marketplace-key":        "the plan dropped the marketplace key",
		"POST /api/me/marketplace-key/rotate": "the plan dropped the marketplace key",
	},
}

// AC1 (R1.12): no out-of-scope route is mounted or listed in routes.json, by name.
func TestOutOfScopeRoutesAreRefused(t *testing.T) {
	f := servicefake.New()
	h := newHarness(t, fakeServices(f))
	o := Options{Services: f.Services(), Auth: h.auth}
	check := func(where, method, path string) {
		for _, p := range outOfScope.prefixes {
			if path == p || strings.HasPrefix(path, p+"/") {
				t.Errorf("%s holds %s %s: the %s group is out of scope", where, method, path, p)
			}
		}
		if why, ok := outOfScope.routes[method+" "+path]; ok {
			t.Errorf("%s holds %s %s: out of scope, %s", where, method, path, why)
		}
	}
	for _, r := range table(&o) {
		check("the router", r.Method, r.Path)
	}
	for _, r := range loadRoutes(t) {
		check("routes.json", r.Method, r.Path)
	}
}
