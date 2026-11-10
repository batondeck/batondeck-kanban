package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"runtime/debug"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// Scheme is the one URI scheme the resources are served under; conductor's legacy conductor://
// is not served (plan §Target surface).
const Scheme = "batondeck"

// resource is one readable resource template (conductor src/resources/index.ts:245-283). Each read
// is one service call as the caller, which authorizes it (tier read on the project); resources
// are readable only: nothing is pushed, and resources/subscribe is not served (ADR-0004).
type resource struct {
	name, path, title, description string
	// segments is the URI's path after the project id, "" where a variable sits.
	segments []string
	read     func(ctx context.Context, svc service.Services, c domain.Caller, projectID string, vars []string, cursor string) (any, error)
}

var resources = []resource{
	{
		name: "board", path: "{projectId}/board/{boardId}", title: "Board", description: "Column layout + per-column task counts",
		segments: []string{"board", ""},
		read: func(ctx context.Context, svc service.Services, c domain.Caller, p string, v []string, _ string) (any, error) {
			return svc.Feed.BoardSnapshot(ctx, c, service.BoardSnapshotReq{ProjectID: p, BoardID: v[0]})
		},
	},
	{
		name: "task", path: "{projectId}/task/{taskId}", title: "Task", description: "The task document",
		segments: []string{"task", ""},
		read: func(ctx context.Context, svc service.Services, c domain.Caller, p string, v []string, _ string) (any, error) {
			return svc.Tasks.Get(ctx, c, service.GetTaskReq{ProjectID: p, TaskID: v[0]})
		},
	},
	{
		name: "task-context", path: "{projectId}/task/{taskId}/context", title: "Task context", description: "Composed briefing (summary, items, memory, deps)",
		segments: []string{"task", "", "context"},
		read: func(ctx context.Context, svc service.Services, c domain.Caller, p string, v []string, _ string) (any, error) {
			return svc.Context.GetTaskContext(ctx, c, service.GetTaskContextReq{ProjectID: p, TaskID: v[0]})
		},
	},
	{
		name: "board-feed", path: "{projectId}/board/{boardId}/feed", title: "Activity feed", description: "Recent events for a board",
		segments: []string{"board", "", "feed"},
		read:     readFeed,
	},
	{
		name: "board-feed-cursor", path: "{projectId}/board/{boardId}/feed{?cursor}", title: "Activity feed",
		description: "Older events for a board: re-read with ?cursor=<nextCursor> while nextCursor is not empty",
		segments:    []string{"board", "", "feed"},
		read:        readFeed,
	},
	{
		name: "chronicle", path: "{projectId}/chronicle", title: "Chronicle index",
		description: "Decision records and topic pages for this project. Paginated: when nextCursor is present more pages exist; re-read with ?cursor=<nextCursor>",
		segments:    []string{"chronicle"},
		read:        readChronicleIndex,
	},
	{
		name: "chronicle-cursor", path: "{projectId}/chronicle{?cursor}", title: "Chronicle index",
		description: "Decision records and topic pages for this project. Paginated: when nextCursor is present more pages exist; re-read with ?cursor=<nextCursor>",
		segments:    []string{"chronicle"},
		read:        readChronicleIndex,
	},
	{
		name: "chronicle-page", path: "{projectId}/chronicle/{pageId}", title: "Chronicle page", description: "A decision record with its current blocks and provenance",
		segments: []string{"chronicle", ""},
		read: func(ctx context.Context, svc service.Services, c domain.Caller, p string, v []string, _ string) (any, error) {
			return svc.Chronicle.GetPage(ctx, c, service.GetChroniclePageReq{ProjectID: p, PageID: v[0]})
		},
	},
	{
		name: "chronicle-history", path: "{projectId}/chronicle/{pageId}/versions", title: "Chronicle history",
		description: "Version history for a page, newest first. Paginated: when nextCursor is present older versions exist; re-read with ?cursor=<nextCursor>",
		segments:    []string{"chronicle", "", "versions"},
		read:        readChronicleHistory,
	},
	{
		name: "chronicle-history-cursor", path: "{projectId}/chronicle/{pageId}/versions{?cursor}", title: "Chronicle history",
		description: "Version history for a page, newest first. Paginated: when nextCursor is present older versions exist; re-read with ?cursor=<nextCursor>",
		segments:    []string{"chronicle", "", "versions"},
		read:        readChronicleHistory,
	},
	{
		name: "chronicle-version", path: "{projectId}/chronicle/{pageId}/versions/{versionId}", title: "Chronicle version", description: "One immutable version of a page, blocks included",
		segments: []string{"chronicle", "", "versions", ""},
		read: func(ctx context.Context, svc service.Services, c domain.Caller, p string, v []string, _ string) (any, error) {
			return svc.Chronicle.GetVersion(ctx, c, service.GetChronicleVersionReq{ProjectID: p, PageID: v[0], VersionID: v[1]})
		},
	},
}

