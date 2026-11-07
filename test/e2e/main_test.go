//go:build e2e

// Package e2e drives the built batondeck-kanban binary the way a client does: it runs migrate,
// user add and token create, then serve on a free 127.0.0.1 port, and calls every MCP tool, prompt
// and resource template over Streamable HTTP, once on SQLite and once on Postgres when
// BATONDECK_TEST_POSTGRES_DSN is set. Each scenario sets up what its tool needs through other tools,
// reads the effect back through a separate read (another tool, REST or the board feed) and, where
// the tool has one, takes a refusal. The coverage guard (guard_test.go) fails, naming each one, when
// a tool, prompt or template the live server lists has no scenario.
//
// Run it with `make e2e`. It builds the binary unless BATONDECK_E2E_BIN names one.
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// binPath is the binary every engine runs.
var binPath string

func TestMain(m *testing.M) {
	code, err := run(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func run(m *testing.M) (int, error) {
	if p := os.Getenv("BATONDECK_E2E_BIN"); p != "" {
		abs, err := filepath.Abs(p)
		if err != nil {
			return 0, err
		}
		if _, err := os.Stat(abs); err != nil {
			return 0, fmt.Errorf("BATONDECK_E2E_BIN: %w", err)
		}
		binPath = abs
		return m.Run(), nil
	}
	dir, err := os.MkdirTemp("", "bk-e2e-bin-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	binPath = filepath.Join(dir, "batondeck-kanban")
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return 0, err
	}
	build := exec.Command("go", "build", "-trimpath", "-ldflags", "-X main.version=0.0.0-e2e", "-o", binPath, "./cmd/batondeck-kanban")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("building the binary: %v\n%s", err, out)
	}
	return m.Run(), nil
}
