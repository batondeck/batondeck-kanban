package mcp_test

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// Review B1 of #65, on every engine: a non-member's refused calls on project P, and a call to a tool
// nobody registered naming P, add nothing to P's operations summary; the owner's own call does.
func TestForeignCallsStayOutOfTheSummary(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bootStack(t, s)
		ada := &caller{t: t, cs: h.Connect(t, stackOwner), called: map[string]int{}}
		grace := h.Connect(t, stackGrace)
		p := str(ada.call("create_project", map[string]any{"name": "Victim"})["projectId"])
		ctx := context.Background()
		for range 3 {
			_, _ = grace.CallTool(ctx, &sdk.CallToolParams{Name: "get_project", Arguments: map[string]any{"projectId": p}})
		}
		_, _ = grace.CallTool(ctx, &sdk.CallToolParams{Name: "BUY-CHEAP-PILLS-example.com", Arguments: map[string]any{"projectId": p}})
		// Arguments refused before the service judged membership: an unknown key and a wrong type
		// (go-sdk's input schema), and a NUL character (domain.CheckText).
		_, _ = grace.CallTool(ctx, &sdk.CallToolParams{Name: "get_project", Arguments: map[string]any{"projectId": p, "zzz": 1}})
		_, _ = grace.CallTool(ctx, &sdk.CallToolParams{Name: "get_task", Arguments: map[string]any{"projectId": p, "taskId": 5}})
		_, _ = grace.CallTool(ctx, &sdk.CallToolParams{Name: "get_task", Arguments: map[string]any{"projectId": p, "taskId": "T-\x001"}})
		ada.call("get_project", map[string]any{"projectId": p})
		if err := s.Core.FlushToolCalls(ctx); err != nil {
			t.Fatal(err)
		}
		sum, err := s.Services.Analytics.Operations(ctx, domain.Caller{Identity: "u-ada", TokenScope: "u-ada"}, service.OperationsReq{ProjectID: p})
		if err != nil {
			t.Fatal(err)
		}
		if sum.Total != 1 || sum.Errors != 0 || len(sum.ByTool) != 1 || sum.ByTool[0].Tool != "get_project" {
			t.Fatalf("project %s's summary = total %d, errors %d, byTool %+v; want the owner's one get_project", p, sum.Total, sum.Errors, sum.ByTool)
		}
	})
}
