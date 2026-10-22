package conformance

import (
	"context"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("toolcalls", toolCalls) }

// toolCalls is the tool-call area: a batch lands whole, a bad row refuses the whole batch, and the
// retention delete is by age and bounded. The contract has no reader, so the delete's count is what
// proves the rows landed.
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
			} {
				wantErr(t, s.InsertToolCalls(ctx, []store.ToolCall{good, bad}), domain.CodeValidation, "")
			}
			if n := deleted(t, s, 1<<62, 10); n != 0 {
				t.Fatalf("refused batches left %d rows", n)
			}
			_, err := s.DeleteToolCallsOlderThan(ctx, 1<<62, 0)
			wantErr(t, err, domain.CodeValidation, "")
		})
	})
}
