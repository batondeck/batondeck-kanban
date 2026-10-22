// Package migrations embeds the per-engine schema migrations so the binary migrates itself —
// no external goose binary is needed.
package migrations

import "embed"

// SQLite holds migrations/sqlite. `all:` because the directory holds no migration yet, only its
// .gitkeep, and a bare pattern refuses a directory with no embeddable file.
//
//go:embed all:sqlite
var SQLite embed.FS

// Postgres holds migrations/postgres, the same way.
//
//go:embed all:postgres
var Postgres embed.FS
