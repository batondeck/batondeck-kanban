package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/api"
	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/auth/oauth"
	"github.com/batondeck/batondeck-kanban/internal/blob"
	"github.com/batondeck/batondeck-kanban/internal/bus"
	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/mcp/tools"
	"github.com/batondeck/batondeck-kanban/internal/retention"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/wake"
	"github.com/batondeck/batondeck-kanban/internal/web"
)

// app is what serve composes over one store: the MCP server, the OAuth server when public_url is
// set, the service core, the change-log bus and the device session directory with its socket-only
// routes. A bearer is an API token (`batondeck-kanban token create`) or, when publicURL is set, an
// OAuth access token the authorization server issued for /mcp; both resolve through the one auth
// service, on /mcp and /internal/wake-session alike. The session bookkeeping and the tool_calls rows go through the
// service core, and so do the projects and boards tools; R1.18 wires the remaining services. Close
// the core after the HTTP server, so it flushes the tool calls still buffered.
type app struct {
	mcp  *mcp.Server
	as   *oauth.Server // nil while public_url is unset
	core *service.Core
	bus  *bus.Bus
	// dir is the device session directory (R0.25); serveUntil starts it.
	dir *wake.Directory
	// sweep is the retention sweep, run every sweepEvery while serveUntil serves.
	sweep      *retention.Sweeper
	sweepEvery time.Duration
	// engine is the store's, which /healthz names.
	engine string
	// mount adds the OAuth routes, the socket-only POST and DELETE /internal/wake-session and the
	// REST BFF (api.Mount) to the mux handler builds.
	mount func(*http.ServeMux)
}

// compose builds the app over st. With publicURL unset there is no authorization server and /mcp's
// 401 names no resource metadata. sealer seals a wake session's inbox token; nil refuses one. media
// is the blob store; with its Files and Signer set, the signed PUT and GET /blobs/{id} are mounted.
// ret is the retention sweep's windows and interval; a zero field is core.DefaultRetention's.
func compose(st store.Store, version, publicURL string, logger *log.Logger, sealer service.Sealer, media service.MediaOptions, ret core.Retention) (*app, error) {
	return composeWith(st, version, publicURL, logger, sealer, media, ret, wakeTuning{registry: wakeRegistry(), sweep: wakeSweep})
}

// wakeTuning is the device session directory's registry, sweep interval and coalescing windows.
// compose's, and so serve's, is wakeRegistry(), wakeSweep and the zero windows (ADR-0004's
// numbers); a test passes its own to composeWith, so that tests running in parallel share no
// package variable.
type wakeTuning struct {
	registry string
	sweep    time.Duration
	windows  wake.Windows
}

