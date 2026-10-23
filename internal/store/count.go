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
