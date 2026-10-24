package api

import (
	"encoding/base64"
	"mime"
	"net/http"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/oncall"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// The on-call routes (conductor app.ts:2906-2984): the source catalog, served from oncall.Sources
// rather than a tool, and each other route one service.Oncall call with the project and the
// integration in the path. A secret is in a create, update or rotate answer once (the service's
// shownOnce) and never in a listing; the raw route answers the kept bytes themselves.
func oncallRoutes(o *Options) []route {
	const pi = "/api/oncall/{projectId}/integrations"
	const pii = pi + "/{integrationId}"
	on := o.Services.Oncall
	integration := in{path: []string{"projectId", "integrationId"}}
	delivery := in{path: []string{"projectId", "integrationId", "deliveryId"}, noBody: true}
	return []route{
		{Method: "GET", Path: "/api/oncall/sources", h: oncallSources()},
		{Method: "GET", Path: pi, h: call(o, on.ListIntegrations, in{path: byProject.path, query: []string{"includeDeleted", "limit"}})},
		{Method: "POST", Path: pi, h: call(o, on.CreateIntegration, byProject)},
		{Method: "POST", Path: pii, h: call(o, on.UpdateIntegration, integration)},
		{Method: "DELETE", Path: pii, h: call(o, on.DeleteIntegration, in{path: integration.path, query: []string{"version"}, noBody: true})},
		{Method: "POST", Path: pii + "/rotate-secret", h: call(o, on.RotateIntegrationSecret, integration)},
		{Method: "POST", Path: pii + "/test", h: call(o, on.SendTestEvent, integration)},
		{Method: "GET", Path: pii + "/deliveries", h: call(o, on.ListDeliveries, in{path: integration.path, query: []string{"cursor", "kind", "limit"}})},
		{Method: "POST", Path: pii + "/deliveries/{deliveryId}/replay", h: call(o, on.ReplayDelivery, delivery)},
		{Method: "GET", Path: pii + "/deliveries/{deliveryId}/raw", h: deliveryRaw(o, on)},
	}
}

// oncallSource is one catalog entry as the SPA's OncallSource reads it.
type oncallSource struct {
	ID        string            `json:"id"`
	Label     string            `json:"label"`
	AuthModes []oncall.AuthMode `json:"authModes"`
	DocsPage  string            `json:"docsPage"`
	AuthNote  string            `json:"authNote"`
}

// oncallSources is GET /api/oncall/sources: the catalog the setup form renders itself from. Any
// signed-in caller may read it; it names no integration. docsPage is conductor's guide name,
// oncall-<id>.
func oncallSources() http.Handler {
	out := struct {
		Sources []oncallSource `json:"sources"`
	}{Sources: make([]oncallSource, 0, len(oncall.Sources))}
	for _, s := range oncall.Sources {
		out.Sources = append(out.Sources, oncallSource{ID: s.ID, Label: s.Label, AuthModes: s.AuthModes, DocsPage: "oncall-" + s.ID, AuthNote: s.AuthNote})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.CallerFrom(r.Context()); !ok {
			auth.WriteError(w, http.StatusUnauthorized, domain.CodeUnauthenticated, "unauthenticated")
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// deliveryRaw is GET …/deliveries/{deliveryId}/raw: read_delivery_raw's kept body as the bytes
// themselves, never JSON. They are an unauthenticated sender's, so they go out as an octet-stream
// attachment with nosniff (as /blobs does): a navigation saves them, and the SPA's viewer reads
// them as text into an escaped text node. A body no longer kept is the service's NOT_FOUND.
func deliveryRaw(o *Options, on service.Oncall) http.Handler {
	logger := o.logger()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := auth.CallerFrom(r.Context())
		if !ok {
			auth.WriteError(w, http.StatusUnauthorized, domain.CodeUnauthenticated, "unauthenticated")
			return
		}
		resp, err := on.ReadDeliveryRaw(r.Context(), caller, service.ReadDeliveryRawReq{
			ProjectID: r.PathValue("projectId"), IntegrationID: r.PathValue("integrationId"), DeliveryID: r.PathValue("deliveryId"),
		})
		if err == nil && resp.PayloadBase64 == nil {
			err = domain.NewError(domain.CodeNotFound, "This delivery's raw body is no longer kept", map[string]any{"deliveryId": r.PathValue("deliveryId")})
		}
		var body []byte
		if err == nil {
			if body, err = base64.StdEncoding.DecodeString(*resp.PayloadBase64); err != nil {
				logger.Printf("api: delivery %s raw body: %v", resp.DeliveryID, err)
				err = domain.NewError(domain.CodeInternal, "Internal error", nil)
			}
		}
		if err != nil {
			writeServiceError(w, err, logger)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": resp.SuggestedFilename}))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "private, no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}
