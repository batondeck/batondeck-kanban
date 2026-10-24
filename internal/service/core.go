package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/oncall"
	"github.com/batondeck/batondeck-kanban/internal/oncall/auth"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The defaults Options falls back to.
const (
	// DefaultIdempotencyTTL is conductor's DEFAULT_IDEMPOTENCY_TTL_MS (24 h).
	DefaultIdempotencyTTL = 24 * time.Hour
	// DefaultAccessTTL is how long authorize keeps a membership and grant (conductor's 60 s
	// GUARD_CACHE_TTL_MS, the disable latency).
	DefaultAccessTTL = 60 * time.Second
	// DefaultWakeSessionTTL is how long a registered wake session lives. TODO(spec): no window is
	// named by the plan; R0.15 owns the number.
	DefaultWakeSessionTTL = 24 * time.Hour
	// DefaultToolCallBatch rows fill one InsertToolCalls; DefaultToolCallFlush is the longest a row
	// waits; DefaultToolCallBuffer is the most rows held while the store is failing.
	DefaultToolCallBatch  = 100
	DefaultToolCallFlush  = time.Second
	DefaultToolCallBuffer = 10_000
)

// GrantLookup reads one member's capability grant: ok false is no grant, which restricts nothing.
// The store contract has no grant reader yet, so the composition root supplies one; nil is "no
// member has a grant".
type GrantLookup func(ctx context.Context, projectID, identityID string) (grant domain.CapabilityGrant, ok bool, err error)

// Sealer seals a secret that must be read back later, bound to aad (core.Keyring is the one).
type Sealer interface {
	Encrypt(plaintext, aad []byte) ([]byte, error)
	Decrypt(sealed, aad []byte) ([]byte, error)
}

// Options configure a Core; every zero value takes its default.
type Options struct {
	// Logger receives internal errors with their stacks; nil is stderr.
	Logger *log.Logger
	// Now is the clock; nil is time.Now.
	Now            func() time.Time
	IdempotencyTTL time.Duration
	AccessTTL      time.Duration
	WakeSessionTTL time.Duration
	Grants         GrantLookup
	ToolCallBatch  int
	// ToolCallFlush is the background flush interval; a negative value runs no background flush
	// (the caller flushes with FlushToolCalls).
	ToolCallFlush  time.Duration
	ToolCallBuffer int
	// Sealer seals a directory entry's bearer and inbox token; nil refuses an entry (INTERNAL).
	Sealer Sealer
	// BlobGC is delete_task's blob hook (delete.go); nil is CollectBlobs when Media.Files is set,
	// else a no-op.
	BlobGC BlobGC
	// Media is the blob store attach_file and list_attachments sign URLs into (media.go).
	Media MediaOptions
	// JWKSFetch is how a jwt-mode integration's key set is fetched (POST /hooks); nil is
	// auth.SafeFetch, the SSRF-guarded https GET. A test stubs it.
	JWKSFetch auth.Fetch
}

// Core is the service core (ADR-0003 layer 2): the one place the store and the domain are composed.
// mutate (mutate.go) is the only transaction shape a mutation runs in, authorize (authz.go) the only
// membership, tier and grant check; ToolCalls (toolcalls.go) and Sessions (sessions.go) are its two
// services that write no event. The other services are built on it by their own tasks.
type Core struct {
	store   store.Store
	log     *log.Logger
	now     func() time.Time
	idemTTL time.Duration
	wakeTTL time.Duration
	grants  GrantLookup
	access  *accessCache
	calls   *toolCalls
	boards  *snapshotCache
	sealer  Sealer
	blobGC  BlobGC
	media   MediaOptions
	devices atomic.Pointer[DeviceSessions]
	enders  atomic.Pointer[AgentSessionEnder]
	// limiter is the per-integration ingest budget: POST /hooks, send_test_event and
	// replay_delivery all draw on it (Core.ingest).
	limiter *oncall.RateLimiter
	// jwt verifies a jwt-mode integration's bearer, its JWKS cached per URL.
	jwt *auth.Verifier
}

// SetDeviceSessions hands the core the device session directory, which internal/wake builds after
// the core (the service imports no transport, ADR-0003 row 5).
func (c *Core) SetDeviceSessions(d DeviceSessions) { c.devices.Store(&d) }

