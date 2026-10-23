package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// engine is what the area code is written against, once for both engines: the Store, and its sqlc
// queries over the right handle.
type engine interface {
	Store
	queries(write bool) sqlQueries
}

// sqlQueries is one engine's sqlc queries for the areas in this package, in sqlitedb's types: the
// SQLite Queries satisfy it as generated, and pgQueries converts the Postgres ones (the two
// generators emit the same fields, sqlc.yaml's overrides, so each conversion is a struct
// conversion the compiler checks).
type sqlQueries interface {
	projectQueries
	memberQueries
	boardQueries
	counterQueries
	idempotencyQueries
	taskQueries
	claimQueries
	eventQueries
	changeQueries
	auditQueries
	toolCallQueries
	contextQueries
	memoryQueries
	summaryQueries
	dependencyQueries
	subtaskQueries
	agentSessionQueries
	wakeSessionQueries
	attachmentQueries
	blobQueries
	searchQueries
	artifactQueries
	commentQueries
	followUpQueries
	chronicleQueries
	oncallQueries
}

// pgQueries is the Postgres sqlc queries behind sqlQueries; its methods are in the area files.
type pgQueries struct{ q *pgdb.Queries }

// queries is the transaction's handle inside Atomically, for reads too (a read on the pool there
// would not see the transaction's own writes); outside it, the write connection for a write and the
// read pool for a read.
func (s *SQLite) queries(write bool) sqlQueries {
	var db sqlitedb.DBTX = s.r
	switch {
	case s.tx != nil:
		db = s.tx
	case write:
		db = s.w
	}
	return sqlitedb.New(sqliteCounted{db, s.issued})
}

// queries is the transaction inside Atomically, the pool outside it.
func (s *Postgres) queries(bool) sqlQueries {
	var db pgdb.DBTX = s.pool
	if s.tx != nil {
		db = s.tx
	}
	return pgQueries{pgdb.New(pgCounted{db, s.issued})}
}

// on is the engine behind a Store that Atomically handed its callback, which is always this
// package's own.
func on(s Store) engine { return s.(engine) }

// statementCounter is the context key under which a test asks for the statements a call issues.
type statementCounter struct{}

// countingStatements returns a context under which every statement the area code issues adds one
// to n: the measure behind the pinned per-call statement counts.
func countingStatements(ctx context.Context, n *atomic.Int64) context.Context {
	return context.WithValue(ctx, statementCounter{}, n)
}

// countStatement adds one statement to the engine's issued count and, under countingStatements, to
// the context's.
func countStatement(ctx context.Context, issued *atomic.Int64) {
	if issued != nil {
		issued.Add(1)
	}
	if n, ok := ctx.Value(statementCounter{}).(*atomic.Int64); ok {
		n.Add(1)
	}
}

type sqliteCounted struct {
	db     sqlitedb.DBTX
	issued *atomic.Int64
}

func (c sqliteCounted) ExecContext(ctx context.Context, q string, args ...interface{}) (sql.Result, error) {
	countStatement(ctx, c.issued)
	return c.db.ExecContext(ctx, q, args...)
}

func (c sqliteCounted) PrepareContext(ctx context.Context, q string) (*sql.Stmt, error) {
	return c.db.PrepareContext(ctx, q)
}

func (c sqliteCounted) QueryContext(ctx context.Context, q string, args ...interface{}) (*sql.Rows, error) {
	countStatement(ctx, c.issued)
	return c.db.QueryContext(ctx, q, args...)
}

func (c sqliteCounted) QueryRowContext(ctx context.Context, q string, args ...interface{}) *sql.Row {
	countStatement(ctx, c.issued)
	return c.db.QueryRowContext(ctx, q, args...)
}

type pgCounted struct {
	db     pgdb.DBTX
	issued *atomic.Int64
}

func (c pgCounted) Exec(ctx context.Context, q string, args ...interface{}) (pgconn.CommandTag, error) {
	countStatement(ctx, c.issued)
	return c.db.Exec(ctx, q, args...)
}

func (c pgCounted) Query(ctx context.Context, q string, args ...interface{}) (pgx.Rows, error) {
	countStatement(ctx, c.issued)
	return c.db.Query(ctx, q, args...)
}

func (c pgCounted) QueryRow(ctx context.Context, q string, args ...interface{}) pgx.Row {
	countStatement(ctx, c.issued)
	return c.db.QueryRow(ctx, q, args...)
}

// isNoRows is the engines' one "no row" answer: database/sql's and pgx's.
func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows)
}

// found maps a lookup's error: no row is domain.NotFound(entity, id), anything else the engine's
// error.
func found(err error, entity, id string) error {
	switch {
	case err == nil:
		return nil
	case isNoRows(err):
		return domain.NotFound(entity, id)
	default:
		return fmt.Errorf("store: %w", err)
	}
}

// isForeignKeyViolation reports a statement refused by a foreign key, on either engine.
func isForeignKeyViolation(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY
	}
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23503"
}

// newID is prefix and n random hex digits, the shape of conductor's P-, B- and C- ids.
func newID(prefix string, n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("store: crypto/rand: %v", err))
	}
	return prefix + hex.EncodeToString(b)[:n]
}

func nowMillis() int64 { return time.Now().UnixMilli() }

func nullInt(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func intPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// convert maps every row of a Postgres list to its sqlitedb type.
func convert[From, To any](rows []From, f func(From) To) []To {
	out := make([]To, len(rows))
	for i, r := range rows {
		out[i] = f(r)
	}
	return out
}
