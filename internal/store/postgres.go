package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/batondeck/batondeck-kanban/migrations"
)

// Postgres is the engine selected by a postgres:// database.url: same Store contract, the
// same conformance suite, pgx underneath. Migrations run through goose over a database/sql handle
// derived from the pool; the queries the areas add run on the pool directly.
type Postgres struct {
	pool *pgxpool.Pool
	// tx is set on the copy Atomically hands its callback.
	tx pgx.Tx
}

var _ Store = (*Postgres)(nil)

// OpenPostgres opens a connection pool on dsn through pgxpool. It does not connect until the first
// statement, and it does not create the schema; Migrate does.
func OpenPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

// migrator opens a database/sql handle over the pool for goose; the caller closes it.
//
// Several processes, on several hosts, may share one Postgres, and each migrates as it starts:
// a session-level advisory lock makes them take turns, so one applies the migrations and the
// rest find nothing pending.
func (s *Postgres) migrator() (migrator, *sql.DB, error) {
	sub, err := fs.Sub(migrations.Postgres, "postgres")
	if err != nil {
		return migrator{}, nil, fmt.Errorf("store: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return migrator{}, nil, fmt.Errorf("store: %w", err)
	}
	db := stdlib.OpenDBFromPool(s.pool)
	return migrator{db: db, dialect: goose.DialectPostgres, fsys: sub, locker: locker}, db, nil
}

// Migrate applies every pending migration. Processes sharing the database take turns under goose's
// session-level advisory lock, so one applies and the others find nothing pending.
func (s *Postgres) Migrate(ctx context.Context) (Migrated, error) {
	m, db, err := s.migrator()
	if err != nil {
		return Migrated{}, err
	}
	defer db.Close()
	return m.up(ctx)
}

// SchemaCurrent reports an error unless the schema is exactly the version this binary migrates to;
// the error says whether it is behind or ahead.
func (s *Postgres) SchemaCurrent(ctx context.Context) error {
	m, db, err := s.migrator()
	if err != nil {
		return err
	}
	defer db.Close()
	return m.schemaCurrent(ctx)
}

// MigrateDown rolls the schema back to version 0. It exists for the conformance suite's up, down and
// up check and is not on the Store interface.
func (s *Postgres) MigrateDown(ctx context.Context) error {
	m, db, err := s.migrator()
	if err != nil {
		return err
	}
	defer db.Close()
	return m.downToZero(ctx)
}

// ServerVersion reads `version()` from the server.
func (s *Postgres) ServerVersion(ctx context.Context) (string, error) {
	var v string
	if err := s.pool.QueryRow(ctx, "SELECT version()").Scan(&v); err != nil {
		return "", fmt.Errorf("store: postgres: %w", err)
	}
	return v, nil
}

// Atomically runs fn on a copy of this store whose statements all go through one transaction.
func (s *Postgres) Atomically(ctx context.Context, fn func(tx Store) error) error {
	if s.tx != nil {
		return fn(s)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	if err := fn(&Postgres{pool: s.pool, tx: tx}); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// Close closes the connection pool and returns nil.
func (s *Postgres) Close() error {
	s.pool.Close()
	return nil
}
