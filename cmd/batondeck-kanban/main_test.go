package main

import (
	"runtime/debug"
	"testing"
)

// The stamp wins; without one, only a build of a released module version out of the module cache
// reports that version, and a checkout build stays a dev build.
func TestResolveVersion(t *testing.T) {
	mod := func(v string, settings ...string) *debug.BuildInfo {
		info := &debug.BuildInfo{Main: debug.Module{Path: "github.com/batondeck/batondeck-kanban", Version: v}}
		for _, k := range settings {
			info.Settings = append(info.Settings, debug.BuildSetting{Key: k, Value: "x"})
		}
		return info
	}
	for _, c := range []struct {
		name    string
		stamped string
		info    *debug.BuildInfo
		ok      bool
		want    string
	}{
		{"a stamped release build", "0.1.0", mod("v0.1.0", "vcs.revision"), true, "0.1.0"},
		{"a stamp wins over the module version", "0.1.0-staging+abc", mod("v0.1.0"), true, "0.1.0-staging+abc"},
		{"go install of a released version", devVersion, mod("v0.1.0"), true, "0.1.0"},
		{"go install of a pseudo-version", devVersion, mod("v0.1.1-0.20261110000000-abcdefabcdef"), true, "0.1.1-0.20261110000000-abcdefabcdef"},
		{"an unstamped build in a checkout at the tag", devVersion, mod("v0.1.0", "vcs", "vcs.revision", "vcs.modified"), true, devVersion},
		{"go build with -buildvcs=false", devVersion, mod("(devel)"), true, devVersion},
		{"a dirty module version", devVersion, mod("v0.1.0+dirty"), true, devVersion},
		{"no module version", devVersion, mod(""), true, devVersion},
		{"no build info", devVersion, nil, false, devVersion},
	} {
		if got := resolveVersion(c.stamped, c.info, c.ok); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
