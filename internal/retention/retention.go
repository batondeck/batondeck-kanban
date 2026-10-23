// Package retention is the retention sweep (plan §Performance "Retention is a job, not a request
// path"), ported from hdtp's services/retention: a timer, never a request, deletes what has
// outlived its window — memory past its own expiry, change-log rows past ChangeLog, tool_calls rows
// past ToolCalls. Task events are kept. Each table's delete is bounded per statement and per tick,
// so one sweep never holds the write lock for long: what a tick leaves, the next one takes.
//
// Deliveries and raw webhook bodies are not swept here yet: their Store methods belong to the
// on-call lane, and no window is configured for them until one exists.
package retention

import (
	"context"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
)

const (
	// SweepInterval is how often Run sweeps: far finer than any window, and cheap — each delete
	// asks for rows by age and finds none most hours.
	SweepInterval = time.Hour
	// Batch is the most rows one delete statement takes.
	Batch = 500
	// MaxPerTick is the most rows one table loses in one sweep.
	MaxPerTick = 50_000
)

// Store is the slice of the store a sweep deletes through.
type Store interface {
	DeleteExpiredMemory(ctx context.Context, now int64, limit int) (int64, error)
	DeleteChangesOlderThan(ctx context.Context, before int64, limit int) (int64, error)
	DeleteToolCallsOlderThan(ctx context.Context, before int64, limit int) (int64, error)
}

// Windows is how long each table swept by age keeps a row.
type Windows struct {
	ChangeLog time.Duration
	ToolCalls time.Duration
}

// WindowsOf is the config's windows.
func WindowsOf(r core.Retention) Windows {
	return Windows{ChangeLog: days(r.ChangeLogDays), ToolCalls: days(r.ToolCallsDays)}
}

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

// Swept is what one sweep deleted, per table.
type Swept struct {
	Memory, ChangeLog, ToolCalls int64
}

// Sweeper is one store's sweep. Now defaults to time.Now; Report hears a step that failed (nil
// drops it), and the sweep goes on to the next step.
type Sweeper struct {
	Store   Store
	Windows Windows
	Now     func() time.Time
	Report  func(step string, err error)
}

// Sweep runs one pass: memory, then the change log, then tool calls.
func (s *Sweeper) Sweep(ctx context.Context) Swept {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	ms := now().UnixMilli()
	var out Swept
	step := func(name string, del func(int) (int64, error)) int64 {
		var total int64
		for total < MaxPerTick {
			n, err := del(int(min(Batch, MaxPerTick-total)))
			total += n
			if err != nil {
				if s.Report != nil && ctx.Err() == nil {
					s.Report(name, err)
				}
				break
			}
			if n < Batch {
				break
			}
		}
		return total
	}
	out.Memory = step("memory", func(limit int) (int64, error) { return s.Store.DeleteExpiredMemory(ctx, ms, limit) })
	out.ChangeLog = step("change log", func(limit int) (int64, error) {
		return s.Store.DeleteChangesOlderThan(ctx, ms-s.Windows.ChangeLog.Milliseconds(), limit)
	})
	out.ToolCalls = step("tool calls", func(limit int) (int64, error) {
		return s.Store.DeleteToolCallsOlderThan(ctx, ms-s.Windows.ToolCalls.Milliseconds(), limit)
	})
	return out
}

// Run sweeps once at once, so a window shortened while stopped takes effect, then every interval
// (<= 0 is SweepInterval) until ctx ends. It blocks, and never returns while a sweep runs.
func (s *Sweeper) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = SweepInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	s.Sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Sweep(ctx)
		}
	}
}