// composeWith is compose with the directory's tuning given.
func composeWith(st store.Store, version, publicURL string, logger *log.Logger, sealer service.Sealer, media service.MediaOptions, ret core.Retention, wt wakeTuning) (*app, error) {
	resolver := &auth.Service{Accounts: &auth.Accounts{Store: st}, Tokens: &auth.TokenService{Store: st, Audit: st}, Sessions: st}
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
	media.MaxBytes = cmp.Or(media.MaxBytes, core.DefaultBlobMaxBytes)
	def := core.DefaultRetention
	ret = core.Retention{ChangeLogDays: cmp.Or(ret.ChangeLogDays, def.ChangeLogDays), ToolCallsDays: cmp.Or(ret.ToolCallsDays, def.ToolCallsDays),
		DeliveriesDays: cmp.Or(ret.DeliveriesDays, def.DeliveriesDays), RawBodiesDays: cmp.Or(ret.RawBodiesDays, def.RawBodiesDays),
		SweepIntervalSeconds: cmp.Or(ret.SweepIntervalSeconds, def.SweepIntervalSeconds)}
	day := 24 * time.Hour
	sc := service.New(st, service.Options{Logger: logger, Sealer: sealer, Media: media,
		DeliveryRetention: time.Duration(ret.DeliveriesDays) * day, RawBodyRetention: time.Duration(ret.RawBodiesDays) * day,
		ToolCallRetentionDays: int64(ret.ToolCallsDays)})
	sweep := &retention.Sweeper{Store: st, Windows: retention.WindowsOf(ret), Report: func(step string, err error) { logger.Printf("retention: %s: %+v", step, err) }}
	if media.Files != nil {
		sweep.CollectBlobs = sc.CollectBlobs
	}
	sweepEvery := time.Duration(ret.SweepIntervalSeconds) * time.Second
	logger.Printf("serve: retention sweep every %s: change log %d days, tool calls %d days, deliveries %d days, raw bodies %d days, agent sessions %s idle",
		sweepEvery, ret.ChangeLogDays, ret.ToolCallsDays, ret.DeliveriesDays, ret.RawBodiesDays, retention.AgentSessionIdle)
	opts := mcp.Options{
		Version:   version,
		PublicURL: publicURL,
		Resolver:  resolver,
		Services: service.Services{Projects: sc.Projects(), Boards: sc.Boards(), Tasks: sc.Tasks(), Lifecycle: sc.Lifecycle(), Context: sc.Context(), Media: sc.Media(), Artifacts: sc.Artifacts(), Comments: sc.Comments(), FollowUps: sc.FollowUps(), Chronicle: sc.Chronicle(), Feed: sc.Feed(), Sessions: sc.Sessions(), ToolCalls: sc.ToolCalls(), Oncall: sc.Oncall(), Runs: sc.Runs(), Sprints: sc.Sprints(),
			Notifications: sc.Notifications(), Profiles: sc.Profiles(), Anomalies: sc.Anomalies(), Ranking: sc.Ranking(), Me: sc.Me(), Audit: sc.Audit(), Grants: sc.Grants(), Analytics: sc.Analytics()},
		Logger:      logger,
		HiddenTools: sc.HiddenTools,
	}
	if as != nil {
		opts.ResourceMetadataURL = as.ResourceMetadataURL()
	}
	// The engines wake the tail on commit (store.ChangeWatcher); without one it polls.
	watcher, _ := st.(store.ChangeWatcher)
	b := bus.New(st, bus.Options{Watcher: watcher, OnError: func(err error) { logger.Printf("bus: %v", err) }})
	dir, err := wake.NewDirectory(wake.Options{Bus: b, Resolver: resolver, Scopes: sc.WakeScopes, Sessions: sc.WakeStore(), Registry: wt.registry, Logger: logger, Sweep: wt.sweep, Windows: wt.windows})
	if err != nil {
		return nil, err
	}
	sc.SetDeviceSessions(dir)
	srv := mcp.New(opts)
	sc.SetAgentSessionEnder(srv)
	tools.Register(srv, opts.Services)
	// The REST BFF calls every service; its presence frames read the device directory.
	bff := api.Options{Services: opts.Services, Auth: resolver, Users: resolver.Accounts, Tokens: resolver.Tokens, Bus: b, Presence: dir, Logger: logger, OAuth: as != nil}
	return &app{mcp: srv, as: as, core: sc, bus: b, dir: dir, sweep: sweep, sweepEvery: sweepEvery, engine: engineOf(st),
		mount: func(mux *http.ServeMux) {
			api.Mount(mux, bff)
			if as != nil {
				as.Mount(mux)
			}
			// GET /wake is retired (ADR-0004): 404, not the SPA shell the catch-all would answer.
			mux.Handle("GET /wake", http.NotFoundHandler())
			mux.Handle("POST "+internalPrefix+"wake-session", wake.RegisterHandler(dir, resolver, logger))
			mux.Handle("DELETE "+internalPrefix+"wake-session/{pid}", wake.UnregisterHandler(dir, resolver, logger))
			// The webhook ingest: outside every bearer, session and CSRF check (its signature is the
			// credential), over TCP as well as the socket, since telemetry senders are remote.
			mux.Handle("POST /hooks/{integrationId}", web.WithoutCSP(api.HookRoutes{Hooks: sc, Logger: logger}.Handler()))
			if media.Files != nil && media.Signer != nil {
				blobs := &api.BlobRoutes{Signer: media.Signer, Files: media.Files, MaxBytes: media.MaxBytes, Uploads: sc, Logger: logger}
				mux.Handle("PUT /blobs/{id}", web.WithoutCSP(blobs.Upload()))
				mux.Handle("GET /blobs/{id}", web.WithoutCSP(blobs.Download()))
			}
		},
	}, nil
}

// engineOf names st's engine for /healthz ("" for a store that does not say).
func engineOf(st store.Store) string {
	if e, ok := st.(interface{ Engine() string }); ok {
		return e.Engine()
	}
	return ""
}

// internalPrefix is the socket-only namespace: no path under it is served over TCP.
const internalPrefix = "/internal/"

// handler is the app's routes on both networks. Over TCP every path under /internal/ is 404 whatever
// its method, before the mux sees it, so a socket-only route answers there exactly as one that does
// not exist (no 405, no Allow header naming its method). Every response carries web.SecurityHeaders.
func (a *app) handler(version string) http.Handler {
	mux := engineHandler(version, a.engine, a.mcp.Handler(), a.mount)
	return web.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !core.ViaUnixSocket(r.Context()) && strings.HasPrefix(r.URL.Path, internalPrefix) {
			http.NotFound(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	}))
}

// openKeyring is the keyring (the sealer for wake-session inbox tokens and the blob signing key's
// source): core.OpenKeyring from
// BATONDECK_MASTER_KEY, else from master.key beside the SQLite file, or on Postgres beside the
// config file. On Postgres with neither, serve runs without one (an inbox token is then refused)
// and says so once.
func openKeyring(cfg *core.Config, cfgPath string, lookup func(string) (string, bool), logger *log.Logger) (*core.Keyring, error) {
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
	return core.OpenKeyring(path, lookup)
}

