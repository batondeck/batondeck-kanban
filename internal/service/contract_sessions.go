package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Sessions is the agent and wake session bookkeeping. The MCP transport touches the caller's agent
// session on every request and drops it when the session ends; the device session directory
// (internal/wake, R0.25) persists its entries here. None of these writes an event, an audit row or
// a doorbell.
type Sessions interface {
	// TouchAgentSession records one request from the caller's agent session (identity and agent from
	// the caller): first seen, last seen, the request and per-tool counts, the client.
	TouchAgentSession(ctx context.Context, caller domain.Caller, req TouchAgentSessionReq) (TouchAgentSessionResp, error)

	// RemoveAgentSession drops the caller's agent session when its MCP session ends or is collected.
	RemoveAgentSession(ctx context.Context, caller domain.Caller, req RemoveAgentSessionReq) (RemoveAgentSessionResp, error)

	// ListAgentSessions is list_agent_sessions.
	// Not tenant-scoped; workspace admin for every identity, else the caller's own. Writes nothing.
	ListAgentSessions(ctx context.Context, caller domain.Caller, req ListAgentSessionsReq) (ListAgentSessionsResp, error)

	// DisconnectAgent is disconnect_agent: it ends every live MCP session of (identityId, agentId)
	// (AgentSessionEnder) and deletes its agent_sessions row, so the agent's next request on an
	// ended session is answered 404 session not found and it initialises again. Not sticky: unlike
	// conductor's revocation, a new initialize is admitted. Not tenant-scoped; the caller's own
	// identity, or any for a workspace admin, else FORBIDDEN (whether or not the identity exists).
	// Neither a row nor a live session is NOT_FOUND. Event: none. Audit: agent.disconnected
	// (domain.DescribeAudit). Doorbell: none.
	DisconnectAgent(ctx context.Context, caller domain.Caller, req DisconnectAgentReq) (DisconnectAgentResp, error)

	// RegisterWakeSession records a device session-directory entry for the caller: the client
	// process the directory validated, where its doorbells are delivered, and the caller's bearer,
	// sealed, so the entry can be re-resolved later. req.ID, when set, replaces that row.
	RegisterWakeSession(ctx context.Context, caller domain.Caller, req RegisterWakeSessionReq) (RegisterWakeSessionResp, error)
}

// WakeStore is the device session directory's persistence (internal/wake, R0.25): system calls the
// directory makes, never a tool, so outside the (ctx, caller, req) shape of the tool services.
type WakeStore interface {
	// RegisterWakeSession is Sessions.RegisterWakeSession.
	RegisterWakeSession(ctx context.Context, caller domain.Caller, req RegisterWakeSessionReq) (RegisterWakeSessionResp, error)

	// ListWakeSessions is every directory row with its secrets opened, for the directory's start-up
	// rebuild. A row whose secrets do not open is listed with Sealed false and an empty Bearer, so
	// the directory drops it.
	ListWakeSessions(ctx context.Context) ([]WakeSessionRow, error)

	// SetWakeSessionCursor records the last cursor delivered to the entry.
	SetWakeSessionCursor(ctx context.Context, id string, cursor int64) error

	// DropWakeSession deletes the entry's row; a missing row is not an error.
	DropWakeSession(ctx context.Context, id string) error
}

// The roles a directory entry registers with.
const (
	RoleMaster = "master"
	RoleWorker = "worker"
)

// TouchAgentSessionReq is one request seen on an agent session.
type TouchAgentSessionReq struct {
	SessionKey    string      `json:"sessionKey"`
	AgentName     string      `json:"agentName"`
	Kind          SessionKind `json:"kind"`
	ClientName    string      `json:"clientName,omitzero"`
	ClientVersion string      `json:"clientVersion,omitzero"`
	OAuthClientID string      `json:"oauthClientId,omitzero"`
	Tool          string      `json:"tool,omitzero"`
	ProjectID     string      `json:"projectId,omitzero"`
}

// TouchAgentSessionResp is empty.
type TouchAgentSessionResp struct{}

// RemoveAgentSessionReq names the session that ended.
type RemoveAgentSessionReq struct {
	SessionKey string `json:"sessionKey"`
}

// RemoveAgentSessionResp is empty.
type RemoveAgentSessionResp struct{}

// RegisterWakeSessionReq is one directory entry: the client process (PID, Role), its agent label
// ("" for the caller's own AgentID), the socket the doorbell is delivered on ("" for a client with
// no adapter) and the token that socket expects ("" for none), what Claude Code's registry said of
// the process (ClientSessionID, ProcStart), the change-log cursor it has seen, and the bearer it
// registered with (sealed, never stored as given). ID "" is a new row.
type RegisterWakeSessionReq struct {
	ID              string `json:"id,omitzero"`
	PID             int64  `json:"pid"`
	Role            string `json:"role"`
	Agent           string `json:"agent,omitzero"`
	SocketPath      string `json:"socketPath,omitzero"`
	SocketToken     string `json:"socketToken,omitzero"`
	ClientSessionID string `json:"clientSessionId,omitzero"`
	ProcStart       string `json:"procStart,omitzero"`
	Cursor          int64  `json:"cursor,omitzero"`
	Bearer          string `json:"-"`
}

