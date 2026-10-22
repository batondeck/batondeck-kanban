// Package store is the persistence boundary: one Store contract, one implementation per
// engine — SQLite (default) and Postgres. internal/store/conformance is the single suite both
// are judged by, so the engines cannot drift apart; TestSQLiteConformance and
// TestPostgresConformance run it.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/pressly/goose/v3/lock"
)

// Store is the whole persistence surface every engine implements: the role interfaces below,
// one per area of the schema. A consumer that needs one area takes that role, not the whole Store.
// Each area's role is declared in its contract_<area>.go.
type Store interface {
	Lifecycle
	Projects
	Members
	Boards
	Counters
	Idempotency
}

// Lifecycle opens, migrates, transacts and closes: what every engine does before it holds anything.
type Lifecycle interface {
	// Migrate applies every pending migration of the engine's schema (migrations/sqlite or
	// migrations/postgres). Postgres processes sharing a database take turns under a session-level
	// advisory lock. With no migration in the binary it creates goose's version table alone.
	Migrate(ctx context.Context) (Migrated, error)
	// SchemaCurrent reports whether the store's schema is exactly the one this binary migrates
	// to: an error names the two versions when it is behind (a migration has not run) or ahead (a
	// newer binary migrated it).
	SchemaCurrent(ctx context.Context) error
	// ServerVersion is the engine and its version as the live connection reports them
	// (`sqlite_version()`, Postgres's `version()`), never as the config names them.
	ServerVersion(ctx context.Context) (string, error)
	// Atomically runs fn against a Store whose every statement is ONE transaction: all of it lands,
	// or — if fn returns an error — none of it does, and Atomically returns fn's error. Called
	// inside fn, it runs on the same transaction. fn must use the Store it is given.
	Atomically(ctx context.Context, fn func(tx Store) error) error
	// Close releases the engine's connections. SQLite's closes its pools and returns the driver's
	// error; Postgres's closes the pool and returns nil.
	Close() error
}

// Migrated is what one Migrate did.
type Migrated struct {
	// None is true when the binary carries no migration for the engine: goose had nothing to
	// apply, and only its version table was made sure of.
	None bool
	// Applied counts the migrations this call applied; Version is the schema version after it.
	Applied int
	Version int64
}

// migrator is one engine's migrations as goose sees them: the handle it runs on, the dialect, the
// embedded directory, and the session locker (nil on SQLite, whose transactions already take turns
// at BEGIN).
type migrator struct {
	db      *sql.DB
	dialect goose.Dialect
	fsys    fs.FS
	locker  lock.SessionLocker
}

// provider is goose's provider over the migrations. goose v3.28.0 refuses a directory holding no
// migration (NewProvider returns ErrNoMigrations, provider.go:148): that error is passed through
// for the callers to branch on.
func (m migrator) provider() (*goose.Provider, error) {
	var opts []goose.ProviderOption
	if m.locker != nil {
		opts = append(opts, goose.WithSessionLocker(m.locker))
	}
	p, err := goose.NewProvider(m.dialect, m.db, m.fsys, opts...)
	if err != nil && !errors.Is(err, goose.ErrNoMigrations) {
		return nil, fmt.Errorf("store: %w", err)
	}
	return p, err
}

func (m migrator) up(ctx context.Context) (Migrated, error) {
	p, err := m.provider()
	if errors.Is(err, goose.ErrNoMigrations) {
		return Migrated{None: true}, m.ensureVersionTable(ctx)
	}
	if err != nil {
		return Migrated{}, err
	}
	res, err := p.Up(ctx)
	if err != nil {
		return Migrated{}, fmt.Errorf("store: migrate: %w", err)
	}
	v, err := p.GetDBVersion(ctx)
	if err != nil {
		return Migrated{}, fmt.Errorf("store: schema version: %w", err)
	}
	return Migrated{Applied: len(res), Version: v}, nil
}

