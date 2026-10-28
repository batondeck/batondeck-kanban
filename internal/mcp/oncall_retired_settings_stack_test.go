package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// TestOncallRetiredSettingsOnTheRealStack (P-310, owner decision 6): on every selected engine, the
// four on-call settings nothing read (policy's autoReviewCycle, openPrForReview and reviewerAgent,
// and routing's fallbackBroadcast) are refused by create_integration's and update_integration's
// input schemas, beside the same calls without them answering (the positive control). A row stored
// before the removal, still carrying all four keys, lists, saves (a rename, then a new routing) and
// routes a test event to its on-call agent: the keys are ignored, with no migration.
func TestOncallRetiredSettingsOnTheRealStack(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bootStack(t, s)
		root := &caller{t: t, cs: h.Connect(t, stackRoot), called: map[string]int{}}
		p := str(root.call("create_project", map[string]any{"name": "Oncall"})["projectId"])
		b := str(obj(root.call("create_board", map[string]any{"projectId": p, "name": "Incidents"})["board"])["id"])
		create := func(extra map[string]any, routing map[string]any) map[string]any {
			args := map[string]any{"projectId": p, "name": "Alerts", "source": "generic", "boardId": b,
				"auth": map[string]any{"mode": "hmac"}, "routing": routing}
			for k, v := range extra {
				args[k] = v
			}
			return args
		}
		refused := func(tool string, args map[string]any, key string) {
			t.Helper()
			res, err := root.cs.CallTool(testCtx(t), &sdk.CallToolParams{Name: tool, Arguments: args})
			if err == nil && !res.IsError {
				t.Fatalf("%s with %s answered %v; want it refused by the input schema", tool, key, res.StructuredContent)
			}
			msg := ""
			if err != nil {
				msg = err.Error()
			} else {
				msg = text(res)
			}
			if !strings.Contains(msg, key) {
				t.Fatalf("%s with %s refused for another reason: %s", tool, key, msg)
			}
			t.Logf("%s: %s with %s refused: %.160s", s.Engine, tool, key, msg)
		}
		broadcast := map[string]any{"mode": "broadcast"}
		refused("create_integration", create(map[string]any{"policy": map[string]any{"autoReviewCycle": true}}, broadcast), "policy")
		refused("create_integration", create(nil, map[string]any{"mode": "oncall", "onCallAgent": "claude-oncall", "fallbackBroadcast": true}), "fallbackBroadcast")
		created := root.call("create_integration", create(nil, broadcast)) // the positive control
		ig := obj(created["integration"])
		iid := str(ig["id"])
		version := int64(ig["version"].(float64))
		refused("update_integration", map[string]any{"projectId": p, "integrationId": iid, "version": version,
			"patch": map[string]any{"policy": map[string]any{"openPrForReview": true, "reviewerAgent": "rev"}}}, "policy")
		refused("update_integration", map[string]any{"projectId": p, "integrationId": iid, "version": version,
			"patch": map[string]any{"routing": map[string]any{"mode": "broadcast", "fallbackBroadcast": true}}}, "fallbackBroadcast")

		// A row as it was stored before the removal: all four keys, written through the store.
		legacyRouting := `{"mode":"oncall","onCallAgent":"claude-oncall","fallbackBroadcast":true}`
		legacyPolicy := `{"autoReviewCycle":true,"openPrForReview":true,"reviewerAgent":"rev"}`
		row, err := s.Store.UpdateIntegration(testCtx(t), p, iid, version, store.IntegrationPatch{
			Routing: json.RawMessage(legacyRouting), Policy: json.RawMessage(legacyPolicy), UpdatedAt: 1})
		if err != nil {
			t.Fatal(err)
		}
		version = row.Version

		listed := root.call("list_integrations", map[string]any{"projectId": p})
		var found map[string]any
		for _, e := range listed["integrations"].([]any) {
			if str(obj(e)["id"]) == iid {
				found = obj(e)
			}
		}
		if found == nil || str(obj(found["routing"])["mode"]) != "oncall" || int64(found["version"].(float64)) != version {
			t.Fatalf("list_integrations of the legacy row: %v", found)
		}

		renamed := obj(root.call("update_integration", map[string]any{"projectId": p, "integrationId": iid, "version": version,
			"patch": map[string]any{"name": "Renamed"}})["integration"])
		if str(renamed["name"]) != "Renamed" || int64(renamed["version"].(float64)) != version+1 {
			t.Fatalf("update_integration of the legacy row: %v", renamed)
		}
		version++

		// The routing the row was saved with still routes: the test event is assigned to its agent.
		ev := root.call("send_test_event", map[string]any{"projectId": p, "integrationId": iid})
		if str(ev["doorbell"]) != "assigned" {
			t.Fatalf("send_test_event on the legacy routing: %v, want doorbell assigned (to claude-oncall)", ev)
		}

		// A new routing replaces the stored one whole, so the retired key leaves the row with it.
		root.call("update_integration", map[string]any{"projectId": p, "integrationId": iid, "version": version,
			"patch": map[string]any{"routing": map[string]any{"mode": "broadcast"}}})
		got, err := s.Store.GetIntegration(testCtx(t), p, iid)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(got.Routing), "fallbackBroadcast") || string(got.Policy) != legacyPolicy {
			t.Fatalf("after the saves: routing %s (want no fallbackBroadcast), policy %s (want the stored %s, untouched)", got.Routing, got.Policy, legacyPolicy)
		}
		t.Logf("%s: the legacy row listed, renamed (v%d), routed a test event %s, and was re-routed: routing %s, policy %s",
			s.Engine, version, str(ev["doorbell"]), got.Routing, got.Policy)
	})
}
