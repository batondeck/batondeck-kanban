package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// errorMiddleware gives every failed tools/call the one error shape (contract/errors.json): a
// result with isError, the BatonDeckError {code, message, details?, retryable} as structured
// content and as its text. A handler's BatonDeckError passes through; any other failed result is
// go-sdk refusing the arguments before the handler ran (the service returns nothing but
// BatonDeckErrors), so it is VALIDATION. A panic is INTERNAL, logged with its stack.
func (s *Server) errorMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (res mcp.Result, err error) {
		if method != "tools/call" {
			return next(ctx, method, req)
		}
		defer func() {
			if p := recover(); p != nil {
				s.log.Printf("mcp: tool %s panicked: %v\n%s", toolName(req), p, debug.Stack())
				res, err = errorResult(domain.NewError(domain.CodeInternal, "internal error", nil)), nil
			}
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
