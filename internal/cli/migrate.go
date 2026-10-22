package cli

import (
	"context"
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
// core.ServeLockWait for another holder; Postgres serialises in goose's session lock.
func migrate(args []string, stdout, stderr io.Writer) int {
	var cfgPath string
	fs := commonFlags("migrate", &cfgPath, stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "migrate:", err)
		return 1
	}
	if cfg.Engine() == core.EngineSQLite {
		lock, err := lockSQLiteDir(cfg, acquireLockWaiting)
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
// another process holds it; then it returns core.AcquireLock's refusal, which names the data dir.
func acquireLockWaiting(dir string) (*core.Lock, error) {
	deadline := time.Now().Add(core.ServeLockWait)
	for {
		lock, err := core.AcquireLock(dir)
		if err == nil || time.Now().After(deadline) {
			return lock, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}
