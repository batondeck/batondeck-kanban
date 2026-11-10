package mcp_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// A resource read whose URI carries a NUL in an id is VALIDATION on every engine, as get_task's is:
// on Postgres it reached the store and came back a retryable INTERNAL, on SQLite NOT_FOUND.
func TestResourceReadRefusesNULOnEveryEngine(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bootStack(t, s)
		c := &caller{t: t, cs: h.Connect(t, stackOwner), called: map[string]int{}}
		p := str(c.call("create_project", map[string]any{"name": "Nul", "idempotencyKey": "p-nul"})["projectId"])
		_, err := c.cs.ReadResource(testCtx(t), &sdk.ReadResourceParams{URI: "batondeck://" + p + "/task/T%00"})
		var werr *jsonrpc.Error
		var data domain.BatonDeckError
		if !errors.As(err, &werr) || werr.Code != jsonrpc.CodeInvalidParams || json.Unmarshal(werr.Data, &data) != nil || data.Code != domain.CodeValidation {
			t.Errorf("%s: read of a task id with a NUL = %v, want JSON-RPC invalid params with VALIDATION", s.Engine, err)
		}
	})
}