// mediaOptions is the blob store at cfg.BlobDir() and, with a keyring, the URL signer keyed by its
// blob.SigningLabel key. The URLs start with public_url, else with the first TCP listener's
// address. Without a keyring attach_file is refused and no download URL is signed, said once.
func mediaOptions(cfg *core.Config, keys *core.Keyring, lns []net.Listener, logger *log.Logger) (service.MediaOptions, error) {
	files, err := blob.Open(cfg.BlobDir())
	if err != nil {
		return service.MediaOptions{}, err
	}
	m := service.MediaOptions{Files: files, MaxBytes: cfg.Blob.MaxBytes, BaseURL: cfg.PublicURL}
	if keys == nil {
		logger.Printf("serve: WARN no keyring: attach_file is refused and no download URL is signed; set %s (or run with -config)", core.MasterKeyEnv)
		return m, nil
	}
	key, err := keys.DeriveKey(blob.SigningLabel)
	if err != nil {
		return service.MediaOptions{}, err
	}
	if m.Signer, err = blob.NewSigner(key); err != nil {
		return service.MediaOptions{}, err
	}
	for _, ln := range lns {
		if m.BaseURL == "" && ln.Addr().Network() == "tcp" {
			m.BaseURL = "http://" + ln.Addr().String()
		}
	}
	logger.Printf("serve: blobs in %s, up to %d bytes each, signed URLs at %s/blobs/", files.Dir(), m.MaxBytes, m.BaseURL)
	return m, nil
}

// shutdownBudget bounds the MCP session close and the HTTP drain; flushBudget the tool-call flush
// after them, with its own clock. Variables only so a test can spend the first quickly.
var (
	shutdownBudget = 10 * time.Second
	flushBudget    = 5 * time.Second
)

// wakeSweep is how often serve's directory re-resolves every entry's bearer (wake.Sweep). A test
// that needs a quicker sweep passes its own through composeWith (wakeTuning).
var wakeSweep = wake.Sweep

// wakeRegistry is Claude Code's session registry directory: $CLAUDE_CONFIG_DIR/sessions, else
// ~/.claude/sessions. A variable only so a test can point it at a fake registry.
var wakeRegistry = func() string {
	if d, ok := os.LookupEnv("CLAUDE_CONFIG_DIR"); ok && d != "" {
		return filepath.Join(d, "sessions")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "sessions")
}

// snapshotTapBuffer is the board snapshot cache's bus.Tap buffer: a change it drops clears every
// snapshot.
const snapshotTapBuffer = 1024

// feedSnapshots hands every change the bus delivers to the board snapshot cache
// (Core.ObserveChange, ADR-0004) until the tap closes with the bus, then turns the cache off. A change
// the tap dropped clears every snapshot, since which board it named is unknown. It reads only the
// tap: no statement, no poll.
func feedSnapshots(tap *bus.Subscription, sc *service.Core, logger *log.Logger) {
	defer sc.ResetSnapshots(false)
	var dropped int64
	for c := range tap.C() {
		sc.ObserveChange(c)
		if n := tap.Dropped(); n > dropped {
			logger.Printf("serve: the board snapshot cache's tap dropped %d change(s); clearing every snapshot", n-dropped)
			dropped = n
			sc.ResetSnapshots(true)
		}
	}
}

// runServer serves the app over st on every listener until ctx ends or one fails.
func runServer(ctx context.Context, lns []net.Listener, st store.Store, version, publicURL string, logger *log.Logger, sealer service.Sealer, media service.MediaOptions, ret core.Retention) error {
	a, err := compose(st, version, publicURL, logger, sealer, media, ret)
	if err != nil {
		return err
	}
	return serveUntil(ctx, lns, a, version, logger)
}

// serveUntil serves one handler on every listener, with the retention sweep running beside it, until
// ctx ends or one fails, then stops in order: the sweep (it returns before the store can close), the
// MCP sessions (an attached client's open GET stream would otherwise hold Shutdown to its
// deadline; a handler still running at the deadline is cancelled), the bus (which stops the
// session directory's dispatcher and the board snapshot cache's feed), the HTTP server, and the
// tool-call flush. The cache's feed is the bus's second tap, so it adds no statement. It returns the first
// failure, naming the step.
func serveUntil(ctx context.Context, lns []net.Listener, a *app, version string, logger *log.Logger) error {
	if err := a.dir.Start(ctx); err != nil {
		return fmt.Errorf("starting the session directory: %w", err)
	}
	snaps, err := a.bus.Tap(ctx, snapshotTapBuffer)
	if err != nil {
		a.bus.Close()
		return fmt.Errorf("subscribing the board snapshot cache to the bus: %w", err)
	}
	a.core.ResetSnapshots(true)
	go feedSnapshots(snaps, a.core, logger)
	sweepCtx, stopSweep := context.WithCancel(ctx)
	swept := make(chan struct{})
	go func() { defer close(swept); a.sweep.Run(sweepCtx, a.sweepEvery) }()
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
	stopSweep()
	<-swept
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