func (m migrator) downToZero(ctx context.Context) error {
	p, err := m.provider()
	if errors.Is(err, goose.ErrNoMigrations) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := p.DownTo(ctx, 0); err != nil {
		return fmt.Errorf("store: migrate down: %w", err)
	}
	return nil
}

func (m migrator) schemaCurrent(ctx context.Context) error {
	p, err := m.provider()
	var current, target int64
	switch {
	case errors.Is(err, goose.ErrNoMigrations):
		current, err = m.storedVersion(ctx)
		if err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		current, target, err = p.GetVersions(ctx)
		if err != nil {
			return fmt.Errorf("store: schema version: %w", err)
		}
	}
	switch {
	case current < target:
		return fmt.Errorf("store: the schema is at version %d and this binary needs %d: it has not been migrated", current, target)
	case current > target:
		return fmt.Errorf("store: the schema is at version %d, newer than this binary's %d: a newer batondeck-kanban migrated it", current, target)
	}
	return nil
}

// versionTable is goose's own version-table store for the dialect: its DDL, its existence check
// and its insert, so a table made here is the table goose finds once a migration exists.
func (m migrator) versionTable() (database.Store, error) {
	st, err := database.NewStore(m.dialect, goose.DefaultTablename)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return st, nil
}

// tableExists asks the catalog where goose's dialect can (Postgres), and otherwise reads the
// version-0 row as goose's own fallback does (SQLite), outside any transaction.
func tableExists(ctx context.Context, st database.Store, conn database.DBTxConn) (bool, error) {
	if ext, ok := st.(database.StoreExtender); ok {
		exists, err := ext.TableExists(ctx, conn)
		if !errors.Is(err, errors.ErrUnsupported) {
			return exists, err
		}
	}
	_, err := st.GetMigration(ctx, conn, 0)
	return err == nil || errors.Is(err, database.ErrVersionNotFound), nil
}

// ensureVersionTable creates goose's version table and its version-0 row, unless it is there, with
// the check and the creation in ONE transaction. goose's own tryEnsureVersionTable checks outside
// its transaction and retries the race that opens; here there is no race to retry: on SQLite the
// transaction begins IMMEDIATE, so the check is made under the write lock, and on Postgres the
// migration's session lock is held around the whole of it.
func (m migrator) ensureVersionTable(ctx context.Context) (err error) {
	st, err := m.versionTable()
	if err != nil {
		return err
	}
	conn, err := m.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer conn.Close()
	if m.locker != nil {
		if err := m.locker.SessionLock(ctx, conn); err != nil {
			return fmt.Errorf("store: migration lock: %w", err)
		}
		defer func() {
			if uerr := m.locker.SessionUnlock(context.WithoutCancel(ctx), conn); uerr != nil && err == nil {
				err = fmt.Errorf("store: migration unlock: %w", uerr)
			}
		}()
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	// Inside the transaction: Postgres answers from the catalog, and SQLite's fallback read of a
	// missing table fails one statement without ending the transaction.
	exists, err := tableExists(ctx, st, tx)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("store: version table: %w", err)
	}
	if exists {
		return tx.Rollback()
	}
	if err := st.CreateVersionTable(ctx, tx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("store: create version table: %w", err)
	}
	if err := st.Insert(ctx, tx, database.InsertRequest{Version: 0}); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("store: insert version 0: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// storedVersion is the highest version the version table records: 0 when it holds only version 0
// or does not exist yet.
func (m migrator) storedVersion(ctx context.Context) (int64, error) {
	st, err := m.versionTable()
	if err != nil {
		return 0, err
	}
	// The fallback existence check reads a failed query as "no table": an unreachable database
	// must fail here instead of reading as an empty schema.
	if err := m.db.PingContext(ctx); err != nil {
		return 0, fmt.Errorf("store: %w", err)
	}
	exists, err := tableExists(ctx, st, m.db)
	if err != nil {
		return 0, fmt.Errorf("store: version table: %w", err)
	}
	if !exists {
		return 0, nil
	}
	v, err := st.GetLatestVersion(ctx, m.db)
	if errors.Is(err, database.ErrVersionNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	return v, nil
}
