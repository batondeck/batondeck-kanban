// Package migrations embeds the per-engine schema migrations so the binary migrates itself —
// no external goose binary is needed.
package migrations

import "embed"

// SQLite holds migrations/sqlite.
//
//go:embed sqlite/*.sql
var SQLite embed.FS

// Postgres holds migrations/postgres.
//
//go:embed postgres/*.sql
var Postgres embed.FS
