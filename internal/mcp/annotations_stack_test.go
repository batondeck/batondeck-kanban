package mcp_test

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// TestReadEffectWritesNothing ties EffectRead to behaviour: every tool classified a read, called on
// a board seeded so that detect_anomalies has a first sighting to record, leaves the board feed and
// the change log as they were. The control after it is detect_anomalies writing on that seed.
func TestReadEffectWritesNothing(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bootStack(t, s)
		c := &caller{t: t, cs: h.Connect(t, stackRoot), called: map[string]int{}}
		p := str(c.call("create_project", map[string]any{"name": "reads"})["projectId"])
		b := str(obj(c.call("create_board", map[string]any{"projectId": p, "name": "reads"})["board"])["id"])
		task := str(obj(c.call("create_task", map[string]any{"projectId": p, "boardId": b, "title": "thrash"})["task"])["id"])
		for range domain.DefaultAnomalyPolicy.ClaimThrashCount { // a claim_thrash first sighting
			lease := str(c.call("claim_task", map[string]any{"projectId": p, "taskId": task})["leaseId"])
			c.call("release_task", map[string]any{"projectId": p, "taskId": task, "leaseId": lease})
		}
		sprint := str(obj(c.call("create_sprint", map[string]any{"projectId": p, "boardId": b, "name": "S1", "goal": "g", "acceptance": "a"})["sprint"])["id"])
		integration := str(obj(c.call("create_integration", map[string]any{"projectId": p, "name": "reads", "source": "generic", "boardId": b,
			"auth": oncallSourceAuth["generic"], "routing": map[string]any{"mode": "broadcast"}})["integration"])["id"])

		values := map[string]any{"projectId": p, "boardId": b, "taskId": task, "parentTaskId": task, "sprintId": sprint,
			"integrationId": integration, "deliveryId": "D-none", "query": "thrash"}
		extra := map[string]map[string]any{"detect_anomalies": {"boardId": b}, "recall_memory": {"projectId": p}}
		refused := map[string]bool{"read_delivery_raw": true} // no delivery: NOT_FOUND
		listed, err := c.cs.ListTools(testCtx(t), nil)
		if err != nil {
			t.Fatal(err)
		}
		schemas := map[string]*sdk.Tool{}
		for _, tl := range listed.Tools {
			schemas[tl.Name] = tl
		}

		before := writes(t, s, p, b)
		reads := 0
		for _, name := range domain.ClassifiedTools() {
			if posture, _ := domain.ToolPosture(name); posture.Effect != domain.EffectRead {
				continue
			}
			tl, ok := schemas[name]
			if !ok {
				t.Fatalf("%s is a read and not listed", name)
			}
			args := map[string]any{}
			required, _ := tl.InputSchema.(map[string]any)["required"].([]any)
			for _, r := range required {
				v, ok := values[r.(string)]
				if !ok {
					t.Fatalf("%s requires %s: give it a value", name, r)
				}
				args[r.(string)] = v
			}
			for k, v := range extra[name] {
				args[k] = v
			}
			res, err := c.cs.CallTool(testCtx(t), &sdk.CallToolParams{Name: name, Arguments: args})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if res.IsError != refused[name] {
				t.Errorf("%s %v: isError=%v: %s", name, args, res.IsError, text(res))
			}
			if after := writes(t, s, p, b); after != before {
				t.Errorf("%s is EffectRead and wrote: (events, changes) %v -> %v", name, before, after)
				before = after
			}
			reads++
		}
		t.Logf("%d reads called on %s", reads, s.Engine)

		c.call("detect_anomalies", map[string]any{"projectId": p, "boardId": b})
		if after := writes(t, s, p, b); after[0] <= before[0] {
			t.Fatalf("control: detect_anomalies recorded no first sighting on the seed: %v -> %v", before, after)
		}
	})
}

// writes is the board feed's event count and the change log's length.
func writes(t *testing.T, s *servicetest.Stack, p, b string) [2]int {
	t.Helper()
	ctx := context.Background()
	feed, err := s.Store.ListBoardFeed(ctx, p, b, "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if feed.NextCursor != "" {
		t.Fatal("the board feed has more than one page")
	}
	changes, err := s.Store.ReadChanges(ctx, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	return [2]int{len(feed.Events), len(changes)}
}
