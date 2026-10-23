package api

// Owner: R2.10 replaces these stubs (app.ts:2906-2984); the source catalog is served, not a tool.
func oncallRoutes(o *Options) []route {
	const pi = "/api/oncall/{projectId}/integrations"
	const pii = pi + "/{integrationId}"
	return []route{
		{Method: "GET", Path: "/api/oncall/sources", h: stub(o, "the on-call source catalog", "R2.10")},
		{Method: "GET", Path: pi, h: stub(o, "list_integrations", "R2.10")},
		{Method: "POST", Path: pi, h: stub(o, "create_integration", "R2.10")},
		{Method: "POST", Path: pii, h: stub(o, "update_integration", "R2.10")},
		{Method: "DELETE", Path: pii, h: stub(o, "delete_integration", "R2.10")},
		{Method: "POST", Path: pii + "/rotate-secret", h: stub(o, "rotate_integration_secret", "R2.10")},
		{Method: "POST", Path: pii + "/test", h: stub(o, "send_test_event", "R2.10")},
		{Method: "GET", Path: pii + "/deliveries", h: stub(o, "list_deliveries", "R2.10")},
		{Method: "POST", Path: pii + "/deliveries/{deliveryId}/replay", h: stub(o, "replay_delivery", "R2.10")},
		{Method: "GET", Path: pii + "/deliveries/{deliveryId}/raw", h: stub(o, "read_delivery_raw", "R2.10")},
	}
}
