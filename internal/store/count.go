package store

import (
	"context"
	"sync/atomic"
)

// CountingStatements is ctx counting, into n, every statement the area code of either SQL engine
// issues under it (handles.go's sqliteCounted and pgCounted): the measure behind the per-call
// statement pins of test/perf. A transaction's BEGIN and COMMIT are not area statements and are not
// counted; memstore issues none. The counter only reads a context value, so production pays one
// lookup per statement.
func CountingStatements(ctx context.Context, n *atomic.Int64) context.Context {
	return countingStatements(ctx, n)
}

// StatementCounter is an engine's count of every statement its area code has issued since it
// opened, from any goroutine and under any context: the bench's idle cost (internal/cli bench),
// where the statements would come from goroutines no caller holds the context of (the bus tail,
// an HTTP handler, the session directory's sweep). BEGIN and COMMIT are not counted, as above.
type StatementCounter interface {
	StatementsIssued() int64
}

var (
	_ StatementCounter = (*SQLite)(nil)
	_ StatementCounter = (*Postgres)(nil)
)

// StatementsIssued is the statements s has issued since it opened (StatementCounter).
func (s *SQLite) StatementsIssued() int64 { return s.issued.Load() }

// StatementsIssued is the statements s has issued since it opened (StatementCounter).
func (s *Postgres) StatementsIssued() int64 { return s.issued.Load() }
