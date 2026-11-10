package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
)

// migrate runs goose on the engine database.url selects, from the binary's embedded
// migrations/{sqlite,postgres}, and says what it did. goose's default refusal of an out-of-order
// version stands. On SQLite it holds the data-dir lock alone while it migrates, waiting up to
// core.ServeLockWait for another holder (and saying so), but failing at once beside a running serve,
// whose lock does not come free; Postgres serialises in goose's session lock.
func migrate(args []string, stdout, stderr io.Writer) int {
	var cfgPath string
	fs := commonFlags("migrate", &cfgPath, stderr)
	serverHelp(fs, "Runs the store's migrations on the engine database.url selects (serve also runs them as it starts).")
	if code, ok := parseFlags(fs, args, stdout); !ok {
		return code
	}
	if !noArgs(fs) {
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "migrate:", err)
		return 1
	}
	if cfg.Engine() == core.EngineSQLite {
		lock, err := lockSQLiteDir(cfg, func(dir string) (*core.Lock, error) { return acquireLockWaiting(dir, stderr) })
		if err != nil {
			fmt.Fprintln(stderr, "migrate:", err)
			return 1
		}
		defer lock.Release()
	}
	st, err := openStore(context.Background(), cfg, true)
	if err != nil {
		fmt.Fprintf(stderr, "migrate: %s: %v\n", cfg.Engine(), err)
		return 1
	}
	defer st.Close()
	m, err := st.Migrate(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "migrate: %s: %v\n", cfg.Engine(), err)
		return 1
	}
	fmt.Fprintf(stdout, "migrate: %s: %s\n", cfg.Engine(), migratedLine(m))
	return 0
}

// lockSQLiteDir makes the SQLite file's directory, the data dir, and takes its lock with acquire.
func lockSQLiteDir(cfg *core.Config, acquire func(string) (*core.Lock, error)) (*core.Lock, error) {
	dir := filepath.Dir(cfg.SQLitePath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return acquire(dir)
}

// acquireLockWaiting takes the data-dir lock alone, retrying for up to core.ServeLockWait while
// another process holds it alone (another migrate, or a serve migrating), and saying on stderr that
// it waits; then it returns core.AcquireLock's refusal, which names the data dir. Held by a running
// serve, it fails at once: that lock is held for the serve's lifetime, and the serve migrated the
// store as it started.
func acquireLockWaiting(dir string, stderr io.Writer) (*core.Lock, error) {
	deadline := time.Now().Add(core.ServeLockWait)
	waiting := false
	for {
		lock, err := core.AcquireLock(dir)
		switch {
		case err == nil:
			return lock, nil
		case errors.Is(err, core.ErrServeRunning):
			return nil, fmt.Errorf("a serve is running on data dir %s: serve migrates the store as it starts, so restart it to apply a new binary's migrations, or stop it to run migrate", dir)
		case time.Now().After(deadline):
			return nil, err
		case !waiting:
			fmt.Fprintf(stderr, "migrate: data dir %s is held by another batondeck-kanban process (a migrate, or a serve migrating); waiting up to %s\n", dir, core.ServeLockWait)
			waiting = true
		}
		time.Sleep(100 * time.Millisecond)
	}
}
