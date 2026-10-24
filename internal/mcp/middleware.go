package mcp

import (
	"context"
	"fmt"
	"reflect"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service/dtoschema"
)

// Call is a tool's body: one service call, from the caller and the decoded request to the response
// or a *domain.BatonDeckError. Membership, the role tier and the capability grant are the service's
// (service.authorize), never the transport's.
type Call[Req, Resp any] func(ctx context.Context, caller domain.Caller, req Req) (Resp, error)

// Tool registers the tool name of area on s, calling call. Its input and output schemas are Req's
// and Resp's as internal/service/dtoschema infers them, which contract/dto_test.go holds equal to
// contract/expected.json. Every call then passes one chain, outermost first: the caller from the
// bearer (withCaller); go-sdk validating the arguments against the input schema (an unknown key, a
// wrong type or a null into an Optional) and decoding them into Req, either failure a VALIDATION
// before the service is called (errorMiddleware); call; a non-BatonDeckError made INTERNAL and
// logged with its stack (AddTool); the one error shape and one tool_calls row (errorMiddleware).
// A tool with no domain.ToolPosture panics here: an unclassified tool is refused at registration,
// never served. It is listed to every caller unless its posture is AdminOnlyOnly.
func Tool[Req, Resp any](s *Server, area, name, description string, call Call[Req, Resp]) {
	posture, ok := domain.ToolPosture(name)
	if !ok {
		panic(fmt.Sprintf("mcp: tool %s has no domain.ToolPosture: classify it in internal/domain/grants.go", name))
	}
	t := &mcp.Tool{
		Name:         name,
		Description:  description,
		InputSchema:  dtoschema.For(reflect.TypeFor[Req]()),
		OutputSchema: dtoschema.For(reflect.TypeFor[Resp]()),
		Annotations:  annotations(posture),
	}
	var visible func(admin bool) bool
	if posture.AdminOnly == domain.AdminOnlyOnly {
		visible = func(admin bool) bool { return admin }
	}
	s.manifest.add(ManifestEntry{Name: name, Area: area, Posture: posture})
	AddTool(s, t, visible, func(ctx context.Context, _ *mcp.CallToolRequest, req Req) (*mcp.CallToolResult, Resp, error) {
		caller, ok := CallerFrom(ctx)
		if !ok {
			var zero Resp
			return nil, zero, domain.NewError(domain.CodeUnauthenticated, "no caller on the request", nil)
		}
		resp, err := call(ctx, caller, req)
		return nil, resp, err
	})
}

// annotations are the MCP hints for posture's Effect. All four are set: go-sdk omits a nil
// destructiveHint or openWorldHint, and a client reads an absent one as true.
func annotations(p domain.Posture) *mcp.ToolAnnotations {
	read := p.Effect == domain.EffectRead
	destructive := p.Effect == domain.EffectDestructive
	closed := false
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    read,
		DestructiveHint: &destructive,
		IdempotentHint:  read || p.Idempotent,
		OpenWorldHint:   &closed,
	}
}
