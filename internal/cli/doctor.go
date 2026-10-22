package cli

import (
	"context"
	"fmt"
	"io"
	"time"
)

// doctor reports the configuration and which engine is live, as the connection itself answers
// (`SELECT sqlite_version()` / `SELECT version()`), not as the config names it. It exits 1 when
// either fails, naming the engine.
func doctor(args []string, stdout, stderr io.Writer) int {
	var cfgPath string
	fs := commonFlags("doctor", &cfgPath, stderr)
	if err := fs.Parse(args); err != nil {
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
	fmt.Fprintf(stdout, "ok   store        %s, the connection reports: %s\n", cfg.Engine(), v)
	return 0
}
