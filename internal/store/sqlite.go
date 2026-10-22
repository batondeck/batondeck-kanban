package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/batondeck/batondeck-kanban/migrations"
)

// SQLite is the default engine: pure Go driver, one file, WAL mode, foreign keys ON (SQLite
// leaves them off unless asked).
//
// How it is opened was measured in hdtp-gateway, the skeleton this engine is copied from, against
// a store of a million messages (its scale_test.go), one setting at a time:
//
//   - `_txlock=immediate`. Every transaction this store opens writes. SQLite's default starts one as
//     a reader and upgrades it at the first write, and an upgrade that meets another writer fails at
//     once — `busy_timeout` does not apply to it. Taking the write lock at BEGIN avoids that.
//   - Four connections, kept. Parallel reads were 2.2 times faster through four than through an
//     unbounded pool, and slower again at eight and sixteen.
//   - `synchronous` is FULL, SQLite's default, unless the config opts into NORMAL: NORMAL made a
//     write 1.3 to 3 times faster; under WAL it cannot corrupt the file, but a power cut can take
//     the last commits with it.
type SQLite struct {
	db *sql.DB
}

var _ Store = (*SQLite)(nil)

// sqliteConns is the size of the connection pool, and the number kept open. See the type's comment.
const sqliteConns = 4

// OpenSQLite opens the SQLite file at path with the connection settings the type's comment explains:
// write-ahead logging, foreign keys on, a five-second busy timeout, synchronous FULL or NORMAL as
// given, transactions that take the write lock at BEGIN, and a pool of four connections. It does
// not create the schema; Migrate does. The file is created if it is not there.
func OpenSQLite(path, synchronous string) (*SQLite, error) {
	switch synchronous {
	case "FULL", "NORMAL":
	default:
		return nil, fmt.Errorf("store: synchronous %q (want FULL|NORMAL)", synchronous)
	}
	dsn := fmt.Sprintf("file:%s?_txlock=immediate&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(%s)", path, synchronous)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	db.SetMaxOpenConns(sqliteConns)
	db.SetMaxIdleConns(sqliteConns)
	return &SQLite{db: db}, nil
}

// ErrNotFound is what a store method returns when the row asked for is not there, on either
// engine. It IS `sql.ErrNoRows` — the same value — and it exists so that a caller outside this
// package can ask the question without importing `database/sql`: a package that can name the
// driver's types can write a statement.
var ErrNotFound = sql.ErrNoRows

// Migrate applies every pending migration of this engine's schema.
func (s *SQLite) Migrate(ctx context.Context) error {
	p, err := s.provider()
	if err != nil {
		return err
	}
	_, err = p.Up(ctx)
	return err
}

// SchemaCurrent reports an error unless the schema is exactly the version this binary migrates to;
// the error says whether it is behind or ahead.
func (s *SQLite) SchemaCurrent(ctx context.Context) error {
	p, err := s.provider()
	if err != nil {
		return err
	}
	return schemaCurrent(ctx, p)
}

// schemaCurrent is SchemaCurrent over either engine's migrations.
func schemaCurrent(ctx context.Context, p *goose.Provider) error {
	current, target, err := p.GetVersions(ctx)
	if err != nil {
		return fmt.Errorf("store: schema version: %w", err)
	}
	switch {
	case current < target:
		return fmt.Errorf("store: the schema is at version %d and this binary needs %d: it has not been migrated", current, target)
	case current > target:
		return fmt.Errorf("store: the schema is at version %d, newer than this binary's %d: a newer batondeck-kanban migrated it", current, target)
	}
	return nil
}

// MigrateDown rolls back everything; exists for the up/down/up cleanliness check.
func (s *SQLite) MigrateDown(ctx context.Context) error {
	p, err := s.provider()
	if err != nil {
		return err
	}
	_, err = p.DownTo(ctx, 0)
	return err
}

func (s *SQLite) provider() (*goose.Provider, error) {
	sub, err := fs.Sub(migrations.SQLite, "sqlite")
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, s.db, sub)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return p, nil
}

// Close closes the connection pool. A transaction still open on it is the caller's to have ended.
func (s *SQLite) Close() error { return s.db.Close() }
