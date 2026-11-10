// Command batondeck-kanban is the self-hosted BatonDeck task orchestrator. The plan of record is
// docs/release/plan-2026-10-22.md.
package main

import (
	"os"
	"runtime/debug"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/cli"
)

// devVersion is what an unstamped build reports.
const devVersion = "0.0.0-dev"

// version is stamped by the release build via -ldflags; the default marks dev builds.
var version = devVersion

func main() {
	info, ok := debug.ReadBuildInfo()
	os.Exit(cli.Run(os.Args[1:], resolveVersion(version, info, ok), os.Stdout, os.Stderr))
}

// resolveVersion is the version the binary reports: the stamp when the build carries one, and
// otherwise, for a build of a released module version out of the module cache (`go install
// …/cmd/batondeck-kanban@v0.1.0`, which no -ldflags reach), that version without its "v". A build
// in a checkout stays a dev build whatever tag it sits on: its build info carries VCS settings, and
// only the stamp says a release was built (scripts/release-check.sh refuses the rest).
func resolveVersion(stamped string, info *debug.BuildInfo, ok bool) string {
	if stamped != devVersion || !ok || info == nil {
		return stamped
	}
	for _, s := range info.Settings {
		if strings.HasPrefix(s.Key, "vcs") {
			return stamped
		}
	}
	v := info.Main.Version
	if !strings.HasPrefix(v, "v") || strings.Contains(v, "+dirty") {
		return stamped // "(devel)" or empty: not a module version
	}
	return strings.TrimPrefix(v, "v")
}
