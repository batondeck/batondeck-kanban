package cli

import (
	"context"
	"fmt"
	"io"
)

// migrate runs goose on the engine database.url selects, from the binary's embedded
// migrations/{sqlite,postgres}, and says what it did. goose's default refusal of an out-of-order
// version stands.
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
