package service

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/memstore"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// analyticsEngines is every store the summary is proven on; the Postgres leg skips without
// BATONDECK_TEST_POSTGRES_DSN.
var analyticsEngines = []struct {
	name string
	open func(t *testing.T) store.Store
}{
	{"memstore", func(t *testing.T) store.Store {
		st := memstore.New()
		if _, err := st.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		return st
	}},
	{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }},
	{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }},
}

// The operations summary over a seeded tool_calls fixture equals the hand-computed aggregateOps
// result; days 365 is clamped to 90 (a row 100 days old stays out, one 23 days old comes in); days
// 0 is the default 7; a non-member is FORBIDDEN and a member is answered.
func TestOperationsSummary(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	at := func(month time.Month, day, hour int) int64 {
		return time.Date(2026, month, day, hour, 0, 0, 0, time.UTC).UnixMilli()
	}
	for _, e := range analyticsEngines {
		t.Run(e.name, func(t *testing.T) {
			ctx := context.Background()
			st := e.open(t)
			c := New(st, Options{ToolCallFlush: -1, Now: func() time.Time { return now }})
			t.Cleanup(func() { _ = c.Close(context.Background()) })
			alice := domain.Caller{Identity: "alice", Agent: "claude", TokenScope: "alice"}
			p, err := c.Projects().Create(ctx, alice, CreateProjectReq{Name: "ops"})
			if err != nil {
				t.Fatal(err)
			}
			id := p.ProjectID
			row := func(project, tool, agent string, ts int64, outcome store.ToolCallOutcome, code string, ms int64) store.ToolCall {
				return store.ToolCall{TS: ts, Tool: tool, Identity: "alice", Agent: agent, Outcome: outcome, Code: code, LatencyMs: ms, ProjectID: project}
			}
			if err := st.InsertToolCalls(ctx, []store.ToolCall{
				row(id, "claim_next", "claude", at(10, 24, 10), store.ToolCallOK, "OK", 10),
				row(id, "claim_next", "claude", at(10, 24, 10), store.ToolCallOK, "OK", 20),
				row(id, "claim_next", "claude", at(10, 24, 11), store.ToolCallOK, "OK", 31),
				row(id, "claim_next", "claude", at(10, 23, 9), store.ToolCallError, "STALE", 4),
				row(id, "get_task", "", at(10, 23, 9), store.ToolCallOK, "OK", 5),
				row(id, "get_task", "", at(10, 23, 9), store.ToolCallOK, "OK", 6),
				row(id, "get_task", "codex", at(10, 20, 9), store.ToolCallError, "NOT_FOUND", 100),
				row("P-other", "claim_next", "claude", at(10, 24, 10), store.ToolCallOK, "OK", 1000),
				row(id, "list_tasks", "claude", at(10, 1, 9), store.ToolCallOK, "OK", 2),
				row(id, "list_tasks", "claude", at(7, 16, 9), store.ToolCallOK, "OK", 2),
			}); err != nil {
				t.Fatal(err)
			}
			retention := OpsRetentionRow{Days: 45, OldestDay: "2026-09-09"}

			week, err := c.Analytics().Operations(ctx, alice, OperationsReq{ProjectID: id, Days: 7})
			if err != nil {
				t.Fatalf("member: %v", err)
			}
			want := OperationsResp{
				ProjectID: id, Days: 7, Total: 7, Errors: 2, ErrorRate: 2.0 / 7, AvgLatencyMs: 25.1,
				ByTool:    []OpsToolRow{{Tool: "claim_next", Calls: 4, AvgMs: 16.3}, {Tool: "get_task", Calls: 3, AvgMs: 37}},
				ByDay:     []OpsDayRow{{Day: "2026-10-20", Calls: 1}, {Day: "2026-10-23", Calls: 3}, {Day: "2026-10-24", Calls: 3}},
				ByAgent:   []OpsAgentRow{{Agent: "claude", IdentityID: "alice", Calls: 4}, {Agent: "(unknown)", IdentityID: "alice", Calls: 2}, {Agent: "codex", IdentityID: "alice", Calls: 1}},
				Retention: retention,
			}
			if !reflect.DeepEqual(week, want) {
				t.Fatalf("7 days =\n%+v\nwant\n%+v", week, want)
			}

			def, err := c.Analytics().Operations(ctx, alice, OperationsReq{ProjectID: id})
			if err != nil || !reflect.DeepEqual(def, want) {
				t.Fatalf("no days = %+v, %v; want the 7-day summary", def, err)
			}

			year, err := c.Analytics().Operations(ctx, alice, OperationsReq{ProjectID: id, Days: 365})
			if err != nil {
				t.Fatal(err)
			}
			if year.Days != 90 || year.Total != 8 || year.Retention != retention {
				t.Fatalf("365 days = days %d, total %d, retention %+v; want 90, 8 (the row 100 days old left out), %+v", year.Days, year.Total, year.Retention, retention)
			}

			_, err = c.Analytics().Operations(ctx, domain.Caller{Identity: "mallory", TokenScope: "mallory"}, OperationsReq{ProjectID: id, Days: 7})
			if be, ok := domain.AsBatonDeckError(err); !ok || be.Code != domain.CodeForbidden {
				t.Fatalf("non-member = %v, want FORBIDDEN", err)
			}

			day, err := c.Analytics().Operations(ctx, alice, OperationsReq{ProjectID: id, Days: 1})
			if err != nil || day.Total != 3 || day.ErrorRate != 0 || len(day.ByAgent) != 1 {
				t.Fatalf("1 day = %+v, %v; want today's 3 ok calls", day, err)
			}
		})
	}
}

// P-335: an agent is its identity plus its name. alice's and bob's claude-code (each a client's own
// name on a default-label token) are two byAgent rows, each naming its identity and its own count.
func TestOperationsByAgentKeysTheAgentWithItsIdentity(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	ts := now.Add(-time.Hour).UnixMilli()
	for _, e := range analyticsEngines {
		t.Run(e.name, func(t *testing.T) {
			ctx := context.Background()
			st := e.open(t)
			c := New(st, Options{ToolCallFlush: -1, Now: func() time.Time { return now }})
			t.Cleanup(func() { _ = c.Close(context.Background()) })
			alice := domain.Caller{Identity: "alice", Agent: "claude-code", TokenScope: "alice"}
			p, err := c.Projects().Create(ctx, alice, CreateProjectReq{Name: "two users"})
			if err != nil {
				t.Fatal(err)
			}
			row := func(identity, tool string) store.ToolCall {
				return store.ToolCall{TS: ts, Tool: tool, Identity: identity, Agent: "claude-code", Outcome: store.ToolCallOK, Code: "OK", LatencyMs: 1, ProjectID: p.ProjectID}
			}
			if err := st.InsertToolCalls(ctx, []store.ToolCall{
				row("alice", "get_task"), row("alice", "get_task"), row("alice", "list_tasks"),
				row("bob", "get_task"), row("bob", "claim_next"),
			}); err != nil {
				t.Fatal(err)
			}
			got, err := c.Analytics().Operations(ctx, alice, OperationsReq{ProjectID: p.ProjectID, Days: 7})
			if err != nil {
				t.Fatal(err)
			}
			want := []OpsAgentRow{{Agent: "claude-code", IdentityID: "alice", Calls: 3}, {Agent: "claude-code", IdentityID: "bob", Calls: 2}}
			if !reflect.DeepEqual(got.ByAgent, want) || got.Total != 5 {
				t.Fatalf("byAgent = %+v (total %d), want %+v: one name under two identities is two agents", got.ByAgent, got.Total, want)
			}
		})
	}
}
