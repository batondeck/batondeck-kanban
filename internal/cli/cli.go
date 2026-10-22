package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/web"
)

// Run dispatches os.Args-style arguments; version is the build-stamped version
// string. It returns a process exit code. The commands' output, their usage and their flag parse
// errors go to the stdout and stderr it is given, not to the process's.
//
// The first argument names the command: serve, migrate, token, user, doctor, healthcheck or
// version. No arguments, or a command it does not know, prints the usage to stderr and returns 2.
// Across the commands 0 is success, 1 is a failure of the work, and 2 is a usage error (a flag
// that does not parse, an unknown command) or a command not implemented in this release.
func Run(args []string, version string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version":
		fmt.Fprintln(stdout, "batondeck-kanban "+version)
		return 0
	case "serve":
		return serve(rest, version, stderr)
	case "healthcheck":
		return healthcheck(rest, stderr)
	case "migrate":
		return migrate(rest, stdout, stderr)
	case "doctor":
		return doctor(rest, stdout, stderr)
	case "token", "user":
		fmt.Fprintf(stderr, "%s: not implemented in this release\n", cmd)
		return 2
	default:
		fmt.Fprintf(stderr, "batondeck-kanban: unknown command %q\n", cmd)
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: batondeck-kanban <command> [flags]

commands:
  serve        migrate the store, then run the server: GET /healthz and the embedded web UI
  migrate      run the store's migrations on the engine database.url selects
  token        manage API tokens (not implemented in this release)
  user         manage users (not implemented in this release)
  doctor       check the config and report the store engine the connection answers as
  healthcheck  probe /healthz on the configured listen address (container HEALTHCHECK)
  version      print the version
`)
}

// commonFlags returns a FlagSet with the -config flag every subcommand shares.
//
// It takes the caller's stderr because `Run` is handed writers and must use them.
// Without SetOutput, flag writes usage and parse errors to the process's
// os.Stderr instead: invisible to an embedder capturing output, noisy in tests
// that asked for silence.
func commonFlags(name string, cfgPath *string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(cfgPath, "config", os.Getenv("BATONDECK_CONFIG"), "path to config file (JSON)")
	return fs
}

func loadConfig(cfgPath string) (*core.Config, error) {
	return core.Load(cfgPath, os.LookupEnv)
}

// openStore opens the engine database.url selects. For SQLite, create says whether a missing file
// may be made (with its directory): serve and migrate make it, doctor does not.
func openStore(ctx context.Context, cfg *core.Config, create bool) (store.Store, error) {
	if cfg.Engine() == core.EnginePostgres {
		return store.OpenPostgres(ctx, cfg.Database.URL)
	}
	path := cfg.SQLitePath()
	if create {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return store.OpenSQLite(path, cfg.Database.Synchronous)
}

// migratedLine says what one Migrate did.
func migratedLine(m store.Migrated) string {
	if m.None {
		return "no migrations in this binary; goose's version table is in place"
	}
	if m.Applied == 0 {
		return fmt.Sprintf("no change, schema at version %d", m.Version)
	}
	return fmt.Sprintf("%d applied, schema at version %d", m.Applied, m.Version)
}

// healthBody is /healthz's whole answer: liveness only, naming the binary and its version.
func healthBody(version string) string { return "batondeck-kanban " + version + " ok\n" }

// handler is everything serve answers: /healthz, and the embedded SPA for every other path.
func handler(version string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, healthBody(version))
	})
	web.MountSPA(mux)
	return mux
}

// serve listens on the configured address until SIGINT or SIGTERM, then shuts down.
func serve(args []string, version string, stderr io.Writer) int {
	var cfgPath string
	fs := commonFlags("serve", &cfgPath, stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "serve:", err)
		return 1
	}
	log := core.NewLogger(stderr)
	// On SQLite the data-dir lock is held for serve's lifetime: alone while this serve migrates,
	// then shared, so a serve that starts beside another finds it migrated and does not migrate.
	var lock *core.Lock
	if cfg.Engine() == core.EngineSQLite {
		if lock, err = lockSQLiteDir(cfg, core.AcquireServeLock); err != nil {
			fmt.Fprintln(stderr, "serve:", err)
			return 1
		}
		defer lock.Release()
	}
	// The store is migrated before anything listens, so a fresh SQLite file has its schema by the
	// time /healthz answers (hdtp-gateway's serve does the same).
	st, err := openStore(context.Background(), cfg, true)
	if err != nil {
		fmt.Fprintf(stderr, "serve: %s: %v\n", cfg.Engine(), err)
		return 1
	}
	defer st.Close()
	line := "migrated by the serve already holding the data dir"
	if lock == nil || lock.Exclusive() {
		var m store.Migrated
		if m, err = st.Migrate(context.Background()); err == nil {
			line = migratedLine(m)
		}
	}
	if err == nil {
		err = st.SchemaCurrent(context.Background())
	}
	if err == nil && lock != nil {
		err = lock.Share()
	}
	if err != nil {
		fmt.Fprintf(stderr, "serve: %s: %v\n", cfg.Engine(), err)
		return 1
	}
	log.Printf("serve: store %s: %s", cfg.Engine(), line)
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		fmt.Fprintln(stderr, "serve:", err)
		return 1
	}
	srv := &http.Server{Handler: handler(version), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Printf("serve: batondeck-kanban %s listening on %s", version, ln.Addr())
	select {
	case err := <-errc:
		fmt.Fprintln(stderr, "serve:", err)
		return 1
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(stderr, "serve: shutdown:", err)
		return 1
	}
	log.Printf("serve: stopped")
	return 0
}

// healthcheck probes /healthz on the configured listen address; the container HEALTHCHECK runs
// this (distroless has no shell or curl). Exit 0 iff it answers 200.
func healthcheck(args []string, stderr io.Writer) int {
	var cfgPath string
	fs := commonFlags("healthcheck", &cfgPath, stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	addr, err := dialAddr(cfg.Listen)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(stderr, "healthcheck: status", resp.Status)
		return 1
	}
	return 0
}

// dialAddr turns a listen address into one to dial: a listener on every interface (":8080",
// "0.0.0.0:8080", "[::]:8080") is reached on loopback.
func dialAddr(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("listen %q: %w", listen, err)
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}
