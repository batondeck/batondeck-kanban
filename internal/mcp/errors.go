package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"time"

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
		defer func() {
			if p := recover(); p != nil {
				s.log.Printf("mcp: tool %s panicked: %v\n%s", toolName(req), p, debug.Stack())
				res, err = errorResult(domain.NewError(domain.CodeInternal, "internal error", nil)), nil
			}
			s.record(ctx, req, res, err, time.Since(start))
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
			msg := "invalid arguments"
			if cause := r.GetError(); cause != nil {
				msg = cause.Error()
			}
			e = domain.Validation(msg, nil)
		}
		return errorResult(e), nil
	}
}

// record writes the call's tool_calls row: outcome ok with code OK, else error with the code the
// client received (INTERNAL for a protocol error, whose result go-sdk hands back as a typed nil),
// and the projectId the call named, which the operations summary groups by.
// It never fails or aborts the call: ToolCalls.Record returns no error to act on, and a panic in
// the bookkeeping is recovered and logged here.
func (s *Server) record(ctx context.Context, req mcp.Request, res mcp.Result, err error, took time.Duration) {
	defer func() {
		if p := recover(); p != nil {
			s.log.Printf("mcp: recording tool call %s panicked: %v\n%s", toolName(req), p, debug.Stack())
		}
	}()
	row := service.RecordToolCallReq{Tool: toolName(req), Outcome: "ok", Code: "OK", LatencyMs: took.Milliseconds(), ProjectID: projectOf(req)}
	r, _ := res.(*mcp.CallToolResult)
	if err != nil || r == nil || r.IsError {
		row.Outcome, row.Code = "error", string(domain.CodeInternal)
		if err == nil && r != nil {
			if e, ok := domain.AsBatonDeckError(r.GetError()); ok {
				row.Code = string(e.Code)
			}
		}
	}
	caller, _ := CallerFrom(ctx)
	if _, rerr := s.services.ToolCalls.Record(ctx, caller, row); rerr != nil {
		s.log.Printf("mcp: recording tool call %s failed: %+v", row.Tool, rerr)
	}
}

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

// projectOf is the call's projectId argument, the row's project; "" when it names none or no string.
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
	return id
}

func toolName(req mcp.Request) string {
	if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok {
		return p.Name
	}
	return "?"
}
