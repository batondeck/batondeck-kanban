package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// errorMiddleware gives every failed tools/call the one error shape (contract/errors.json): a
// result with isError, the BatonDeckError {code, message, details?, retryable} as structured
// content and as its text. A handler's BatonDeckError passes through (AddTool has already made any
// other handler error INTERNAL); any other failed result is go-sdk refusing the arguments before
// the handler ran, so it is VALIDATION. A panic is INTERNAL, logged with its stack. Every call,
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
		r, ok := res.(*mcp.CallToolResult)
		if err != nil || !ok || !r.IsError || r.StructuredContent != nil {
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
// client received (INTERNAL for a protocol error). ToolCalls.Record never fails the call.
func (s *Server) record(ctx context.Context, req mcp.Request, res mcp.Result, err error, took time.Duration) {
	row := service.RecordToolCallReq{Tool: toolName(req), Outcome: "ok", Code: "OK", LatencyMs: took.Milliseconds()}
	if r, ok := res.(*mcp.CallToolResult); err != nil || !ok || r.IsError {
		row.Outcome, row.Code = "error", string(domain.CodeInternal)
		if ok && r.IsError {
			if e, isBDE := domain.AsBatonDeckError(r.GetError()); isBDE {
				row.Code = string(e.Code)
			}
		}
	}
	caller, _ := CallerFrom(ctx)
	_, _ = s.services.ToolCalls.Record(ctx, caller, row)
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

func toolName(req mcp.Request) string {
	if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok {
		return p.Name
	}
	return "?"
}
