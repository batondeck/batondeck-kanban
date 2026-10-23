package service

import (
	"context"
	"sync"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// toolCalls is service.ToolCalls: Record buffers one row and returns, and a flusher writes the
// buffer in batches (store.ToolCalls.InsertToolCalls) every flush interval or as soon as a batch
// fills. Nothing it does fails the call it records: a row the store would refuse is dropped and
// logged before it can refuse the batch it rides in, a failed batch is logged and dropped, and a
// buffer full while the store is failing drops the newest rows and counts them.
type toolCalls struct {
	c     *Core
	batch int
	max   int

	mu      sync.Mutex
	buf     []store.ToolCall
	dropped int64

	kick    chan struct{}
	quit    chan struct{}
	done    chan struct{}
	stopped sync.Once
}

var _ ToolCalls = (*toolCalls)(nil)

func newToolCalls(c *Core, batch, max int, every time.Duration) *toolCalls {
	tc := &toolCalls{c: c, batch: batch, max: max, kick: make(chan struct{}, 1), quit: make(chan struct{}), done: make(chan struct{})}
	if every < 0 {
		close(tc.done)
		return tc
	}
	go tc.loop(every)
	return tc
}

// ToolCalls is the tool-call recorder the MCP middleware calls once per call.
func (c *Core) ToolCalls() ToolCalls { return c.calls }

// FlushToolCalls writes every buffered row now, and returns the store's error, which Record never
// does. Close calls it.
func (c *Core) FlushToolCalls(ctx context.Context) error { return c.calls.flush(ctx) }

func (tc *toolCalls) Record(_ context.Context, caller domain.Caller, req RecordToolCallReq) (RecordToolCallResp, error) {
	row := store.ToolCall{
		TS: tc.c.nowMs(), Tool: req.Tool, Identity: caller.Identity, Agent: caller.Agent,
		Outcome: store.ToolCallOutcome(req.Outcome), Code: req.Code, LatencyMs: req.LatencyMs, ProjectID: req.ProjectID,
	}
	if reason := invalidToolCall(row); reason != "" {
		tc.c.log.Printf("service: tool_calls: dropped a row (%s): tool=%q identity=%q outcome=%q code=%q", reason, row.Tool, row.Identity, row.Outcome, row.Code)
		return RecordToolCallResp{}, nil
	}
	tc.mu.Lock()
	if len(tc.buf) >= tc.max {
		tc.dropped++
		tc.mu.Unlock()
		return RecordToolCallResp{}, nil
	}
	tc.buf = append(tc.buf, row)
	full := len(tc.buf) >= tc.batch
	tc.mu.Unlock()
	if full {
		select {
		case tc.kick <- struct{}{}:
		default:
		}
	}
	return RecordToolCallResp{}, nil
}

// invalidToolCall is why the store would refuse the row (contract_toolcalls.go), "" when it would not.
func invalidToolCall(r store.ToolCall) string {
	switch {
	case r.Tool == "":
		return "no tool"
	case r.Identity == "":
		return "no identity"
	case r.Code == "":
		return "no code"
	case r.Outcome != store.ToolCallOK && r.Outcome != store.ToolCallError:
		return "unknown outcome"
	case r.LatencyMs < 0:
		return "negative latency"
	}
	return ""
}

// flush writes the buffer, one batch at a time; a batch the store refuses is logged and dropped.
func (tc *toolCalls) flush(ctx context.Context) error {
	tc.mu.Lock()
	rows, dropped := tc.buf, tc.dropped
	tc.buf, tc.dropped = nil, 0
	tc.mu.Unlock()
	if dropped > 0 {
		tc.c.log.Printf("service: tool_calls: dropped %d rows: the buffer of %d was full", dropped, tc.max)
	}
	var first error
	for len(rows) > 0 {
		n := min(tc.batch, len(rows))
		if err := tc.c.store.InsertToolCalls(ctx, rows[:n]); err != nil {
			tc.c.log.Printf("service: tool_calls: dropped a batch of %d rows: %+v", n, err)
			if first == nil {
				first = err
			}
		}
		rows = rows[n:]
	}
	return first
}

func (tc *toolCalls) loop(every time.Duration) {
	defer close(tc.done)
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-tc.quit:
			return
		case <-tick.C:
		case <-tc.kick:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = tc.flush(ctx)
		cancel()
	}
}

// stop ends the flusher and flushes what is left.
func (tc *toolCalls) stop(ctx context.Context) error {
	tc.stopped.Do(func() { close(tc.quit) })
	select {
	case <-tc.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return tc.flush(ctx)
}
