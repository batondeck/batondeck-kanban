package cli

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	kanban "github.com/batondeck/batondeck-kanban"
	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/web"
)

// Run dispatches os.Args-style arguments; version is the build-stamped version
// string. It returns a process exit code. The commands' output, their usage and their flag parse
// errors go to the stdout and stderr it is given, not to the process's.
//
// The first argument names the command: serve, migrate, token, user, session, doctor,
// healthcheck, licenses or version; help, -h and --help print the usage to stdout and return 0. No arguments,
// or a command it does not know, prints the usage to stderr and returns 2.
// Help is asked for the same way at every level, and always answered on stdout with 0: `user`,
// `token` and `session` take help, -h and --help in place of a subcommand, and every command and
// subcommand takes -h and --help among its flags (parseFlags).
// Across the commands 0 is success, 1 is a failure of the work, and 2 is a usage error (a flag
// that does not parse, an unknown command or subcommand, a missing argument), printed on stderr;
// session's failures alone exit 0, since a client's hook runs it.
func Run(args []string, version string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	cmd, rest := args[0], args[1:]
	if isHelp(cmd) {
		usage(stdout)
		return 0
	}
	switch cmd {
	case "version":
		return printVersion(rest, version, stdout, stderr)
	case "serve":
		return serve(rest, version, stdout, stderr)
	case "healthcheck":
		return healthcheck(rest, stdout, stderr)
	case "migrate":
		return migrate(rest, stdout, stderr)
	case "doctor":
		return doctor(rest, stdout, stderr)
	case "user":
		return user(rest, stdout, stderr)
	case "token":
		return token(rest, stdout, stderr)
	case "session":
		return session(rest, stdout, stderr)
	case "licenses", "licences":
		return licenses(rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "batondeck-kanban: unknown command %q\n", cmd)
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage: batondeck-kanban <command> [flags]

commands:
  serve        migrate the store, then run the server: GET /healthz, the MCP server at /mcp and the web UI
  migrate      run the store's migrations on the engine database.url selects
  token        create, list and revoke API tokens (a token is printed once, at create)
  user         add, disable, list users and set their passwords (no sign-up page: users are made here)
  session      register or unregister this client session for the server's wake doorbell (hooks run it)
  doctor       check the config and report the store engine the connection answers as
  healthcheck  probe /healthz on the configured listen address (container HEALTHCHECK)
  licenses     print the licence, the NOTICE and the third-party notices this binary carries
  version      print the version
  help         print this usage

run batondeck-kanban serve -h for the configuration: every BATONDECK_* variable and its default.
`)
}

// licenses prints the texts that go with every copy of the binary: LICENSE, NOTICE and
// THIRD_PARTY_NOTICES, as the build embedded them (the repository root's package), each under its
// file name. A release ships the bare binary, so this is where its recipient reads them.
func licenses(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("licenses", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "usage: batondeck-kanban licenses\n\nprints LICENSE, NOTICE and THIRD_PARTY_NOTICES, the texts this binary carries.\n")
	}
	if code, ok := parseFlags(fs, args, stdout); !ok {
		return code
	}
	if !noArgs(fs) {
		return 2
	}
	rule := strings.Repeat("=", 80)
	for _, f := range []struct{ name, text string }{
		{"LICENSE", kanban.License}, {"NOTICE", kanban.Notice}, {"THIRD_PARTY_NOTICES", kanban.ThirdPartyNotices},
	} {
		fmt.Fprintf(stdout, "%s\n%s\n%s\n\n%s\n\n", rule, f.name, rule, strings.TrimRight(f.text, "\n"))
	}
	return 0
}

// printVersion is `batondeck-kanban version`: the build-stamped version on stdout.
func printVersion(args []string, version string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(fs.Output(), "usage: batondeck-kanban version\n\nprints the version.\n") }
	if code, ok := parseFlags(fs, args, stdout); !ok {
		return code
	}
	if !noArgs(fs) {
		return 2
	}
	fmt.Fprintln(stdout, "batondeck-kanban "+version)
	return 0
}

// noArgs reports whether fs, parsed, was given no positional argument; when it was, it prints the
// refusal and fs's usage on fs's output, the command's stderr, and the command returns 2. A command
// that takes none would otherwise run against the defaults: `migrate prod.json` (the file named
// without -config) migrated the default SQLite file, and flag parsing stops at the first
// positional, so the flags after it were dropped too.
func noArgs(fs *flag.FlagSet) bool {
	if fs.NArg() == 0 {
		return true
	}
	fmt.Fprintf(fs.Output(), "%s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
	fs.Usage()
	return false
}

// isHelp reports whether arg, in a command's or a subcommand's place, asks for the usage.
func isHelp(arg string) bool { return arg == "help" || arg == "-h" || arg == "--help" }

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

// serverHelp sets fs's -h to name the command, its flags, every BATONDECK_* variable with its
// default (core.EnvVars) and the config-file rule.
func serverHelp(fs *flag.FlagSet, what string) {
	fs.Usage = func() {
		w := fs.Output()
		fmt.Fprintf(w, "usage: batondeck-kanban %s [-config PATH]\n\n%s\n\nflags:\n", fs.Name(), what)
		fs.PrintDefaults()
		fmt.Fprint(w, `
configuration: environment > config file > defaults. The config file is JSON, read from -config PATH,
else from BATONDECK_CONFIG; with neither, no file is read. A relative path (the file's, or a SQLite
path in it or in BATONDECK_DATABASE_URL) is relative to the working directory.

environment:
`)
		for _, v := range core.EnvVars() {
			fmt.Fprintf(w, "  %s\n    \t%s\n    \tdefault: %s\n", v.Name, v.Doc, v.Default)
		}
	}
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

// sqliteModeWarning is why the SQLite file at path is open to other local accounts, naming the
// chmod that closes it, or "" when it is private (or not there). store.OpenSQLite creates the file
// 0600, and SQLite gives its -wal and -shm files the same mode; a file made before, or copied in
// under a looser umask, keeps the mode it has, which serve and doctor warn of.
func sqliteModeWarning(path string) string {
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm()&0o077 == 0 {
		return ""
	}
	return fmt.Sprintf("sqlite file %s is mode %o: other local accounts can read the board; chmod 600 it, and its -wal and -shm files", path, fi.Mode().Perm())
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

// healthBody is /healthz's whole answer: liveness only, naming the binary, its version and, when
// known, the store's engine.
func healthBody(version, engine string) string {
	if engine == "" {
		return "batondeck-kanban " + version + " ok\n"
	}
	return "batondeck-kanban " + version + " ok " + engine + "\n"
}

// handler is everything serve answers: /healthz, the MCP server at /mcp, the routes mounts add
// (compose's OAuth server when public_url is set, /wake, /internal/wake-session and the REST BFF
// under /api), and the embedded SPA for every other path.
func handler(version string, mcp http.Handler, mounts ...func(*http.ServeMux)) http.Handler {
	return engineHandler(version, "", mcp, mounts...)
}

// engineHandler is handler with /healthz naming engine.
func engineHandler(version, engine string, mcp http.Handler, mounts ...func(*http.ServeMux)) http.Handler {
	mux := http.NewServeMux()
	for _, m := range mounts {
		m(mux)
	}
	// One pattern per method the transport answers: a bare "/mcp" conflicts with the SPA's "GET /".
	mcp = web.WithoutCSP(mcp)
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		mux.Handle(m+" /mcp", mcp)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, healthBody(version, engine))
	})
	web.MountSPA(mux)
	return mux
}

// serve listens on the configured address until SIGINT or SIGTERM, then shuts down.
func serve(args []string, version string, stdout, stderr io.Writer) int {
	var cfgPath string
	fs := commonFlags("serve", &cfgPath, stderr)
	serverHelp(fs, "Migrates the store, then serves /healthz, the MCP server at /mcp and the web UI.")
	if code, ok := parseFlags(fs, args, stdout); !ok {
		return code
	}
	if !noArgs(fs) {
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "serve:", err)
		return 1
	}
	log := core.NewLogger(stderr)
	log.Printf("serve: store %s", dataLine(cfg))
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
	var migrated store.Migrated
	if lock == nil || lock.Exclusive() {
		if migrated, err = st.Migrate(context.Background()); err == nil {
			line = migratedLine(migrated)
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
	if cfg.Engine() == core.EngineSQLite {
		if w := sqliteModeWarning(cfg.SQLitePath()); w != "" {
			log.Printf("serve: WARN %s", w)
		}
	}
	keys, err := openKeyring(cfg, cfgPath, os.LookupEnv, log, migrated.Fresh)
	if err != nil {
		fmt.Fprintln(stderr, "serve:", err)
		return 1
	}
	lns, err := listenAll(cfg.Listen, log)
	if err != nil {
		fmt.Fprintln(stderr, "serve:", err)
		return 1
	}
	media, err := mediaOptions(cfg, keys, lns, log)
	if err != nil {
		fmt.Fprintln(stderr, "serve:", err)
		return 1
	}
	var sealer service.Sealer
	if keys != nil {
		sealer = keys
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	proxies, err := core.ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		fmt.Fprintln(stderr, "serve:", err)
		return 1
	}
	if err := runServer(ctx, lns, st, version, cfg.PublicURL, log, sealer, media, cfg.Retention, proxies); err != nil {
		fmt.Fprintln(stderr, "serve:", err)
		return 1
	}
	log.Printf("serve: stopped")
	return 0
}

// dataLine names where serve keeps its state: the SQLite file, or the Postgres host and database
// as libpq and pgx read them (core.Config.PostgresHostDB; never the URL, which may carry a
// password), and the blob dir.
func dataLine(cfg *core.Config) string {
	if cfg.Engine() != core.EnginePostgres {
		return fmt.Sprintf("sqlite at %s, blobs in %s", cfg.SQLitePath(), cfg.BlobDir())
	}
	host, db := cfg.PostgresHostDB()
	return fmt.Sprintf("postgres on host %s, database %s, blobs in %s", cmp.Or(host, "?"), cmp.Or(db, "?"), cfg.BlobDir())
}

// listenAll binds every listen entry, closing the ones bound when one fails.
func listenAll(entries []string, logger *log.Logger) ([]net.Listener, error) {
	var lns []net.Listener
	for _, e := range entries {
		a, err := core.ParseListen(e)
		if err == nil {
			var ln net.Listener
			if ln, err = core.Listen(a, logger); err == nil {
				lns = append(lns, ln)
				continue
			}
		}
		for _, ln := range lns {
			ln.Close()
		}
		return nil, err
	}
	return lns, nil
}

// parseFlags parses args into fs, whose output is the command's stderr. ok is false when the
// command is to stop and return code: -h or --help printed fs's usage to stdout (0), or a flag that
// does not parse printed its error and the usage to stderr (2).
func parseFlags(fs *flag.FlagSet, args []string, stdout io.Writer) (code int, ok bool) {
	stderr := fs.Output()
	var buf bytes.Buffer
	fs.SetOutput(&buf)
	err := fs.Parse(args)
	fs.SetOutput(stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		_, _ = buf.WriteTo(stdout)
		return 0, false
	case err != nil:
		_, _ = buf.WriteTo(stderr)
		return 2, false
	}
	_, _ = buf.WriteTo(stderr)
	return 0, true
}

// healthcheck probes /healthz on the configured listen address; the container HEALTHCHECK runs
// this (distroless has no shell or curl). Exit 0 iff it answers 200.
func healthcheck(args []string, stdout, stderr io.Writer) int {
	var cfgPath string
	fs := commonFlags("healthcheck", &cfgPath, stderr)
	serverHelp(fs, "Probes /healthz on the first listen address; exits 0 if it answers 200 (the container HEALTHCHECK).")
	if code, ok := parseFlags(fs, args, stdout); !ok {
		return code
	}
	if !noArgs(fs) {
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	// The first listen entry is probed: a TCP address on loopback, a Unix socket by its path.
	a, err := core.ParseListen(cfg.Listen[0])
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	client := &http.Client{Timeout: 3 * time.Second}
	addr := "x"
	if a.Network == core.NetworkUnix {
		client.Transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, core.NetworkUnix, a.Address)
		}}
	} else if addr, err = dialAddr(a.Address); err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
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
