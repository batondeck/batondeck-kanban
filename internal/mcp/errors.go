package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strings"
	"time"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// errorMiddleware gives every failed tools/call the one error shape (contract/errors.json): a
// result with isError, the BatonDeckError {code, message, details?, retryable} as structured
// content and as its text. A handler's BatonDeckError passes through (AddTool has already made any
// other handler error INTERNAL); any other failed result is go-sdk refusing the arguments before
// the handler ran, so it is VALIDATION. A panic, and a non-protocol error go-sdk returns after the
// handler (its output failing the output schema), are INTERNAL, logged. Every call,
// whatever its outcome, is then recorded once through service.ToolCalls.
func (s *Server) errorMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (res mcp.Result, err error) {
		if method != "tools/call" {
			return next(ctx, method, req)
		}
		start := time.Now()
		note := &callNote{}
		ctx = context.WithValue(ctx, callNoteKey{}, note)
		defer func() {
			if p := recover(); p != nil {
				s.log.Printf("mcp: tool %s panicked: %v\n%s", toolName(req), p, debug.Stack())
				res, err = errorResult(domain.NewError(domain.CodeInternal, "internal error", nil)), nil
			}
			s.record(ctx, req, res, err, time.Since(start), note.refused)
		}()
		res, err = next(ctx, method, req)
		if err != nil {
			if _, protocol := err.(*jsonrpc.Error); !protocol {
				// A non-protocol error after the handler (go-sdk refusing the tool's own output
				// against its output schema, or failing to marshal it) is the server's bug: INTERNAL.
				s.log.Printf("mcp: tool %s failed after its handler: %+v", toolName(req), err)
				return errorResult(domain.NewError(domain.CodeInternal, "internal error", nil)), nil
			}
			return res, err
		}
		r, _ := res.(*mcp.CallToolResult)
		if r == nil || !r.IsError || r.StructuredContent != nil {
			return res, err
		}
		e, ok := domain.AsBatonDeckError(r.GetError())
		if !ok {
			note.refused = true
			msg := "invalid arguments"
			if cause := r.GetError(); cause != nil {
				msg = cause.Error()
			}
			e = domain.Validation(msg, nil)
		}
		return errorResult(e), nil
	}
}

// callNote is what one tools/call's chain tells errorMiddleware about it: refused is set when its
// arguments were refused before any service judged the caller (go-sdk's input schema, or
// domain.CheckText in Tool), so no membership of the project they name was ever checked.
type callNote struct{ refused bool }

type callNoteKey struct{}

// noteRefused marks the call in ctx as refused before the service.
func noteRefused(ctx context.Context) {
	if n, ok := ctx.Value(callNoteKey{}).(*callNote); ok {
		n.refused = true
	}
}

// record writes the call's tool_calls row: outcome ok with code OK, else error with the code the
// client received (for a protocol error, whose result go-sdk hands back as a typed nil, VALIDATION
// when it is invalid params, as an unknown tool's -32602 is, else INTERNAL), and the projectId the
// call named when countsInProject allows it, which the operations summary groups by. A call whose
// arguments were refused before the service (refused) carries no project: the caller's membership
// of it was never checked, so a non-member could otherwise move any project's summary with
// malformed calls naming it.
// It never fails or aborts the call: ToolCalls.Record returns no error to act on, and a panic in
// the bookkeeping is recovered and logged here.
func (s *Server) record(ctx context.Context, req mcp.Request, res mcp.Result, err error, took time.Duration, refused bool) {
	defer func() {
		if p := recover(); p != nil {
			s.log.Printf("mcp: recording tool call %s panicked: %v\n%s", toolName(req), p, debug.Stack())
		}
	}()
	row := service.RecordToolCallReq{Tool: s.registeredName(req), Outcome: "ok", Code: "OK", LatencyMs: took.Milliseconds()}
	r, _ := res.(*mcp.CallToolResult)
	if err != nil || r == nil || r.IsError {
		row.Outcome, row.Code = "error", string(domain.CodeInternal)
		if rpc, ok := err.(*jsonrpc.Error); ok && rpc.Code == jsonrpc.CodeInvalidParams {
			row.Code = string(domain.CodeValidation)
		}
		if err == nil && r != nil {
			if e, ok := domain.AsBatonDeckError(r.GetError()); ok {
				row.Code = string(e.Code)
			}
		}
	}
	if !refused && s.countsInProject(row) {
		row.ProjectID = projectOf(req)
	}
	caller, _ := CallerFrom(ctx)
	if _, rerr := s.services.ToolCalls.Record(ctx, caller, row); rerr != nil {
		s.log.Printf("mcp: recording tool call %s failed: %+v", row.Tool, rerr)
	}
}

// unknownTool is the name a tools/call of a tool nobody registered is recorded and counted under
// (tool_calls, agent_sessions.tool_counts): the name it sent is the caller's free text (P-115).
const unknownTool = "(unknown)"

// errorResult is e as a tool result.
func errorResult(e *domain.BatonDeckError) *mcp.CallToolResult {
	text, err := json.Marshal(e)
	if err != nil {
		text = []byte(fmt.Sprintf(`{"code":%q,"message":"unencodable error","retryable":%t}`, e.Code, e.Retryable))
	}
	r := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(text)}}, StructuredContent: json.RawMessage(text)}
	r.SetError(e)
	return r
}

// countsInProject is whether the row may carry the call's project: not for a tool nobody registered
// (its name is the caller's free text) nor for a FORBIDDEN or UNAUTHENTICATED call, so a non-member
// moves no project's summary.
func (s *Server) countsInProject(row service.RecordToolCallReq) bool {
	if row.Code == string(domain.CodeForbidden) || row.Code == string(domain.CodeUnauthenticated) {
		return false
	}
	s.tools.mu.Lock()
	defer s.tools.mu.Unlock()
	return s.tools.known[row.Tool]
}

// registeredName is the call's tool as the bookkeeping writes it: its name when a tool of that name
// is registered, else unknownTool, so no caller text reaches tool_calls or agent_sessions.
func (s *Server) registeredName(req mcp.Request) string {
	name := toolName(req)
	s.tools.mu.Lock()
	defer s.tools.mu.Unlock()
	if s.tools.known[name] {
		return name
	}
	return unknownTool
}

// idMaxBytes bounds an id the bookkeeping takes from a call's arguments: the server's own are a
// dozen bytes ("P-" and twelve), so a longer one is a caller's free text.
const idMaxBytes = 64

// projectOf is the call's projectId argument, the row's project; "" when it names none, no string,
// or nothing id-shaped: longer than idMaxBytes, or carrying a control character (a NUL, which
// Postgres cannot store, would refuse the whole tool_calls batch the row rides in).
func projectOf(req mcp.Request) string {
	p, ok := req.GetParams().(*mcp.CallToolParamsRaw)
	if !ok || len(p.Arguments) == 0 {
		return ""
	}
	var args struct {
		ProjectID any `json:"projectId"`
	}
	_ = json.Unmarshal(p.Arguments, &args)
	id, _ := args.ProjectID.(string)
	if len(id) > idMaxBytes || strings.ContainsFunc(id, unicode.IsControl) {
		return ""
	}
	return id
}

func toolName(req mcp.Request) string {
	if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok {
		return p.Name
	}
	return "?"
}
