package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/api"
	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/auth/oauth"
	"github.com/batondeck/batondeck-kanban/internal/bus"
	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/mcp/tools"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/wake"
)

// app is what serve composes over one store: the MCP server, the OAuth server when public_url is
// set, the service core, the change-log bus and the doorbell routes. A bearer is an API token
// (`batondeck-kanban token create`) or, when publicURL is set, an OAuth access token the
// authorization server issued for /mcp; both resolve through the one auth service, on /mcp, /wake
// and /internal/wake-session alike. The session bookkeeping and the tool_calls rows go through the
// service core, and so do the projects and boards tools; R1.18 wires the remaining services. Close
// the core after the HTTP server, so it flushes the tool calls still buffered.
type app struct {
	mcp  *mcp.Server
	as   *oauth.Server // nil while public_url is unset
	core *service.Core
	bus  *bus.Bus
	// mount adds the OAuth routes, GET /wake and the socket-only POST /internal/wake-session to the
	// mux handler builds.
	mount func(*http.ServeMux)
}

// compose builds the app over st. With publicURL unset there is no authorization server and /mcp's
// 401 names no resource metadata. sealer seals a wake session's inbox token; nil refuses one.
func compose(st store.Store, version, publicURL string, logger *log.Logger, sealer service.Sealer) (*app, error) {
	resolver := &auth.Service{Accounts: &auth.Accounts{Store: st}, Tokens: &auth.TokenService{Store: st}, Sessions: st}
	var as *oauth.Server
	if publicURL != "" {
		var err error
		if as, err = oauth.New(oauth.Options{PublicURL: publicURL, Store: st, Auth: resolver, Logger: logger}); err != nil {
			return nil, err
		}
		resolver.OAuth, resolver.OAuthResource = st, as.Resource()
	} else {
		logger.Printf("serve: public_url is unset, so the OAuth authorization server is off; /mcp takes API tokens only")
	}
	sc := service.New(st, service.Options{Logger: logger, Sealer: sealer})
	opts := mcp.Options{
		Version:  version,
		Resolver: resolver,
		Services: service.Services{Projects: sc.Projects(), Boards: sc.Boards(), Sessions: sc.Sessions(), ToolCalls: sc.ToolCalls()},
		Logger:   logger,
	}
	if as != nil {
		opts.ResourceMetadataURL = as.ResourceMetadataURL()
	}
	// The engines wake the tail on commit (store.ChangeWatcher); without one it polls.
	watcher, _ := st.(store.ChangeWatcher)
	b := bus.New(st, bus.Options{Watcher: watcher, OnError: func(err error) { logger.Printf("bus: %v", err) }})
	doorbell := wake.Handler(wake.Options{Bus: b, Resolver: resolver, Scopes: sc.WakeScopes, Logger: logger, Recheck: wakeRecheck})
	inbox := api.WakeSessionHandler(resolver, sc.Sessions(), logger)
	srv := mcp.New(opts)
	tools.Register(srv, opts.Services)
	return &app{mcp: srv, as: as, core: sc, bus: b,
		mount: func(mux *http.ServeMux) {
			if as != nil {
				as.Mount(mux)
			}
			mux.Handle("GET /wake", doorbell)
			mux.Handle("POST "+internalPrefix+"wake-session", inbox)
		},
	}, nil
}

// internalPrefix is the socket-only namespace: no path under it is served over TCP.
const internalPrefix = "/internal/"

// handler is the app's routes on both networks. Over TCP every path under /internal/ is 404 whatever
// its method, before the mux sees it, so a socket-only route answers there exactly as one that does
// not exist (no 405, no Allow header naming its method).
func (a *app) handler(version string) http.Handler {
	mux := handler(version, a.mcp.Handler(), a.mount)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !core.ViaUnixSocket(r.Context()) && strings.HasPrefix(r.URL.Path, internalPrefix) {
			http.NotFound(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// openKeyring is the sealer for wake-session inbox tokens: core.OpenKeyring from
// BATONDECK_MASTER_KEY, else from master.key beside the SQLite file, or on Postgres beside the
// config file. On Postgres with neither, serve runs without one (an inbox token is then refused)
// and says so once.
func openKeyring(cfg *core.Config, cfgPath string, lookup func(string) (string, bool), logger *log.Logger) (service.Sealer, error) {
	var path string
	_, fromEnv := lookup(core.MasterKeyEnv)
	switch {
	case fromEnv:
	case cfg.Engine() == core.EngineSQLite:
		path = filepath.Join(filepath.Dir(cfg.SQLitePath()), "master.key")
	case cfgPath != "":
		path = filepath.Join(filepath.Dir(cfgPath), "master.key")
	default:
		logger.Printf("serve: WARN no keyring: set %s (or run with -config) to register a wake session with an inbox token", core.MasterKeyEnv)
		return nil, nil
	}
	k, err := core.OpenKeyring(path, lookup)
	if err != nil {
		return nil, err
	}
	return k, nil
}

// shutdownBudget bounds the MCP session close and the HTTP drain; flushBudget the tool-call flush
// after them, with its own clock. Variables only so a test can spend the first quickly.
var (
	shutdownBudget = 10 * time.Second
	flushBudget    = 5 * time.Second
)

// wakeRecheck is how often an open /wake stream re-resolves its bearer (wake.Recheck). A variable
// only so a test can watch a revocation end a stream quickly.
var wakeRecheck = wake.Recheck

// runServer serves the app over st on every listener until ctx ends or one fails.
func runServer(ctx context.Context, lns []net.Listener, st store.Store, version, publicURL string, logger *log.Logger, sealer service.Sealer) error {
	a, err := compose(st, version, publicURL, logger, sealer)
	if err != nil {
		return err
	}
	return serveUntil(ctx, lns, a, version, logger)
}

// serveUntil serves one handler on every listener until ctx ends or one fails, then stops in
// order: the MCP sessions (an attached client's open GET stream would otherwise hold Shutdown to its
// deadline; a handler still running at the deadline is cancelled), the bus (which ends every /wake
// stream, for the same reason), the HTTP server, and the tool-call flush. It returns the first
// failure, naming the step.
func serveUntil(ctx context.Context, lns []net.Listener, a *app, version string, logger *log.Logger) error {
	srv := &http.Server{Handler: a.handler(version), ReadHeaderTimeout: 10 * time.Second, ConnContext: core.ConnContext}
	errc := make(chan error, len(lns))
	for _, ln := range lns {
		go func() { errc <- srv.Serve(ln) }()
		logger.Printf("serve: batondeck-kanban %s listening on %s", version, ln.Addr())
	}
	var failed error
	select {
	case err := <-errc:
		failed = err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), shutdownBudget)
	defer cancel()
	if err := a.mcp.Close(shutdown); err != nil && failed == nil {
		failed = fmt.Errorf("closing the MCP sessions: %w", err)
	}
	a.bus.Close()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) && failed == nil {
		failed = fmt.Errorf("http shutdown: %w", err)
	}
	flush, cancelFlush := context.WithTimeout(context.Background(), flushBudget)
	defer cancelFlush()
	if err := a.core.Close(flush); err != nil {
		logger.Printf("serve: flushing the buffered tool calls: %+v", err)
	}
	return failed
}