// RegisterWakeSessionResp is the registered entry.
type RegisterWakeSessionResp struct {
	ID        string `json:"id"`
	AgentID   string `json:"agentId"`
	ExpiresAt string `json:"expiresAt"`
}

// WakeSessionRow is a directory row as the directory rebuilds from it, its secrets opened.
type WakeSessionRow struct {
	ID              string
	IdentityID      string
	AgentID         string
	PID             int64
	Role            string
	SocketPath      string
	SocketToken     string
	ClientSessionID string
	ProcStart       string
	LastCursor      int64
	CreatedAt       int64
	Bearer          string
	Sealed          bool // the bearer opened
}

// DeviceSessions is the device session directory as the web UI's presence frames read it (R1.11):
// the live entries, and a signal on every register, unregister and delivered cursor. Changed's
// channel is closed on the next change; read Entries, then call Changed again.
type DeviceSessions interface {
	Entries() []DeviceSession
	Changed() <-chan struct{}
}

// DeviceSession is one live entry: a CLI client process on this device. Adapter is "claude-code"
// for a client the directory can ring, "" for one it lists only. LastDeliveredCursor is the
// change-log id of the last doorbell written to it (wake_sessions.last_cursor), 0 for none: a
// session that should have been rung and shows 0 is a silent one (plan: "silence is visible").
type DeviceSession struct {
	PID                 int64  `json:"pid"`
	Role                string `json:"role"`
	IdentityID          string `json:"identityId"`
	AgentID             string `json:"agentId"`
	Adapter             string `json:"adapter,omitzero"`
	RegisteredAt        string `json:"registeredAt"`
	LastDeliveredCursor int64  `json:"lastDeliveredCursor"`
}

// ListAgentSessionsReq is list_agent_sessions's input.
type ListAgentSessionsReq struct {
	All   bool  `json:"all,omitzero"`
	Limit int64 `json:"limit,omitzero"`
}

// ListAgentSessionsResp is list_agent_sessions's output.
type ListAgentSessionsResp struct {
	Sessions []AgentSession `json:"sessions"`
}

// AgentSession is list_agent_sessions's output sessions[], first seen there.
type AgentSession struct {
	AgentID         string             `json:"agentId"`
	AgentName       string             `json:"agentName"`
	FirstSeenAt     string             `json:"firstSeenAt"`
	IdentityID      string             `json:"identityId"`
	Kind            SessionKind        `json:"kind"`
	LastSeenAt      string             `json:"lastSeenAt"`
	RequestCount    float64            `json:"requestCount"`
	SessionCount    float64            `json:"sessionCount"`
	SessionKey      string             `json:"sessionKey"`
	ToolCounts      map[string]float64 `json:"toolCounts"`
	ClientName      string             `json:"clientName,omitzero"`
	ClientVersion   string             `json:"clientVersion,omitzero"`
	IdentityDisplay *string            `json:"identityDisplay,omitzero"`
	LastProjectID   string             `json:"lastProjectId,omitzero"`
	LastTool        string             `json:"lastTool,omitzero"`
	OAuthClientFP   string             `json:"oauthClientFp,omitzero"`
	RevokedAt       string             `json:"revokedAt,omitzero"`
	RevokedBy       string             `json:"revokedBy,omitzero"`
	TenantID        string             `json:"tenantId,omitzero"`
	WakeAttachedAt  string             `json:"wakeAttachedAt,omitzero"`
	WakeBound       bool               `json:"wakeBound,omitzero"`
}

// SessionKind is how an agent session authenticated.
type SessionKind string

// SessionKindValues are SessionKind's values, in the schema's order.
var SessionKindValues = []SessionKind{"dev", "oauth_user", "service_account"}

// DisconnectAgentReq is disconnect_agent's input.
type DisconnectAgentReq struct {
	AgentID    string `json:"agentId"`
	IdentityID string `json:"identityId"`
}

// DisconnectAgentResp is disconnect_agent's output.
type DisconnectAgentResp struct {
	Disconnected bool `json:"disconnected"`
}

// AgentSessionEnder ends the live MCP sessions of one agent session: internal/mcp's server, handed
// to the core after both are built (SetAgentSessionEnder), since the service imports no transport.
// EndAgentSessions returns how many it ended; a session other than the calling request's is closed
// before it returns, the calling one right after its response.
type AgentSessionEnder interface {
	EndAgentSessions(ctx context.Context, identityID, agentID string) int
}
