package store

import (
	"context"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The tool-call area of contract_toolcalls.go, on both engines: a batch is validated whole before
// any statement, then written one INSERT per row inside one Atomically; the retention delete is one
// bounded DELETE, oldest (ts, id) first.

type toolCallQueries interface {
	InsertToolCall(ctx context.Context, arg sqlitedb.InsertToolCallParams) error
	DeleteToolCallsOlderThan(ctx context.Context, arg sqlitedb.DeleteToolCallsOlderThanParams) (int64, error)
}

func (s *SQLite) InsertToolCalls(ctx context.Context, batch []ToolCall) error {
	return insertToolCalls(ctx, s, batch)
}

func (s *Postgres) InsertToolCalls(ctx context.Context, batch []ToolCall) error {
	return insertToolCalls(ctx, s, batch)
}

func (s *SQLite) DeleteToolCallsOlderThan(ctx context.Context, before int64, limit int) (int64, error) {
	return deleteToolCallsOlderThan(ctx, s, before, limit)
}

func (s *Postgres) DeleteToolCallsOlderThan(ctx context.Context, before int64, limit int) (int64, error) {
	return deleteToolCallsOlderThan(ctx, s, before, limit)
}

func validToolCall(c ToolCall) error {
	switch {
	case c.Tool == "" || c.Identity == "" || c.Code == "":
		return domain.Validation("a tool call needs a tool, an identity and a code", nil)
	case c.Outcome != ToolCallOK && c.Outcome != ToolCallError:
		return domain.Validation("unknown tool-call outcome: "+string(c.Outcome), map[string]any{"outcome": c.Outcome})
	case c.LatencyMs < 0:
		return domain.Validation("a tool call's latency cannot be negative", map[string]any{"latencyMs": c.LatencyMs})
	}
	return nil
}

func insertToolCalls(ctx context.Context, e engine, batch []ToolCall) error {
	for _, c := range batch {
		if err := validToolCall(c); err != nil {
			return err
		}
	}
	if len(batch) == 0 {
		return nil
	}
	return e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		for _, c := range batch {
			if err := q.InsertToolCall(ctx, sqlitedb.InsertToolCallParams{
				Ts: c.TS, Tool: c.Tool, Identity: c.Identity, Agent: c.Agent, Outcome: string(c.Outcome),
				Code: c.Code, LatencyMs: c.LatencyMs, ProjectID: c.ProjectID,
			}); err != nil {
				return fmt.Errorf("store: insert tool calls: %w", err)
			}
		}
		return nil
	})
}

func deleteToolCallsOlderThan(ctx context.Context, e engine, before int64, limit int) (int64, error) {
	if limit <= 0 {
		return 0, domain.Validation("a delete needs a positive limit", map[string]any{"limit": limit})
	}
	n, err := e.queries(true).DeleteToolCallsOlderThan(ctx, sqlitedb.DeleteToolCallsOlderThanParams{Before: before, PageLimit: int64(limit)})
	if err != nil {
		return 0, fmt.Errorf("store: delete tool calls: %w", err)
	}
	return n, nil
}

func (p pgQueries) InsertToolCall(ctx context.Context, arg sqlitedb.InsertToolCallParams) error {
	return p.q.InsertToolCall(ctx, pgdb.InsertToolCallParams(arg))
}

func (p pgQueries) DeleteToolCallsOlderThan(ctx context.Context, arg sqlitedb.DeleteToolCallsOlderThanParams) (int64, error) {
	return p.q.DeleteToolCallsOlderThan(ctx, pgdb.DeleteToolCallsOlderThanParams(arg))
}
