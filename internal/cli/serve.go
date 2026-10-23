package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/auth/oauth"
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// mcpServer is the MCP server over st. A bearer is an API token (`batondeck-kanban token
// create`) or, when publicURL is set, an OAuth access token the authorization server it returns
// issued for /mcp; both resolve through the one auth service. With publicURL unset there is no
// authorization server and /mcp's 401 names no resource metadata. The session bookkeeping and the
// tool_calls rows go through the service core. R0 registers no tool; R1.18 wires the remaining
// services. Close the core after the HTTP server, so it flushes the tool calls still buffered.
func mcpServer(st store.Store, version, publicURL string, logger *log.Logger) (*mcp.Server, *oauth.Server, *service.Core, error) {
	resolver := &auth.Service{Accounts: &auth.Accounts{Store: st}, Tokens: &auth.TokenService{Store: st}, Sessions: st}
	var as *oauth.Server
	if publicURL != "" {
		var err error
		if as, err = oauth.New(oauth.Options{PublicURL: publicURL, Store: st, Auth: resolver, Logger: logger}); err != nil {
			return nil, nil, nil, err
		}
		resolver.OAuth, resolver.OAuthResource = st, as.Resource()
	} else {
		logger.Printf("serve: public_url is unset, so the OAuth authorization server is off; /mcp takes API tokens only")
	}
	core := service.New(st, service.Options{Logger: logger})
	opts := mcp.Options{
		Version:  version,
		Resolver: resolver,
		Services: service.Services{Sessions: core.Sessions(), ToolCalls: core.ToolCalls()},
		Logger:   logger,
	}
	if as != nil {
		opts.ResourceMetadataURL = as.ResourceMetadataURL()
	}
	return mcp.New(opts), as, core, nil
}

// shutdownBudget bounds the MCP session close and the HTTP drain; flushBudget the tool-call flush
// after them, with its own clock. Variables only so a test can spend the first quickly.
var (
	shutdownBudget = 10 * time.Second
	flushBudget    = 5 * time.Second
)

// runServer serves the MCP server, and the OAuth server when publicURL is set, over st on ln until
// ctx ends or the listener fails.
func runServer(ctx context.Context, ln net.Listener, st store.Store, version, publicURL string, logger *log.Logger) error {
	mcpSrv, as, core, err := mcpServer(st, version, publicURL, logger)
	if err != nil {
		return err
	}
	return serveUntil(ctx, ln, mcpSrv, as, core, version, logger)
}

// serveUntil serves until ctx ends or the listener fails, then stops in order: the MCP sessions
// (an attached client's open GET stream would otherwise hold Shutdown to its deadline; a handler
// still running at the deadline is cancelled), the HTTP server, and the tool-call flush. It
// returns the first failure, naming the step.
func serveUntil(ctx context.Context, ln net.Listener, mcpSrv *mcp.Server, as *oauth.Server, core *service.Core, version string, logger *log.Logger) error {
	srv := &http.Server{Handler: handler(version, mcpSrv.Handler(), as), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	logger.Printf("serve: batondeck-kanban %s listening on %s", version, ln.Addr())
	var failed error
	select {
	case err := <-errc:
		failed = err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), shutdownBudget)
	defer cancel()
	if err := mcpSrv.Close(shutdown); err != nil && failed == nil {
		failed = fmt.Errorf("closing the MCP sessions: %w", err)
	}
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) && failed == nil {
		failed = fmt.Errorf("http shutdown: %w", err)
	}
	flush, cancelFlush := context.WithTimeout(context.Background(), flushBudget)
	defer cancelFlush()
	if err := core.Close(flush); err != nil {
		logger.Printf("serve: flushing the buffered tool calls: %+v", err)
	}
	return failed
}
