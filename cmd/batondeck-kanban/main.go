// Command batondeck-kanban is the self-hosted BatonDeck task orchestrator. The plan of record is
// docs/release/plan-2026-10-22.md.
package main

import (
	"os"

	"github.com/batondeck/batondeck-kanban/internal/cli"
)

// version is stamped by the release build via -ldflags; the default marks dev builds.
var version = "0.0.0-dev"

func main() {
	os.Exit(cli.Run(os.Args[1:], version, os.Stdout, os.Stderr))
}
