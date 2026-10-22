package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/batondeck/batondeck-kanban/migrations"
)

// Postgres is the engine selected by a postgres:// database.url: same Store contract,
// same conformance suite, pgx underneath. Migrations run through goose over a
// database/sql handle derived from the pool; the queries the areas add run on the pool directly.
type Postgres struct {
	pool *pgxpool.Pool
}

var _ Store = (*Postgres)(nil)

// OpenPostgres opens a connection pool on dsn through pgxpool. It does not create the schema;
// Migrate does.
func OpenPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

func (s *Postgres) provider() (*goose.Provider, *sql.DB, error) {
	sub, err := fs.Sub(migrations.Postgres, "postgres")
	if err != nil {
		return nil, nil, fmt.Errorf("store: %w", err)
	}
	db := stdlib.OpenDBFromPool(s.pool)
	// Several processes, on several hosts, may share one Postgres, and each migrates as it starts:
	// a session-level advisory lock makes them take turns, so one applies the migrations and the
	// rest find nothing pending.
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("store: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, sub, goose.WithSessionLocker(locker))
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("store: %w", err)
	}
	return p, db, nil
}

// Migrate applies every pending migration. Processes sharing the database take turns under goose's
// session-level advisory lock, so one applies and the others find nothing pending.
func (s *Postgres) Migrate(ctx context.Context) error {
	p, db, err := s.provider()
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = p.Up(ctx)
	return err
}

// SchemaCurrent reports an error unless the schema is exactly the version this binary migrates to;
// the error says whether it is behind or ahead.
func (s *Postgres) SchemaCurrent(ctx context.Context) error {
	p, db, err := s.provider()
	if err != nil {
		return err
	}
	defer db.Close()
	return schemaCurrent(ctx, p)
}

// MigrateDown rolls the schema back to version 0. It exists for the conformance suite's up, down and
// up check and is not on the Store interface.
func (s *Postgres) MigrateDown(ctx context.Context) error {
	p, db, err := s.provider()
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = p.DownTo(ctx, 0)
	return err
}

// Close closes the connection pool and returns nil.
func (s *Postgres) Close() error {
	s.pool.Close()
	return nil
}
