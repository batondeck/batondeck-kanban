package mcp

import (
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// middleware filters tools/list to the caller's audience and memoises the first page per audience;
// AddTool clears the memo, and a list read across a registration is not memoised. On any request it
// first notes the session's audience, and tells the session its list changed when that differs from
// the one its last request carried.
func (l *toolList) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		c, _ := CallerFrom(ctx)
		a := l.audienceOf(ctx, c)
		if ss, ok := req.GetSession().(*mcp.ServerSession); ok && ss.ID() != "" {
			l.note(ss, a, method == "initialize")
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
			if len(l.memo) >= maxMemo {
				clear(l.memo)
			}
			l.memo[a] = &out
		}
		return &out, nil
	}
}

// maxMemo bounds the memoised lists: an audience per distinct grant shape, so a project admin
// writing many grants grows it; past the bound it starts again.
const maxMemo = 256

// audienceOf is c's audience. A failed grant read is logged and hides nothing: the list is
// visibility, and the call is still refused by its grant (service.authorize).
func (l *toolList) audienceOf(ctx context.Context, c domain.Caller) audience {
	a := audience{admin: c.WorkspaceAdmin}
	if l.hidden == nil {
		return a
	}
	tools, err := l.hidden(ctx, c)
	if err != nil {
		l.log.Printf("mcp: the tools hidden from %s: %+v", c.Identity, err)
		return a
	}
	a.hidden = strings.Join(tools, ",")
	return a
}

// note records ss's audience a, and when its previous request carried another, sends ss
// notifications/tools/list_changed (asynchronously: the request does not wait on the stream). Only
// an initialize adds an entry: a session that never initialises (go-sdk's server/discover probe)
// has no end that would forget it.
func (l *toolList) note(ss *mcp.ServerSession, a audience, initialize bool) {
	l.mu.Lock()
	prev, known := l.seen[ss.ID()]
	if !known && !initialize {
		l.mu.Unlock()
		return
	}
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
