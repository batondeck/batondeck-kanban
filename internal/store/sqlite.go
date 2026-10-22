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
//   - Four connections, kept, for reads. Parallel reads were 2.2 times faster through four than
//     through an unbounded pool, and slower again at eight and sixteen.
//   - `synchronous` is FULL, SQLite's default, unless the config opts into NORMAL: NORMAL made a
//     write 1.3 to 3 times faster; under WAL it cannot corrupt the file, but a power cut can take
//     the last commits with it.
//
// Writes go through ONE connection (the plan's SQLite tuning: one connection for writes, a pool for
// reads): SQLite admits one writer at a time, so a second write connection in the same process
// only waits on the first. Every transaction, every write and the migrations run on it. Reads run
// on the pool, whose connections are `query_only` and take no lock at BEGIN.
type SQLite struct {
	w *sql.DB // the one write connection
	r *sql.DB // the read pool
	// tx is set on the copy Atomically hands its callback.
	tx *sql.Tx
}

var _ Store = (*SQLite)(nil)

// sqliteConns is the size of the read pool, and the number kept open. See the type's comment.
const sqliteConns = 4

// sqliteDSN is the connection string for path. The write connection takes the write lock at BEGIN;
// a read connection refuses writes, and its pragma comes last so that journal_mode(WAL) has run
// on a fresh file before it.
func sqliteDSN(path, synchronous string, write bool) string {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(%s)", path, synchronous)
	if write {
		return dsn + "&_txlock=immediate"
	}
	return dsn + "&_pragma=query_only(1)"
}

// OpenSQLite opens the SQLite file at path with the connection settings the type's comment explains:
// write-ahead logging, foreign keys on, a five-second busy timeout, synchronous FULL or NORMAL as
// given, one write connection whose transactions take the write lock at BEGIN, and a read pool of
// four. It does not create the schema; Migrate does. The file is created if it is not there.
func OpenSQLite(path, synchronous string) (*SQLite, error) {
	switch synchronous {
	case "FULL", "NORMAL":
	default:
		return nil, fmt.Errorf("store: synchronous %q (want FULL|NORMAL)", synchronous)
	}
	w, err := sql.Open("sqlite", sqliteDSN(path, synchronous, true))
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	r, err := sql.Open("sqlite", sqliteDSN(path, synchronous, false))
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("store: %w", err)
	}
	r.SetMaxOpenConns(sqliteConns)
	r.SetMaxIdleConns(sqliteConns)
	return &SQLite{w: w, r: r}, nil
}

// ErrNotFound is what a store method returns when the row asked for is not there, on either
// engine. It IS `sql.ErrNoRows` — the same value — and it exists so that a caller outside this
// package can ask the question without importing `database/sql`: a package that can name the
// driver's types can write a statement.
var ErrNotFound = sql.ErrNoRows

func (s *SQLite) migrator() (migrator, error) {
	sub, err := fs.Sub(migrations.SQLite, "sqlite")
	if err != nil {
		return migrator{}, fmt.Errorf("store: %w", err)
	}
	return migrator{db: s.w, dialect: goose.DialectSQLite3, fsys: sub}, nil
}

// Migrate applies every pending migration of this engine's schema, on the write connection.
func (s *SQLite) Migrate(ctx context.Context) (Migrated, error) {
	m, err := s.migrator()
	if err != nil {
		return Migrated{}, err
	}
	return m.up(ctx)
}

// SchemaCurrent reports an error unless the schema is exactly the version this binary migrates to;
// the error says whether it is behind or ahead.
func (s *SQLite) SchemaCurrent(ctx context.Context) error {
	m, err := s.migrator()
	if err != nil {
		return err
	}
	return m.schemaCurrent(ctx)
}

// MigrateDown rolls back everything; exists for the up/down/up cleanliness check.
func (s *SQLite) MigrateDown(ctx context.Context) error {
	m, err := s.migrator()
	if err != nil {
		return err
	}
	return m.downToZero(ctx)
}

// ServerVersion reads `sqlite_version()` from a read connection.
func (s *SQLite) ServerVersion(ctx context.Context) (string, error) {
	var v string
	if err := s.r.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&v); err != nil {
		return "", fmt.Errorf("store: sqlite: %w", err)
	}
	return "SQLite " + v, nil
}

// Atomically runs fn on a copy of this store whose statements all go through one transaction on
// the write connection, begun IMMEDIATE (_txlock): it holds the write lock from BEGIN to the
// commit, against every connection and every process.
func (s *SQLite) Atomically(ctx context.Context, fn func(tx Store) error) error {
	if s.tx != nil {
		return fn(s)
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	if err := fn(&SQLite{w: s.w, r: s.r, tx: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// Close closes both pools. A transaction still open on them is the caller's to have ended.
func (s *SQLite) Close() error {
	rerr := s.r.Close()
	if err := s.w.Close(); err != nil {
		return err
	}
	return rerr
}
