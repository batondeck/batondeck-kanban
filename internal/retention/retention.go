// Package retention is the retention sweep (plan §Performance "Retention is a job, not a request
// path"), ported from hdtp's services/retention: a timer, never a request, deletes what has
// outlived its window — memory past its own expiry, change-log rows past ChangeLog, tool_calls rows
// past ToolCalls, the agent sessions idle past AgentSessionIdle (ADR-0004), the on-call deliveries
// and the raw webhook bodies past the expiry stamped on each at write time (retention.deliveries_days
// and raw_bodies_days, 30 and 7 by default, plan §Clarifications 5), the browser sessions expired and
// the API tokens expired or revoked more than LapsedGrace ago (a token's newest row per label stays,
// as the label-ownership record), the OAuth codes and tokens expired more than LapsedGrace ago, and
// the blobs no attachment or memory entry references any more.
// Task events are kept. Each table's delete is bounded per
// statement and per tick, so one sweep never holds the write lock for long: what a tick leaves, the
// next one takes.
package retention

import (
	"context"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

const (
	// SweepInterval is how often Run sweeps: far finer than any window, and cheap — each delete
	// asks for rows by age and finds none most hours.
	SweepInterval = time.Hour
	// Batch is the most rows one delete statement takes.
	Batch = 500
	// MaxPerTick is the most rows one table loses in one sweep.
	MaxPerTick = 50_000
	// BlobGrace is how old an unreferenced blob is before a sweep collects it.
	BlobGrace = time.Hour
	// AgentSessionIdle is how long an agent session row outlives its last request (ADR-0004: the
	// GC that replaced prune_agent_sessions).
	AgentSessionIdle = 30 * time.Minute
	// LapsedGrace is how long a lapsed credential's row outlives its expiry or revocation before a
	// sweep deletes it: a browser session past its expiry, an API token past its expiry or
	// revocation, an OAuth code or token past its expiry. For that week `token list` still shows a
	// lapsed token with its status, and a rotated OAuth refresh token presented again still finds
	// its row and revokes its family.
	LapsedGrace = 7 * 24 * time.Hour
)

// Store is the slice of the store a sweep deletes through.
type Store interface {
	DeleteExpiredMemory(ctx context.Context, now int64, limit int) (int64, error)
	DeleteChangesOlderThan(ctx context.Context, before int64, limit int) (int64, error)
	DeleteToolCallsOlderThan(ctx context.Context, before int64, limit int) (int64, error)
	DeleteIdleAgentSessions(ctx context.Context, before int64, limit int) (int64, error)
	DeleteExpiredSessions(ctx context.Context, before int64, limit int) (int64, error)
	DeleteLapsedTokens(ctx context.Context, before int64, limit int) (int64, error)
	DeleteExpiredOAuthCodes(ctx context.Context, before int64, limit int) (int64, error)
	DeleteExpiredOAuthTokens(ctx context.Context, before int64, limit int) (int64, error)
	DeleteExpiredDeliveries(ctx context.Context, now int64, limit int) (int64, error)
	DeleteExpiredDeliveryRawBodies(ctx context.Context, now int64, limit int) (int64, error)
	ListUnreferencedBlobs(ctx context.Context, olderThan int64, limit int) ([]store.Blob, error)
}

// Windows is how long each table swept by age keeps a row.
type Windows struct {
	ChangeLog     time.Duration
	ToolCalls     time.Duration
	AgentSessions time.Duration
}

// WindowsOf is the config's windows, and AgentSessionIdle.
func WindowsOf(r core.Retention) Windows {
	return Windows{ChangeLog: days(r.ChangeLogDays), ToolCalls: days(r.ToolCallsDays), AgentSessions: AgentSessionIdle}
}

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

// Swept is what one sweep deleted, per table.
type Swept struct {
	Memory, ChangeLog, ToolCalls, AgentSessions, Sessions, Tokens, OAuthCodes, OAuthTokens int64
	Deliveries, RawBodies, Blobs                                                           int64
}

// Sweeper is one store's sweep. Now defaults to time.Now; Report hears a step that failed (nil
// drops it), and the sweep goes on to the next step. CollectBlobs deletes the unreferenced blobs a
// sweep lists, row and file (service.Core.CollectBlobs); nil sweeps no blob.
type Sweeper struct {
	Store        Store
	Windows      Windows
	Now          func() time.Time
	Report       func(step string, err error)
	CollectBlobs func(ctx context.Context, sha256s []string)
}

// Sweep runs one pass: memory, then the change log, then tool calls, then the idle agent sessions,
// then the browser sessions, API tokens, OAuth codes and OAuth tokens lapsed more than LapsedGrace
// ago, then the expired deliveries (each with its raw body) and the expired raw bodies of kept deliveries, then one Batch of the blobs
// unreferenced for BlobGrace (a blob CollectBlobs could not delete is listed again next tick).
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
	out.AgentSessions = step("agent sessions", func(limit int) (int64, error) {
		return s.Store.DeleteIdleAgentSessions(ctx, ms-s.Windows.AgentSessions.Milliseconds(), limit)
	})
	lapsed := ms - LapsedGrace.Milliseconds()
	out.Sessions = step("sessions", func(limit int) (int64, error) { return s.Store.DeleteExpiredSessions(ctx, lapsed, limit) })
	out.Tokens = step("tokens", func(limit int) (int64, error) { return s.Store.DeleteLapsedTokens(ctx, lapsed, limit) })
	out.OAuthCodes = step("oauth codes", func(limit int) (int64, error) { return s.Store.DeleteExpiredOAuthCodes(ctx, lapsed, limit) })
	out.OAuthTokens = step("oauth tokens", func(limit int) (int64, error) { return s.Store.DeleteExpiredOAuthTokens(ctx, lapsed, limit) })
	out.Deliveries = step("deliveries", func(limit int) (int64, error) { return s.Store.DeleteExpiredDeliveries(ctx, ms, limit) })
	out.RawBodies = step("raw bodies", func(limit int) (int64, error) { return s.Store.DeleteExpiredDeliveryRawBodies(ctx, ms, limit) })
	if s.CollectBlobs != nil {
		bs, err := s.Store.ListUnreferencedBlobs(ctx, ms-BlobGrace.Milliseconds(), Batch)
		if err != nil && s.Report != nil && ctx.Err() == nil {
			s.Report("blobs", err)
		}
		hashes := make([]string, len(bs))
		for i, b := range bs {
			hashes[i] = b.SHA256
		}
		if len(hashes) > 0 {
			s.CollectBlobs(ctx, hashes)
		}
		out.Blobs = int64(len(hashes))
	}
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