func readChronicleIndex(ctx context.Context, svc service.Services, c domain.Caller, p string, _ []string, cursor string) (any, error) {
	return svc.Chronicle.ListPages(ctx, c, service.ListChroniclePagesReq{ProjectID: p, Cursor: cursor})
}

func readChronicleHistory(ctx context.Context, svc service.Services, c domain.Caller, p string, v []string, cursor string) (any, error) {
	return svc.Chronicle.ListVersions(ctx, c, service.ListChronicleVersionsReq{ProjectID: p, PageID: v[0], Cursor: cursor})
}

func readFeed(ctx context.Context, svc service.Services, c domain.Caller, p string, v []string, cursor string) (any, error) {
	return svc.Feed.BoardFeed(ctx, c, service.BoardFeedReq{ProjectID: p, BoardID: v[0], Cursor: cursor})
}

// registerResources adds the resource templates, each reading through srv's services as the
// request's caller. The ids and the cursor taken from the URI pass domain.CheckText first, as a
// tool's arguments do (a NUL is VALIDATION, never a store error), and a panic in the read is
// INTERNAL with its stack logged, as a tool's is: go-sdk recovers no handler panic.
func registerResources(srv *Server) {
	for _, r := range resources {
		srv.sdk.AddResourceTemplate(&mcp.ResourceTemplate{
			Name: r.name, Title: r.title, Description: r.description, MIMEType: "application/json",
			URITemplate: Scheme + "://" + r.path,
		}, func(ctx context.Context, req *mcp.ReadResourceRequest) (res *mcp.ReadResourceResult, err error) {
			uri := req.Params.URI
			projectID, vars, cursor, ok := r.match(uri)
			if !ok {
				return nil, mcp.ResourceNotFoundError(uri)
			}
			caller, ok := CallerFrom(ctx)
			if !ok {
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "no caller on the request"}
			}
			if err := domain.CheckText(r.args(projectID, vars, cursor)); err != nil {
				return nil, readError(uri, err)
			}
			defer func() {
				if p := recover(); p != nil {
					srv.log.Printf("mcp: resource %s read panicked: %v\n%s", uri, p, debug.Stack())
					res, err = nil, readError(uri, domain.NewError(domain.CodeInternal, "internal error", nil))
				}
			}()
			out, err := r.read(ctx, srv.services, caller, projectID, vars, cursor)
			if err == nil {
				var text []byte
				if text, err = json.Marshal(out); err == nil {
					return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(text)}}}, nil
				}
			}
			if _, ok := domain.AsBatonDeckError(err); !ok {
				srv.log.Printf("mcp: resource %s read failed: %+v", uri, err)
			}
			return nil, readError(uri, err)
		})
	}
}

// args is a read's URI values by their template names (projectId, taskId, ..., cursor), for
// domain.CheckText to name the one it refuses.
func (r resource) args(projectID string, vars []string, cursor string) map[string]any {
	out := map[string]any{"projectId": projectID, "cursor": cursor}
	i := 0
	for _, seg := range strings.Split(r.path, "/")[1:] {
		if name, ok := strings.CutPrefix(seg, "{"); ok && i < len(vars) {
			out[strings.TrimSuffix(name, "}")] = vars[i]
			i++
		}
	}
	return out
}

// match splits uri into the project id, the variables of r's path in order and the cursor query
// parameter; ok is false for any other scheme or shape.
func (r resource) match(uri string) (projectID string, vars []string, cursor string, ok bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != Scheme || u.Host == "" || u.User != nil || u.Fragment != "" {
		return "", nil, "", false
	}
	segs := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(segs) != len(r.segments) {
		return "", nil, "", false
	}
	for i, want := range r.segments {
		switch {
		case want == "" && segs[i] != "":
			vars = append(vars, segs[i])
		case want == "" || segs[i] != want:
			return "", nil, "", false
		}
	}
	return u.Host, vars, u.Query().Get("cursor"), true
}

// readError is a failed read as a JSON-RPC error: NOT_FOUND and VALIDATION are invalid params (MCP's
// resource-not-found code, SEP-2164), any other BatonDeckError internal error; each carries the
// BatonDeckError {code, message, details?, retryable} as its data, and any other error is INTERNAL.
func readError(uri string, err error) error {
	e, ok := domain.AsBatonDeckError(err)
	if !ok {
		e = domain.NewError(domain.CodeInternal, "internal error", nil)
	}
	code := int64(jsonrpc.CodeInternalError)
	if e.Code == domain.CodeNotFound || e.Code == domain.CodeValidation {
		code = jsonrpc.CodeInvalidParams
	}
	data, merr := json.Marshal(e)
	if merr != nil {
		data = []byte(fmt.Sprintf(`{"code":%q,"retryable":%t}`, e.Code, e.Retryable))
	}
	return &jsonrpc.Error{Code: code, Message: fmt.Sprintf("%s: %s", uri, e.Message), Data: data}
}
