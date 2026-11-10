package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
)

// doctor reports the configuration and which engine is live, as the connection itself answers
// (`SELECT sqlite_version()` / `SELECT version()`), not as the config names it. It exits 1 when
// either fails, naming the engine.
func doctor(args []string, stdout, stderr io.Writer) int {
	var cfgPath string
	fs := commonFlags("doctor", &cfgPath, stderr)
	serverHelp(fs, "Checks the config and reports the store engine the connection answers as.")
	if code, ok := parseFlags(fs, args, stdout); !ok {
		return code
	}
	if !noArgs(fs) {
		return 2
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(stdout, "FAIL config       %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "ok   config")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// doctor diagnoses; it does not create the SQLite file it is asked about.
	st, err := openStore(ctx, cfg, false)
	if errors.Is(err, os.ErrNotExist) && cfg.Engine() == core.EngineSQLite {
		fmt.Fprintf(stdout, "ok   store        sqlite at %s: not created yet; serve creates it\n", cfg.SQLitePath())
		return 0
	}
	if err != nil {
		fmt.Fprintf(stdout, "FAIL store        %s: %v\n", cfg.Engine(), err)
		return 1
	}
	defer st.Close()
	v, err := st.ServerVersion(ctx)
	if err != nil {
		fmt.Fprintf(stdout, "FAIL store        %s: %v\n", cfg.Engine(), err)
		return 1
	}
	fmt.Fprintf(stdout, "ok   store        %s, the connection reports: %s\n", dataLine(cfg), v)
	if cfg.Engine() == core.EngineSQLite {
		if w := sqliteModeWarning(cfg.SQLitePath()); w != "" {
			fmt.Fprintf(stdout, "WARN store        %s\n", w)
		}
	}
	return 0
}
