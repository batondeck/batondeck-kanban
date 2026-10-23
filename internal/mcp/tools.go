package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// middleware filters tools/list to the caller's audience and memoises the first page per audience;
// AddTool clears the memo, and a list read across a registration is not memoised. On any request it
// first notes the session's audience, and tells the session its list changed when that differs from
// the one its last request carried.
func (l *toolList) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		c, _ := CallerFrom(ctx)
		a := audience{admin: c.WorkspaceAdmin}
		if ss, ok := req.GetSession().(*mcp.ServerSession); ok && ss.ID() != "" {
			l.note(ss, a)
		}
		if method != "tools/list" {
			return next(ctx, method, req)
		}
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

// note records ss's audience a, and when its previous request carried another, sends ss
// notifications/tools/list_changed (asynchronously: the request does not wait on the stream).
func (l *toolList) note(ss *mcp.ServerSession, a audience) {
	l.mu.Lock()
	prev, known := l.seen[ss.ID()]
	l.seen[ss.ID()] = a
	send := l.send
	l.mu.Unlock()
	if !known || prev == a || send == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req := &mcp.ServerRequest[*mcp.ToolListChangedParams]{Session: ss, Params: &mcp.ToolListChangedParams{}}
		if _, err := send(ctx, "notifications/tools/list_changed", req); err != nil {
			l.log.Printf("mcp: tools/list_changed to session %s failed: %+v", ss.ID(), err)
		}
	}()
}

// forget drops an ended session's audience.
func (l *toolList) forget(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.seen, id)
}
