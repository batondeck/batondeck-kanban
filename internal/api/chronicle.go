package api

import (
	"context"
	"net/http"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// The chronicle routes (conductor app.ts:3393-3503), the URLs and bodies web/src/api/chronicle.ts
// sends. The four GETs are the chronicle resources' reads; create, edit and delete are the tools of
// the same name; revert is edit_chronicle_page with the chosen version's blocks over the head.
// ingest_chronicle_page stays MCP-only: app.ts mounts no route for it and the SPA never ingests.
func chronicleRoutes(o *Options) []route {
	const pc = pp + "/chronicle"
	ch := o.Services.Chronicle
	page := in{path: []string{"projectId", "pageId"}}
	return []route{
		{Method: "GET", Path: pc, h: fromURL(o, ch.ListPages, func(r *http.Request) service.ListChroniclePagesReq {
			return service.ListChroniclePagesReq{ProjectID: r.PathValue("projectId"), Cursor: r.URL.Query().Get("cursor")}
		})},
		{Method: "GET", Path: pc + "/{pageId}", h: fromURL(o, ch.GetPage, func(r *http.Request) service.GetChroniclePageReq {
			return service.GetChroniclePageReq{ProjectID: r.PathValue("projectId"), PageID: r.PathValue("pageId")}
		})},
		{Method: "POST", Path: pc, h: call(o, ch.CreatePage, byProject)},
		{Method: "DELETE", Path: pc + "/{pageId}", h: fromURL(o, ch.DeletePage, func(r *http.Request) service.DeleteChroniclePageReq {
			return service.DeleteChroniclePageReq{ProjectID: r.PathValue("projectId"), PageID: r.PathValue("pageId"),
				ExpectedHeadVersionID: r.URL.Query().Get("expectedHeadVersionId")}
		})},
		{Method: "GET", Path: pc + "/{pageId}/versions", h: fromURL(o, ch.ListVersions, func(r *http.Request) service.ListChronicleVersionsReq {
			return service.ListChronicleVersionsReq{ProjectID: r.PathValue("projectId"), PageID: r.PathValue("pageId"), Cursor: r.URL.Query().Get("cursor")}
		})},
		{Method: "GET", Path: pc + "/{pageId}/versions/{versionId}", h: fromURL(o, ch.GetVersion, func(r *http.Request) service.GetChronicleVersionReq {
			return service.GetChronicleVersionReq{ProjectID: r.PathValue("projectId"), PageID: r.PathValue("pageId"), VersionID: r.PathValue("versionId")}
		})},
		{Method: "POST", Path: pc + "/{pageId}/edit", h: call(o, ch.EditPage, page)},
		{Method: "POST", Path: pc + "/{pageId}/revert", h: call(o, revertChronicle(ch), page)},
	}
}

// fromURL is a route whose arguments are all in its URL: build reads Req from the path values and
// the query string, which call reads only on a GET (the chronicle DELETE pins its head as
// ?expectedHeadVersionId=…). A GET's body is ignored, as call ignores it; any other method's body
// takes no field. The service validates the values.
func fromURL[Req, Resp any](o *Options, fn func(context.Context, domain.Caller, Req) (Resp, error), build func(*http.Request) Req) http.Handler {
	logger := o.logger()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, ok := auth.CallerFrom(r.Context())
		if !ok {
			auth.WriteError(w, http.StatusUnauthorized, domain.CodeUnauthenticated, "unauthenticated")
			return
		}
		if _, err := arguments(w, r, nil, in{noBody: true}); err != nil {
			writeServiceError(w, err, logger)
			return
		}
		resp, err := fn(r.Context(), caller, build(r))
		if err != nil {
			writeServiceError(w, err, logger)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})
}

// chronicleRevertReq is POST …/chronicle/{pageId}/revert's arguments: the version to restore.
type chronicleRevertReq struct {
	PageID    string `json:"pageId"`
	ProjectID string `json:"projectId"`
	VersionID string `json:"versionId"`
}

// revertChronicle writes a new version holding versionId's blocks, its parent the head as read: a
// version, never a rewind, so the history keeps everything after the restored one. A missing page
// or version is NOT_FOUND; a head that moves between the read and the write is the edit's STALE.
// Each block's origin is the edit's to inherit (edit_chronicle_page discards a caller's). The
// answer is the edit's, as app.ts returns it.
func revertChronicle(ch service.Chronicle) func(context.Context, domain.Caller, chronicleRevertReq) (service.EditChroniclePageResp, error) {
	return func(ctx context.Context, caller domain.Caller, req chronicleRevertReq) (service.EditChroniclePageResp, error) {
		cur, err := ch.GetPage(ctx, caller, service.GetChroniclePageReq{ProjectID: req.ProjectID, PageID: req.PageID})
		if err != nil {
			return service.EditChroniclePageResp{}, err
		}
		if cur.Page == nil || cur.Head == nil {
			return service.EditChroniclePageResp{}, domain.NewError(domain.CodeNotFound, "chronicle page not found", map[string]any{"pageId": req.PageID})
		}
		old, err := ch.GetVersion(ctx, caller, service.GetChronicleVersionReq{ProjectID: req.ProjectID, PageID: req.PageID, VersionID: req.VersionID})
		if err != nil {
			return service.EditChroniclePageResp{}, err
		}
		if old.Version == nil {
			return service.EditChroniclePageResp{}, domain.NewError(domain.CodeNotFound, "chronicle version not found", map[string]any{"versionId": req.VersionID})
		}
		blocks := make([]service.ChronicleBlockInput, len(old.Version.Blocks))
		for i, b := range old.Version.Blocks {
			blocks[i] = service.ChronicleBlockInput{ID: b.ID, Text: b.Text, Citations: b.Citations}
		}
		return ch.EditPage(ctx, caller, service.EditChroniclePageReq{
			ProjectID: req.ProjectID, PageID: req.PageID, ExpectedParentVersionID: cur.Head.ID, Blocks: blocks,
		})
	}
}
