package store

import "context"

// AgentSession is one agent's MCP connection telemetry, one row per (identity, agent). Times are
// Unix milliseconds. ToolCounts is the number of calls per tool name.
type AgentSession struct {
	IdentityID    string
	AgentID       string
	AgentName     string
	Kind          string
	ClientName    string
	ClientVersion string
	OAuthClientID string
	FirstSeenAt   int64
	LastSeenAt    int64
	SessionCount  int64
	RequestCount  int64
	ToolCounts    map[string]int64
	LastTool      string
	LastProjectID string
}

// AgentSessionStart is a connection (MCP initialize) at At.
type AgentSessionStart struct {
	IdentityID    string
	AgentID       string
	AgentName     string
	Kind          string
	ClientName    string
	ClientVersion string
	OAuthClientID string
	At            int64
}

// AgentSessionTouch is Count calls (0 is 1) of Tool at At, in ProjectID ("" for none).
type AgentSessionTouch struct {
	IdentityID string
	AgentID    string
	AgentName  string
	Kind       string
	Tool       string
	ProjectID  string
	Count      int64
	At         int64
}

// AgentSessions is the agent-session area. Keys: (identity_id, agent_id); AgentName is the mutable
// display label, rewritten by every upsert and touch. Errors: an empty identity, agent, name or kind,
// an empty tool on a touch and a negative count are domain.Validation; a missing session is
// domain.NotFound("AgentSession", agentID). Revocation (disconnect_agent) is not part of this
// contract. Lists are newest activity first (last_seen_at, then agent id), not paginated.
type AgentSessions interface {
	// UpsertAgentSession records a connection: it creates the row (FirstSeenAt = At) or updates it
	// (FirstSeenAt kept), adds one to SessionCount, sets LastSeenAt to At and writes each client field
	// that is non-empty (an empty one leaves the stored value). It returns the row after.
	UpsertAgentSession(ctx context.Context, in AgentSessionStart) (AgentSession, error)
	// TouchAgentSession records activity: RequestCount and ToolCounts[Tool] grow by Count, LastTool
	// becomes Tool, LastProjectID becomes ProjectID when it is non-empty, LastSeenAt becomes At. A
	// touch with no row creates one with FirstSeenAt = At and SessionCount 0.
	TouchAgentSession(ctx context.Context, in AgentSessionTouch) error
	// DeleteAgentSession deletes the row.
	DeleteAgentSession(ctx context.Context, identityID, agentID string) error
	// ListAgentSessions is identityID's rows, or every row when identityID is "", at most limit
	// (<= 0 is 100, above 1000 is 1000).
	ListAgentSessions(ctx context.Context, identityID string, limit int) ([]AgentSession, error)
	// DeleteIdleAgentSessions deletes at most limit rows whose LastSeenAt is before before, the
	// longest idle first, except the rows keep names (an agent with a live MCP session is not idle
	// however long since its last request, P-279), and returns how many it deleted. One statement.
	DeleteIdleAgentSessions(ctx context.Context, before int64, limit int, keep []AgentSessionKey) (int64, error)
}

// AgentSessionKey names one agent_sessions row.
type AgentSessionKey struct {
	IdentityID string
	AgentID    string
}

var (
	_ AgentSessions = (*SQLite)(nil)
	_ AgentSessions = (*Postgres)(nil)
)
