package mcp_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// oncallSourceAuth is an auth each source can produce (oncall.Sources' first mode), with fixture
// material where the mode takes the caller's.
var oncallSourceAuth = map[string]map[string]any{
	"generic":      {"mode": "hmac"},
	"grafana":      {"mode": "api_key", "apiKey": "fixture-grafana-key"},
	"alertmanager": {"mode": "api_key", "apiKey": "fixture-alertmanager-key"},
	"dynatrace":    {"mode": "api_key", "apiKey": "fixture-dynatrace-key"},
	"datadog":      {"mode": "api_key", "apiKey": "fixture-datadog-key"},
	"gcp":          {"mode": "basic", "apiKey": "fixture-gcp-password"},
	"pagerduty":    {"mode": "hmac", "externalSecret": "fixture-pagerduty-secret"},
	"webhook":      {"mode": "hmac"},
}

// TestOncallOnTheRealStack, on every selected engine through a real client: the sealed secret in
// the stored row and none in list_integrations (AC1); send_test_event on each of the eight sources
// recording one delivery whose result is in the vocabulary (AC4); and the nine tools listed to a
// workspace admin only, each FORBIDDEN by name to a non-admin who is the project's own admin (AC6).
func TestOncallOnTheRealStack(t *testing.T) {
	vocabulary := []string{"created", "appended", "annotated", "closed", "recorded", "rejected", "rate_limited", "coalesced"}
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bootStack(t, s)
		root := &caller{t: t, cs: h.Connect(t, stackRoot), called: map[string]int{}}
		ada := h.Connect(t, stackOwner)
		p := str(root.call("create_project", map[string]any{"name": "Oncall"})["projectId"])
		b := str(obj(root.call("create_board", map[string]any{"projectId": p, "name": "Incidents"})["board"])["id"])

		var rows []string
		for _, source := range []string{"alertmanager", "datadog", "dynatrace", "gcp", "generic", "grafana", "pagerduty", "webhook"} {
			created := root.call("create_integration", map[string]any{"projectId": p, "name": source + " alerts", "source": source, "boardId": b,
				"auth": oncallSourceAuth[source], "routing": map[string]any{"mode": "broadcast"}})
			iid := str(obj(created["integration"])["id"])
			if source == "generic" {
				row, err := s.Store.GetIntegration(testCtx(t), p, iid)
				if err != nil {
					t.Fatal(err)
				}
				var auth struct {
					HMAC struct {
						Current string `json:"currentEncrypted"`
					} `json:"hmac"`
				}
				_ = json.Unmarshal(row.Auth, &auth)
				secret := str(created["secret"])
				if !strings.HasPrefix(auth.HMAC.Current, "enc1:") || secret == "" || strings.Contains(string(row.Auth), secret) {
					t.Fatalf("AC1: the stored auth %s is not sealed, or no secret was answered", row.Auth)
				}
				t.Logf("AC1 %s: stored auth.hmac.currentEncrypted %.20s… (%d chars); secret answered once (%d chars)", s.Engine, auth.HMAC.Current, len(auth.HMAC.Current), len(secret))
			}
			sent := root.call("send_test_event", map[string]any{"projectId": p, "integrationId": iid})
			list := root.call("list_deliveries", map[string]any{"projectId": p, "integrationId": iid})["deliveries"].([]any)
			if len(list) != 1 {
				t.Fatalf("%s: %d deliveries after one test event", source, len(list))
			}
			d := obj(list[0])
			if !slices.Contains(vocabulary, str(d["result"])) || d["kind"] != "test" || str(d["deliveryId"]) != str(sent["deliveryId"]) {
				t.Fatalf("%s: delivery %v", source, d)
			}
			rows = append(rows, source+": kind="+str(d["kind"])+" result="+str(d["result"])+" taskId="+str(d["taskId"])+" doorbell="+str(sent["doorbell"]))
		}
		t.Logf("AC4 %s: one delivery per source:\n  %s", s.Engine, strings.Join(rows, "\n  "))

		listed, err := root.cs.ListTools(testCtx(t), nil)
		if err != nil {
			t.Fatal(err)
		}
		all, err := json.Marshal(root.call("list_integrations", map[string]any{"projectId": p}))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(all), `"secret"`) || strings.Contains(string(all), "enc1:") || strings.Contains(string(all), "fixture-") {
			t.Fatalf("AC1: list_integrations carries secret material: %s", all)
		}
		memberList, err := ada.ListTools(testCtx(t), nil)
		if err != nil {
			t.Fatal(err)
		}
		var adminNames, memberNames []string
		for _, tl := range listed.Tools {
			adminNames = append(adminNames, tl.Name)
		}
		for _, tl := range memberList.Tools {
			memberNames = append(memberNames, tl.Name)
		}
		for _, tc := range oncallCases() {
			n := tc.tool
			if !slices.Contains(adminNames, n) || slices.Contains(memberNames, n) {
				t.Errorf("AC6: %s listed to the admin %v, to the non-admin %v", n, slices.Contains(adminNames, n), slices.Contains(memberNames, n))
			}
			e := bk.ToolError(t, ada, n, with(tc.args, "projectId", p))
			if e["code"] != "FORBIDDEN" || obj(e["details"])["requiredCapability"] != "workspace-admin" {
				t.Errorf("AC6: a non-admin's %s by name = %v, want FORBIDDEN for workspace-admin", n, e)
			}
		}
		t.Logf("AC6 %s: the admin lists %d tools, the non-admin (the project's admin) %d; the 9 on-call tools are FORBIDDEN to the non-admin by name", s.Engine, len(adminNames), len(memberNames))
	})
}
