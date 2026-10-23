package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// middleware filters tools/list to the caller's audience and memoises the first page per audience;
// AddTool clears the memo, and a list read across a registration is not memoised.
func (l *toolList) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method != "tools/list" {
			return next(ctx, method, req)
		}
		c, _ := CallerFrom(ctx)
		a := audience{admin: c.WorkspaceAdmin}
		first := true
		if p, ok := req.GetParams().(*mcp.ListToolsParams); ok && p != nil && p.Cursor != "" {
			first = false
		}
		l.mu.Lock()
		gen := l.gen
		memo, ok := l.memo[a]
		l.mu.Unlock()
		if first && ok {
			return memo, nil
		}
		res, err := next(ctx, method, req)
		r, ok := res.(*mcp.ListToolsResult)
		if err != nil || !ok {
			return res, err
		}
		l.mu.Lock()
		defer l.mu.Unlock()
		out := *r
		out.Tools = make([]*mcp.Tool, 0, len(r.Tools))
		for _, t := range r.Tools {
			if v, ok := l.visible[t.Name]; !ok || v(a) {
				out.Tools = append(out.Tools, t)
			}
		}
		if first && gen == l.gen {
			l.memo[a] = &out
		}
		return &out, nil
	}
}