// SetAgentSessionEnder hands the core what ends live MCP sessions (disconnect_agent): internal/mcp's
// server, built after the core.
func (c *Core) SetAgentSessionEnder(e AgentSessionEnder) { c.enders.Store(&e) }

// endAgentSessions ends (identity, agent)'s live MCP sessions, 0 when no ender is set.
func (c *Core) endAgentSessions(ctx context.Context, identityID, agentID string) int {
	if e := c.enders.Load(); e != nil {
		return (*e).EndAgentSessions(ctx, identityID, agentID)
	}
	return 0
}

// DeviceSessions is the device session directory, nil until SetDeviceSessions (R1.11's presence).
func (c *Core) DeviceSessions() DeviceSessions {
	if d := c.devices.Load(); d != nil {
		return *d
	}
	return nil
}

// New builds the core over st and starts the tool-call flusher; Close stops it.
func New(st store.Store, o Options) *Core {
	if st == nil {
		panic("service: New needs a store")
	}
	c := &Core{
		store:   st,
		log:     o.Logger,
		now:     o.Now,
		idemTTL: orDefault(o.IdempotencyTTL, DefaultIdempotencyTTL),
		wakeTTL: orDefault(o.WakeSessionTTL, DefaultWakeSessionTTL),
		grants:  o.Grants,
		sealer:  o.Sealer,
		blobGC:  o.BlobGC,
		media:   o.Media,
	}
	c.media.MaxBytes = orDefault(c.media.MaxBytes, core.DefaultBlobMaxBytes)
	switch {
	case c.blobGC != nil:
	case c.media.Files != nil:
		c.blobGC = c.CollectBlobs
	default:
		c.blobGC = func(context.Context, []string) {}
	}
	if c.log == nil {
		c.log = core.NewLogger(os.Stderr)
	}
	if c.now == nil {
		c.now = time.Now
	}
	c.limiter = oncall.NewRateLimiter()
	c.jwt = &auth.Verifier{Fetch: o.JWKSFetch, Logf: c.log.Printf, Now: c.now}
	c.access = newAccessCache(orDefault(o.AccessTTL, DefaultAccessTTL))
	c.boards = newSnapshotCache()
	c.calls = newToolCalls(c, orDefault(o.ToolCallBatch, DefaultToolCallBatch), orDefault(o.ToolCallBuffer, DefaultToolCallBuffer), orDefault(o.ToolCallFlush, DefaultToolCallFlush))
	return c
}

// Close flushes the buffered tool calls and stops the flusher. It does not close the store.
func (c *Core) Close(ctx context.Context) error {
	return c.calls.stop(ctx)
}

func orDefault[T int | int64 | time.Duration](v, def T) T {
	if v == 0 {
		return def
	}
	return v
}

// nowMs is the clock in Unix milliseconds, the schema's unit.
func (c *Core) nowMs() int64 { return c.now().UnixMilli() }

// mapErr is the error model (rule 4): a *domain.BatonDeckError passes through with its code; any
// other error is logged whole and becomes INTERNAL, its text kept out of the client's message.
func (c *Core) mapErr(op string, err error) error {
	if err == nil {
		return nil
	}
	if e, ok := domain.AsBatonDeckError(err); ok {
		return e
	}
	c.log.Printf("service: %s: internal error: %+v", op, err)
	return domain.NewError(domain.CodeInternal, "Internal error in "+op, nil)
}

// recovered turns a panic into INTERNAL with the stack logged (conductor rule 9a: the stack, not the
// message). It is deferred inside a transaction so the store rolls back on the error it returns.
func (c *Core) recovered(op string, err *error) {
	if p := recover(); p != nil {
		c.log.Printf("service: %s: panic: %v\n%s", op, p, debug.Stack())
		*err = domain.NewError(domain.CodeInternal, "Internal error in "+op, nil)
	}
}

// isoMs is a Unix-millisecond time as the wire writes it: RFC 3339 UTC with milliseconds, as
// JavaScript's toISOString does.
func isoMs(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
}

func forbidden(message string, details map[string]any) *domain.BatonDeckError {
	return domain.NewError(domain.CodeForbidden, message, details)
}

func internalf(format string, args ...any) *domain.BatonDeckError {
	return domain.NewError(domain.CodeInternal, fmt.Sprintf(format, args...), nil)
}
