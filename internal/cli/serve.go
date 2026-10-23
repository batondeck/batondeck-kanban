package cli

import (
	"context"
	"log"
	"net/http"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// mcpHandler is /mcp over st: a bearer is an API token (`batondeck-kanban token create`), resolved
// by the auth service. R0 registers no tool, so the bundle carries only the session bookkeeping
// and the tool-call rows; R1.18 completes the wiring.
func mcpHandler(st store.Store, version string, logger *log.Logger) http.Handler {
	resolver := &auth.Service{Accounts: &auth.Accounts{Store: st}, Tokens: &auth.TokenService{Store: st}, Sessions: st}
	return mcp.New(mcp.Options{
		Version:  version,
		Resolver: resolver,
		Services: service.Services{Sessions: noSessions{}, ToolCalls: noToolCalls{}},
		Logger:   logger,
	}).Handler()
}

// TODO(R0.21): replaced by service core's Sessions/ToolCalls when it lands
type noSessions struct{}

func (noSessions) TouchAgentSession(context.Context, domain.Caller, service.TouchAgentSessionReq) (service.TouchAgentSessionResp, error) {
	return service.TouchAgentSessionResp{}, nil
}

func (noSessions) RemoveAgentSession(context.Context, domain.Caller, service.RemoveAgentSessionReq) (service.RemoveAgentSessionResp, error) {
	return service.RemoveAgentSessionResp{}, nil
}

func (noSessions) ListAgentSessions(context.Context, domain.Caller, service.ListAgentSessionsReq) (service.ListAgentSessionsResp, error) {
	return service.ListAgentSessionsResp{Sessions: []service.AgentSession{}}, nil
}

func (noSessions) RegisterWakeSession(context.Context, domain.Caller, service.RegisterWakeSessionReq) (service.RegisterWakeSessionResp, error) {
	return service.RegisterWakeSessionResp{}, nil
}

// TODO(R0.21): replaced by service core's Sessions/ToolCalls when it lands
type noToolCalls struct{}

func (noToolCalls) Record(context.Context, domain.Caller, service.RecordToolCallReq) (service.RecordToolCallResp, error) {
	return service.RecordToolCallResp{}, nil
}
