package mcp_test

import (
	"reflect"
	"slices"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
)

// oncallTools are the nine on-call configuration tools (ADR-0004: workspace-admin only).
var oncallTools = []string{"create_integration", "delete_integration", "list_deliveries", "list_integrations",
	"read_delivery_raw", "replay_delivery", "rotate_integration_secret", "send_test_event", "update_integration"}

func oncallRow() service.Integration {
	until := "2026-10-24T00:00:00.000Z"
	return service.Integration{
		Auth:      service.IntegrationAuthView{Mode: "hmac", RotatedAt: "2026-10-23T00:00:00.000Z", DualAcceptUntil: &until},
		CreatedAt: "2026-10-23T00:00:00.000Z", CreatedBy: "u-admin", Enabled: true, EndpointPath: "/hooks/I-1", ID: "I-1",
		Lifecycle: "correlate", Limits: service.IntegrationLimits{PayloadCapKb: 256, RatePerMin: 60},
		Mapping: service.JSONObject{}, Name: "Alerts", Policy: service.JSONObject{}, ProjectID: "P-1",
		Routing: service.JSONObject{"mode": "broadcast"}, Source: "generic", Target: service.IntegrationTarget{BoardID: "B-1"},
		UpdatedAt: "2026-10-23T00:00:00.000Z", Version: 2,
	}
}

func oncallCases() []toolCase {
	task, title, b64 := "T-1", "disk full", "eyJ4IjoxfQ=="
	return []toolCase{
		{tool: "create_integration", method: "Oncall.CreateIntegration", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "name": "Alerts", "source": "generic", "boardId": "B-1",
				"auth": map[string]any{"mode": "hmac"}, "routing": map[string]any{"mode": "oncall", "onCallAgent": "u-ada"},
				"limits": map[string]any{"ratePerMin": 30}, "mapping": map[string]any{"labels": []any{"prod"}, "titleTemplate": nil},
				"idempotencyKey": "k-1"},
			want: service.CreateIntegrationReq{ProjectID: "P-1", Name: "Alerts", Source: "generic", BoardID: "B-1",
				Auth: service.IntegrationAuthInput{Mode: "hmac"}, Routing: service.IntegrationRouting{Mode: "oncall", OnCallAgent: service.Value("u-ada")},
				Limits: service.IntegrationLimitsIn{RatePerMin: 30}, Mapping: service.IntegrationMapping{Labels: []string{"prod"}, TitleTemplate: service.Null[string]()},
				IdempotencyKey: "k-1"},
			resp: service.CreateIntegrationResp{Integration: oncallRow(), Secret: "whsec_fixture", ShownOnce: true}},
		{tool: "update_integration", method: "Oncall.UpdateIntegration", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "integrationId": "I-1", "version": 1,
				"patch": map[string]any{"enabled": false, "columnId": nil, "name": "Renamed"}},
			want: service.UpdateIntegrationReq{ProjectID: "P-1", IntegrationID: "I-1", Version: 1,
				Patch: service.IntegrationPatch{Enabled: service.Some(false), ColumnID: service.Null[string](), Name: "Renamed"}},
			resp: service.UpdateIntegrationResp{Integration: oncallRow()}},
		{tool: "delete_integration", method: "Oncall.DeleteIntegration", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "integrationId": "I-1", "version": 2},
			want: service.DeleteIntegrationReq{ProjectID: "P-1", IntegrationID: "I-1", Version: 2},
			resp: service.DeleteIntegrationResp{Deleted: true, Integration: oncallRow()}},
		{tool: "list_integrations", method: "Oncall.ListIntegrations", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "limit": 5, "includeDeleted": true},
			want: service.ListIntegrationsReq{ProjectID: "P-1", Limit: 5, IncludeDeleted: true},
			resp: service.ListIntegrationsResp{Integrations: []service.Integration{oncallRow()}}},
		{tool: "rotate_integration_secret", method: "Oncall.RotateIntegrationSecret", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "integrationId": "I-1", "version": 2, "revokePrevious": true},
			want: service.RotateIntegrationSecretReq{ProjectID: "P-1", IntegrationID: "I-1", Version: 2, RevokePrevious: true},
			resp: service.RotateIntegrationSecretResp{Integration: oncallRow(), Secret: "whsec_fixture", ShownOnce: true}},
		{tool: "list_deliveries", method: "Oncall.ListDeliveries", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "integrationId": "I-1", "kind": "test", "limit": 10, "cursor": "c"},
			want: service.ListDeliveriesReq{ProjectID: "P-1", IntegrationID: "I-1", Kind: "test", Limit: 10, Cursor: "c"},
			resp: service.ListDeliveriesResp{Deliveries: []service.Delivery{{DeliveryID: "d-1", Kind: "test", Result: "created",
				Ts: "2026-10-23T00:00:00.000Z", TaskID: &task, Title: &title}}, NextCursor: "n"}},
		{tool: "read_delivery_raw", method: "Oncall.ReadDeliveryRaw", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "integrationId": "I-1", "deliveryId": "d-1"},
			want: service.ReadDeliveryRawReq{ProjectID: "P-1", IntegrationID: "I-1", DeliveryID: "d-1"},
			resp: service.ReadDeliveryRawResp{ContentType: "application/octet-stream", DeliveryID: "d-1", PayloadBase64: &b64, SuggestedFilename: "delivery-d-1.txt"}},
		{tool: "replay_delivery", method: "Oncall.ReplayDelivery", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "integrationId": "I-1", "deliveryId": "d-1"},
			want: service.ReplayDeliveryReq{ProjectID: "P-1", IntegrationID: "I-1", DeliveryID: "d-1"},
			resp: service.ReplayDeliveryResp{NewDeliveryID: "replay:d-1:1", Result: "created", TaskID: &task}},
		{tool: "send_test_event", method: "Oncall.SendTestEvent", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "integrationId": "I-1", "title": "drill", "severity": "critical"},
			want: service.SendTestEventReq{ProjectID: "P-1", IntegrationID: "I-1", Title: "drill", Severity: "critical"},
			resp: service.SendTestEventResp{DeliveryID: "test:I-1:1", Doorbell: "broadcast", Result: "created", TaskID: &task}},
	}
}

