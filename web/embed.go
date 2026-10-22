// Package web carries the compiled web UI.
//
// dist/ is COMMITTED, deliberately: go:embed needs the files at build time, and
// keeping them in the tree means `go build` and `make dist` need no Node
// toolchain — which is what keeps a release rebuildable byte-for-byte by someone
// who has only Go (RELEASING.md). `make web-dist-check` fails when it differs from a
// fresh `npm ci && npm run build`.
package web

import "embed"

// Dist is the built SPA. `all:` because Vite may emit files whose names start
// with a dot or underscore, which a bare pattern would silently skip.
//
//go:embed all:dist
var Dist embed.FS
