package conformance

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("toolcalls", toolCalls) }

// toolCalls is the tool-call area: a batch lands whole, a bad row refuses the whole batch, the
// retention delete is by age and bounded, and the summary groups one project's rows since a time by
// tool, outcome, agent, identity and UTC day.
func toolCalls(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error { return s.InsertToolCalls(ctx, nil) }
	area(t, "ToolCalls", newStore, probe, func(t *testing.T) {
		call := func(ts int64, outcome store.ToolCallOutcome, code string) store.ToolCall {
			return store.ToolCall{TS: ts, Tool: "claim_next", Identity: "alice", Agent: "claude", Outcome: outcome, Code: code, LatencyMs: 12, ProjectID: "P-1"}
		}
		deleted := func(t *testing.T, s store.Store, before int64, limit int) int64 {
			t.Helper()
			n, err := s.DeleteToolCallsOlderThan(ctx, before, limit)
			noErr(t, err)
			return n
		}

		t.Run("ABatchLandsAndRetentionDeletesByAgeBounded", func(t *testing.T) {
			s := migrated(t, newStore)
			noErr(t, s.InsertToolCalls(ctx, []store.ToolCall{
				call(100, store.ToolCallOK, "OK"), call(200, store.ToolCallError, "STALE"),
				call(300, store.ToolCallOK, "OK"), call(400, store.ToolCallOK, "OK"),
			}))
			if n := deleted(t, s, 300, 10); n != 2 {
				t.Fatalf("deleted %d older than 300, want 2", n)
			}
			if n := deleted(t, s, 1<<62, 1); n != 1 {
				t.Fatalf("a delete of limit 1 deleted %d", n)
			}
			if n := deleted(t, s, 1<<62, 10); n != 1 {
				t.Fatalf("the last delete deleted %d, want the one row left", n)
			}
			noErr(t, s.InsertToolCalls(ctx, nil))
			if n := deleted(t, s, 1<<62, 10); n != 0 {
				t.Fatalf("an empty batch wrote %d rows", n)
			}
		})

		t.Run("ABadRowRefusesTheWholeBatch", func(t *testing.T) {
			s := migrated(t, newStore)
			good := call(100, store.ToolCallOK, "OK")
			for _, bad := range []store.ToolCall{
				{TS: 1, Identity: "alice", Outcome: store.ToolCallOK, Code: "OK"},
				{TS: 1, Tool: "t", Outcome: store.ToolCallOK, Code: "OK"},
				{TS: 1, Tool: "t", Identity: "alice", Outcome: "maybe", Code: "OK"},
				{TS: 1, Tool: "t", Identity: "alice", Outcome: store.ToolCallOK},
				{TS: 1, Tool: "t", Identity: "alice", Outcome: store.ToolCallOK, Code: "OK", LatencyMs: -1},
				// A NUL in any text field: Postgres cannot store it, so every engine refuses it alike.
				{TS: 1, Tool: "t\x00", Identity: "alice", Outcome: store.ToolCallOK, Code: "OK"},
				{TS: 1, Tool: "t", Identity: "alice", Agent: "a\x00", Outcome: store.ToolCallOK, Code: "OK"},
				{TS: 1, Tool: "t", Identity: "alice", Outcome: store.ToolCallOK, Code: "OK", ProjectID: "P\x00"},
			} {
				wantErr(t, s.InsertToolCalls(ctx, []store.ToolCall{good, bad}), domain.CodeValidation, "")
			}
			if n := deleted(t, s, 1<<62, 10); n != 0 {
				t.Fatalf("refused batches left %d rows", n)
			}
			_, err := s.DeleteToolCallsOlderThan(ctx, 1<<62, 0)
			wantErr(t, err, domain.CodeValidation, "")
		})

		t.Run("TheSummaryGroupsOneProjectSinceATimeByUTCDay", func(t *testing.T) {
			s := migrated(t, newStore)
			late := time.Date(2026, 10, 1, 23, 59, 0, 0, time.UTC).UnixMilli() // a minute before a UTC midnight
			row := func(project, tool, agent string, ts int64, outcome store.ToolCallOutcome, code string, ms int64) store.ToolCall {
				return store.ToolCall{TS: ts, Tool: tool, Identity: "alice", Agent: agent, Outcome: outcome, Code: code, LatencyMs: ms, ProjectID: project}
			}
			noErr(t, s.InsertToolCalls(ctx, []store.ToolCall{
				row("P-1", "claim_next", "claude", late, store.ToolCallOK, "OK", 10),
				row("P-1", "claim_next", "claude", late+30_000, store.ToolCallOK, "OK", 20),
				row("P-1", "claim_next", "claude", late+120_000, store.ToolCallOK, "OK", 5),
				row("P-1", "claim_next", "claude", late+120_000, store.ToolCallError, "STALE", 7),
				row("P-1", "get_task", "", late, store.ToolCallOK, "OK", 3),
				row("P-2", "claim_next", "claude", late, store.ToolCallOK, "OK", 100),
				row("P-1", "get_task", "", late-24*3600_000, store.ToolCallOK, "OK", 9),
			}))
			got, err := s.SummarizeToolCalls(ctx, "P-1", late-3600_000)
			noErr(t, err)
			want := []store.ToolCallGroup{
				{Tool: "claim_next", Outcome: store.ToolCallError, Agent: "claude", Identity: "alice", Day: "2026-10-02", Calls: 1, LatencySum: 7},
				{Tool: "claim_next", Outcome: store.ToolCallOK, Agent: "claude", Identity: "alice", Day: "2026-10-01", Calls: 2, LatencySum: 30},
				{Tool: "claim_next", Outcome: store.ToolCallOK, Agent: "claude", Identity: "alice", Day: "2026-10-02", Calls: 1, LatencySum: 5},
				{Tool: "get_task", Outcome: store.ToolCallOK, Agent: "", Identity: "alice", Day: "2026-10-01", Calls: 1, LatencySum: 3},
			}
			if !slices.Equal(got, want) {
				t.Fatalf("summary = %+v\nwant      %+v", got, want)
			}
			none, err := s.SummarizeToolCalls(ctx, "P-404", 0)
			noErr(t, err)
			if none == nil || len(none) != 0 {
				t.Fatalf("a project with no rows = %#v, want an empty, non-nil slice", none)
			}
			_, err = s.SummarizeToolCalls(ctx, "", 0)
			wantErr(t, err, domain.CodeValidation, "")
		})

		// P-335: one agent name under two identities is two groups, ordered by identity.
		t.Run("TheSummaryGroupsTheSameAgentOfTwoIdentitiesApart", func(t *testing.T) {
			s := migrated(t, newStore)
			at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).UnixMilli()
			row := func(identity string, ms int64) store.ToolCall {
				return store.ToolCall{TS: at, Tool: "get_task", Identity: identity, Agent: "claude-code", Outcome: store.ToolCallOK, Code: "OK", LatencyMs: ms, ProjectID: "P-1"}
			}
			noErr(t, s.InsertToolCalls(ctx, []store.ToolCall{row("bob", 4), row("alice", 1), row("alice", 2)}))
			got, err := s.SummarizeToolCalls(ctx, "P-1", 0)
			noErr(t, err)
			want := []store.ToolCallGroup{
				{Tool: "get_task", Outcome: store.ToolCallOK, Agent: "claude-code", Identity: "alice", Day: "2026-10-01", Calls: 2, LatencySum: 3},
				{Tool: "get_task", Outcome: store.ToolCallOK, Agent: "claude-code", Identity: "bob", Day: "2026-10-01", Calls: 1, LatencySum: 4},
			}
			if !slices.Equal(got, want) {
				t.Fatalf("summary = %+v\nwant      %+v", got, want)
			}
		})
	})
}