// oncallCodes are the codes every on-call tool declares from the service.
var oncallCodes = []domain.Code{domain.CodeForbidden, domain.CodeNotFound, domain.CodeValidation, domain.CodeStale}

// TestOncallTools: each of the nine tools maps its arguments to one service.Oncall call and its
// answer back, for a non-admin (the transport refuses nothing: FORBIDDEN is the service's) and a
// workspace admin; an unknown key is VALIDATION without a call; each declared code comes back from
// the service; and each description is 1 to 200 characters.
func TestOncallTools(t *testing.T) {
	f := servicefake.New()
	h := bootTools(t, f.Services())
	ada, admin := h.Connect(t, bearerAda), h.Connect(t, bearerAdmin)
	callers := map[*sdk.ClientSession]domain.Caller{ada: atService(toolsResolver().Callers[bearerAda]), admin: atService(toolsResolver().Callers[bearerAdmin])}
	cases := oncallCases()
	if len(cases) != len(oncallTools) {
		t.Fatalf("%d cases, want the %d on-call tools", len(cases), len(oncallTools))
	}
	listed, err := admin.ListTools(testCtx(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	desc := map[string]string{}
	for _, tl := range listed.Tools {
		desc[tl.Name] = tl.Description
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			if d, ok := desc[tc.tool]; !ok || d == "" || len(d) > 200 {
				t.Errorf("%s description (%d chars, listed %v) must be 1 to 200 characters", tc.tool, len(d), ok)
			}
			for _, b := range []struct {
				name string
				cs   *sdk.ClientSession
			}{{"happy", ada}, {"happy_admin", admin}} {
				t.Run(b.name, func(t *testing.T) {
					f.Reset()
					f.Set(tc.method, tc.resp, nil)
					res, err := b.cs.CallTool(testCtx(t), &sdk.CallToolParams{Name: tc.tool, Arguments: tc.args})
					if err != nil || res.IsError {
						t.Fatalf("%s: %+v, %v", tc.tool, res, err)
					}
					got := callsOf(f, tc.method)
					if len(got) != 1 {
						t.Fatalf("%s called %s %d times, want 1", tc.tool, tc.method, len(got))
					}
					if !reflect.DeepEqual(got[0].Req, tc.want) {
						t.Errorf("%s reached the service as %#v, want %#v", tc.tool, got[0].Req, tc.want)
					}
					if want := callers[b.cs]; got[0].Caller != want {
						t.Errorf("%s reached the service as caller %+v, want %+v", tc.tool, got[0].Caller, want)
					}
					if want := asJSON(t, tc.resp); !reflect.DeepEqual(res.StructuredContent, want) {
						t.Errorf("%s answered %v, want the service's %v", tc.tool, res.StructuredContent, want)
					}
				})
			}
			t.Run("VALIDATION_unknown_key", func(t *testing.T) {
				f.Reset()
				e := bk.ToolError(t, admin, tc.tool, with(tc.args, "unexpectedKey", true))
				if e["code"] != "VALIDATION" || len(callsOf(f, tc.method)) != 0 {
					t.Errorf("%s with an unknown key = %v after %d calls, want VALIDATION and none", tc.tool, e, len(callsOf(f, tc.method)))
				}
			})
			for _, code := range oncallCodes {
				t.Run(string(code), func(t *testing.T) {
					f.Reset()
					e := bk.ToolError(t, admin, tc.tool, with(tc.args, tc.trigger, servicefake.Trigger(code)))
					if e["code"] != string(code) || len(callsOf(f, tc.method)) != 1 {
						t.Errorf("%s = %v, want %s from one service call", tc.tool, e, code)
					}
				})
			}
		})
	}
}

// TestOncallToolsAreAdminOnlyInToolsList: a workspace admin's tools/list carries all nine on-call
// tools and a non-admin's none of them; every other tool is listed to both (the control).
func TestOncallToolsAreAdminOnlyInToolsList(t *testing.T) {
	h := bootTools(t, servicefake.New().Services())
	names := func(bearer string) []string {
		res, err := h.Connect(t, bearer).ListTools(testCtx(t), nil)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, tl := range res.Tools {
			out = append(out, tl.Name)
		}
		slices.Sort(out)
		return out
	}
	admin, ada := names(bearerAdmin), names(bearerAda)
	for _, n := range oncallTools {
		if !slices.Contains(admin, n) {
			t.Errorf("%s is not listed to a workspace admin", n)
		}
		if slices.Contains(ada, n) {
			t.Errorf("%s is listed to a non-admin", n)
		}
	}
	for _, n := range admin {
		if !slices.Contains(oncallTools, n) && n != "list_audit_log" && !slices.Contains(ada, n) {
			t.Errorf("%s is listed to the admin only, and is neither an on-call tool nor list_audit_log", n)
		}
	}
	t.Logf("workspace admin lists %d tools, the non-admin %d: the difference is the %d on-call tools and list_audit_log", len(admin), len(ada), len(oncallTools))
}
