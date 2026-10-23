package cli

import (
	"context"
	"log"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// mcpServer is the MCP server over st. A bearer is an API token (`batondeck-kanban token
// create`), resolved by the auth service; the session bookkeeping and the tool_calls rows go
// through the service core. R0 registers no tool; R1.18 wires the remaining services. Close the
// core after the HTTP server, so it flushes the tool calls still buffered.
func mcpServer(st store.Store, version string, logger *log.Logger) (*mcp.Server, *service.Core) {
	resolver := &auth.Service{Accounts: &auth.Accounts{Store: st}, Tokens: &auth.TokenService{Store: st}, Sessions: st}
	core := service.New(st, service.Options{Logger: logger})
	srv := mcp.New(mcp.Options{
		Version:  version,
		Resolver: resolver,
		Services: service.Services{Sessions: core.Sessions(), ToolCalls: core.ToolCalls()},
		Logger:   logger,
	})
	return srv, core
}

// closeCore flushes and stops the core, logging a failure.
func closeCore(ctx context.Context, core *service.Core, logger *log.Logger) {
	if err := core.Close(ctx); err != nil {
		logger.Printf("serve: flushing tool calls: %+v", err)
	}
}
